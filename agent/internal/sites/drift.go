package sites

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"text/template"
)

// Drift is a managed config file whose content no longer matches what Ike
// would write, usually because someone edited it by hand.
type Drift struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`  // nginx | apache | php-pool | app-unit | app-script | cron | slice
	Owner string `json:"owner"` // domain or account
	State string `json:"state"` // changed | missing
	Diff  string `json:"diff,omitempty"`
}

type expectedFile struct {
	path, kind, owner string
	body              []byte
	mode              os.FileMode
}

func renderBytes(t *template.Template, data any) []byte {
	var b bytes.Buffer
	_ = t.Execute(&b, data)
	return b.Bytes()
}

// expected lists every file Ike manages with the content it should have.
func (m *Manager) expected() []expectedFile {
	var out []expectedFile
	sites, _ := m.List()
	for i := range sites {
		s := &sites[i]
		L := m.layout(s)
		if L.usesNginx() {
			out = append(out, expectedFile{L.VhostFile, "nginx", s.Domain, renderBytes(nginxTmpl, L), 0o644})
		}
		if L.usesApache() {
			out = append(out, expectedFile{L.ApacheFile, "apache", s.Domain, renderBytes(apacheTmpl, L), 0o644})
		}
		if s.Runtime == RuntimePHP {
			out = append(out, expectedFile{L.PoolFile, "php-pool", s.Domain, renderBytes(poolTmpl, L), 0o644})
		}
		if L.IsApp {
			out = append(out, expectedFile{L.UnitFile, "app-unit", s.Domain, renderBytes(unitTmpl, L), 0o644},
				expectedFile{L.StartScript, "app-script", s.Domain, renderBytes(startScriptTmpl, L), 0o755})
		}
	}
	accts, _ := m.Accounts()
	for _, a := range accts {
		if body := m.cronBody(&a); body != nil {
			out = append(out, expectedFile{strings.TrimPrefix(m.cronFile(a.Name), m.cfg.Root), "cron", a.Name, body, 0o644})
		}
	}
	return out
}

// DetectDrift compares managed files with what Ike would write.
func (m *Manager) DetectDrift() []Drift {
	m.mu.Lock()
	defer m.mu.Unlock()
	drifts := []Drift{}
	for _, e := range m.expected() {
		cur, err := os.ReadFile(m.p(e.path))
		switch {
		case err != nil:
			drifts = append(drifts, Drift{Path: e.path, Kind: e.kind, Owner: e.owner, State: "missing"})
		case !bytes.Equal(cur, e.body):
			drifts = append(drifts, Drift{Path: e.path, Kind: e.kind, Owner: e.owner, State: "changed", Diff: lineDiff(string(e.body), string(cur))})
		}
	}
	return drifts
}

// Reconcile rewrites drifted files (all, or only those in paths) and reloads
// the affected services. Web server configs are tested before reloading; if a
// test fails, the previous content is put back.
func (m *Manager) Reconcile(ctx context.Context, paths []string) ([]Drift, error) {
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fixed := []Drift{}
	reload := map[string]bool{}
	var restartUnits []string
	type backup struct {
		path string
		old  []byte
		had  bool
		mode os.FileMode
	}
	var backups []backup
	for _, e := range m.expected() {
		if len(want) > 0 && !want[e.path] {
			continue
		}
		cur, err := os.ReadFile(m.p(e.path))
		if err == nil && bytes.Equal(cur, e.body) {
			continue
		}
		backups = append(backups, backup{e.path, cur, err == nil, e.mode})
		if err := writeFile(m.p(e.path), e.body, e.mode); err != nil {
			return fixed, err
		}
		state := "changed"
		if err != nil {
			state = "missing"
		}
		fixed = append(fixed, Drift{Path: e.path, Kind: e.kind, Owner: e.owner, State: state})
		switch e.kind {
		case "nginx":
			reload["nginx"] = true
		case "apache":
			reload["apache"] = true
		case "php-pool":
			for _, v := range strings.Split(e.path, "/") {
				if phpVersionRe.MatchString(v) {
					reload["php"+v+"-fpm"] = true
				}
			}
		case "app-unit", "app-script":
			reload["daemon"] = true
			restartUnits = append(restartUnits, "ike-app-"+strings.TrimSuffix(strings.TrimPrefix(lastPart(e.path), "ike-app-"), ".service"))
		}
	}
	restore := func() {
		for _, b := range backups {
			if b.had {
				_ = writeFile(m.p(b.path), b.old, b.mode)
			}
		}
	}
	if reload["nginx"] {
		if _, err := m.run.Run(ctx, "nginx", "-t"); err != nil {
			restore()
			return nil, fmt.Errorf("Nginx rejected the restored config, nothing was changed: %w", err)
		}
		_, _ = m.run.Run(ctx, "systemctl", "reload", "nginx")
	}
	if reload["apache"] {
		if _, err := m.run.Run(ctx, "apache2ctl", "configtest"); err != nil {
			restore()
			return nil, fmt.Errorf("Apache rejected the restored config, nothing was changed: %w", err)
		}
		_, _ = m.run.Run(ctx, "systemctl", "reload", "apache2")
	}
	for k := range reload {
		if strings.HasPrefix(k, "php") {
			_, _ = m.run.Run(ctx, "systemctl", "reload", k)
		}
	}
	if reload["daemon"] {
		_, _ = m.run.Run(ctx, "systemctl", "daemon-reload")
	}
	seen := map[string]bool{}
	for _, u := range restartUnits {
		u = strings.TrimSuffix(u, ".sh")
		if !seen[u] {
			seen[u] = true
			_, _ = m.run.Run(ctx, "systemctl", "restart", u)
		}
	}
	return fixed, nil
}

func lastPart(p string) string {
	i := strings.LastIndex(p, "/")
	return p[i+1:]
}

// lineDiff shows lines Ike expects (-) and lines found on disk (+). Small and
// readable, not a full diff algorithm: enough to see what someone edited.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	inW, inG := map[string]int{}, map[string]int{}
	for _, l := range w {
		inW[l]++
	}
	for _, l := range g {
		inG[l]++
	}
	var b strings.Builder
	n := 0
	for _, l := range w {
		if inG[l] == 0 && strings.TrimSpace(l) != "" {
			b.WriteString("- " + l + "\n")
			n++
		}
	}
	for _, l := range g {
		if inW[l] == 0 && strings.TrimSpace(l) != "" {
			b.WriteString("+ " + l + "\n")
			n++
		}
		if n > 60 {
			b.WriteString("…\n")
			break
		}
	}
	return b.String()
}
