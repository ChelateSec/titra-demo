// Command titra is Chelate's runtime guard for agentic CI/CD. It watches what
// AI agents do inside a pipeline and blocks the dangerous actions, in real time.
//
// Titra installs itself on PATH under the names of the tools it guards
// (git, gh, curl, ...), busybox-style. So the binary has two faces:
//
//   - Invoked as `titra`, it runs subcommands: install, guard, report, init.
//   - Invoked under a tool name (via a symlink), it guards that tool invocation:
//     evaluate against policy, log it, then run the real tool or refuse.
//
// The block decision is deterministic policy, never a model — a guard that asked
// an LLM whether to block could itself be prompt-injected.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChelateSec/titra/internal/guard"
	"github.com/ChelateSec/titra/internal/install"
	"github.com/ChelateSec/titra/internal/policy"
	"github.com/ChelateSec/titra/internal/report"
	"github.com/ChelateSec/titra/internal/server"
)

// version is over(ridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// Busybox dispatch: if we were invoked under a guarded tool's name, guard it.
	base := filepath.Base(os.Args[0])
	if !isTitra(base) {
		guard.Run(base, os.Args[1:])
		return // unreachable: guard.Run execs or exits
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "guard":
		cmdGuard(os.Args[2:])
	case "install":
		cmdInstall(os.Args[2:])
	case "report":
		cmdReport(os.Args[2:])
	case "trace":
		cmdTrace(os.Args[2:])
	case "dns":
		cmdDNS(os.Args[2:])
	case "hook":
		cmdHook(os.Args[2:])
	case "serve":
		cmdServe(os.Args[2:])
	case "init":
		cmdInit(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("titra %s\n", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "titra: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func isTitra(base string) bool {
	return base == "titra" || base == "titra.exe" || strings.HasPrefix(base, "titra")
}

// cmdGuard is the explicit form a wrapper script can call: `titra guard git push`.
// (The symlink form reaches guard.Run directly, without this subcommand.)
func cmdGuard(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: titra guard <tool> [args...]")
		os.Exit(2)
	}
	guard.Run(args[0], args[1:])
}

func cmdInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	dir := fs.String("dir", defaultShimDir(), "directory to create the tool shims in")
	tools := fs.String("tools", "", "comma-separated tools to guard (default: built-in set)")
	logPath := fs.String("log", defaultLogPath(), "path for the JSONL action log")
	polPath := fs.String("policy", "", "path to a policy file (optional; built-in defaults used if empty)")
	mode := fs.String("mode", "", "override policy mode: monitor | dry-run | enforce")
	server := fs.String("server", os.Getenv("TITRA_SERVER"), "Titra server URL to stream actions to (optional)")
	token := fs.String("token", os.Getenv("TITRA_TOKEN"), "ingest token for the server (optional)")
	project := fs.String("project", os.Getenv("TITRA_PROJECT"), "project name for dashboard grouping (optional)")
	ghEnv := fs.Bool("github-env", false, "also wire PATH/env into $GITHUB_PATH and $GITHUB_ENV")
	_ = fs.Parse(args)

	self, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	var toolList []string
	if *tools != "" {
		toolList = splitComma(*tools)
	}

	dirOut, err := install.Run(self, install.Options{
		Dir:        *dir,
		Tools:      toolList,
		LogPath:    *logPath,
		PolicyPath: *polPath,
		Mode:       *mode,
		Server:     *server,
		Token:      *token,
		Project:    *project,
		GitHubEnv:  *ghEnv,
	})
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "titra: guarding via %s\n", dirOut)
}

func cmdReport(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	logPath := fs.String("log", defaultLogPath(), "path to the JSONL action log")
	format := fs.String("format", "text", "output format: text | md | json")
	failOn := fs.String("fail-on", "", "exit non-zero if any action was blocked (block) or would-block|any")
	stateDir := fs.String("state", os.Getenv("TITRA_STATE"), "trace state dir (enables the full run report)")
	out := fs.String("out", "", "also write the report to this file")
	_ = fs.Parse(args)

	story, legacy, err := buildReport(*logPath, *stateDir)
	if err != nil {
		fatal(fmt.Errorf("reading log %s: %w", *logPath, err))
	}

	var w io.Writer = os.Stdout
	var file *os.File
	if *out != "" {
		file, err = os.Create(*out)
		if err != nil {
			fatal(err)
		}
		defer file.Close()
		w = io.MultiWriter(os.Stdout, file)
	}

	if story != nil {
		switch *format {
		case "md", "markdown":
			report.StoryMarkdown(w, *story)
		case "json":
			_ = report.StoryJSON(w, *story)
		default:
			report.StoryTerminal(w, *story)
		}
		if shouldFail(*failOn, len(story.Blocked) > 0, story.Warnings() > 0) {
			os.Exit(1)
		}
		return
	}

	switch *format {
	case "md", "markdown":
		report.Markdown(w, legacy)
	case "json":
		_ = report.StoryJSON(w, report.Story{}) // empty shell; no trace data
	default:
		report.Terminal(w, legacy)
	}
	if shouldFail(*failOn, legacy.Blocked > 0, legacy.HasFindings()) {
		os.Exit(1)
	}
}

