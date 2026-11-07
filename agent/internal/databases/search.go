package databases

// Each account can run its own Meilisearch for Laravel Scout and other apps:
// a systemd instance running as the account's user, listening on a local port
// with a master key, data in the account's home. Other accounts can reach the
// port but not without the key.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
)

// SearchInstance is an account's Meilisearch.
type SearchInstance struct {
	Port      int       `json:"port"`
	MemoryMB  int       `json:"memory_mb"` // indexing memory
	CreatedAt time.Time `json:"created_at"`
}

// SearchRequest creates or changes it.
type SearchRequest struct {
	MemoryMB int `json:"memory_mb"`
}

const searchPortStart = 7700

func (m *Manager) searchEnv(account string) string {
	return filepath.Join("/etc/ike/meilisearch", account+".env")
}

func (m *Manager) searchDir(account string) string {
	return filepath.Join(m.env.Home(account), ".meilisearch")
}

// SearchInstalled reports whether Meilisearch is on this server.
func (m *Manager) SearchInstalled() bool { return m.env.Installed("/usr/local/bin/meilisearch") }

// freeSearchPort picks a port no other account's instance uses.
func (m *Manager) freeSearchPort() (int, error) {
	used := map[int]bool{}
	files, _ := filepath.Glob(filepath.Join(m.env.P(m.env.Cfg.DataDir), "databases", "*.json"))
	for _, f := range files {
		var a AccountDBs
		if kit.ReadJSON(f, &a) == nil && a.Search != nil {
			used[a.Search.Port] = true
		}
	}
	for p := searchPortStart; p < searchPortStart+2000; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port for Meilisearch")
}

func renderSearchEnv(dir, key string, port, memMB int) string {
	var b strings.Builder
	b.WriteString("# Written by Ike. Change it in the panel under Databases → Search.\n")
	fmt.Fprintf(&b, "MEILI_HTTP_ADDR=127.0.0.1:%d\nMEILI_MASTER_KEY=%s\nMEILI_ENV=production\nMEILI_NO_ANALYTICS=true\n", port, key)
	fmt.Fprintf(&b, "MEILI_DB_PATH=%s/data\nMEILI_DUMP_DIR=%s/dumps\nMEILI_SNAPSHOT_DIR=%s/snapshots\n", dir, dir, dir)
	fmt.Fprintf(&b, "MEILI_MAX_INDEXING_MEMORY=%dMb\nMEILI_MAX_INDEXING_THREADS=1\nMEILI_HTTP_PAYLOAD_SIZE_LIMIT=50Mb\n", memMB)
	return b.String()
}

func readMasterKey(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "MEILI_MASTER_KEY="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// SetSearch creates the account's Meilisearch or changes its memory. The
// master key is returned when it is made or reset.
func (m *Manager) SetSearch(ctx context.Context, account string, r SearchRequest, newKey bool) (*Credentials, *SearchInstance, error) {
	if !m.SearchInstalled() {
		return nil, nil, fmt.Errorf("Meilisearch %w", kit.ErrNotInstalled)
	}
	if r.MemoryMB == 0 {
		r.MemoryMB = 256
	}
	if r.MemoryMB < 64 || r.MemoryMB > 16384 {
		return nil, nil, fmt.Errorf("indexing memory must be 64-16384 MB")
	}
	unlock := m.lock(account)
	defer unlock()
	a, err := m.load(account)
	if err != nil {
		return nil, nil, err
	}
	envPath := m.env.P(m.searchEnv(account))
	key := ""
	if a.Search != nil && !newKey {
		key = readMasterKey(envPath)
	}
	if key == "" {
		key = kit.RandomHex(24)
		newKey = true
	}
	inst := &SearchInstance{MemoryMB: r.MemoryMB, CreatedAt: kit.Now()}
	created := a.Search == nil
	if created {
		if inst.Port, err = m.freeSearchPort(); err != nil {
			return nil, nil, err
		}
	} else {
		inst.Port, inst.CreatedAt = a.Search.Port, a.Search.CreatedAt
	}
	dir := m.searchDir(account)
	if _, err := m.env.Run.Run(ctx, "install", "-d", "-m", "0700", "-o", account, "-g", account, dir); err != nil {
		return nil, nil, err
	}
	if err := kit.WriteFile(envPath, []byte(renderSearchEnv(dir, key, inst.Port, r.MemoryMB)), 0o600); err != nil {
		return nil, nil, err
	}
	unit := "ike-meilisearch@" + account + ".service"
	cmd := []string{"restart", unit}
	if created {
		cmd = []string{"enable", "--now", unit}
	}
	if _, err := m.env.Run.Run(ctx, "systemctl", cmd...); err != nil {
		return nil, nil, fmt.Errorf("Meilisearch did not start: %w", err)
	}
	a.Search = inst
	if err := m.save(a); err != nil {
		return nil, nil, err
	}
	if !newKey {
		return nil, inst, nil
	}
	host := "http://127.0.0.1:" + strconv.Itoa(inst.Port)
	return &Credentials{Engine: "meilisearch", Password: key, Host: host, Port: inst.Port, URL: host}, inst, nil
}

// RemoveSearch stops it and deletes its data.
func (m *Manager) RemoveSearch(ctx context.Context, account string) error {
	a, err := m.load(account)
	if err != nil {
		return err
	}
	if a.Search == nil {
		return fmt.Errorf("Meilisearch for %s %w", account, kit.ErrNotFound)
	}
	_, _ = m.env.Run.Run(ctx, "systemctl", "disable", "--now", "ike-meilisearch@"+account+".service")
	_ = os.Remove(m.env.P(m.searchEnv(account)))
	_, _ = m.env.Run.Run(ctx, "rm", "-rf", "--one-file-system", m.searchDir(account))
	a.Search = nil
	return m.save(a)
}

// SearchStatus is live state from the instance.
type SearchStatus struct {
	Running bool   `json:"running"`
	Version string `json:"version,omitempty"`
	Indexes int    `json:"indexes"`
	Docs    int    `json:"documents"`
	SizeMB  int    `json:"size_mb"`
	Port    int    `json:"port"`
}

// SearchHTTP is replaced in tests.
var SearchHTTP = &http.Client{Timeout: 5 * time.Second}

func (m *Manager) SearchStatus(ctx context.Context, account string) (*SearchStatus, error) {
	a, err := m.load(account)
	if err != nil {
		return nil, err
	}
	if a.Search == nil {
		return nil, fmt.Errorf("Meilisearch for %s %w", account, kit.ErrNotFound)
	}
	st := &SearchStatus{Port: a.Search.Port}
	key := readMasterKey(m.env.P(m.searchEnv(account)))
	get := func(path string, out any) error {
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:"+strconv.Itoa(a.Search.Port)+path, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := SearchHTTP.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
	var v struct {
		PkgVersion string `json:"pkgVersion"`
	}
	if get("/version", &v) != nil {
		return st, nil
	}
	st.Running, st.Version = true, v.PkgVersion
	var s struct {
		DatabaseSize int64 `json:"databaseSize"`
		Indexes      map[string]struct {
			NumberOfDocuments int `json:"numberOfDocuments"`
		} `json:"indexes"`
	}
	if get("/stats", &s) == nil {
		st.SizeMB = int(s.DatabaseSize >> 20)
		st.Indexes = len(s.Indexes)
		for _, ix := range s.Indexes {
			st.Docs += ix.NumberOfDocuments
		}
	}
	return st, nil
}
