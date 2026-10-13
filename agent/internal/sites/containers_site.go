package sites

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

// Websites with the docker runtime run their release as a container named
// ike-<account>-<id>-<a|b>, published on 127.0.0.1 and proxied by Nginx.

func siteKey(s *Site) string { return s.Account + "-" + siteID(s.Domain) }

func containerName(s *Site, color string) string { return "ike-" + siteKey(s) + "-" + color }

func (m *Manager) activeContainer(s *Site) string {
	color := "a"
	if s.Deploy != nil && s.Deploy.Color == "b" {
		color = "b"
	}
	return containerName(s, color)
}

// docker runs the Docker CLI as root, with the account's registry logins.
func (m *Manager) docker(ctx context.Context, account string, out func(string, ...any), args ...string) ([]byte, error) {
	c := runner.Command{Name: "docker", Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/root", "DOCKER_BUILDKIT=1"}, Timeout: 30 * time.Minute}
	if account != "" {
		if dir := m.dockerConfigDir(account); m.exists(filepath.Join(dir, "config.json")) {
			c.Args = append(c.Args, "--config", dir)
		}
	}
	c.Args = append(c.Args, args...)
	if out != nil {
		c.Output = logWriter(out)
	}
	return m.run.Exec(ctx, c)
}

func (m *Manager) dockerConfigDir(account string) string {
	return filepath.Join(filepath.Dir(m.cfg.RegistriesFile), "docker", account)
}

func (m *Manager) containerAction(ctx context.Context, s *Site, action string) error {
	_, err := m.docker(ctx, "", nil, action, m.activeContainer(s))
	return err
}

func (m *Manager) containerState(ctx context.Context, s *Site) string {
	out, err := m.docker(ctx, "", nil, "inspect", "-f", "{{.State.Status}}", m.activeContainer(s))
	switch st := strings.TrimSpace(string(out)); {
	case err != nil:
		return "not deployed"
	case st == "running":
		return "active"
	case st == "":
		return "unknown"
	default:
		return st
	}
}

func (m *Manager) containerLogs(ctx context.Context, s *Site, lines int) (string, error) {
	out, err := m.docker(ctx, "", nil, "logs", "--tail", strconv.Itoa(lines), "--timestamps", m.activeContainer(s))
	return string(out), err
}

// buildImage produces the image for a container deployment.
func (m *Manager) buildImage(ctx context.Context, site *Site, L layout, d *Deployment, commit string, log func(string, ...any)) error {
	st := site.Deploy
	tag := "ike/" + siteKey(site) + ":" + d.Release
	if st.Type == "image" {
		log("Pulling %s", d.Image)
		if _, err := m.docker(ctx, site.Account, log, "pull", d.Image); err != nil {
			return fmt.Errorf("couldn't pull %s: %w", d.Image, err)
		}
		if _, err := m.docker(ctx, "", nil, "tag", d.Image, tag); err != nil {
			return err
		}
		return nil
	}
	rel := filepath.Join(m.releasesDir(L), d.Release)
	if err := m.exportRelease(ctx, site, L, commit, rel, log); err != nil {
		return err
	}
	defer func() { _, _ = m.asUser(context.Background(), L, L.SiteDir, nil, nil, "rm", "-rf", rel) }()
	switch st.Type {
	case "dockerfile":
		if !m.exists(filepath.Join(rel, "Dockerfile")) {
			return fmt.Errorf("the repository has no Dockerfile%s", map[bool]string{true: " in " + st.Git.Subdir}[st.Git.Subdir != ""])
		}
		log("Building the Dockerfile")
		if _, err := m.docker(ctx, site.Account, log, "build", "--pull", "--label", "ike.site="+site.Domain, "-t", tag, rel); err != nil {
			return fmt.Errorf("docker build failed: %w", err)
		}
	case "buildpack":
		if !m.exists(m.cfg.PackPath) {
			return fmt.Errorf("the buildpacks tool (pack) isn't installed on this server")
		}
		log("Building with Cloud Native Buildpacks")
		if _, err := m.run.Exec(ctx, runner.Command{Name: m.cfg.PackPath, Args: []string{"build", tag, "--path", rel,
			"--builder", "paketobuildpacks/builder-jammy-base", "--pull-policy", "if-not-present"},
			Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/root"}, Output: logWriter(log), Timeout: 30 * time.Minute}); err != nil {
			return fmt.Errorf("buildpack build failed: %w", err)
		}
	}
	return nil
}

// containerEnvFile copies the website's .env into a root-only file for
// docker run. The website's file is read without following links.
func (m *Manager) containerEnvFile(site *Site) (string, error) {
	vars, err := m.GetEnv(site.Domain)
	if err != nil {
		vars = nil
	}
	if err := os.MkdirAll(m.p(m.cfg.DeployTmpDir), 0o711); err != nil {
		return "", err
	}
	f := filepath.Join(m.cfg.DeployTmpDir, randomSecret(8)+".env")
	var b strings.Builder
	for _, v := range vars {
		b.WriteString(v.Key + "=" + v.Value + "\n")
	}
	return f, writeFile(m.p(f), []byte(b.String()), 0o600)
}

func (m *Manager) runArgs(site *Site, name, image, envFile string, port, cport int) []string {
	return []string{"run", "-d", "--name", name, "--restart", "unless-stopped",
		"-p", "127.0.0.1:" + strconv.Itoa(port) + ":" + strconv.Itoa(cport),
		"--env-file", envFile, "-e", "PORT=" + strconv.Itoa(cport),
		"--label", "ike.account=" + site.Account, "--label", "ike.site=" + site.Domain,
		"--cgroup-parent", "ike-acct-" + site.Account + ".slice",
		"--security-opt", "no-new-privileges", "--pids-limit", "1024",
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3",
		image}
}

// switchContainer starts the image on the idle port, checks it, moves
// traffic and removes the old container.
func (m *Manager) switchContainer(ctx context.Context, site *Site, d *Deployment, log func(string, ...any)) error {
	st := site.Deploy
	from, to := st.Color, "b"
	if from == "" {
		from = "a"
	}
	if from == "b" {
		to = "a"
	}
	if st.Current == "" { // first deployment: the "a" slot is free
		from, to = "", "a"
	}
	port := site.Port
	if to == "b" {
		port = st.AltPort
	}
	tag := "ike/" + siteKey(site) + ":" + d.Release
	if _, err := m.docker(ctx, "", nil, "image", "inspect", tag); err != nil {
		return fmt.Errorf("the image for release %s is gone; deploy again instead", d.Release)
	}
	envFile, err := m.containerEnvFile(site)
	if err != nil {
		return err
	}
	defer os.Remove(m.p(envFile))
	name := containerName(site, to)
	_, _ = m.docker(ctx, "", nil, "rm", "-f", name)
	log("Starting container %s on port %d", name, port)
	if _, err := m.docker(ctx, "", nil, m.runArgs(site, name, tag, envFile, port, st.ContainerPort)...); err != nil {
		return fmt.Errorf("the container didn't start: %w", err)
	}
	if err := m.healthy(ctx, port, site.Domain, st.HealthPath); err != nil {
		logs, _ := m.docker(context.Background(), "", nil, "logs", "--tail", "30", name)
		log("Container output:\n%s", strings.TrimSpace(string(logs)))
		_, _ = m.docker(context.Background(), "", nil, "rm", "-f", name)
		return fmt.Errorf("the new container didn't answer on %s, so traffic stayed where it was: %w", st.HealthPath, err)
	}
	m.mu.Lock()
	cur, err := m.Get(site.Domain)
	if err == nil {
		old := *cur
		nd := *cur.Deploy
		nd.Current, nd.Color = d.Release, to
		nd.Colors = map[string]string{to: d.Release}
		if from != "" && cur.Deploy.Colors[from] != "" {
			nd.Colors[from] = cur.Deploy.Colors[from]
		}
		cur.Deploy = &nd
		err = m.applyWeb(ctx, cur, &old)
	}
	m.mu.Unlock()
	if err != nil {
		_, _ = m.docker(context.Background(), "", nil, "rm", "-f", name)
		return err
	}
	if from != "" {
		oldName := containerName(site, from)
		log("Traffic moved; removing %s", oldName)
		time.AfterFunc(drainDelay, func() { _, _ = m.docker(context.Background(), "", nil, "rm", "-f", oldName) })
	}
	return nil
}

func (m *Manager) pruneImages(ctx context.Context, site *Site, keep map[string]bool, log func(string, ...any)) {
	repo := "ike/" + siteKey(site)
	out, err := m.docker(ctx, "", nil, "image", "ls", repo, "--format", "{{.Tag}}")
	if err != nil {
		return
	}
	tags := strings.Fields(string(out))
	sort.Sort(sort.Reverse(sort.StringSlice(tags)))
	for i, t := range tags {
		if i < site.Deploy.KeepReleases || keep[t] {
			continue
		}
		if _, err := m.docker(ctx, "", nil, "image", "rm", repo+":"+t); err == nil {
			log("Removed old image %s", t)
		}
	}
}