func shouldFail(failOn string, blocked, findings bool) bool {
	switch failOn {
	case "block":
		return blocked
	case "would-block", "any":
		return findings
	}
	return false
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", envOr("TITRA_ADDR", ":8080"), "listen address")
	data := fs.String("data", envOr("TITRA_DATA", "./titra-data"), "data directory")
	base := fs.String("base-url", os.Getenv("TITRA_PUBLIC_URL"), "public base URL (for OAuth and the GitHub App); defaults to http://localhost:<port>")
	_ = fs.Parse(args)

	store, err := server.OpenStore(*data)
	if err != nil {
		fatal(err)
	}
	baseURL := *base
	if baseURL == "" {
		baseURL = "http://localhost" + portSuffix(*addr)
	}
	srv := server.New(store, baseURL)

	fmt.Printf("titra: dashboard on %s  (data: %s)\n", baseURL, *data)
	if !store.Config().AppConfigured() {
		fmt.Printf("titra: no GitHub App yet — open %s/setup to create one, or just stream actions and watch the dashboard.\n", baseURL)
	}
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		fatal(err)
	}
}

// portSuffix turns a listen address like ":8080" or "0.0.0.0:8080" into ":8080".
func portSuffix(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":8080"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	path := fs.String("path", ".titra/policy.yml", "where to write the starter policy")
	mode := fs.String("mode", "monitor", "starting mode: monitor | dry-run | enforce")
	_ = fs.Parse(args)

	if _, err := os.Stat(*path); err == nil {
		fatal(fmt.Errorf("%s already exists; refusing to overwrite", *path))
	}
	if err := os.MkdirAll(filepath.Dir(*path), 0o755); err != nil {
		fatal(err)
	}
	content := strings.Replace(starterPolicy, "__MODE__", *mode, 1)
	if err := os.WriteFile(*path, []byte(content), 0o644); err != nil {
		fatal(err)
	}
	// Sanity-check that what we wrote parses.
	if _, err := policy.Load(*path); err != nil {
		fatal(fmt.Errorf("wrote %s but it failed to parse: %w", *path, err))
	}
	fmt.Printf("titra: wrote starter policy to %s (mode: %s)\n", *path, *mode)
}

func defaultShimDir() string {
	if d := os.Getenv("TITRA_SHIM_DIR"); d != "" {
		return d
	}
	if rt := os.Getenv("RUNNER_TEMP"); rt != "" {
		return filepath.Join(rt, "titra-bin")
	}
	return filepath.Join(os.TempDir(), "titra-bin")
}

func defaultLogPath() string {
	if l := os.Getenv("TITRA_LOG"); l != "" {
		return l
	}
	if rt := os.Getenv("RUNNER_TEMP"); rt != "" {
		return filepath.Join(rt, "titra-actions.jsonl")
	}
	return filepath.Join(os.TempDir(), "titra-actions.jsonl")
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "titra: %v\n", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `titra — Chelate's runtime guard for agentic CI/CD

Usage:
  titra install [--dir D] [--tools a,b,c] [--log F] [--policy F] [--mode M] [--github-env]
      Install the tool shims and emit the environment that guards this job.
  titra trace start|stop|collect [--state DIR] [--dns] [--lock] [--github-env]
      Watch the whole job with the kernel: every process, connection and
      credential read, not only guarded commands. Needs root.
  titra hook claude
      Claude Code PreToolUse hook: log and (in enforce) block the agent's own
      tool calls. Reads the hook event on stdin.
  titra guard <tool> [args...]
      Evaluate and run a single tool invocation (the shims call this).
  titra report [--log F] [--state DIR] [--format text|md|json] [--fail-on block|any|off] [--out F]
      Render the run report. With --state it tells the whole story: who
      triggered the run, which agent ran, what it tried, and what else the job did.
  titra dns --upstream H --log F [--listen 127.0.0.1:53]
      Logging DNS forwarder (started by 'trace' to name outbound connections).
  titra init [--path .titra/policy.yml] [--mode monitor|dry-run|enforce]
      Write a starter policy file.
  titra serve [--addr :8080] [--data DIR]
      Optional self-hosted dashboard (Titra Enterprise).
  titra version

The block decision is deterministic policy, never a model — a guard that asked
an LLM whether to block could itself be prompt-injected.
`)
}

// starterPolicy is the template written by `titra init`. __MODE__ is replaced.
const starterPolicy = `# Titra policy — https://github.com/ChelateSec/titra
# Watch what AI agents do in this pipeline and block the dangerous actions.
version: 1

# monitor  : log every action, block nothing (start here)
# dry-run  : log + mark what WOULD block, but still allow
# enforce  : block matching actions in real time
mode: __MODE__

# Verdict when no rule matches. Almost always allow.
default: allow

rules:
  - id: block-git-push
    description: Agent must not push code to the repository
    action: block
    severity: critical
    match:
      tool: git
      args_regex: '(^|\s)push(\s|$)'

  - id: block-pr-approval
    description: Agent must not approve or merge pull requests
    action: block
    severity: critical
    match:
      tool: gh
      args_regex: 'pr\s+(review|merge)'

  - id: block-workflow-dispatch
    description: Agent must not trigger other workflows (privilege-escalation pivot)
    action: block
    severity: high
    match:
      tool: gh
      args_regex: 'workflow\s+run|actions/workflows/.+/dispatches'

  - id: block-release-publish
    description: Agent must not publish packages or releases
    action: block
    severity: critical
    match:
      args_regex: '(^|\s)(npm|pnpm|yarn)\s+publish|gh\s+release\s+create|twine\s+upload'

  - id: flag-credential-read
    description: Agent read a credential store (exfiltration primitive)
    action: block
    severity: high
    match:
      args_regex: '\.git/config|/proc/\d+/environ|\.npmrc|\.env(\s|$)|id_rsa'
`
