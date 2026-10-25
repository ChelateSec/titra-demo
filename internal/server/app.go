package server

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"time"
)

// This file implements GitHub App onboarding: the one-click "create app from
// manifest" flow, the install redirect, and the webhook receiver that tracks
// installs. Together they turn a bare server into an installable GitHub App.

// handleSetup renders the onboarding / status page.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Config()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if !cfg.AppConfigured() {
		page(w, "Set up Titra", `
			<h1>Set up Titra</h1>
			<p class="muted">Create the GitHub App that lets your org install Titra in one click.
			GitHub creates the app and hands the credentials straight back to this server — you
			don't copy a single secret.</p>
			<p><a class="btn" href="/github/app/manifest">Create the GitHub App →</a></p>
			<p class="muted small">Personal account. For an org, append <code>?org=YOUR_ORG</code>.</p>
		`)
		return
	}

	app := cfg.AppInfo()
	// Ensure there is at least one ingest token to show.
	var proj *Project
	if cfg.HasProjects() {
		for _, p := range cfg.Projects {
			proj = p
			break
		}
	} else {
		proj = cfg.NewProject("default")
	}

	var installs string
	for _, in := range cfg.ListInstallations() {
		installs += fmt.Sprintf("<li><b>%s</b> <span class=\"muted small\">(%s, installed %s)</span></li>",
			html.EscapeString(in.Account), html.EscapeString(in.AccountType), in.CreatedAt.Format("2006-01-02"))
	}
	if installs == "" {
		installs = `<li class="muted">No installations yet.</li>`
	}

	page(w, "Titra setup", fmt.Sprintf(`
		<h1>Titra is configured</h1>
		<p class="muted">GitHub App <b>%s</b> is registered. Install it on an org, then add the
		Action and the secrets below to any repo running an AI agent.</p>

		<h2>1. Install the app</h2>
		<p><a class="btn" href="/github/app/install">Install on your org →</a></p>
		<ul>%s</ul>

		<h2>2. Add the secrets to your repo</h2>
		<p class="muted">Repo → Settings → Secrets and variables → Actions:</p>
		<pre><code>TITRA_SERVER = %s
TITRA_TOKEN  = %s</code></pre>

		<h2>3. Add the Action</h2>
		<pre><code>- uses: ChelateSec/titra@v1
  with:
    mode: enforce
    server: ${{ secrets.TITRA_SERVER }}
    token:  ${{ secrets.TITRA_TOKEN }}</code></pre>

		<p><a class="btn-ghost" href="/">Open the dashboard →</a></p>
	`, html.EscapeString(app.Name), installs, s.baseURL, proj.Token))
}

// handleManifest renders an auto-submitting form that POSTs an App manifest to
// GitHub. GitHub creates the app and redirects back to /github/app/created.
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	state := randHex(12)
	http.SetCookie(w, &http.Cookie{Name: "titra_manifest_state", Value: state, Path: "/", HttpOnly: true, MaxAge: 600, SameSite: http.SameSiteLaxMode})

	manifest := map[string]any{
		"name": "Titra",
		"url":  s.baseURL,
		"hook_attributes": map[string]any{
			"url":    s.baseURL + "/github/webhook",
			"active": true,
		},
		"redirect_url": s.baseURL + "/github/app/created",
		"public":       false,
		"default_permissions": map[string]string{
			"checks":        "write", // post Check Runs with the verdict
			"pull_requests": "read",
			"contents":      "read",
			"metadata":      "read",
		},
		"default_events": []string{"pull_request", "issues", "check_suite"},
	}
	raw, _ := json.Marshal(manifest)

	target := "https://github.com/settings/apps/new?state=" + state
	if org := r.URL.Query().Get("org"); org != "" {
		target = "https://github.com/organizations/" + html.EscapeString(org) + "/settings/apps/new?state=" + state
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><body>
<form id="f" action="%s" method="post">
  <input type="hidden" name="manifest" value='%s'>
</form>
<script>document.getElementById('f').submit()</script>
<p>Redirecting to GitHub…</p>`, target, html.EscapeString(string(raw)))
}

// handleAppCreated receives GitHub's redirect after App creation and exchanges
// the temporary code for the App's permanent credentials.
func (s *Server) handleAppCreated(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("titra_manifest_state"); err != nil || c.Value != r.URL.Query().Get("state") {
		http.Error(w, "bad manifest state", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	app, err := convertManifest(code)
	if err != nil {
		http.Error(w, "app creation failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if err := s.store.Config().SetApp(app); err != nil {
		http.Error(w, "saving app failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/setup", http.StatusFound)
}

// handleInstall sends the admin to the App's installation page.
func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	app := s.store.Config().AppInfo()
	if app == nil {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	http.Redirect(w, r, "https://github.com/apps/"+app.Slug+"/installations/new", http.StatusFound)
}

// handleWebhook receives GitHub events and tracks installs. The payload is
// verified against the App's webhook secret.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	app := s.store.Config().AppInfo()
	if app != nil && !verifyWebhook(app.WebhookSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}

	switch r.Header.Get("X-GitHub-Event") {
	case "installation":
		var ev struct {
			Action       string `json:"action"`
			Installation struct {
				ID      int64 `json:"id"`
				Account struct {
					Login string `json:"login"`
					Type  string `json:"type"`
				} `json:"account"`
			} `json:"installation"`
		}
		if json.Unmarshal(body, &ev) == nil {
			cfg := s.store.Config()
			switch ev.Action {
			case "created", "new_permissions_accepted":
				_ = cfg.AddInstallation(&Installation{
					ID: ev.Installation.ID, Account: ev.Installation.Account.Login,
					AccountType: ev.Installation.Account.Type, CreatedAt: time.Now().UTC(),
				})
				// A fresh project token per installed account.
				if !hasProjectNamed(cfg, ev.Installation.Account.Login) {
					cfg.NewProject(ev.Installation.Account.Login)
				}
			case "deleted":
				_ = cfg.RemoveInstallation(ev.Installation.Account.Login)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func hasProjectNamed(cfg *Config, name string) bool {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	for _, p := range cfg.Projects {
		if p.Name == name {
			return true
		}
	}
	return false
}

// page wraps body HTML in the shared setup chrome.
func page(w io.Writer, title, body string) {
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title><link rel="stylesheet" href="/static/app.css"></head>
<body class="setup"><main class="card">%s</main></body></html>`, html.EscapeString(title), body)
}
