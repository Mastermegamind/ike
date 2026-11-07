// Package objects gives each hosting account S3-compatible storage on this
// server, using Garage. Every account gets one access key that can reach only
// its own buckets (named <account>-<name>), each with a size quota. Garage's
// S3 endpoint is served through an Ike website (s3.example.com) so apps and
// tools use it like any S3 service, with path-style addresses.
package objects

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
	"github.com/Mastermegamind/ike/agent/internal/validate"
)

const (
	garageBinary = "/usr/local/bin/garage"
	adminURL     = "http://127.0.0.1:3903"
	s3Port       = 3900
	tokenFile    = "/etc/ike/garage/admin.token"
	Region       = "garage"
)

var bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,40}[a-z0-9]$`)

// Bucket is one of an account's buckets.
type Bucket struct {
	Name      string    `json:"name"` // full name: <account>-<short>
	ID        string    `json:"id"`
	QuotaMB   int       `json:"quota_mb"`
	CreatedAt time.Time `json:"created_at"`
	// Usage, filled when listing.
	UsedMB  float64 `json:"used_mb"`
	Objects int     `json:"objects"`
}

type accountState struct {
	KeyID   string   `json:"key_id,omitempty"`
	Secret  string   `json:"secret,omitempty"` // sealed
	Buckets []Bucket `json:"buckets"`
}

// Settings say where the S3 endpoint is served.
type Settings struct {
	Domain  string `json:"domain,omitempty"`  // s3.example.com
	Account string `json:"account,omitempty"` // owner of its website
}

// Admin talks to Garage's admin API (v2). Replaced in tests.
type Admin interface {
	Call(ctx context.Context, method, endpoint string, query url.Values, body, out any) error
}

// Manager keeps accounts' keys and buckets.
type Manager struct {
	env   *kit.Env
	keys  *secrets.Keyring
	mu    sync.Mutex
	set   kit.Store[Settings]
	Admin Admin
	// ProxySite makes the website that serves the S3 endpoint (as for LiveKit).
	ProxySite func(ctx context.Context, account, domain string, port int) (string, error)
}

func New(env *kit.Env, keys *secrets.Keyring) *Manager {
	m := &Manager{env: env, keys: keys}
	m.set.Path = env.P(filepath.Join(env.Cfg.DataDir, "objects", "settings.json"))
	m.Admin = &httpAdmin{base: adminURL, tokenPath: env.P(tokenFile), client: &http.Client{Timeout: 20 * time.Second}}
	if keys != nil {
		keys.Register("objects", m.reseal)
	}
	return m
}

// Installed reports whether Garage is on this server.
func (m *Manager) Installed() bool { return m.env.Installed(garageBinary) }

func (m *Manager) stateFile(account string) string {
	return m.env.P(filepath.Join(m.env.Cfg.DataDir, "objects", account+".json"))
}

func (m *Manager) load(account string) (*accountState, error) {
	if err := m.env.CheckAccount(account); err != nil {
		return nil, err
	}
	st := &accountState{}
	if err := kit.ReadJSON(m.stateFile(account), st); err != nil && !errors.Is(err, kit.ErrNotFound) {
		return nil, err
	}
	if st.Buckets == nil {
		st.Buckets = []Bucket{}
	}
	return st, nil
}

func (m *Manager) save(account string, st *accountState) error {
	return kit.WriteJSON(m.stateFile(account), st)
}

func (m *Manager) reseal(open, seal func(string) (string, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	files, _ := filepath.Glob(filepath.Join(m.env.P(m.env.Cfg.DataDir), "objects", "*.json"))
	for _, f := range files {
		if filepath.Base(f) == "settings.json" {
			continue
		}
		var st accountState
		if kit.ReadJSON(f, &st) != nil || st.Secret == "" {
			continue
		}
		p, err := open(st.Secret)
		if err != nil {
			return err
		}
		if st.Secret, err = seal(p); err != nil {
			return err
		}
		if err := kit.WriteJSON(f, st); err != nil {
			return err
		}
	}
	return nil
}

// Endpoint is the S3 address apps use.
func (m *Manager) Endpoint() string {
	s, _ := m.set.Load()
	if s.Domain == "" {
		return ""
	}
	return "https://" + s.Domain
}

// Credentials are an account's S3 login; the secret is shown once.
type Credentials struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key,omitempty"`
	PathStyle bool   `json:"path_style"`
}

// View is an account's storage.
type View struct {
	Installed bool     `json:"installed"`
	Endpoint  string   `json:"endpoint"`
	Region    string   `json:"region"`
	AccessKey string   `json:"access_key,omitempty"`
	Buckets   []Bucket `json:"buckets"`
}

