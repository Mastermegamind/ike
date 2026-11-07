package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// Account containers: each account runs its own containers, named
// ike-<account>-c-<name>, labelled ike.account=<account>, attached to the
// account's own network and limited by the account's resource slice.

// ContainerSpec describes a container to run.
type ContainerSpec struct {
	Name     string           `json:"name"`
	Image    string           `json:"image"`
	Command  []string         `json:"command,omitempty"`
	Env      []EnvVar         `json:"env,omitempty"`
	Ports    []PortMap        `json:"ports,omitempty"`
	Volumes  []VolumeMount    `json:"volumes,omitempty"`
	Restart  string           `json:"restart,omitempty"` // no | always | unless-stopped (default) | on-failure
	CPUs     float64          `json:"cpus,omitempty"`
	MemoryMB int              `json:"memory_mb,omitempty"`
	Pids     int              `json:"pids,omitempty"`
	Health   *ContainerHealth `json:"health,omitempty"`
	Network  string           `json:"network,omitempty"` // one of the account's networks; default its own
}

// PortMap publishes a container port. Ports bind to 127.0.0.1 so they are
// reached through a website (proxy runtime) unless a server admin makes
// them public.
type PortMap struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port,omitempty"` // 0: any free port
	Protocol      string `json:"protocol,omitempty"`  // tcp | udp
	Public        bool   `json:"public,omitempty"`    // admins only: listen on all addresses
}

