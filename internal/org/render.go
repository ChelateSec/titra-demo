package org

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"
	"time"
)

// JSON writes the view as indented JSON.
func JSON(w io.Writer, v View) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Terminal writes a compact org summary.
func Terminal(w io.Writer, v View) {
	p := func(f string, a ...any) { fmt.Fprintf(w, f, a...) }
	p("titra org: %s\n", v.Org)
	p("  %d repos, %d run agents, %d exposed · %d runs, %d blocked, %d would-block\n",
		v.Totals.Repos, v.Totals.AgentRepos, v.Totals.Exposed, v.Totals.Runs, v.Totals.Blocked, v.Totals.WouldBlock)
	for _, r := range v.Repos {
		tag := ""
		if r.Exposed {
			tag = "  ⚠ EXPOSED"
		}
		p("  [%3d] %-40s agents=%s mode=%s blocked=%d%s\n",
			r.Risk, r.Name, join(r.Agents), join(r.Modes), r.Blocked, tag)
	}
	if len(v.Audit) > 0 {
		p("\n  audit (newest first):\n")
		for i, e := range v.Audit {
			if i >= 20 {
				p("  ... %d more\n", len(v.Audit)-20)
				break
			}
			p("  %s  %-22s %-11s %s  [%s]\n",
				e.Time.Format("01-02 15:04"), e.Repo, strings.ToUpper(e.Verdict), e.Command, e.Rule)
		}
	}
}

func join(ss []string) string {
	if len(ss) == 0 {
		return "-"
	}
	return strings.Join(ss, ",")
}

