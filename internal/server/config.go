package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Config is the server's persistent state: the registered GitHub App, the orgs
// that installed it, and the project ingest tokens. Stored as JSON next to the
// action log. Holds secrets (the App private key, webhook secret), so the file
// is written 0600.
type Config struct {
	mu            sync.RWMutex
	App           *GitHubApp               `json:"app,omitempty"`
	Installations map[string]*Installation `json:"installations,omitempty"` // account login -> install
	Projects      map[string]*Project      `json:"projects,omitempty"`      // ingest token -> project
	path          string
}

// GitHubApp holds the credentials GitHub hands back after the App is created
// from a manifest. With these the server can mint installation tokens, post
// Checks, verify webhooks, and run OAuth login.
type GitHubApp struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	HTMLURL       string `json:"html_url"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"` // PKCS#1/PKCS#8 RSA private key
}

// Installation is one org/user that installed the App.
type Installation struct {
	ID          int64     `json:"id"`
	Account     string    `json:"account"`
	AccountType string    `json:"account_type"`
	CreatedAt   time.Time `json:"created_at"`
}

// Project is an ingest identity: a token the guard presents when streaming
// actions, mapped to a display name (usually the org/repo).
type Project struct {
	Token     string    `json:"token"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) loadConfig() error {
	cfg := &Config{
		Installations: map[string]*Installation{},
		Projects:      map[string]*Project{},
		path:          s.cfgPath,
	}
	raw, err := os.ReadFile(s.cfgPath)
	if err == nil {
		if err := json.Unmarshal(raw, cfg); err != nil {
			return err
		}
		if cfg.Installations == nil {
			cfg.Installations = map[string]*Installation{}
		}
		if cfg.Projects == nil {
			cfg.Projects = map[string]*Project{}
		}
		cfg.path = s.cfgPath
	} else if !os.IsNotExist(err) {
		return err
	}
	s.cfg = cfg
	return nil
}

// Config returns the server config.
func (s *Store) Config() *Config { return s.cfg }

func (c *Config) save() error {
	c.mu.RLock()
	raw, err := json.MarshalIndent(c, "", "  ")
	c.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, raw, 0o600)
}

// AppConfigured reports whether the GitHub App has been registered.
func (c *Config) AppConfigured() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.App != nil && c.App.ID != 0
}

// SetApp stores freshly created App credentials.
func (c *Config) SetApp(app *GitHubApp) error {
	c.mu.Lock()
	c.App = app
	c.mu.Unlock()
	return c.save()
}

// AppInfo returns a copy of the App (nil if unconfigured).
func (c *Config) AppInfo() *GitHubApp {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.App == nil {
		return nil
	}
	cp := *c.App
	return &cp
}

// AddInstallation records an org/user install.
func (c *Config) AddInstallation(in *Installation) error {
	c.mu.Lock()
	c.Installations[in.Account] = in
	c.mu.Unlock()
	return c.save()
}

// RemoveInstallation drops an install.
func (c *Config) RemoveInstallation(account string) error {
	c.mu.Lock()
	delete(c.Installations, account)
	c.mu.Unlock()
	return c.save()
}

// ListInstallations returns installs, stable order not guaranteed.
func (c *Config) ListInstallations() []*Installation {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Installation, 0, len(c.Installations))
	for _, in := range c.Installations {
		cp := *in
		out = append(out, &cp)
	}
	return out
}

// NewProject creates and stores a project with a fresh token.
func (c *Config) NewProject(name string) *Project {
	p := &Project{Token: randToken(), Name: name, CreatedAt: time.Now().UTC()}
	c.mu.Lock()
	c.Projects[p.Token] = p
	c.mu.Unlock()
	_ = c.save()
	return p
}

// ProjectByToken returns the project for a token, or nil.
func (c *Config) ProjectByToken(token string) *Project {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Projects[token]
}

// HasProjects reports whether any ingest tokens exist (i.e. ingest is gated).
func (c *Config) HasProjects() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.Projects) > 0
}

func randToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "ttr_" + hex.EncodeToString(b)
}