// Get lists an account's buckets with their usage.
func (m *Manager) Get(ctx context.Context, account string) (*View, error) {
	st, err := m.load(account)
	if err != nil {
		return nil, err
	}
	v := &View{Installed: m.Installed(), Endpoint: m.Endpoint(), Region: Region, AccessKey: st.KeyID, Buckets: st.Buckets}
	for i := range v.Buckets {
		var info struct {
			Bytes   int64 `json:"bytes"`
			Objects int   `json:"objects"`
		}
		if m.Admin.Call(ctx, "GET", "GetBucketInfo", url.Values{"id": {v.Buckets[i].ID}}, nil, &info) == nil {
			v.Buckets[i].UsedMB = float64(info.Bytes*10>>20) / 10
			v.Buckets[i].Objects = info.Objects
		}
	}
	return v, nil
}

// ensureKey makes the account's access key the first time.
func (m *Manager) ensureKey(ctx context.Context, account string, st *accountState) (*Credentials, error) {
	if st.KeyID != "" {
		return nil, nil
	}
	var k struct {
		ID     string `json:"accessKeyId"`
		Secret string `json:"secretAccessKey"`
	}
	if err := m.Admin.Call(ctx, "POST", "CreateKey", nil, map[string]any{"name": "ike-" + account}, &k); err != nil {
		return nil, fmt.Errorf("Garage didn't make a key: %w", err)
	}
	if k.ID == "" || k.Secret == "" {
		return nil, fmt.Errorf("Garage returned an empty key")
	}
	sealed, err := m.seal(k.Secret)
	if err != nil {
		return nil, err
	}
	st.KeyID, st.Secret = k.ID, sealed
	return &Credentials{Endpoint: m.Endpoint(), Region: Region, AccessKey: k.ID, SecretKey: k.Secret, PathStyle: true}, nil
}

func (m *Manager) seal(v string) (string, error) {
	if m.keys == nil {
		return v, nil
	}
	return m.keys.Seal(v)
}

