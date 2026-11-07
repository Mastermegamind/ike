package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// Compose stacks. The account's compose.yaml lives in ~/stacks/<name>/ and is
// read by "docker compose config" running as the account user (so it can't
// pull in files the account can't read). Ike checks the normalized result
// against a safety policy, adds its own labels and limits, and starts the
// checked copy as root.

// Stack is one Compose project.
type Stack struct {
	Name     string         `json:"name"`
	Account  string         `json:"account"`
	Status   string         `json:"status"` // running(2), exited(1) ... as Docker reports
	Services []StackService `json:"services,omitempty"`
	Compose  string         `json:"compose,omitempty"`
}

// StackService is one service's container state.
type StackService struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Ports   string `json:"ports"`
}

// StackRequest creates or updates a stack.
type StackRequest struct {
	Compose string   `json:"compose"`
	Env     []EnvVar `json:"env,omitempty"` // written to the stack's .env (used for ${VAR} in compose.yaml)
}

func stackProject(account, name string) string { return "ike-" + account + "-" + name }

func (m *Manager) stackUserDir(account, name string) string {
	return filepath.Join(m.accountLayout(account).Home, "stacks", name)
}
func (m *Manager) stackRootFile(account, name string) string {
	return filepath.Join(m.cfg.StacksDir, account, name, "compose.json")
}

// checkCompose enforces the policy on normalized compose JSON and adds Ike's
// settings. It returns the JSON to start.
func (m *Manager) checkCompose(account string, raw []byte, admin bool) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("compose config isn't valid: %w", err)
	}
	home := m.accountLayout(account).Home + "/"
	services, _ := doc["services"].(map[string]any)
	if len(services) == 0 {
		return nil, fmt.Errorf("the compose file has no services")
	}
	if len(services) > 30 {
		return nil, fmt.Errorf("at most 30 services per stack")
	}
	underHome := func(p string) bool {
		c := filepath.Clean(p)
		return strings.HasPrefix(c+"/", home) && !strings.Contains(c, "docker.sock")
	}
	for name, sv := range services {
		svc, ok := sv.(map[string]any)
		if !ok {
			continue
		}
		bad := func(why string) error { return fmt.Errorf("service %s: %s", name, why) }
		if v, _ := svc["privileged"].(bool); v {
			return nil, bad("privileged containers aren't allowed")
		}
		for _, k := range []string{"cap_add", "devices", "sysctls", "device_cgroup_rules", "volumes_from"} {
			if x, ok := svc[k]; ok && x != nil {
				if l, ok := x.([]any); ok && len(l) == 0 {
					continue
				}
				if mp, ok := x.(map[string]any); ok && len(mp) == 0 {
					continue
				}
				return nil, bad(k + " isn't allowed")
			}
		}
		for _, k := range []string{"network_mode", "pid", "ipc", "userns_mode", "cgroup", "uts"} {
			if v, _ := svc[k].(string); v == "host" || strings.HasPrefix(v, "container:") {
				return nil, bad(k + ": " + v + " isn't allowed")
			}
		}
		if opts, ok := svc["security_opt"].([]any); ok {
			for _, o := range opts {
				if s, _ := o.(string); strings.Contains(s, "unconfined") || strings.HasPrefix(s, "label") {
					return nil, bad("security_opt " + s + " isn't allowed")
				}
			}
		}
		if vols, ok := svc["volumes"].([]any); ok {
			for _, v := range vols {
				vm, _ := v.(map[string]any)
				if t, _ := vm["type"].(string); t == "bind" {
					src, _ := vm["source"].(string)
					if !underHome(src) {
						return nil, bad("only folders in " + home + " can be mounted (" + src + ")")
					}
				}
			}
		}
		if b, ok := svc["build"].(map[string]any); ok {
			ctxDir, _ := b["context"].(string)
			if !underHome(ctxDir) {
				return nil, bad("the build context must be inside " + home)
			}
			if df, _ := b["dockerfile"].(string); df != "" && filepath.IsAbs(df) && !underHome(df) {
				return nil, bad("the Dockerfile must be inside the build context")
			}
		}
		if ports, ok := svc["ports"].([]any); ok {
			for _, p := range ports {
				pm, _ := p.(map[string]any)
				ip, _ := pm["host_ip"].(string)
				if ip == "" || ip == "0.0.0.0" || ip == "::" {
					if admin {
						continue
					}
					pm["host_ip"] = "127.0.0.1" // reached through a website, like every account port
				} else if ip != "127.0.0.1" && !admin {
					return nil, bad("ports can only be published on 127.0.0.1")
				}
				if pub := fmt.Sprint(pm["published"]); pub != "" && pub != "<nil>" {
					if n, err := strconv.Atoi(strings.Split(pub, "-")[0]); err == nil && n < 1024 && n != 0 {
						return nil, bad("host ports below 1024 are reserved for the server")
					}
				}
			}
		}
		labels, _ := svc["labels"].(map[string]any)
		if labels == nil {
			labels = map[string]any{}
		}
		labels["ike.account"] = account
		svc["labels"] = labels
		svc["cgroup_parent"] = "ike-acct-" + account + ".slice"
		opts, _ := svc["security_opt"].([]any)
		svc["security_opt"] = append(opts, "no-new-privileges:true")
		if _, ok := svc["pids_limit"]; !ok {
			svc["pids_limit"] = 1024
		}
	}
	for _, sect := range []string{"networks", "volumes"} {
		items, _ := doc[sect].(map[string]any)
		for key, it := range items {
			im, _ := it.(map[string]any)
			if ext, _ := im["external"].(bool); ext {
				n, _ := im["name"].(string)
				if n == "" {
					n = key
				}
				if !strings.HasPrefix(n, "ike-"+account) {
					return nil, fmt.Errorf("%s %s belongs to someone else; stacks can only use the account's own", sect[:len(sect)-1], n)
				}
			}
			if d, _ := im["driver"].(string); sect == "networks" && d != "" && d != "bridge" {
				return nil, fmt.Errorf("network %s: only bridge networks are allowed", key)
			}
		}
	}
	return json.MarshalIndent(doc, "", "  ")
}