// VolumeMount attaches a named volume or a folder in the account's home.
type VolumeMount struct {
	Volume   string `json:"volume,omitempty"`    // named volume (created when missing)
	HostPath string `json:"host_path,omitempty"` // relative to the account's home
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// ContainerHealth is a Docker health check.
type ContainerHealth struct {
	Command     string `json:"command"`
	IntervalSec int    `json:"interval_sec,omitempty"`
	Retries     int    `json:"retries,omitempty"`
	StartSec    int    `json:"start_sec,omitempty"`
}

// Container is one container as listed by Docker.
type Container struct {
	Name    string `json:"name"`
	Short   string `json:"short"` // name without the account prefix
	Image   string `json:"image"`
	State   string `json:"state"`  // running | exited | created | restarting | paused
	Status  string `json:"status"` // "Up 3 hours (healthy)"
	Ports   string `json:"ports"`
	Created string `json:"created"`
	Site    string `json:"site,omitempty"`  // website deployments
	Stack   string `json:"stack,omitempty"` // Compose stacks
	Account string `json:"account"`
}

var (
	cNameRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,39}$`)
	targetPath  = regexp.MustCompile(`^/[A-Za-z0-9._@+/-]{0,200}$`)
	cmdArgRe    = regexp.MustCompile(`^[^\x00-\x1f\x7f]{0,500}$`)
	restartPols = map[string]bool{"no": true, "always": true, "unless-stopped": true, "on-failure": true}
)

func (m *Manager) dockerReady() error {
	if _, err := os.Stat(m.p(m.cfg.DockerSocket)); err != nil {
		return fmt.Errorf("Docker isn't installed on this server. Install it with: install.sh --docker")
	}
	return nil
}

// containersAllowed checks the admin let accounts use containers.
func (m *Manager) containersAllowed(admin bool) error {
	if err := m.dockerReady(); err != nil {
		return err
	}
	if !admin && !m.cfg.DockerForAccounts {
		return fmt.Errorf("containers are only available to server admins on this server")
	}
	return nil
}

func accountContainer(account, name string) string { return "ike-" + account + "-c-" + name }
func accountNetwork(account string) string         { return "ike-" + account }

func (m *Manager) ensureNetwork(ctx context.Context, account, name string) error {
	if _, err := m.docker(ctx, "", nil, "network", "inspect", name); err == nil {
		return nil
	}
	_, err := m.docker(ctx, "", nil, "network", "create", "--label", "ike.account="+account, name)
	return err
}

// homePath resolves a path inside the account's home and refuses symlinks
// on the way, so a mount can't be redirected to system files.
func (m *Manager) homePath(account, rel string) (string, error) {
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || strings.Contains(rel, "..") || !targetPath.MatchString("/"+rel) {
		return "", fmt.Errorf("%q must be a folder inside the account's home, like data/uploads", rel)
	}
	A := m.accountLayout(account)
	full := filepath.Join(A.Home, rel)
	cur := A.Home
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(m.p(cur))
		if err != nil {
			break
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a link; mount the real folder", rel)
		}
	}
	if _, err := os.Lstat(m.p(full)); err != nil {
		return "", fmt.Errorf("%s doesn't exist in the account's home yet", rel)
	}
	return full, nil
}

func (s *ContainerSpec) check(admin bool) error {
	if !cNameRe.MatchString(s.Name) {
		return fmt.Errorf("container name must be 1-40 lowercase letters, numbers, dots, dashes or underscores")
	}
	if !imageRe.MatchString(s.Image) {
		return fmt.Errorf("image must look like nginx:1.27 or ghcr.io/owner/app:1.0")
	}
	if len(s.Command) > 30 {
		return fmt.Errorf("command has too many parts")
	}
	for _, a := range s.Command {
		if !cmdArgRe.MatchString(a) {
			return fmt.Errorf("command parts must be single lines")
		}
	}
	if len(s.Env) > 100 {
		return fmt.Errorf("at most 100 environment variables")
	}
	for _, e := range s.Env {
		if !envKeyRe.MatchString(e.Key) || strings.ContainsAny(e.Value, "\n\r\x00") {
			return fmt.Errorf("invalid environment variable %q", e.Key)
		}
	}
	if s.Restart == "" {
		s.Restart = "unless-stopped"
	}
	if !restartPols[s.Restart] {
		return fmt.Errorf("restart must be no, always, unless-stopped or on-failure")
	}
	if len(s.Ports) > 20 || len(s.Volumes) > 20 {
		return fmt.Errorf("at most 20 ports and 20 volumes")
	}
	for i := range s.Ports {
		p := &s.Ports[i]
		if p.Protocol == "" {
			p.Protocol = "tcp"
		}
		if p.Protocol != "tcp" && p.Protocol != "udp" {
			return fmt.Errorf("protocol must be tcp or udp")
		}
		if p.ContainerPort < 1 || p.ContainerPort > 65535 || p.HostPort < 0 || p.HostPort > 65535 {
			return fmt.Errorf("ports must be 1-65535")
		}
		if p.HostPort != 0 && p.HostPort < 1024 {
			return fmt.Errorf("host ports below 1024 are reserved for the server")
		}
		if p.Public && !admin {
			return fmt.Errorf("only server admins can publish container ports to the internet; put a website in front instead")
		}
	}
	for _, v := range s.Volumes {
		if !targetPath.MatchString(v.Target) || v.Target == "/" || strings.Contains(v.Target, "..") {
			return fmt.Errorf("mount target must be an absolute path inside the container")
		}
		if (v.Volume == "") == (v.HostPath == "") {
			return fmt.Errorf("each mount needs either a volume name or a folder")
		}
		if v.Volume != "" && !cNameRe.MatchString(v.Volume) {
			return fmt.Errorf("volume names are lowercase letters, numbers, dots, dashes or underscores")
		}
	}
	if s.CPUs < 0 || s.CPUs > 64 || s.MemoryMB < 0 || (s.MemoryMB > 0 && s.MemoryMB < 16) || s.Pids < 0 || s.Pids > 32768 {
		return fmt.Errorf("limits: CPUs 0-64, memory at least 16 MB, processes 0-32768")
	}
	if h := s.Health; h != nil {
		if strings.TrimSpace(h.Command) == "" || !cmdArgRe.MatchString(h.Command) {
			return fmt.Errorf("health check command must be one line")
		}
		if h.IntervalSec == 0 {
			h.IntervalSec = 30
		}
		if h.Retries == 0 {
			h.Retries = 3
		}
		if h.IntervalSec < 5 || h.IntervalSec > 3600 || h.Retries < 1 || h.Retries > 20 || h.StartSec < 0 || h.StartSec > 3600 {
			return fmt.Errorf("health check: every 5-3600 seconds, 1-20 retries")
		}
	}
	if s.Network != "" && !cNameRe.MatchString(s.Network) {
		return fmt.Errorf("invalid network name")
	}
	return nil
}

// CreateContainer runs a new container for an account.
func (m *Manager) CreateContainer(ctx context.Context, account string, spec ContainerSpec, admin bool) (*Container, error) {
	if err := m.containersAllowed(admin); err != nil {
		return nil, err
	}
	a, err := m.getAccount(account)
	if err != nil {
		return nil, err
	}
	if a.Suspended {
		return nil, fmt.Errorf("%s is suspended", account)
	}
	if err := spec.check(admin); err != nil {
		return nil, err
	}
	name := accountContainer(account, spec.Name)
	if _, err := m.docker(ctx, "", nil, "container", "inspect", name); err == nil {
		return nil, fmt.Errorf("container %s %w", spec.Name, ErrExists)
	}
	network := accountNetwork(account)
	if spec.Network != "" {
		network = accountNetwork(account) + "-" + spec.Network
	}
	if err := m.ensureNetwork(ctx, account, network); err != nil {
		return nil, err
	}
	args := []string{"run", "-d", "--name", name, "--restart", spec.Restart, "--network", network,
		"--label", "ike.account=" + account, "--label", "ike.name=" + spec.Name,
		"--cgroup-parent", "ike-acct-" + account + ".slice", "--security-opt", "no-new-privileges",
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3"}
	pids := spec.Pids
	if pids == 0 {
		pids = 1024
	}
	args = append(args, "--pids-limit", strconv.Itoa(pids))
	if spec.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(spec.CPUs, 'f', 2, 64))
	}
	if spec.MemoryMB > 0 {
		args = append(args, "--memory", strconv.Itoa(spec.MemoryMB)+"m")
	}
	for _, p := range spec.Ports {
		host := "127.0.0.1:"
		if p.Public {
			host = ""
		}
		hp := ""
		if p.HostPort > 0 {
			hp = strconv.Itoa(p.HostPort)
		}
		args = append(args, "-p", host+hp+":"+strconv.Itoa(p.ContainerPort)+"/"+p.Protocol)
	}
	bindsHome := false
	for _, v := range spec.Volumes {
		ro := ""
		if v.ReadOnly {
			ro = ",readonly"
		}
		if v.Volume != "" {
			vol := "ike-" + account + "-" + v.Volume
			if _, err := m.docker(ctx, "", nil, "volume", "inspect", vol); err != nil {
				if _, err := m.docker(ctx, "", nil, "volume", "create", "--label", "ike.account="+account, vol); err != nil {
					return nil, err
				}
			}
			args = append(args, "--mount", "type=volume,source="+vol+",target="+v.Target+ro)
			continue
		}
		src, err := m.homePath(account, v.HostPath)
		if err != nil {
			return nil, err
		}
		bindsHome = true
		args = append(args, "--mount", "type=bind,source="+src+",target="+v.Target+ro)
	}
	if bindsHome && !admin {
		// Files written into the home belong to the account, not root.
		uid, gid := "0", "0"
		if !m.cfg.DryRun {
			if u, err := lookupUID(account); err == nil {
				uid, gid = strconv.Itoa(u), strconv.Itoa(u)
			}
		}
		args = append(args, "--user", uid+":"+gid)
	}
	if h := spec.Health; h != nil {
		args = append(args, "--health-cmd", h.Command, "--health-interval", strconv.Itoa(h.IntervalSec)+"s",
			"--health-retries", strconv.Itoa(h.Retries), "--health-start-period", strconv.Itoa(h.StartSec)+"s")
	}
	envFile := ""
	if len(spec.Env) > 0 {
		var b strings.Builder
		for _, e := range spec.Env {
			b.WriteString(e.Key + "=" + e.Value + "\n")
		}
		_ = os.MkdirAll(m.p(m.cfg.DeployTmpDir), 0o711)
		envFile = filepath.Join(m.cfg.DeployTmpDir, randomSecret(8)+".env")
		if err := writeFile(m.p(envFile), []byte(b.String()), 0o600); err != nil {
			return nil, err
		}
		defer os.Remove(m.p(envFile))
		args = append(args, "--env-file", envFile)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Command...)
	if _, err := m.docker(ctx, account, nil, args...); err != nil {
		_, _ = m.docker(context.Background(), "", nil, "rm", "-f", name)
		return nil, fmt.Errorf("couldn't start the container: %w", err)
	}
	for _, p := range spec.Ports {
		if p.Public && p.HostPort > 0 {
			m.publicPortOpened(ctx, p.HostPort, p.Protocol)
		}
	}
	return &Container{Name: name, Short: spec.Name, Image: spec.Image, State: "running", Account: account}, nil
}

// publicPortOpened is set by the API so public container ports open the firewall.
var publicPortHook func(ctx context.Context, port int, proto string)

func (m *Manager) publicPortOpened(ctx context.Context, port int, proto string) {
	if publicPortHook != nil {
		publicPortHook(ctx, port, proto)
	}
}

// SetPublicPortHook lets the API open the firewall for public container ports.
func SetPublicPortHook(f func(ctx context.Context, port int, proto string)) { publicPortHook = f }

type psLine struct {
	Names, Image, State, Status, Ports, CreatedAt, Labels string
}

func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// Containers lists an account's containers (or every Ike-managed container
// when account is empty).
func (m *Manager) Containers(ctx context.Context, account string) ([]Container, error) {
	if err := m.dockerReady(); err != nil {
		return nil, err
	}
	filter := "label=ike.account"
	if account != "" {
		filter = "label=ike.account=" + account
	}
	out, err := m.docker(ctx, "", nil, "ps", "-a", "--filter", filter, "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	list := []Container{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l == "" {
			continue
		}
		var p psLine
		if json.Unmarshal([]byte(l), &p) != nil {
			continue
		}
		lb := parseLabels(p.Labels)
		c := Container{Name: p.Names, Image: p.Image, State: p.State, Status: p.Status, Ports: p.Ports, Created: p.CreatedAt,
			Account: lb["ike.account"], Site: lb["ike.site"], Stack: lb["com.docker.compose.project"]}
		c.Short = strings.TrimPrefix(c.Name, "ike-"+c.Account+"-c-")
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// ownContainer finds an account's container by its short name.
func (m *Manager) ownContainer(ctx context.Context, account, short string) (string, error) {
	if !cNameRe.MatchString(short) {
		return "", fmt.Errorf("invalid container name")
	}
	name := accountContainer(account, short)
	out, err := m.docker(ctx, "", nil, "inspect", "-f", "{{index .Config.Labels \"ike.account\"}}", name)
	if err != nil {
		return "", fmt.Errorf("container %s %w", short, ErrNotFound)
	}
	if got := strings.TrimSpace(string(out)); got != account && !m.cfg.DryRun {
		return "", fmt.Errorf("container %s %w", short, ErrNotFound)
	}
	return name, nil
}

// ContainerAction starts, stops, restarts or removes an account's container.
func (m *Manager) ContainerAction(ctx context.Context, account, short, action string) error {
	name, err := m.ownContainer(ctx, account, short)
	if err != nil {
		return err
	}
	switch action {
	case "start", "stop", "restart":
		if action != "stop" {
			if a, err := m.getAccount(account); err == nil && a.Suspended {
				return fmt.Errorf("%s is suspended", account)
			}
		}
		_, err = m.docker(ctx, "", nil, action, name)
	case "remove":
		_, err = m.docker(ctx, "", nil, "rm", "-f", name)
	default:
		return fmt.Errorf("action must be start, stop, restart or remove")
	}
	return err
}

// ContainerLogs returns a container's recent output.
func (m *Manager) ContainerLogs(ctx context.Context, account, short string, lines int) (string, error) {
	name, err := m.ownContainer(ctx, account, short)
	if err != nil {
		return "", err
	}
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	out, err := m.docker(ctx, "", nil, "logs", "--tail", strconv.Itoa(lines), "--timestamps", name)
	return string(out), err
}

// ContainerExec runs one command inside the container (a non-interactive shell).
func (m *Manager) ContainerExec(ctx context.Context, account, short, command string) (string, error) {
	name, err := m.ownContainer(ctx, account, short)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(command) == "" || !cmdArgRe.MatchString(command) {
		return "", fmt.Errorf("command must be one line")
	}
	out, err := m.run.Exec(ctx, runner.Command{Name: "docker", Args: []string{"exec", name, "sh", "-c", command},
		Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/root"}, Timeout: 60 * time.Second})
	if len(out) > 256<<10 {
		out = out[len(out)-256<<10:]
	}
	return string(out), err
}

// ContainerStats is a one-off resource sample per container.
type ContainerStats struct {
	Name    string `json:"name"`
	CPU     string `json:"cpu"`
	Memory  string `json:"memory"`
	MemPerc string `json:"mem_percent"`
	NetIO   string `json:"net_io"`
	BlockIO string `json:"block_io"`
	Pids    string `json:"pids"`
}

func (m *Manager) ContainerStatsFor(ctx context.Context, account string) ([]ContainerStats, error) {
	list, err := m.Containers(ctx, account)
	if err != nil {
		return nil, err
	}
	args := []string{"stats", "--no-stream", "--format", "{{json .}}"}
	n := 0
	for _, c := range list {
		if c.State == "running" {
			args = append(args, c.Name)
			n++
		}
	}
	out := []ContainerStats{}
	if n == 0 {
		return out, nil
	}
	raw, err := m.docker(ctx, "", nil, args...)
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var s struct{ Name, CPUPerc, MemUsage, MemPerc, NetIO, BlockIO, PIDs string }
		if json.Unmarshal([]byte(l), &s) == nil && s.Name != "" {
			out = append(out, ContainerStats{Name: s.Name, CPU: s.CPUPerc, Memory: s.MemUsage, MemPerc: s.MemPerc, NetIO: s.NetIO, BlockIO: s.BlockIO, Pids: s.PIDs})
		}
	}
	return out, nil
}

// ------------------------------------------------------- volumes, networks

// DockerVolume is a named volume.
type DockerVolume struct {
	Name    string `json:"name"`
	Short   string `json:"short"`
	Account string `json:"account"`
}

func (m *Manager) Volumes(ctx context.Context, account string) ([]DockerVolume, error) {
	if err := m.dockerReady(); err != nil {
		return nil, err
	}
	out, err := m.docker(ctx, "", nil, "volume", "ls", "--filter", "label=ike.account="+account, "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	list := []DockerVolume{}
	for _, n := range strings.Fields(string(out)) {
		list = append(list, DockerVolume{Name: n, Short: strings.TrimPrefix(n, "ike-"+account+"-"), Account: account})
	}
	return list, nil
}

func (m *Manager) CreateVolume(ctx context.Context, account, short string) (*DockerVolume, error) {
	if err := m.dockerReady(); err != nil {
		return nil, err
	}
	if _, err := m.getAccount(account); err != nil {
		return nil, err
	}
	if !cNameRe.MatchString(short) {
		return nil, fmt.Errorf("volume names are lowercase letters, numbers, dots, dashes or underscores")
	}
	name := "ike-" + account + "-" + short
	if _, err := m.docker(ctx, "", nil, "volume", "create", "--label", "ike.account="+account, name); err != nil {
		return nil, err
	}
	return &DockerVolume{Name: name, Short: short, Account: account}, nil
}

func (m *Manager) RemoveVolume(ctx context.Context, account, short string) error {
	if !cNameRe.MatchString(short) {
		return fmt.Errorf("invalid volume name")
	}
	_, err := m.docker(ctx, "", nil, "volume", "rm", "ike-"+account+"-"+short)
	if err != nil {
		return fmt.Errorf("the volume is in use or doesn't exist: %w", err)
	}
	return nil
}

func (m *Manager) Networks(ctx context.Context, account string) ([]string, error) {
	if err := m.dockerReady(); err != nil {
		return nil, err
	}
	out, err := m.docker(ctx, "", nil, "network", "ls", "--filter", "label=ike.account="+account, "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

func (m *Manager) CreateNetwork(ctx context.Context, account, short string) (string, error) {
	if err := m.dockerReady(); err != nil {
		return "", err
	}
	if !cNameRe.MatchString(short) {
		return "", fmt.Errorf("network names are lowercase letters, numbers, dots, dashes or underscores")
	}
	name := accountNetwork(account) + "-" + short
	return name, m.ensureNetwork(ctx, account, name)
}

func (m *Manager) RemoveNetwork(ctx context.Context, account, short string) error {
	if !cNameRe.MatchString(short) {
		return fmt.Errorf("invalid network name")
	}
	_, err := m.docker(ctx, "", nil, "network", "rm", accountNetwork(account)+"-"+short)
	return err
}

// --------------------------------------------------------------- registries

// RegistryLogin stores credentials for a private registry, per account.
type RegistryLogin struct {
	Registry string `json:"registry"` // ghcr.io, registry.gitlab.com, docker.io ...
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

var registryRe = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]{1,5})?$`)

