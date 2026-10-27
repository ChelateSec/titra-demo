package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ChelateSec/titra/internal/audit"
	"github.com/ChelateSec/titra/internal/dnslog"
	"github.com/ChelateSec/titra/internal/hook"
	"github.com/ChelateSec/titra/internal/jobctx"
	"github.com/ChelateSec/titra/internal/org"
	"github.com/ChelateSec/titra/internal/report"
	"github.com/ChelateSec/titra/internal/trace"
)

// buildReport assembles the full Story when a trace state dir is present and
// readable; otherwise it returns the legacy summary. Exactly one of the two
// return values is meaningful (story != nil means use it).
func buildReport(logPath, stateDir string) (*report.Story, report.Summary, error) {
	recs, err := audit.ReadAll(logPath)
	if err != nil {
		return nil, report.Summary{}, err
	}
	ctx := jobctx.FromEnv()

	in := report.Input{
		Records:        recs,
		Context:        ctx,
		AllowedDomains: splitComma(os.Getenv("TITRA_ALLOWED_DOMAINS")),
		ShimTools:      splitComma(os.Getenv("TITRA_TOOLS")),
	}

	if stateDir != "" {
		if st, lerr := trace.Load(stateDir); lerr == nil {
			in.State = st
			if st.DNSLog != "" {
				in.DNS = dnslog.ReadLog(st.DNSLog)
			}
			if os.Geteuid() == 0 {
				if evs, cerr := trace.Collect(st); cerr == nil {
					in.Events = evs
				} else {
					in.TraceErr = cerr.Error()
				}
			} else {
				in.TraceErr = "report not run as root; kernel events not collected"
			}
			s := report.Build(in)
			return &s, report.Summary{}, nil
		}
	}

	// No trace: still give the richer story shape, just without the trace half,
	// as long as we have job context worth showing. Otherwise legacy summary.
	if ctx.Repo != "" || ctx.Event != "" || len(recs) > 0 {
		s := report.Build(in)
		return &s, report.Summary{}, nil
	}
	return nil, report.Summarize(recs), nil
}

// cmdTrace: `titra trace start|stop|collect`. Needs root.
func cmdTrace(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: titra trace start|stop|collect [flags]")
		os.Exit(2)
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("trace", flag.ExitOnError)
	stateDir := fs.String("state", envOr("TITRA_STATE", defaultStateDir()), "trace state dir")
	workspace := fs.String("workspace", os.Getenv("GITHUB_WORKSPACE"), "job workspace to watch for credential files")
	home := fs.String("home", os.Getenv("HOME"), "home dir to watch for credential files")
	dns := fs.Bool("dns", true, "also run the DNS logger so connections show domain names")
	lock := fs.Bool("lock", false, "make audit rules immutable until reboot (ephemeral runners)")
	ghEnv := fs.Bool("github-env", false, "export TITRA_STATE into $GITHUB_ENV for later steps")
	_ = fs.Parse(rest)

	switch sub {
	case "start":
		self, _ := os.Executable()
		if r, err := filepath.EvalSymlinks(self); err == nil {
			self = r
		}
		st, err := trace.Start(trace.Options{
			StateDir: *stateDir, Titra: self, Workspace: *workspace,
			Home: *home, DNS: *dns, Lock: *lock,
		})
		if err != nil {
			// Tracing is a bonus layer: never fail the job because it couldn't start.
			fmt.Fprintf(os.Stderr, "titra: tracing not available: %v\n", err)
			fmt.Fprintln(os.Stderr, "titra: continuing with command-level guarding only.")
			os.Exit(0)
		}
		if *ghEnv {
			if f := os.Getenv("GITHUB_ENV"); f != "" {
				_ = appendEnv(f, "TITRA_STATE", *stateDir)
			}
		}
		fmt.Fprintf(os.Stderr, "titra: tracing the whole job (pid tree under %d). dns=%v watched=%d\n",
			st.RootPID, st.DNS, len(st.Watched))
		for _, w := range st.Warnings {
			fmt.Fprintf(os.Stderr, "titra: %s\n", w)
		}
	case "stop":
		if err := trace.Stop(*stateDir); err != nil {
			fmt.Fprintf(os.Stderr, "titra: %v\n", err)
		}
	case "collect":
		st, err := trace.Load(*stateDir)
		if err != nil {
			fatal(err)
		}
		evs, err := trace.Collect(st)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("titra: collected %d event(s)\n", len(evs))
	default:
		fmt.Fprintf(os.Stderr, "titra: unknown trace subcommand %q\n", sub)
		os.Exit(2)
	}
}

// cmdDNS: `titra dns` runs the logging DNS forwarder (started by trace).
func cmdDNS(args []string) {
	fs := flag.NewFlagSet("dns", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:53", "listen address")
	upstream := fs.String("upstream", "", "upstream resolver (host or host:port)")
	logPath := fs.String("log", "", "path for the JSONL lookup log")
	ready := fs.String("ready", "", "touch this file once both listeners are bound")
	_ = fs.Parse(args)
	if *upstream == "" || *logPath == "" {
		fatal(fmt.Errorf("dns: --upstream and --log are required"))
	}
	srv := &dnslog.Server{Listen: *listen, Upstream: *upstream, LogPath: *logPath}
	err := srv.Run(func() {
		if *ready != "" {
			_ = os.WriteFile(*ready, []byte("ok"), 0o644)
		}
	})
	fatal(err)
}

// cmdHook: `titra hook claude` — the Claude Code PreToolUse hook.
func cmdHook(args []string) { hook.Main(args) }

// cmdOrgReport builds the organization-wide roll-up: the enterprise single
// pane of glass and audit log, assembled from the per-run JSON reports.
func cmdOrgReport(ghOrg, fromDir, format, out string) {
	var (
		stories []report.Story
		err     error
		name    = ghOrg
	)
	switch {
	case fromDir != "":
		stories, err = org.FromDir(fromDir)
		if name == "" {
			name = filepath.Base(fromDir)
		}
	default:
		fmt.Fprintf(os.Stderr, "titra: collecting Titra reports across %s via gh...\n", ghOrg)
		stories, err = org.FromGitHub(ghOrg, envOr("TITRA_ARTIFACT", "titra-report"), 5)
	}
	if err != nil {
		fatal(err)
	}
	view := org.Build(name, stories)

	var w io.Writer = os.Stdout
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		w = io.MultiWriter(os.Stdout, f)
		if format == "html" || format == "json" {
			w = f // don't dump HTML/JSON to the terminal
		}
	}
	switch format {
	case "html":
		org.HTML(w, view)
		if out != "" {
			fmt.Fprintf(os.Stderr, "titra: wrote org dashboard to %s (%d repos, %d flagged)\n", out, view.Totals.Repos, len(view.Audit))
		}
	case "json":
		_ = org.JSON(w, view)
	default:
		org.Terminal(w, view)
	}
}

func defaultStateDir() string {
	if rt := os.Getenv("RUNNER_TEMP"); rt != "" {
		return filepath.Join(rt, "titra-trace")
	}
	return filepath.Join(os.TempDir(), "titra-trace")
}

func appendEnv(path, k, v string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s=%s\n", k, v)
	return err
}
