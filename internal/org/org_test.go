package org

import (
	"testing"

	"github.com/ChelateSec/titra/internal/audit"
	"github.com/ChelateSec/titra/internal/jobctx"
	"github.com/ChelateSec/titra/internal/report"
)

func TestBuildAggregates(t *testing.T) {
	stories := []report.Story{
		{
			Generated: "2026-10-25T18:00:00Z",
			Mode:      "enforce",
			Context: jobctx.Context{
				Repo: "acme/api", Event: "issues", Actor: "ext", Trusted: false,
				Agents:      []string{"Claude Code (anthropics/claude-code-action@v1)"},
				Permissions: map[string]string{"contents": "write"},
			},
			Blocked: []audit.Record{
				{Time: "2026-10-25T18:00:01Z", Tool: "git", Args: []string{"push"}, Verdict: "block", RuleID: "block-git-push", Severity: "critical", Source: "shim"},
			},
			Actions: make([]audit.Record, 3),
		},
		{
			Generated: "2026-10-24T10:00:00Z",
			Mode:      "monitor",
			Context:   jobctx.Context{Repo: "acme/docs", Event: "push", Actor: "owner", Trusted: true},
			Actions:   make([]audit.Record, 2),
		},
	}
	v := Build("acme", stories)

	if v.Totals.Repos != 2 {
		t.Errorf("repos = %d, want 2", v.Totals.Repos)
	}
	if v.Totals.AgentRepos != 1 {
		t.Errorf("agent repos = %d, want 1", v.Totals.AgentRepos)
	}
	if v.Totals.Exposed != 1 {
		t.Errorf("exposed = %d, want 1 (outsider-triggerable agent with write)", v.Totals.Exposed)
	}
	if v.Totals.Blocked != 1 {
		t.Errorf("blocked = %d, want 1", v.Totals.Blocked)
	}
	if len(v.Audit) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(v.Audit))
	}
	// Highest-risk repo (exposed) must sort first.
	if v.Repos[0].Name != "acme/api" || !v.Repos[0].Exposed {
		t.Errorf("expected acme/api exposed first, got %+v", v.Repos[0])
	}
	// A trusted push-triggered repo is never exposed.
	for _, r := range v.Repos {
		if r.Name == "acme/docs" && r.Exposed {
			t.Errorf("acme/docs should not be exposed")
		}
	}
}

func TestExposureNeedsWriteAndOutsider(t *testing.T) {
	// Agent + outsider trigger but read-only token => not exposed.
	v := Build("x", []report.Story{{
		Context: jobctx.Context{
			Repo: "x/ro", Event: "issues", Trusted: false,
			Agents:      []string{"Claude Code"},
			Permissions: map[string]string{"contents": "read"},
		},
	}})
	if v.Totals.Exposed != 0 {
		t.Errorf("read-only token should not be exposed")
	}
}