func (m *Manager) Registries(account string) ([]string, error) {
	var cfg struct {
		Auths map[string]any `json:"auths"`
	}
	b, err := os.ReadFile(m.p(filepath.Join(m.dockerConfigDir(account), "config.json")))
	if err != nil {
		return []string{}, nil
	}
	_ = json.Unmarshal(b, &cfg)
	out := []string{}
	for k := range cfg.Auths {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (m *Manager) AddRegistry(ctx context.Context, account string, r RegistryLogin) error {
	if err := m.dockerReady(); err != nil {
		return err
	}
	if _, err := m.getAccount(account); err != nil {
		return err
	}
	if !registryRe.MatchString(r.Registry) || !userRe.MatchString(r.Username) || r.Password == "" || len(r.Password) > 4096 {
		return fmt.Errorf("give the registry host, a username and a password or token")
	}
	dir := m.dockerConfigDir(account)
	if err := os.MkdirAll(m.p(dir), 0o700); err != nil {
		return err
	}
	_, err := m.run.Exec(ctx, runner.Command{Name: "docker", Args: []string{"--config", dir, "login", r.Registry, "-u", r.Username, "--password-stdin"},
		Stdin: r.Password, Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/root"}, Timeout: 60 * time.Second})
	if err != nil {
		return fmt.Errorf("the registry refused the login: %w", err)
	}
	return nil
}

func (m *Manager) RemoveRegistry(ctx context.Context, account, registry string) error {
	if !registryRe.MatchString(registry) {
		return fmt.Errorf("invalid registry")
	}
	_, err := m.docker(ctx, account, nil, "logout", registry)
	return err
}

// ----------------------------------------------------------- server level

// DockerOverview is the admin's summary of the Docker host.
type DockerOverview struct {
	Installed   bool             `json:"installed"`
	Version     string           `json:"version,omitempty"`
	Containers  int              `json:"containers"`
	Running     int              `json:"running"`
	Images      []DockerImage    `json:"images"`
	DiskUsage   []map[string]any `json:"disk_usage"`
	ForAccounts bool             `json:"for_accounts"`
}

// DockerImage is one local image.
type DockerImage struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	ID         string `json:"id"`
	Size       string `json:"size"`
	Created    string `json:"created"`
}

func (m *Manager) DockerOverview(ctx context.Context) (*DockerOverview, error) {
	o := &DockerOverview{Images: []DockerImage{}, DiskUsage: []map[string]any{}, ForAccounts: m.cfg.DockerForAccounts}
	if m.dockerReady() != nil {
		return o, nil
	}
	o.Installed = true
	if out, err := m.docker(ctx, "", nil, "version", "--format", "{{.Server.Version}}"); err == nil {
		o.Version = strings.TrimSpace(string(out))
	}
	if out, err := m.docker(ctx, "", nil, "ps", "-a", "--format", "{{.State}}"); err == nil {
		for _, s := range strings.Fields(string(out)) {
			o.Containers++
			if s == "running" {
				o.Running++
			}
		}
	}
	if out, err := m.docker(ctx, "", nil, "image", "ls", "--format", "{{json .}}"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			var im struct{ Repository, Tag, ID, Size, CreatedSince string }
			if json.Unmarshal([]byte(l), &im) == nil && im.ID != "" {
				o.Images = append(o.Images, DockerImage{im.Repository, im.Tag, im.ID, im.Size, im.CreatedSince})
			}
		}
	}
	if out, err := m.docker(ctx, "", nil, "system", "df", "--format", "{{json .}}"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			var row map[string]any
			if json.Unmarshal([]byte(l), &row) == nil {
				o.DiskUsage = append(o.DiskUsage, row)
			}
		}
	}
	return o, nil
}

// PruneDocker removes stopped containers, unused networks, dangling images
// and build cache. Volumes are never pruned: they hold data.
func (m *Manager) PruneDocker(ctx context.Context) (string, error) {
	if err := m.dockerReady(); err != nil {
		return "", err
	}
	out, err := m.docker(ctx, "", nil, "system", "prune", "-f")
	if err != nil {
		return "", err
	}
	out2, _ := m.docker(ctx, "", nil, "builder", "prune", "-f")
	return strings.TrimSpace(string(out) + "\n" + string(out2)), nil
}

// RemoveImage deletes a local image that no container uses.
func (m *Manager) RemoveImage(ctx context.Context, ref string) error {
	if !imageRe.MatchString(ref) && !regexp.MustCompile(`^[0-9a-f]{12,64}$`).MatchString(ref) {
		return fmt.Errorf("invalid image")
	}
	_, err := m.docker(ctx, "", nil, "image", "rm", ref)
	return err
}

// OwnContainer resolves an account's container (web terminal).
func (m *Manager) OwnContainer(ctx context.Context, account, short string) (string, error) {
	return m.ownContainer(ctx, account, short)
}
