// Package server is the Titra control plane: a GitHub App plus a live dashboard.
// The guard in each CI job streams the actions it sees to POST /api/actions; the
// dashboard shows them in real time; the GitHub App lets an org install Titra in
// one click and get Check Runs on their PRs.
//
// It runs in two modes from the same binary:
//   - Local/dev: no GitHub App configured. The dashboard is open (no login) and
//     ingest needs no token. This is the zero-setup demo.
//   - Product: a GitHub App is registered (one click, via the manifest flow).
//     The dashboard requires GitHub login and ingest requires a project token.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/ChelateSec/titra/internal/audit"
)

//go:embed web
var webFS embed.FS

// Server is the HTTP control plane.
type Server struct {
	store   *Store
	sess    *sessions
	baseURL string
	mux     *http.ServeMux
}

// New builds a Server over a store. baseURL is the externally reachable URL
// (used for OAuth and the GitHub App manifest redirect); for a local demo it is
// http://localhost:PORT.
func New(store *Store, baseURL string) *Server {
	s := &Server{
		store:   store,
		sess:    newSessions(),
		baseURL: strings.TrimRight(baseURL, "/"),
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return logRequests(s.mux) }

func (s *Server) routes() {
	// Dashboard + static.
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(mustSub(webFS, "web/static"))))

	// Dashboard JSON API (gated by authed()).
	s.mux.HandleFunc("GET /api/totals", s.guarded(s.apiTotals))
	s.mux.HandleFunc("GET /api/runs", s.guarded(s.apiRuns))
	s.mux.HandleFunc("GET /api/runs/{key}", s.guarded(s.apiRun))
	s.mux.HandleFunc("GET /api/stream", s.guarded(s.apiStream))
	s.mux.HandleFunc("GET /api/me", s.apiMe)

	// Ingest from the guard.
	s.mux.HandleFunc("POST /api/actions", s.apiIngest)

	// GitHub App onboarding + lifecycle.
	s.mux.HandleFunc("GET /setup", s.handleSetup)
	s.mux.HandleFunc("GET /github/app/manifest", s.handleManifest)
	s.mux.HandleFunc("GET /github/app/created", s.handleAppCreated)
	s.mux.HandleFunc("GET /github/app/install", s.handleInstall)
	s.mux.HandleFunc("POST /github/webhook", s.handleWebhook)

	// OAuth login.
	s.mux.HandleFunc("GET /auth/login", s.handleLogin)
	s.mux.HandleFunc("GET /auth/callback", s.handleAuthCallback)
	s.mux.HandleFunc("GET /auth/logout", s.handleLogout)

	// Health.
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
}

// guarded wraps a handler with the dashboard auth check.
func (s *Server) guarded(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	serveFile(w, "web/index.html", "text/html; charset=utf-8")
}

// --- dashboard API ---------------------------------------------------------

func (s *Server) apiTotals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.Totals())
}

func (s *Server) apiRuns(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.Runs())
}

func (s *Server) apiRun(w http.ResponseWriter, r *http.Request) {
	run, acts := s.store.Run(r.PathValue("key"))
	if run == nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"run": run, "actions": acts})
}

func (s *Server) apiMe(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Config()
	resp := map[string]any{
		"app_configured": cfg.AppConfigured(),
		"authed":         s.authed(r),
	}
	if u, ok := s.currentUser(r); ok {
		resp["user"] = u
	}
	writeJSON(w, resp)
}

// apiStream is a Server-Sent Events stream of new actions — the dashboard's
// live feed. A new action anywhere shows up immediately.
func (s *Server) apiStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := s.store.Subscribe()
	defer cancel()

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case a := <-ch:
			raw, _ := json.Marshal(a)
			fmt.Fprintf(w, "event: action\ndata: %s\n\n", raw)
			flusher.Flush()
		}
	}
}

// --- ingest ----------------------------------------------------------------

func (s *Server) apiIngest(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Config()
	// When projects exist, ingest requires a valid token. In dev (no projects),
	// ingest is open so the local demo needs no setup.
	if cfg.HasProjects() {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if cfg.ProjectByToken(tok) == nil {
			http.Error(w, "invalid ingest token", http.StatusUnauthorized)
			return
		}
	}
	var rec audit.Record
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&rec); err != nil {
		http.Error(w, "bad record", http.StatusBadRequest)
		return
	}
	if rec.Tool == "" {
		http.Error(w, "missing tool", http.StatusBadRequest)
		return
	}
	a := s.store.Add(rec)
	writeJSON(w, map[string]any{"id": a.ID})
}

// --- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func serveFile(w http.ResponseWriter, name, ctype string) {
	raw, err := webFS.ReadFile(name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write(raw)
}

func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
	})
}