// CreateBucket adds a bucket for an account (and its key, the first time,
// returned with the secret).
func (m *Manager) CreateBucket(ctx context.Context, account, short string, quotaMB int) (*Bucket, *Credentials, error) {
	if !m.Installed() {
		return nil, nil, fmt.Errorf("S3 storage (Garage) %w", kit.ErrNotInstalled)
	}
	short = strings.ToLower(strings.TrimSpace(short))
	name := account + "-" + short
	if !bucketRe.MatchString(name) || len(name) > 63 {
		return nil, nil, fmt.Errorf("bucket names are lowercase letters, digits and dashes (the account name comes first)")
	}
	if quotaMB < 0 || quotaMB > 10_000_000 {
		return nil, nil, fmt.Errorf("the quota is 0 (none) or megabytes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.load(account)
	if err != nil {
		return nil, nil, err
	}
	for _, b := range st.Buckets {
		if b.Name == name {
			return nil, nil, fmt.Errorf("bucket %s %w", name, kit.ErrExists)
		}
	}
	if len(st.Buckets) >= 50 {
		return nil, nil, fmt.Errorf("at most 50 buckets per account")
	}
	cred, err := m.ensureKey(ctx, account, st)
	if err != nil {
		return nil, nil, err
	}
	if cred != nil {
		if err := m.save(account, st); err != nil { // keep the key even if the bucket fails
			return nil, nil, err
		}
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := m.Admin.Call(ctx, "POST", "CreateBucket", nil, map[string]any{"globalAlias": name}, &created); err != nil {
		return nil, nil, fmt.Errorf("Garage didn't make the bucket: %w", err)
	}
	b := Bucket{Name: name, ID: created.ID, QuotaMB: quotaMB, CreatedAt: kit.Now()}
	if err := m.Admin.Call(ctx, "POST", "AllowBucketKey", nil, map[string]any{"bucketId": b.ID, "accessKeyId": st.KeyID,
		"permissions": map[string]bool{"read": true, "write": true, "owner": true}}, nil); err != nil {
		_ = m.Admin.Call(ctx, "POST", "DeleteBucket", url.Values{"id": {b.ID}}, nil, nil)
		return nil, nil, fmt.Errorf("Garage didn't give the key access: %w", err)
	}
	if err := m.setQuota(ctx, b.ID, quotaMB); err != nil {
		return nil, nil, err
	}
	st.Buckets = append(st.Buckets, b)
	if err := m.save(account, st); err != nil {
		return nil, nil, err
	}
	m.env.Publish("storage.bucket.created", name, map[string]any{"account": account})
	return &b, cred, nil
}

func (m *Manager) setQuota(ctx context.Context, id string, quotaMB int) error {
	var max any
	if quotaMB > 0 {
		max = int64(quotaMB) << 20
	}
	if err := m.Admin.Call(ctx, "POST", "UpdateBucket", url.Values{"id": {id}}, map[string]any{"quotas": map[string]any{"maxSize": max, "maxObjects": nil}}, nil); err != nil {
		return fmt.Errorf("Garage didn't set the quota: %w", err)
	}
	return nil
}

// SetQuota changes a bucket's size limit (0 = none).
func (m *Manager) SetQuota(ctx context.Context, account, name string, quotaMB int) (*Bucket, error) {
	if quotaMB < 0 || quotaMB > 10_000_000 {
		return nil, fmt.Errorf("the quota is 0 (none) or megabytes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.load(account)
	if err != nil {
		return nil, err
	}
	for i := range st.Buckets {
		if st.Buckets[i].Name == name {
			if err := m.setQuota(ctx, st.Buckets[i].ID, quotaMB); err != nil {
				return nil, err
			}
			st.Buckets[i].QuotaMB = quotaMB
			return &st.Buckets[i], m.save(account, st)
		}
	}
	return nil, fmt.Errorf("bucket %s %w", name, kit.ErrNotFound)
}

// DeleteBucket removes an empty bucket.
func (m *Manager) DeleteBucket(ctx context.Context, account, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.load(account)
	if err != nil {
		return err
	}
	for i, b := range st.Buckets {
		if b.Name != name {
			continue
		}
		if err := m.Admin.Call(ctx, "POST", "DeleteBucket", url.Values{"id": {b.ID}}, nil, nil); err != nil {
			return fmt.Errorf("Garage didn't delete it (empty it first): %w", err)
		}
		st.Buckets = append(st.Buckets[:i], st.Buckets[i+1:]...)
		m.env.Publish("storage.bucket.deleted", name, map[string]any{"account": account})
		return m.save(account, st)
	}
	return fmt.Errorf("bucket %s %w", name, kit.ErrNotFound)
}

// NewKey replaces the account's access key: a new key gets its buckets,
// then the old key is deleted. Garage can't change a key's secret.
func (m *Manager) NewKey(ctx context.Context, account string) (*Credentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.load(account)
	if err != nil {
		return nil, err
	}
	old := st.KeyID
	st.KeyID, st.Secret = "", ""
	cred, err := m.ensureKey(ctx, account, st)
	if err != nil {
		return nil, err
	}
	for _, b := range st.Buckets {
		if err := m.Admin.Call(ctx, "POST", "AllowBucketKey", nil, map[string]any{"bucketId": b.ID, "accessKeyId": st.KeyID,
			"permissions": map[string]bool{"read": true, "write": true, "owner": true}}, nil); err != nil {
			_ = m.Admin.Call(ctx, "POST", "DeleteKey", url.Values{"id": {st.KeyID}}, nil, nil)
			return nil, fmt.Errorf("Garage didn't give the new key access; the old key still works: %w", err)
		}
	}
	if err := m.save(account, st); err != nil {
		return nil, err
	}
	if old != "" {
		_ = m.Admin.Call(ctx, "POST", "DeleteKey", url.Values{"id": {old}}, nil, nil)
	}
	return cred, nil
}

// DeleteAccount revokes the account's key and deletes its empty buckets;
// buckets with objects are kept for the admin to look at.
func (m *Manager) DeleteAccount(ctx context.Context, account string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.load(account)
	if err != nil || (st.KeyID == "" && len(st.Buckets) == 0) {
		return
	}
	if st.KeyID != "" {
		_ = m.Admin.Call(ctx, "POST", "DeleteKey", url.Values{"id": {st.KeyID}}, nil, nil)
	}
	for _, b := range st.Buckets {
		_ = m.Admin.Call(ctx, "POST", "DeleteBucket", url.Values{"id": {b.ID}}, nil, nil)
	}
	_ = os.Remove(m.stateFile(account))
}

// GetSettings / SetSettings: where the S3 endpoint is served.
func (m *Manager) GetSettings() Settings { s, _ := m.set.Load(); return s }

func (m *Manager) SetSettings(ctx context.Context, s Settings) (Settings, string, error) {
	if !m.Installed() {
		return s, "", fmt.Errorf("S3 storage (Garage) %w; add it with install.sh --add garage", kit.ErrNotInstalled)
	}
	s.Domain = strings.ToLower(strings.TrimSpace(s.Domain))
	if err := validate.Domain(s.Domain); err != nil {
		return s, "", fmt.Errorf("S3 address: %w", err)
	}
	if err := m.env.CheckAccount(s.Account); err != nil {
		return s, "", fmt.Errorf("the account that owns the S3 website: %w", err)
	}
	note := ""
	if m.ProxySite != nil {
		n, err := m.ProxySite(ctx, s.Account, s.Domain, s3Port)
		if err != nil {
			return s, "", err
		}
		note = n
	}
	_, err := m.set.Update(func(v *Settings) error { *v = s; return nil })
	return s, note, err
}

// ---------------------------------------------------------------- admin API

type httpAdmin struct {
	base, tokenPath string
	client          *http.Client
}

func (h *httpAdmin) Call(ctx context.Context, method, endpoint string, query url.Values, body, out any) error {
	tok, err := os.ReadFile(h.tokenPath)
	if err != nil {
		return fmt.Errorf("Garage's admin token is missing (%s)", h.tokenPath)
	}
	u := h.base + "/v2/" + endpoint
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, u, rd)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Message != "" {
			return errors.New(e.Message)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