// compose runs "docker compose" as root on the checked file.
func (m *Manager) compose(ctx context.Context, account, name string, out func(string, ...any), args ...string) ([]byte, error) {
	base := []string{"compose", "-p", stackProject(account, name), "-f", m.stackRootFile(account, name)}
	return m.docker(ctx, account, out, append(base, args...)...)
}

// SaveStack writes compose.yaml (and .env), checks it and starts the stack.
// It runs as a job.
func (m *Manager) SaveStack(ctx context.Context, account, name string, req StackRequest, admin bool, log func(string, ...any)) error {
	if err := m.containersAllowed(admin); err != nil {
		return err
	}
	a, err := m.getAccount(account)
	if err != nil {
		return err
	}
	if a.Suspended {
		return fmt.Errorf("%s is suspended", account)
	}
	if !cNameRe.MatchString(name) {
		return fmt.Errorf("stack names are lowercase letters, numbers, dots, dashes or underscores")
	}
	if strings.TrimSpace(req.Compose) == "" || len(req.Compose) > 256<<10 {
		return fmt.Errorf("paste a compose file (up to 256 KB)")
	}
	for _, e := range req.Env {
		if !envKeyRe.MatchString(e.Key) || strings.ContainsAny(e.Value, "\n\r\x00") {
			return fmt.Errorf("invalid variable %q", e.Key)
		}
	}
	L := layout{User: account, Home: m.accountLayout(account).Home, TmpDir: m.accountLayout(account).TmpDir, Runtime: ""}
	dir := m.stackUserDir(account, name)
	if _, err := m.asUser(ctx, L, L.Home, nil, nil, "mkdir", "-p", dir); err != nil {
		return err
	}
	if _, err := m.run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", account, "--", "tee", filepath.Join(dir, "compose.yaml")},
		Stdin: req.Compose, Env: m.userEnv(L, dir)}); err != nil {
		return err
	}
	if req.Env != nil {
		var b strings.Builder
		for _, e := range req.Env {
			b.WriteString(e.Key + "=" + e.Value + "\n")
		}
		if _, err := m.run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", account, "--", "tee", filepath.Join(dir, ".env")},
			Stdin: b.String(), Env: m.userEnv(L, dir)}); err != nil {
			return err
		}
	}
	log("Checking compose.yaml")
	raw, err := m.run.Exec(ctx, runner.Command{Name: "runuser",
		Args: []string{"-u", account, "--", "docker", "compose", "-p", stackProject(account, name), "--project-directory", dir,
			"-f", filepath.Join(dir, "compose.yaml"), "config", "--format", "json"},
		Env: m.userEnv(L, dir), Dir: m.p(dir), Timeout: time.Minute})
	if err != nil {
		return fmt.Errorf("compose.yaml has a problem: %w", err)
	}
	checked, err := m.checkCompose(account, raw, admin)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.p(filepath.Dir(m.stackRootFile(account, name))), 0o700); err != nil {
		return err
	}
	if err := writeFile(m.p(m.stackRootFile(account, name)), checked, 0o600); err != nil {
		return err
	}
	log("Starting the stack")
	if _, err := m.compose(ctx, account, name, log, "up", "-d", "--remove-orphans", "--pull", "missing"); err != nil {
		return err
	}
	log("Stack %s is up", name)
	return nil
}

