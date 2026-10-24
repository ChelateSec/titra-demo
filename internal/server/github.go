package server

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// This file implements the GitHub App integration with the standard library
// only: the App JWT (RS256), the one-click manifest conversion that creates the
// App, installation access tokens, Check Runs, and webhook signature checks.

const githubAPI = "https://api.github.com"

// convertManifest exchanges the temporary code GitHub returns after App creation
// for the App's permanent credentials. This is what makes "one-click install"
// real: the admin approves a manifest, GitHub creates the App, and the server
// receives its id, keys and secrets here.
func convertManifest(code string) (*GitHubApp, error) {
	req, _ := http.NewRequest(http.MethodPost, githubAPI+"/app-manifests/"+code+"/conversions", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("manifest conversion: %s: %s", resp.Status, body)
	}
	var out struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		Name          string `json:"name"`
		HTMLURL       string `json:"html_url"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		WebhookSecret string `json:"webhook_secret"`
		PEM           string `json:"pem"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &GitHubApp{
		ID: out.ID, Slug: out.Slug, Name: out.Name, HTMLURL: out.HTMLURL,
		ClientID: out.ClientID, ClientSecret: out.ClientSecret,
		WebhookSecret: out.WebhookSecret, PEM: out.PEM,
	}, nil
}

// appJWT builds a short-lived RS256 JWT identifying the App, used to request
// installation tokens. Hand-rolled so Titra keeps zero third-party deps.
func appJWT(app *GitHubApp) (string, error) {
	key, err := parseRSAKey(app.PEM)
	if err != nil {
		return "", err
	}
	now := time.Now().Add(-30 * time.Second)
	header := b64json(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims := b64json(map[string]any{
		"iat": now.Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": app.ID,
	})
	signing := header + "." + claims
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// installationToken mints a short-lived token scoped to one installation, used
// to act as the App on that org's repos (e.g. post a Check Run).
func (c *Config) installationToken(installationID int64) (string, error) {
	app := c.AppInfo()
	if app == nil {
		return "", fmt.Errorf("github app not configured")
	}
	jwt, err := appJWT(app)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", githubAPI, installationID)
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("installation token: %s: %s", resp.Status, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

// CheckRun posts a Check Run to a repo at headSHA summarizing Titra's verdict —
// the result shows up on the PR. Best-effort; returns an error for logging only.
func (c *Config) CheckRun(installationID int64, repo, headSHA, conclusion, title, summary string) error {
	token, err := c.installationToken(installationID)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"name":         "Titra",
		"head_sha":     headSHA,
		"status":       "completed",
		"conclusion":   conclusion, // success | failure | neutral
		"output":       map[string]string{"title": title, "summary": summary},
		"completed_at": time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/repos/%s/check-runs", githubAPI, repo)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("check run: %s: %s", resp.Status, b)
	}
	return nil
}

// verifyWebhook checks the X-Hub-Signature-256 HMAC over the raw body.
func verifyWebhook(secret string, sigHeader string, body []byte) bool {
	if secret == "" || !strings.HasPrefix(sigHeader, "sha256=") {
		return false
	}
	want := strings.TrimPrefix(sigHeader, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	got := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(got), []byte(want))
}

func parseRSAKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM private key")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	anyKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("unsupported private key: %w", err)
	}
	k, ok := anyKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}
	return k, nil
}

func b64json(v any) string {
	raw, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(raw)
}