// HTML writes a single self-contained dashboard page in the Chelate design
// system. No external assets, no scripts beyond a tiny filter — an org can open
// it locally, commit it, or publish it behind their own auth.
func HTML(w io.Writer, v View) {
	e := html.EscapeString
	p := func(f string, a ...any) { fmt.Fprintf(w, f, a...) }

	p(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	p(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	p(`<title>Titra — %s</title>`, e(v.Org))
	p(`<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>`)
	p(`<link href="https://fonts.googleapis.com/css2?family=Instrument+Serif:ital@0;1&family=DM+Mono:wght@400;500&display=swap" rel="stylesheet">`)
	p("<style>%s</style></head><body>", css)

	// header
	p(`<header class="top"><div class="brand"><span class="mark">Titra</span>`)
	p(`<span class="tag">securing the agentic CI/CD</span></div>`)
	p(`<div class="org">%s · <span class="muted">%s UTC</span></div></header>`,
		e(v.Org), v.Generated.Format("2 Jan 2006 15:04"))

	// stat row
	p(`<section class="stats">`)
	stat(w, fmt.Sprint(v.Totals.AgentRepos), "repos run agents", "")
	stat(w, fmt.Sprint(v.Totals.Exposed), "exposed", clsIf(v.Totals.Exposed > 0, "bad"))
	stat(w, fmt.Sprint(v.Totals.Blocked), "blocked", clsIf(v.Totals.Blocked > 0, "block"))
	stat(w, fmt.Sprint(v.Totals.WouldBlock), "would-block", clsIf(v.Totals.WouldBlock > 0, "warn"))
	stat(w, fmt.Sprintf("%d/%d", v.Totals.EnforceRepos, v.Totals.AgentRepos), "enforcing", "")
	p(`</section>`)

	// repos
	p(`<main><section class="panel"><h2>Repositories</h2>`)
	if len(v.Repos) == 0 {
		p(`<p class="muted empty">No Titra reports collected yet.</p>`)
	}
	p(`<table class="repos"><thead><tr><th>Repository</th><th>Agents</th><th>Mode</th><th>Token write</th><th>Blocked</th><th>Last seen</th></tr></thead><tbody>`)
	for _, r := range v.Repos {
		cls := ""
		if r.Exposed {
			cls = ` class="exposed"`
		}
		p("<tr%s><td class=\"repo\">%s%s</td>", cls, e(r.Name), badge(r.Exposed))
		p("<td>%s</td>", pills(r.Agents, "agent"))
		p("<td>%s</td>", modePills(r.Modes))
		p("<td>%s</td>", pills(r.WriteScopes, "scope"))
		p("<td class=\"num %s\">%s</td>", clsIf(r.Blocked > 0, "block"), dash(r.Blocked))
		p("<td class=\"muted small\">%s</td></tr>", rel(r.LastSeen))
	}
	p(`</tbody></table></section>`)

	// audit log
	p(`<section class="panel"><h2>Audit log</h2>`)
	p(`<p class="muted small">Every action Titra blocked or flagged across the org, newest first.</p>`)
	if len(v.Audit) == 0 {
		p(`<p class="muted empty">Nothing blocked or flagged.</p>`)
	} else {
		p(`<table class="audit"><thead><tr><th>When</th><th>Repo</th><th>Verdict</th><th>Source</th><th>Command</th><th>Rule</th><th>Who</th></tr></thead><tbody>`)
		for _, a := range v.Audit {
			vcls := "allow"
			vtxt := a.Verdict
			if a.Verdict == "block" {
				vcls, vtxt = "block", "blocked"
			} else if a.Verdict == "would-block" {
				vcls, vtxt = "warn", "would block"
			}
			who := e(a.Actor)
			if a.Actor != "" && !a.Trusted {
				who += ` <span class="out">outsider</span>`
			}
			cmd := e(a.Command)
			if a.RunURL != "" {
				cmd = fmt.Sprintf(`<a href="%s">%s</a>`, e(a.RunURL), cmd)
			}
			p(`<tr><td class="muted small">%s</td><td class="repo small">%s</td>`, a.Time.Format("01-02 15:04"), e(a.Repo))
			p(`<td><span class="pill %s">%s</span></td><td class="small">%s</td>`, vcls, vtxt, srcLabel(a.Source))
			p(`<td class="cmd">%s</td><td class="small">%s <span class="sev %s">%s</span></td><td class="small">%s</td></tr>`,
				cmd, e(a.Rule), e(a.Severity), e(a.Severity), who)
		}
		p(`</tbody></table>`)
	}
	p(`</section></main>`)

	p(`<footer class="muted small">Titra · Chelate Sec · securing the agentic CI/CD. Blocking is deterministic policy, never a model.</footer>`)
	p(`</body></html>`)
}

func stat(w io.Writer, n, label, cls string) {
	fmt.Fprintf(w, `<div class="stat"><div class="n %s">%s</div><div class="l">%s</div></div>`, cls, n, label)
}

func pills(ss []string, cls string) string {
	if len(ss) == 0 {
		return `<span class="muted">—</span>`
	}
	var b strings.Builder
	for _, s := range ss {
		fmt.Fprintf(&b, `<span class="pill %s">%s</span>`, cls, html.EscapeString(s))
	}
	return b.String()
}

func modePills(ms []string) string {
	if len(ms) == 0 {
		return `<span class="muted">—</span>`
	}
	var b strings.Builder
	for _, m := range ms {
		cls := "mode"
		if m == "enforce" {
			cls = "mode enforce"
		} else if m == "monitor" {
			cls = "mode monitor"
		}
		fmt.Fprintf(&b, `<span class="pill %s">%s</span>`, cls, html.EscapeString(m))
	}
	return b.String()
}

func badge(exposed bool) string {
	if exposed {
		return ` <span class="flag">exposed</span>`
	}
	return ""
}

func srcLabel(s string) string {
	switch s {
	case "claude-code":
		return "agent"
	case "shim", "":
		return "command"
	}
	return html.EscapeString(s)
}

func clsIf(cond bool, cls string) string {
	if cond {
		return cls
	}
	return ""
}

func dash(n int) string {
	if n == 0 {
		return "—"
	}
	return fmt.Sprint(n)
}

func rel(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

const css = `
:root{--paper:#fdf6ef;--paper2:#f7eee3;--mint:#eaf1ee;--ink:#1c2826;--green:#2d4a43;
--muted:#5a6965;--faint:#8a938f;--line:rgba(45,74,67,.15);--line2:rgba(45,74,67,.3);
--terra:#c4452f;--terra2:#d87355;--amber:#b06f1f;--ok:#2d7d5a;
--serif:"Instrument Serif",Georgia,serif;--mono:"DM Mono",ui-monospace,Menlo,monospace}
*{box-sizing:border-box}html,body{margin:0}
body{background:var(--paper);color:var(--ink);font-family:var(--mono);font-size:13px;line-height:1.5;-webkit-font-smoothing:antialiased}
h2{font-family:var(--serif);font-weight:400;font-size:22px;letter-spacing:-.01em;margin:0 0 14px}
a{color:var(--green);text-decoration:none;border-bottom:1px solid var(--line2)}a:hover{color:var(--terra2);border-color:var(--terra2)}
.top{display:flex;align-items:baseline;justify-content:space-between;padding:22px 32px 18px;border-bottom:1px solid var(--line)}
.brand{display:flex;align-items:baseline;gap:14px}.mark{font-family:var(--serif);font-size:30px;letter-spacing:-.02em}
.tag{color:var(--muted);font-size:11px;letter-spacing:.02em}.org{font-size:13px}.muted{color:var(--muted)}.small{font-size:11.5px}
.stats{display:flex;gap:44px;padding:24px 32px 8px;flex-wrap:wrap}
.stat .n{font-family:var(--serif);font-size:38px;line-height:1;letter-spacing:-.02em}
.stat .l{color:var(--muted);font-size:11px;letter-spacing:.06em;text-transform:lowercase;margin-top:2px}
.n.bad,.n.block{color:var(--terra)}.n.warn{color:var(--amber)}
main{padding:12px 32px 40px}.panel{margin-top:26px}
table{width:100%;border-collapse:collapse}th{text-align:left;font-weight:500;font-size:10.5px;letter-spacing:.07em;text-transform:uppercase;color:var(--muted);padding:8px 10px;border-bottom:1px solid var(--line)}
td{padding:10px 10px;border-bottom:1px solid var(--line);vertical-align:top}
tr.exposed td{background:rgba(196,69,47,.05)}
.repo{font-size:12.5px}.num{font-variant-numeric:tabular-nums}.num.block{color:var(--terra)}
.cmd{font-size:12px;max-width:380px;overflow-wrap:anywhere}
.empty{padding:16px 10px}
.pill{display:inline-block;font-size:10.5px;padding:1px 7px;border:1px solid var(--line2);border-radius:3px;margin:1px 3px 1px 0;white-space:nowrap}
.pill.agent{border-color:var(--green);color:var(--green)}
.pill.scope{color:var(--muted)}
.pill.mode.enforce{border-color:var(--ok);color:var(--ok)}.pill.mode.monitor{color:var(--amber);border-color:var(--amber)}
.pill.block{color:var(--terra);border-color:var(--terra)}.pill.warn{color:var(--amber);border-color:var(--amber)}.pill.allow{color:var(--ok);border-color:var(--ok)}
.flag{display:inline-block;font-size:9.5px;letter-spacing:.06em;text-transform:uppercase;color:#fff;background:var(--terra);border-radius:3px;padding:1px 6px;margin-left:6px}
.out{font-size:9.5px;color:var(--terra);text-transform:uppercase;letter-spacing:.04em}
.sev{font-size:9.5px;text-transform:uppercase;margin-left:6px}.sev.critical{color:var(--terra)}.sev.high{color:var(--amber)}
footer{padding:20px 32px 40px;border-top:1px solid var(--line);margin-top:20px}
@media(max-width:720px){.top,.stats,main,footer{padding-left:16px;padding-right:16px}.stats{gap:26px}.cmd{max-width:none}}
`