// Stacks lists an account's Compose stacks.
func (m *Manager) Stacks(ctx context.Context, account string) ([]Stack, error) {
	if err := m.dockerReady(); err != nil {
		return nil, err
	}
	entries, _ := os.ReadDir(m.p(filepath.Join(m.cfg.StacksDir, account)))
	status := map[string]string{}
	if out, err := m.docker(ctx, "", nil, "compose", "ls", "-a", "--format", "json"); err == nil {
		var rows []struct{ Name, Status string }
		_ = json.Unmarshal(out, &rows)
		for _, r := range rows {
			status[r.Name] = r.Status
		}
	}
	list := []Stack{}
	for _, e := range entries {
		if !e.IsDir() || !cNameRe.MatchString(e.Name()) {
			continue
		}
		st := status[stackProject(account, e.Name())]
		if st == "" {
			st = "stopped"
		}
		list = append(list, Stack{Name: e.Name(), Account: account, Status: st})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// GetStack returns a stack with its compose source and services.
func (m *Manager) GetStack(ctx context.Context, account, name string) (*Stack, error) {
	if !cNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid stack name")
	}
	if _, err := os.Stat(m.p(m.stackRootFile(account, name))); err != nil {
		return nil, fmt.Errorf("stack %s %w", name, ErrNotFound)
	}
	s := &Stack{Name: name, Account: account, Services: []StackService{}}
	uid := -1
	if !m.cfg.DryRun {
		uid, _ = lookupUID(account)
	}
	s.Compose, _ = tailFile(m.p(filepath.Join(m.stackUserDir(account, name), "compose.yaml")), 10000, uid)
	if out, err := m.compose(ctx, account, name, nil, "ps", "-a", "--format", "json"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			var r struct{ Service, Name, Image, State, Status, Ports string }
			if json.Unmarshal([]byte(l), &r) == nil && r.Name != "" {
				s.Services = append(s.Services, StackService(r))
			}
		}
	}
	s.Status = "stopped"
	for _, sv := range s.Services {
		if sv.State == "running" {
			s.Status = "running"
		}
	}
	return s, nil
}

// StackAction runs up, down, restart or pull on a stack.
func (m *Manager) StackAction(ctx context.Context, account, name, action string, log func(string, ...any)) error {
	if !cNameRe.MatchString(name) {
		return fmt.Errorf("invalid stack name")
	}
	if _, err := os.Stat(m.p(m.stackRootFile(account, name))); err != nil {
		return fmt.Errorf("stack %s %w", name, ErrNotFound)
	}
	switch action {
	case "up", "restart", "pull":
		if a, err := m.getAccount(account); err == nil && a.Suspended {
			return fmt.Errorf("%s is suspended", account)
		}
	}
	var err error
	switch action {
	case "up":
		_, err = m.compose(ctx, account, name, log, "up", "-d", "--remove-orphans")
	case "down":
		_, err = m.compose(ctx, account, name, log, "down")
	case "restart":
		_, err = m.compose(ctx, account, name, log, "restart")
	case "pull":
		if _, err = m.compose(ctx, account, name, log, "pull"); err == nil {
			_, err = m.compose(ctx, account, name, log, "up", "-d", "--remove-orphans")
		}
	default:
		return fmt.Errorf("action must be up, down, restart or pull")
	}
	return err
}

