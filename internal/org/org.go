// Package org rolls per-run Titra reports up into one organization-wide view:
// which repositories run AI agents in CI, who can trigger them, what their
// tokens can do, and every action that was blocked or flagged across the org.
// This is the enterprise layer — the single pane of glass and the audit log a
// security team needs — built from the JSON report each run already produces,
// with no hosted service.
package org

import (
	"sort"
	"strings"
	"time"

	"github.com/ChelateSec/titra/internal/audit"
	"github.com/ChelateSec/titra/internal/report"
)

// View is the whole-org roll-up.
type View struct {
	Org       string    `json:"org"`
	Generated time.Time `json:"generated"`
	Window    string    `json:"window,omitempty"`

	Totals Totals  `json:"totals"`
	Repos  []Repo  `json:"repos"`
	Audit  []Entry `json:"audit"` // every blocked/would-block action, newest first
}

// Totals are the headline numbers.
type Totals struct {
	Repos        int `json:"repos"`
	AgentRepos   int `json:"agent_repos"`
	Runs         int `json:"runs"`
	Actions      int `json:"actions"`
	Blocked      int `json:"blocked"`
	WouldBlock   int `json:"would_block"`
	Exposed      int `json:"exposed"` // repos where an outsider can trigger an agent with write access
	EnforceRepos int `json:"enforce_repos"`
}

// Repo is one repository's standing.
type Repo struct {
	Name        string    `json:"name"`
	Runs        int       `json:"runs"`
	LastSeen    time.Time `json:"last_seen"`
	Agents      []string  `json:"agents,omitempty"`
	Modes       []string  `json:"modes,omitempty"`
	Blocked     int       `json:"blocked"`
	WouldBlock  int       `json:"would_block"`
	Exposed     bool      `json:"exposed"` // outsider-triggerable agent holding write access
	WriteScopes []string  `json:"write_scopes,omitempty"`
	Risk        int       `json:"risk"` // 0..100, for ordering
}

// Entry is one line of the org audit log.
type Entry struct {
	Time     time.Time `json:"time"`
	Repo     string    `json:"repo"`
	Verdict  string    `json:"verdict"`
	Tool     string    `json:"tool"`
	Command  string    `json:"command"`
	Rule     string    `json:"rule"`
	Severity string    `json:"severity"`
	Source   string    `json:"source"`
	Actor    string    `json:"actor"`
	Trusted  bool      `json:"trusted"`
	Workflow string    `json:"workflow"`
	RunURL   string    `json:"run_url,omitempty"`
}

// Build aggregates a set of per-run stories into the org view. Later stories
// for the same repo update its standing; every blocked/would-block action
// becomes an audit entry.
func Build(orgName string, stories []report.Story) View {
	v := View{Org: orgName, Generated: time.Now().UTC()}
	byRepo := map[string]*Repo{}

	for _, s := range stories {
		repoName := s.Context.Repo
		if repoName == "" {
			repoName = "(unknown)"
		}
		r := byRepo[repoName]
		if r == nil {
			r = &Repo{Name: repoName}
			byRepo[repoName] = r
		}
		r.Runs++
		v.Totals.Runs++

		gen := parseTime(s.Generated)
		if gen.After(r.LastSeen) {
			r.LastSeen = gen
		}
		for _, a := range s.Context.Agents {
			r.Agents = addUniq(r.Agents, baseName(a))
		}
		if s.Mode != "" {
			r.Modes = addUniq(r.Modes, s.Mode)
		}
		r.Blocked += len(s.Blocked)
		r.WouldBlock += len(s.WouldBlock)
		v.Totals.Actions += len(s.Actions)
		v.Totals.Blocked += len(s.Blocked)
		v.Totals.WouldBlock += len(s.WouldBlock)

		if wa := s.Context.WriteAccess(); len(wa) > 0 {
			for _, w := range wa {
				r.WriteScopes = addUniq(r.WriteScopes, w)
			}
			if len(s.Context.Agents) > 0 && !s.Context.Trusted && outsiderTriggerable(s.Context.Event) {
				r.Exposed = true
			}
		}

		flagged := make([]audit.Record, 0, len(s.Blocked)+len(s.WouldBlock))
		flagged = append(flagged, s.Blocked...)
		flagged = append(flagged, s.WouldBlock...)
		for _, rec := range flagged {
			v.Audit = append(v.Audit, Entry{
				Time:     parseTime(rec.Time),
				Repo:     repoName,
				Verdict:  rec.Verdict,
				Tool:     rec.Tool,
				Command:  strings.TrimSpace(rec.Tool + " " + strings.Join(rec.Args, " ")),
				Rule:     rec.RuleID,
				Severity: rec.Severity,
				Source:   rec.Source,
				Actor:    s.Context.Actor,
				Trusted:  s.Context.Trusted,
				Workflow: s.Context.Workflow,
				RunURL:   s.Context.RunURL,
			})
		}
	}

	for _, r := range byRepo {
		r.Risk = riskScore(r)
		sort.Strings(r.Agents)
		sort.Strings(r.WriteScopes)
		v.Repos = append(v.Repos, *r)
		v.Totals.Repos++
		if len(r.Agents) > 0 {
			v.Totals.AgentRepos++
		}
		if r.Exposed {
			v.Totals.Exposed++
		}
		if contains(r.Modes, "enforce") {
			v.Totals.EnforceRepos++
		}
	}

	sort.Slice(v.Repos, func(i, j int) bool {
		if v.Repos[i].Risk != v.Repos[j].Risk {
			return v.Repos[i].Risk > v.Repos[j].Risk
		}
		return v.Repos[i].Name < v.Repos[j].Name
	})
	sort.SliceStable(v.Audit, func(i, j int) bool { return v.Audit[i].Time.After(v.Audit[j].Time) })
	return v
}

// riskScore ranks a repo so the org view surfaces the worst first.
func riskScore(r *Repo) int {
	s := 0
	if r.Exposed {
		s += 50
	}
	if len(r.Agents) > 0 && !contains(r.Modes, "enforce") {
		s += 20 // runs agents but only watching
	}
	s += min(r.Blocked*5, 20)
	s += min(len(r.WriteScopes)*3, 10)
	return min(s, 100)
}

func outsiderTriggerable(event string) bool {
	switch event {
	case "issues", "issue_comment", "pull_request_target", "pull_request_review",
		"pull_request_review_comment", "discussion", "discussion_comment", "fork", "watch":
		return true
	}
	return false
}

func baseName(agent string) string { return strings.SplitN(agent, " (", 2)[0] }

func addUniq(ss []string, v string) []string {
	if v == "" || contains(ss, v) {
		return ss
	}
	return append(ss, v)
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
