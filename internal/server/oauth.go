package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// User is a logged-in dashboard user from GitHub OAuth.
type User struct {
	Login  string `json:"login"`
	Name   string `json:"name"`
	Avatar string `json:"avatar_url"`
}

// sessions is an in-memory session store. Fine for a pilot; swap for signed
// cookies or a shared store when you run more than one instance.
type sessions struct {
	mu sync.RWMutex
	m  map[string]sessionEntry
}

type sessionEntry struct {
	user    User
	created time.Time
}

func newSessions() *sessions { return &sessions{m: map[string]sessionEntry{}} }

func (s *sessions) create(u User) string {
	id := randHex(24)
	s.mu.Lock()
	s.m[id] = sessionEntry{user: u, created: time.Now()}
	s.mu.Unlock()
	return id
}

func (s *sessions) get(id string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[id]
	return e.user, ok
}

func (s *sessions) delete(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

const sessionCookie = "titra_session"

// currentUser returns the logged-in user, if any.
func (srv *Server) currentUser(r *http.Request) (User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return User{}, false
	}
	return srv.sess.get(c.Value)
}

// authed reports whether the request may view the dashboard. When the App is not
// configured (local/dev), the dashboard is open so the demo needs no login.
func (srv *Server) authed(r *http.Request) bool {
	if !srv.store.Config().AppConfigured() {
		return true
	}
	_, ok := srv.currentUser(r)
	return ok
}

// handleLogin starts the GitHub OAuth flow.
func (srv *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	app := srv.store.Config().AppInfo()
	if app == nil || app.ClientID == "" {
		http.Error(w, "github app not configured", http.StatusServiceUnavailable)
		return
	}
	state := randHex(12)
	http.SetCookie(w, &http.Cookie{Name: "titra_oauth_state", Value: state, Path: "/", HttpOnly: true, MaxAge: 600, SameSite: http.SameSiteLaxMode})
	q := url.Values{}
	q.Set("client_id", app.ClientID)
	q.Set("redirect_uri", srv.baseURL+"/auth/callback")
	q.Set("state", state)
	http.Redirect(w, r, "https://github.com/login/oauth/authorize?"+q.Encode(), http.StatusFound)
}

// handleAuthCallback completes the OAuth flow and starts a session.
func (srv *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("titra_oauth_state"); err != nil || c.Value != r.URL.Query().Get("state") {
		http.Error(w, "bad oauth state", http.StatusBadRequest)
		return
	}
	app := srv.store.Config().AppInfo()
	code := r.URL.Query().Get("code")
	token, err := srv.exchangeOAuthCode(app, code)
	if err != nil {
		http.Error(w, "oauth exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	user, err := githubUser(token)
	if err != nil {
		http.Error(w, "fetching user failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	id := srv.sess.create(user)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, MaxAge: 7 * 24 * 3600, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (srv *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		srv.sess.delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (srv *Server) exchangeOAuthCode(app *GitHubApp, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", app.ClientID)
	form.Set("client_secret", app.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", srv.baseURL+"/auth/callback")
	req, _ := http.NewRequest(http.MethodPost, "https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.AccessToken, nil
}

func githubUser(token string) (User, error) {
	req, _ := http.NewRequest(http.MethodGet, githubAPI+"/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return User{}, err
	}
	defer resp.Body.Close()
	var u User
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &u); err != nil {
		return User{}, err
	}
	return u, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