// DeleteStack stops a stack and forgets it; with volumes, its data goes too.
func (m *Manager) DeleteStack(ctx context.Context, account, name string, volumes bool) error {
	if !cNameRe.MatchString(name) {
		return fmt.Errorf("invalid stack name")
	}
	args := []string{"down", "--remove-orphans"}
	if volumes {
		args = append(args, "--volumes")
	}
	if _, err := m.compose(ctx, account, name, nil, args...); err != nil {
		return err
	}
	return os.RemoveAll(m.p(filepath.Dir(m.stackRootFile(account, name))))
}

// StackLogs returns recent output of every service in a stack.
func (m *Manager) StackLogs(ctx context.Context, account, name string, lines int) (string, error) {
	if !cNameRe.MatchString(name) {
		return "", fmt.Errorf("invalid stack name")
	}
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	out, err := m.compose(ctx, account, name, nil, "logs", "--no-color", "--timestamps", "--tail", strconv.Itoa(lines))
	return string(out), err
}

// ------------------------------------------------------------ image updates

// ImageUpdate says whether a newer image exists for a tag in use.
type ImageUpdate struct {
	Image           string    `json:"image"`
	Containers      []string  `json:"containers"`
	LocalDigest     string    `json:"local_digest,omitempty"`
	RemoteDigest    string    `json:"remote_digest,omitempty"`
	UpdateAvailable bool      `json:"update_available"`
	CheckedAt       time.Time `json:"checked_at"`
	Error           string    `json:"error,omitempty"`
}

func (m *Manager) updatesFile() string {
	return filepath.Join(filepath.Dir(m.cfg.StacksDir), "image-updates.json")
}

// ImageUpdates returns the last check's results.
func (m *Manager) ImageUpdates() []ImageUpdate {
	var list []ImageUpdate
	if m.readJSON(m.updatesFile(), &list) != nil {
		return []ImageUpdate{}
	}
	return list
}

// CheckImageUpdates compares each image tag in use with its registry.
func (m *Manager) CheckImageUpdates(ctx context.Context) ([]ImageUpdate, error) {
	if err := m.dockerReady(); err != nil {
		return nil, err
	}
	out, err := m.docker(ctx, "", nil, "ps", "--filter", "label=ike.account", "--format", "{{.Image}}\t{{.Names}}\t{{.Label \"ike.account\"}}")
	if err != nil {
		return nil, err
	}
	byImage := map[string]*ImageUpdate{}
	account := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(l, "\t")
		if len(f) < 2 || strings.HasPrefix(f[0], "ike/") || strings.Contains(f[0], "@sha256:") || !imageRe.MatchString(f[0]) {
			continue
		}
		u := byImage[f[0]]
		if u == nil {
			u = &ImageUpdate{Image: f[0], CheckedAt: time.Now().UTC()}
			byImage[f[0]] = u
			if len(f) > 2 {
				account[f[0]] = f[2]
			}
		}
		u.Containers = append(u.Containers, f[1])
	}
	list := []ImageUpdate{}
	for img, u := range byImage {
		local, _ := m.docker(ctx, "", nil, "image", "inspect", "-f", "{{join .RepoDigests \",\"}}", img)
		remote, err := m.docker(ctx, account[img], nil, "buildx", "imagetools", "inspect", img, "--format", "{{json .Manifest}}")
		if err != nil {
			u.Error = "couldn't reach the registry"
		} else {
			var man struct {
				Digest string `json:"digest"`
			}
			_ = json.Unmarshal(remote, &man)
			u.RemoteDigest = man.Digest
			for _, d := range strings.Split(strings.TrimSpace(string(local)), ",") {
				if _, dg, ok := strings.Cut(d, "@"); ok {
					u.LocalDigest = dg
					if dg == man.Digest {
						break
					}
				}
			}
			u.UpdateAvailable = man.Digest != "" && !strings.Contains(string(local), man.Digest)
		}
		list = append(list, *u)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Image < list[j].Image })
	_ = m.writeJSON(m.updatesFile(), list)
	return list, nil
}

// RunImageUpdateChecks checks container images for updates once a day.
func (m *Manager) RunImageUpdateChecks(ctx context.Context) {
	t := time.NewTimer(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if m.dockerReady() == nil {
				cctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
				_, _ = m.CheckImageUpdates(cctx)
				cancel()
			}
			t.Reset(24 * time.Hour)
		}
	}
}
