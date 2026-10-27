package org

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ChelateSec/titra/internal/report"
)

// FromDir loads every *.json Titra report under dir (recursively). This is the
// simple, offline path: point it at a folder of collected report artifacts.
func FromDir(dir string) ([]report.Story, error) {
	var out []report.Story
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var s report.Story
		if json.Unmarshal(b, &s) == nil && (s.Context.Repo != "" || len(s.Actions) > 0) {
			out = append(out, s)
		}
		return nil
	})
	return out, err
}

// FromGitHub collects the latest Titra report artifact from recent workflow
// runs across an org's repositories, using the `gh` CLI (so it reuses the
// caller's auth). artifactName is the report artifact the Action uploads.
func FromGitHub(orgName, artifactName string, perRepoRuns int) ([]report.Story, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("the GitHub CLI `gh` is required for --github-org (https://cli.github.com)")
	}
	repos, err := ghRepos(orgName)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "titra-org-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	var stories []report.Story
	for _, repo := range repos {
		runs, err := ghRunsWithArtifact(repo, artifactName, perRepoRuns)
		if err != nil {
			continue
		}
		for _, runID := range runs {
			dst := filepath.Join(tmp, strings.ReplaceAll(repo, "/", "_")+"-"+runID)
			if err := ghDownloadArtifact(repo, runID, artifactName, dst); err != nil {
				continue
			}
			ss, _ := FromDir(dst)
			stories = append(stories, ss...)
		}
	}
	return stories, nil
}

func ghRepos(orgName string) ([]string, error) {
	out, err := ghJSON("repo", "list", orgName, "--limit", "500", "--json", "nameWithOwner", "--jq", ".[].nameWithOwner")
	if err != nil {
		return nil, fmt.Errorf("listing repos for %s: %w", orgName, err)
	}
	return lines(out), nil
}

// ghRunsWithArtifact returns recent run IDs for repo that carry the artifact.
func ghRunsWithArtifact(repo, artifact string, n int) ([]string, error) {
	out, err := ghJSON("run", "list", "--repo", repo, "--limit", fmt.Sprint(n*3),
		"--json", "databaseId,status", "--jq", ".[] | select(.status==\"completed\") | .databaseId")
	if err != nil {
		return nil, err
	}
	ids := lines(out)
	if len(ids) > n {
		ids = ids[:n]
	}
	return ids, nil
}

func ghDownloadArtifact(repo, runID, artifact, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("gh", "run", "download", runID, "--repo", repo, "--name", artifact, "--dir", dst)
	cmd.Stderr = nil
	return cmd.Run()
}

func ghJSON(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
