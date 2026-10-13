package sites

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// EnvVar is one line of a website's .env file.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// Ike sets these itself for apps; a .env can't override them.
var reservedEnv = map[string]bool{"PORT": true, "HOST": true, "PATH": true, "VIRTUAL_ENV": true}

func (m *Manager) envFile(s *Site) string { return filepath.Join(m.layout(s).SiteDir, ".env") }

// GetEnv reads a website's .env (KEY=value lines; comments and blanks skipped).
// The file lives in the account's folder, so it's opened without following links
// and must belong to the account.
func (m *Manager) GetEnv(domain string) ([]EnvVar, error) {
	s, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	uid := -1
	if !m.cfg.DryRun {
		if uid, err = lookupUID(s.Account); err != nil {
			return nil, err
		}
	}
	text, err := tailFile(m.p(m.envFile(s)), 2000, uid)
	if err != nil {
		return nil, err
	}
	out := []EnvVar{}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		l = strings.TrimPrefix(l, "export ")
		k, v, ok := strings.Cut(l, "=")
		if !ok || !envKeyRe.MatchString(strings.TrimSpace(k)) {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out = append(out, EnvVar{Key: strings.TrimSpace(k), Value: v})
	}
	return out, nil
}

// SetEnv replaces a website's .env. The file is written by the account user
// itself (tee on stdin), so values never appear in the process list and root
// never writes into a folder the user controls. Apps are restarted to pick it up.
func (m *Manager) SetEnv(ctx context.Context, domain string, vars []EnvVar) error {
	if len(vars) > 200 {
		return fmt.Errorf("at most 200 variables")
	}
	var b strings.Builder
	b.WriteString("# Managed in the Ike panel. Edits here are kept, but the panel rewrites this file when you save.\n")
	seen := map[string]bool{}
	for _, v := range vars {
		if !envKeyRe.MatchString(v.Key) {
			return fmt.Errorf("%q isn't a valid variable name (letters, numbers, _; can't start with a number)", v.Key)
		}
		if reservedEnv[v.Key] {
			return fmt.Errorf("%s is set by Ike and can't be changed here", v.Key)
		}
		if seen[v.Key] {
			return fmt.Errorf("%s appears twice", v.Key)
		}
		seen[v.Key] = true
		if strings.ContainsAny(v.Value, "\n\r\x00") || len(v.Value) > 4096 {
			return fmt.Errorf("the value of %s must be one line under 4096 characters", v.Key)
		}
		// Single quotes keep $ and spaces literal when the start script sources the file.
		b.WriteString(v.Key + "='" + strings.ReplaceAll(v.Value, "'", `'\''`) + "'\n")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.Get(domain)
	if err != nil {
		return err
	}
	path := m.envFile(s)
	if _, err := m.run.RunInput(ctx, b.String(), "runuser", "-u", s.Account, "--", "tee", path); err != nil {
		return fmt.Errorf("write .env: %w", err)
	}
	if _, err := m.run.Run(ctx, "runuser", "-u", s.Account, "--", "chmod", "600", path); err != nil {
		return err
	}
	if m.cfg.DryRun {
		_ = writeFile(m.p(path), []byte(b.String()), 0o600)
	}
	if IsApp(s.Runtime) && statusOf(s.Status) == StatusActive {
		_, _ = m.run.Run(ctx, "systemctl", "restart", m.layout(s).UnitName)
	}
	return nil
}
