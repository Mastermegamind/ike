package sites

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

// ------------------------------------------------------------------- queue

// DeploySettings are server-wide build limits.
type DeploySettings struct {
	MaxConcurrent int `json:"max_concurrent"` // builds running at once (1-8)
}

// DeployRequest starts a deployment.
type DeployRequest struct {
	RefType string `json:"ref_type,omitempty"` // empty: the website's configured ref
	Ref     string `json:"ref,omitempty"`
	Image   string `json:"image,omitempty"` // image deploys: a different tag
	Trigger string `json:"-"`
	Actor   string `json:"-"`

	rollback string // release id to switch back to
	preview  int
}

type queued struct {
	dep    Deployment
	req    DeployRequest
	cancel context.CancelFunc
}

type deployQueue struct {
	mu      sync.Mutex
	items   []*queued
	running map[string]*queued // domain → running deployment
}

func newDeployQueue() *deployQueue { return &deployQueue{running: map[string]*queued{}} }

func (m *Manager) deploySettings() DeploySettings {
	s := DeploySettings{MaxConcurrent: 1}
	_ = m.readJSON(filepath.Join(m.cfg.DeployDir, "settings.json"), &s)
	if s.MaxConcurrent < 1 {
		s.MaxConcurrent = 1
	}
	return s
}

// SetDeploySettings changes how many builds run at once.
func (m *Manager) SetDeploySettings(s DeploySettings) (DeploySettings, error) {
	if s.MaxConcurrent < 1 || s.MaxConcurrent > 8 {
		return s, fmt.Errorf("run 1-8 builds at once")
	}
	if err := m.writeJSON(filepath.Join(m.cfg.DeployDir, "settings.json"), s); err != nil {
		return s, err
	}
	m.pump()
	return s, nil
}

// QueueStatus lists running and waiting deployments across the server.
type QueueStatus struct {
	Settings DeploySettings `json:"settings"`
	Running  []Deployment   `json:"running"`
	Queued   []Deployment   `json:"queued"`
}

func (m *Manager) DeployQueue() QueueStatus {
	q := m.dq
	q.mu.Lock()
	defer q.mu.Unlock()
	st := QueueStatus{Settings: m.deploySettings(), Running: []Deployment{}, Queued: []Deployment{}}
	for _, it := range q.running {
		st.Running = append(st.Running, it.dep)
	}
	for _, it := range q.items {
		st.Queued = append(st.Queued, it.dep)
	}
	sort.Slice(st.Running, func(i, j int) bool { return st.Running[i].QueuedAt.Before(st.Running[j].QueuedAt) })
	return st
}

// Deploy queues a deployment. A newer request replaces an older one still
// waiting for the same website, so a burst of pushes builds only the last.
func (m *Manager) Deploy(domain string, req DeployRequest) (*Deployment, error) {
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	st := site.Deploy
	if st == nil || (st.Git == nil && st.Type != "image") {
		if req.rollback == "" {
			return nil, fmt.Errorf("%s isn't connected to a repository or image yet", domain)
		}
	}
	if statusOf(site.Status) == StatusSuspended {
		return nil, fmt.Errorf("%s is suspended", domain)
	}
	if req.Trigger == "" {
		req.Trigger = "manual"
	}
	d := Deployment{ID: time.Now().UTC().Format("20060102150405") + "-" + randomSecret(2), Domain: domain,
		Trigger: req.Trigger, Actor: req.Actor, Status: "queued", QueuedAt: time.Now().UTC(), RollbackOf: req.rollback}
	switch {
	case req.rollback != "":
	case st.Type == "image":
		d.Image = st.Image
		if req.Image != "" {
			if !imageRe.MatchString(req.Image) {
				return nil, fmt.Errorf("invalid image %q", req.Image)
			}
			d.Image = req.Image
		}
	default:
		d.RefType, d.Ref = st.Git.RefType, st.Git.Ref
		if req.Ref != "" {
			if req.RefType == "" {
				req.RefType = "branch"
			}
			if err := checkRef(req.RefType, req.Ref); err != nil {
				return nil, err
			}
			d.RefType, d.Ref = req.RefType, req.Ref
		}
	}
	q := m.dq
	q.mu.Lock()
	kept := q.items[:0]
	for _, it := range q.items {
		if it.dep.Domain == domain && it.dep.RollbackOf == "" && req.rollback == "" {
			it.dep.Status = "superseded"
			m.saveDeployment(it.dep)
			continue
		}
		kept = append(kept, it)
	}
	q.items = append(kept, &queued{dep: d, req: req})
	q.mu.Unlock()
	m.saveDeployment(d)
	m.pump()
	return &d, nil
}

// Rollback switches a website back to the release a past deployment made.
func (m *Manager) Rollback(domain, id, actor string) (*Deployment, error) {
	dep, _, err := m.Deployment(domain, id)
	if err != nil {
		return nil, err
	}
	if dep.Release == "" || dep.Status == "failed" {
		return nil, fmt.Errorf("that deployment didn't produce a release to go back to")
	}
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if site.Deploy != nil && site.Deploy.Current == dep.Release {
		return nil, fmt.Errorf("that release is already live")
	}
	d, err := m.Deploy(domain, DeployRequest{Trigger: "rollback", Actor: actor, rollback: dep.Release})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// CancelDeployment stops a waiting or running deployment.
func (m *Manager) CancelDeployment(domain, id string) error {
	q := m.dq
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, it := range q.items {
		if it.dep.Domain == domain && it.dep.ID == id {
			it.dep.Status = "cancelled"
			now := time.Now().UTC()
			it.dep.FinishedAt = &now
			m.saveDeployment(it.dep)
			q.items = append(q.items[:i], q.items[i+1:]...)
			return nil
		}
	}
	if it := q.running[domain]; it != nil && it.dep.ID == id && it.cancel != nil {
		it.cancel()
		return nil
	}
	return fmt.Errorf("deployment %s isn't waiting or running", id)
}

// pump starts waiting deployments while build slots are free, one per website.
func (m *Manager) pump() {
	q := m.dq
	q.mu.Lock()
	defer q.mu.Unlock()
	max := m.deploySettings().MaxConcurrent
	for len(q.running) < max {
		idx := -1
		for i, it := range q.items {
			if q.running[it.dep.Domain] == nil {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		it := q.items[idx]
		q.items = append(q.items[:idx], q.items[idx+1:]...)
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		it.cancel = cancel
		q.running[it.dep.Domain] = it
		go m.runQueued(ctx, it)
	}
}

func (m *Manager) runQueued(ctx context.Context, it *queued) {
	defer func() {
		it.cancel()
		m.dq.mu.Lock()
		delete(m.dq.running, it.dep.Domain)
		m.dq.mu.Unlock()
		m.pump()
	}()
	d := &it.dep
	now := time.Now().UTC()
	d.StartedAt, d.Status = &now, "building"
	m.saveDeployment(*d)

	logPath := m.p(m.logFile(d.Domain, d.ID))
	_ = os.MkdirAll(filepath.Dir(logPath), 0o750)
	f, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	var mu sync.Mutex
	log := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		mu.Lock()
		defer mu.Unlock()
		if f != nil {
			fmt.Fprintf(f, "%s  %s\n", time.Now().UTC().Format("15:04:05"), line)
		}
	}
	err := m.deploy(ctx, d, it.req, log)
	end := time.Now().UTC()
	d.FinishedAt = &end
	switch {
	case err == nil:
		d.Status = "live"
		log("Live in %s", end.Sub(*d.StartedAt).Round(time.Second))
	case ctx.Err() == context.Canceled:
		d.Status, d.Error = "cancelled", "Cancelled"
		log("Cancelled")
	default:
		d.Status, d.Error = "failed", err.Error()
		log("Failed: %s", err)
	}
	if f != nil {
		_ = f.Close()
	}
	m.saveDeployment(*d)
	if d.Status == "live" {
		m.markReplaced(d.Domain, d.ID)
	}
	slog.Info("deployment finished", "domain", d.Domain, "id", d.ID, "status", d.Status)
	m.sendNotify(d)
}

// markReplaced flags older live deployments, so history shows one live release.
func (m *Manager) markReplaced(domain, liveID string) {
	m.histMu.Lock()
	defer m.histMu.Unlock()
	list := m.loadHistory(domain)
	for i := range list {
		if list[i].ID != liveID && list[i].Status == "live" {
			list[i].Status = "replaced"
		}
	}
	_ = m.writeJSON(m.historyFile(domain), list)
}

// --------------------------------------------------------------- pipeline

func (m *Manager) deploy(ctx context.Context, d *Deployment, req DeployRequest, log func(string, ...any)) error {
	site, err := m.Get(d.Domain)
	if err != nil {
		return err
	}
	st := site.Deploy
	if st == nil {
		return fmt.Errorf("deployments aren't set up for this website")
	}
	L := m.layout(site)
	if req.rollback != "" {
		d.Release = req.rollback
		log("Rolling back to release %s", req.rollback)
		return m.switchRelease(ctx, d, log)
	}

	var commit string
	if st.Type == "image" {
		log("Image %s", d.Image)
	} else {
		var msg, author string
		if commit, msg, author, err = m.fetchSource(ctx, site, d.RefType, d.Ref, log); err != nil {
			return err
		}
		d.Commit, d.Message, d.Author = commit, msg, author
		m.saveDeployment(*d)
		log("Commit %s by %s: %s", short(commit), author, msg)
	}
	d.Release = time.Now().UTC().Format("20060102-150405")
	if commit != "" {
		d.Release += "-" + short(commit)
	}

	if site.Runtime == RuntimeDocker {
		if err := m.buildImage(ctx, site, L, d, commit, log); err != nil {
			return err
		}
	} else {
		rel := filepath.Join(m.releasesDir(L), d.Release)
		if err := m.exportRelease(ctx, site, L, commit, rel, log); err != nil {
			return err
		}
		if err := m.linkShared(ctx, L, rel, st.SharedPaths, log); err != nil {
			return err
		}
		cmds := []string{}
		if st.BuildCommand != "" {
			cmds = append(cmds, st.BuildCommand)
		} else {
			cmds = m.detectBuild(L, rel)
		}
		for _, c := range cmds {
			log("$ %s", c)
			if err := m.sandboxed(ctx, L, rel, c, log); err != nil {
				_, _ = m.asUser(context.Background(), L, L.SiteDir, nil, nil, "rm", "-rf", rel)
				return fmt.Errorf("build failed: %w", err)
			}
		}
		if st.ReleaseCommand != "" {
			log("$ %s  (release command)", st.ReleaseCommand)
			if err := m.sandboxed(ctx, L, rel, st.ReleaseCommand, log); err != nil {
				_, _ = m.asUser(context.Background(), L, L.SiteDir, nil, nil, "rm", "-rf", rel)
				return fmt.Errorf("release command failed: %w", err)
			}
		}
		_, _ = m.asUser(ctx, L, rel, nil, nil, "touch", filepath.Join(rel, ".ike-release"))
	}
	if err := m.switchRelease(ctx, d, log); err != nil {
		return err
	}
	m.pruneReleases(ctx, d.Domain, log)
	return nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// gitEnv is the environment for git commands against a website's repository.
// Credentials travel in variables, never in arguments.
func (m *Manager) gitEnv(site *Site) []string {
	sec := m.gitSecrets(site)
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + m.cfg.GitDir, "GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1", "LANG=C.UTF-8"}
	g := site.Deploy.Git
	switch g.Auth {
	case "deploy_key":
		env = append(env, "GIT_SSH_COMMAND=ssh -i "+filepath.Join(sec, "id_ed25519")+
			" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="+
			filepath.Join(m.cfg.GitSecretsDir, "known_hosts"))
	case "token":
		token, _ := os.ReadFile(m.p(filepath.Join(sec, "token")))
		user := g.Username
		if user == "" {
			user = map[string]string{"github": "x-access-token", "gitlab": "oauth2", "bitbucket": "x-token-auth"}[g.Provider]
		}
		if user == "" {
			user = "git"
		}
		env = append(env, "GIT_ASKPASS="+m.askpass(), "IKE_GIT_USERNAME="+user, "IKE_GIT_TOKEN="+strings.TrimSpace(string(token)))
	}
	return env
}

// askpass writes the helper git calls for https credentials.
func (m *Manager) askpass() string {
	p := filepath.Join(m.cfg.GitSecretsDir, "askpass.sh")
	_ = os.MkdirAll(m.p(m.cfg.GitSecretsDir), 0o700)
	_ = writeFile(m.p(p), []byte("#!/bin/sh\ncase \"$1\" in\n  Username*) printf '%s\\n' \"$IKE_GIT_USERNAME\" ;;\n  *) printf '%s\\n' \"$IKE_GIT_TOKEN\" ;;\nesac\n"), 0o700)
	return p
}

func (m *Manager) git(ctx context.Context, site *Site, log func(string, ...any), args ...string) ([]byte, error) {
	c := runner.Command{Name: "git", Args: append([]string{"-c", "credential.helper=", "-c", "core.hooksPath=/dev/null"}, args...),
		Env: m.gitEnv(site), Timeout: 10 * time.Minute}
	return m.run.Exec(ctx, c)
}

// fetchSource updates the website's bare mirror and resolves the ref to a commit.
func (m *Manager) fetchSource(ctx context.Context, site *Site, refType, ref string, log func(string, ...any)) (commit, msg, author string, err error) {
	mirror := m.gitMirror(site)
	if _, statErr := os.Stat(m.p(filepath.Join(mirror, "HEAD"))); statErr != nil {
		_ = os.MkdirAll(m.p(m.cfg.GitDir), 0o700)
		if _, err = m.git(ctx, site, log, "init", "--bare", "-q", mirror); err != nil {
			return
		}
	}
	if _, err = m.git(ctx, site, log, "-C", mirror, "config", "remote.origin.url", site.Deploy.Git.URL); err != nil {
		return
	}
	log("Fetching %s", site.Deploy.Git.URL)
	if _, err = m.git(ctx, site, log, "-C", mirror, "fetch", "--prune", "--force", "origin",
		"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"); err != nil {
		return "", "", "", fmt.Errorf("couldn't fetch the repository. Check the URL and that the deploy key or token has access: %w", err)
	}
	var want string
	switch refType {
	case "branch":
		want = "refs/heads/" + ref
	case "tag":
		if strings.Contains(ref, "*") {
			out, _ := m.git(ctx, site, log, "-C", mirror, "for-each-ref", "--sort=-creatordate", "--format=%(refname:short)", "refs/tags/")
			for _, t := range strings.Fields(string(out)) {
				if tagMatches(ref, t) {
					want = "refs/tags/" + t
					log("Newest tag matching %s is %s", ref, t)
					break
				}
			}
			if want == "" {
				return "", "", "", fmt.Errorf("no tag matches %s", ref)
			}
		} else {
			want = "refs/tags/" + ref
		}
	case "commit":
		want = ref
		if _, e := m.git(ctx, site, log, "-C", mirror, "cat-file", "-e", ref+"^{commit}"); e != nil {
			_, _ = m.git(ctx, site, log, "-C", mirror, "fetch", "origin", ref)
		}
	}
	out, err := m.git(ctx, site, log, "-C", mirror, "rev-parse", "--verify", "--quiet", want+"^{commit}")
	commit = strings.TrimSpace(string(out))
	if err != nil || !commitRe.MatchString(commit) {
		return "", "", "", fmt.Errorf("%s %s doesn't exist in the repository", refType, ref)
	}
	if info, e := m.git(ctx, site, log, "-C", mirror, "log", "-1", "--format=%an%x1f%s", commit); e == nil {
		a, s, _ := strings.Cut(strings.TrimSpace(string(info)), "\x1f")
		author, msg = a, s
	}
	return commit, msg, author, nil
}

// exportRelease writes the commit's files into a new release folder, owned by
// the account. git runs as root but only reads; tar unpacks as the account user.
func (m *Manager) exportRelease(ctx context.Context, site *Site, L layout, commit, rel string, log func(string, ...any)) error {
	if err := m.mkdirs(site.Account, []dirSpec{
		{path: m.releasesDir(L), mode: 0o750 | os.ModeSetgid, webGroup: true},
		{path: m.sharedDir(L), mode: 0o750 | os.ModeSetgid, webGroup: true},
	}); err != nil {
		return err
	}
	if err := os.MkdirAll(m.p(m.cfg.DeployTmpDir), 0o711); err != nil {
		return err
	}
	tarFile := filepath.Join(m.cfg.DeployTmpDir, randomSecret(8)+".tar")
	defer os.Remove(m.p(tarFile))
	tree := commit
	if g := site.Deploy.Git; g != nil && g.Subdir != "" {
		tree = commit + ":" + g.Subdir
	}
	if _, err := m.git(ctx, site, log, "-C", m.gitMirror(site), "archive", "--format=tar", "-o", tarFile, tree); err != nil {
		return fmt.Errorf("export files: %w", err)
	}
	if err := m.chown(tarFile, site.Account, false); err != nil {
		return err
	}
	if _, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "mkdir", "-p", rel); err != nil {
		return err
	}
	if m.cfg.DryRun { // nothing really ran; give later steps a folder to find
		_ = os.MkdirAll(m.p(rel), 0o750)
	}
	if _, err := m.asUser(ctx, L, rel, nil, nil, "tar", "-xf", tarFile, "-C", rel); err != nil {
		return fmt.Errorf("unpack files: %w", err)
	}
	log("Files exported to releases/%s", filepath.Base(rel))
	return nil
}

// linkShared keeps paths like .env or storage/ across releases.
func (m *Manager) linkShared(ctx context.Context, L layout, rel string, paths []string, log func(string, ...any)) error {
	shared := m.sharedDir(L)
	for _, p := range paths {
		target := filepath.Join(shared, p)
		if p == ".env" {
			target = filepath.Join(L.SiteDir, ".env") // the file the Env vars tab edits
		}
		inRelease := filepath.Join(rel, p)
		_, relErr := os.Lstat(m.p(inRelease))
		_, sharedErr := os.Lstat(m.p(target))
		switch {
		case sharedErr != nil && relErr == nil:
			// First deploy: the repository's copy becomes the shared one.
			if _, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "mkdir", "-p", filepath.Dir(target)); err != nil {
				return err
			}
			if _, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "mv", inRelease, target); err != nil {
				return err
			}
		case sharedErr != nil:
			if p == ".env" {
				if _, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "touch", target); err != nil {
					return err
				}
			} else if _, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "mkdir", "-p", target); err != nil {
				return err
			}
		case relErr == nil:
			if _, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "rm", "-rf", inRelease); err != nil {
				return err
			}
		}
		if _, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "mkdir", "-p", filepath.Dir(inRelease)); err != nil {
			return err
		}
		if _, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "ln", "-sfn", target, inRelease); err != nil {
			return err
		}
		log("Shared: %s", p)
	}
	return nil
}

func (m *Manager) exists(path string) bool {
	_, err := os.Lstat(m.p(path))
	return err == nil
}

// detectBuild picks build steps from the files in the release.
func (m *Manager) detectBuild(L layout, rel string) []string {
	has := func(f string) bool { return m.exists(filepath.Join(rel, f)) }
	var cmds []string
	npm := func() {
		switch {
		case has("pnpm-lock.yaml"):
			cmds = append(cmds, "npx --yes pnpm@9 install --frozen-lockfile")
		case has("yarn.lock"):
			cmds = append(cmds, "npx --yes yarn@1 install --frozen-lockfile")
		case has("package-lock.json"):
			cmds = append(cmds, "npm ci")
		default:
			cmds = append(cmds, "npm install")
		}
		if pkgHasScript(m.p(filepath.Join(rel, "package.json")), "build") {
			cmds = append(cmds, "npm run build")
		}
	}
	switch L.Runtime {
	case RuntimePHP:
		if has("composer.json") {
			cmds = append(cmds, "/usr/bin/php"+L.Version+" "+m.cfg.ComposerPath+" install --no-dev --no-interaction --prefer-dist --optimize-autoloader")
		}
		if has("package.json") && pkgHasScript(m.p(filepath.Join(rel, "package.json")), "build") {
			npm()
		}
	case RuntimeStatic, RuntimeNode:
		if has("package.json") {
			npm()
		}
	case RuntimePython:
		cmds = append(cmds, "python3 -m venv .venv")
		switch {
		case has("requirements.txt"):
			cmds = append(cmds, ".venv/bin/pip install --no-input -r requirements.txt")
		case has("pyproject.toml"):
			cmds = append(cmds, ".venv/bin/pip install --no-input .")
		}
	case RuntimeGo:
		cmds = append(cmds, "go build -o app .")
	case RuntimeJava:
		switch {
		case has("gradlew"):
			cmds = append(cmds, "sh ./gradlew --no-daemon build -x test")
		case has("pom.xml"):
			cmds = append(cmds, "mvn -B -q -DskipTests package")
		}
	case RuntimeDart:
		if has("pubspec.yaml") {
			cmds = append(cmds, "dart pub get")
		}
	}
	return cmds
}

func pkgHasScript(path, name string) bool {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 1<<20 {
		return false
	}
	var p struct {
		Scripts map[string]string `json:"scripts"`
	}
	return json.Unmarshal(b, &p) == nil && p.Scripts[name] != ""
}

// buildPath adds Node.js to PHP and static builds that have a package.json.
func (m *Manager) buildPath(L layout, rel string) string {
	p := runtimePath(L, rel)
	if L.Runtime == RuntimePHP || L.Runtime == RuntimeStatic {
		if v := InstalledRuntimes(m.cfg.Root, m.cfg.RuntimesDir)[RuntimeNode]; len(v) > 0 {
			p = rel + "/node_modules/.bin:" + m.runtimeBin(RuntimeNode, v[len(v)-1]) + ":" + p
		}
	}
	return p
}

// sandboxed runs one build or release step as the account user in a
// throwaway systemd unit: inside the account's resource slice, with the rest
// of the system read-only and a 30 minute limit.
func (m *Manager) sandboxed(ctx context.Context, L layout, dir, cmd string, log func(string, ...any)) error {
	args := []string{"--quiet", "--wait", "--pipe", "--collect", "--uid=" + L.User, "--gid=" + L.User,
		"--working-directory=" + dir, "--slice=" + L.Slice,
		"-p", "NoNewPrivileges=yes", "-p", "PrivateTmp=yes", "-p", "PrivateDevices=yes", "-p", "ProtectSystem=strict",
		"-p", "ProtectHome=tmpfs", "-p", "BindPaths=" + L.Home, "-p", "MemoryMax=2G", "-p", "RuntimeMaxSec=1800"}
	for _, e := range m.userEnv(L, dir) {
		if strings.HasPrefix(e, "PATH=") {
			e = "PATH=" + m.buildPath(L, dir)
		}
		args = append(args, "-E", e)
	}
	args = append(args, "-E", "CI=true", "-E", "COMPOSER_HOME="+L.Home+"/.composer")
	script := "set -eo pipefail; if [ -f " + L.SiteDir + "/.env ]; then set -a; . " + L.SiteDir + "/.env; set +a; fi; " + cmd
	args = append(args, "/bin/bash", "-c", script)
	_, err := m.run.Exec(ctx, runner.Command{Name: "systemd-run", Args: args, Output: logWriter(log), Timeout: 31 * time.Minute})
	return err
}

// colorLayout is the layout of one blue-green instance of an app.
func (m *Manager) colorLayout(L layout, color, dir string, port int) layout {
	c := L
	if color == "b" {
		c.UnitName = L.UnitName + "-b"
		c.StartScript = strings.TrimSuffix(L.StartScript, ".sh") + "-b.sh"
	}
	c.UnitFile = filepath.Join("/etc/systemd/system", c.UnitName+".service")
	if dir != "" {
		c.AppDir = dir
		c.AppEnvExtra = m.runtimeEnv(c)
		c.AppPath = runtimePath(c, dir)
	}
	if port != 0 {
		c.Port = port
	}
	return c
}

// switchRelease makes d.Release live.
func (m *Manager) switchRelease(ctx context.Context, d *Deployment, log func(string, ...any)) error {
	site, err := m.Get(d.Domain)
	if err != nil {
		return err
	}
	st := site.Deploy
	L := m.layout(site)
	if site.Runtime == RuntimeDocker {
		return m.switchContainer(ctx, site, d, log)
	}
	rel := filepath.Join(m.releasesDir(L), d.Release)
	if !m.exists(rel) {
		return fmt.Errorf("release %s was cleaned up; deploy its commit again instead", d.Release)
	}
	prev := st.Current

	if IsApp(site.Runtime) && st.Strategy == "blue-green" {
		return m.switchBlueGreen(ctx, site, d, rel, log)
	}
	if err := m.pointCurrent(ctx, L, d.Release); err != nil {
		return err
	}
	m.mu.Lock()
	site, err = m.Get(d.Domain)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	old := *site
	nd := *site.Deploy
	nd.Current = d.Release
	site.Deploy = &nd
	newL := m.layout(site)
	switch {
	case prev == "":
		// First deployment: the web server and app move from public_html/app to the release.
		err = m.applyWeb(ctx, site, &old)
		if err == nil && newL.IsApp {
			err = m.writeApp(ctx, newL, false)
		}
	default:
		err = m.writeJSON(newL.StateFile, site)
		if err == nil && newL.IsApp {
			_, err = m.run.Run(ctx, "systemctl", "restart", newL.ActiveUnit)
		}
	}
	m.mu.Unlock()
	if err != nil {
		if prev != "" {
			_ = m.pointCurrent(context.Background(), L, prev)
		}
		return err
	}
	if newL.IsApp {
		if err := m.healthy(ctx, newL.ActivePort, site.Domain, st.HealthPath); err != nil && prev != "" {
			log("The new release isn't answering (%v); going back to %s", err, prev)
			_ = m.pointCurrent(context.Background(), L, prev)
			m.setCurrent(d.Domain, prev)
			_, _ = m.run.Run(context.Background(), "systemctl", "restart", newL.ActiveUnit)
			return fmt.Errorf("the app didn't start: %w", err)
		}
	}
	if site.Runtime == RuntimePHP {
		_, _ = m.run.Run(ctx, "systemctl", "reload", "php"+site.Version+"-fpm") // clears the opcode cache
	}
	log("Switched to release %s", d.Release)
	return nil
}

func (m *Manager) setCurrent(domain, release string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if site, err := m.Get(domain); err == nil && site.Deploy != nil {
		site.Deploy.Current = release
		_ = m.writeJSON(m.layout(site).StateFile, site)
	}
}

// pointCurrent swaps the current symlink atomically (rename over the old link).
func (m *Manager) pointCurrent(ctx context.Context, L layout, release string) error {
	tmp := filepath.Join(L.SiteDir, ".current-"+randomSecret(3))
	if _, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "ln", "-sfn", "releases/"+release, tmp); err != nil {
		return err
	}
	_, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "mv", "-Tf", tmp, m.currentLink(L))
	return err
}

// switchBlueGreen starts the release on the idle instance, waits until it
// answers, moves traffic to it and stops the old one: no downtime.
func (m *Manager) switchBlueGreen(ctx context.Context, site *Site, d *Deployment, rel string, log func(string, ...any)) error {
	st := site.Deploy
	L := m.layout(site)
	from, to := st.Color, "b"
	if from == "" {
		from = "a"
	}
	if from == "b" {
		to = "a"
	}
	port := site.Port
	if to == "b" {
		port = st.AltPort
	}
	newL := m.colorLayout(L, to, rel, port)
	oldL := m.colorLayout(L, from, "", 0)
	log("Starting the new release on port %d", port)
	if err := m.render(startScriptTmpl, newL, newL.StartScript, 0o755); err != nil {
		return err
	}
	if err := m.render(unitTmpl, newL, newL.UnitFile, 0o644); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "systemctl", "restart", newL.UnitName); err != nil {
		return err
	}
	if err := m.healthy(ctx, port, site.Domain, st.HealthPath); err != nil {
		_, _ = m.run.Run(context.Background(), "systemctl", "stop", newL.UnitName)
		return fmt.Errorf("the new release didn't answer on %s, so traffic stayed on the old one: %w", st.HealthPath, err)
	}
	if err := m.pointCurrent(ctx, L, d.Release); err != nil {
		return err
	}
	m.mu.Lock()
	cur, err := m.Get(site.Domain)
	if err == nil {
		old := *cur
		nd := *cur.Deploy
		nd.Current, nd.Color = d.Release, to
		nd.Colors = map[string]string{}
		for k, v := range cur.Deploy.Colors {
			nd.Colors[k] = v
		}
		nd.Colors[to] = d.Release
		cur.Deploy = &nd
		err = m.applyWeb(ctx, cur, &old) // Nginx now forwards to the new port (graceful reload)
	}
	m.mu.Unlock()
	if err != nil {
		_, _ = m.run.Run(context.Background(), "systemctl", "stop", newL.UnitName)
		return err
	}
	_, _ = m.run.Run(ctx, "systemctl", "enable", newL.UnitName)
	log("Traffic moved to the new release; stopping the old instance")
	time.AfterFunc(drainDelay, func() {
		_, _ = m.run.Run(context.Background(), "systemctl", "disable", "--now", oldL.UnitName)
	})
	return nil
}

// drainDelay lets requests already on the old instance finish.
var drainDelay = 10 * time.Second

// pruneReleases keeps the newest releases (and whatever is live).
func (m *Manager) pruneReleases(ctx context.Context, domain string, log func(string, ...any)) {
	site, err := m.Get(domain)
	if err != nil || site.Deploy == nil {
		return
	}
	st := site.Deploy
	keep := map[string]bool{st.Current: true}
	for _, v := range st.Colors {
		keep[v] = true
	}
	L := m.layout(site)
	if site.Runtime == RuntimeDocker {
		m.pruneImages(ctx, site, keep, log)
		return
	}
	entries, _ := os.ReadDir(m.p(m.releasesDir(L)))
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for i, n := range names {
		if i < st.KeepReleases || keep[n] {
			continue
		}
		if _, err := m.asUser(ctx, L, L.SiteDir, nil, nil, "rm", "-rf", filepath.Join(m.releasesDir(L), n)); err == nil {
			log("Removed old release %s", n)
		}
	}
}

// ---------------------------------------------------------------- health

func waitHealthy(ctx context.Context, port int, host, path string) error {
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(90 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
		req.Host = host
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
			last = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return last
}

// ---------------------------------------------------------------- notify

func (m *Manager) sendNotify(d *Deployment) {
	site, err := m.Get(d.Domain)
	if err != nil || site.Deploy == nil || site.Deploy.NotifyURL == "" {
		return
	}
	secret, _ := os.ReadFile(m.p(filepath.Join(m.gitSecrets(site), "webhook_secret")))
	body, _ := json.Marshal(map[string]any{"event": "deployment.finished", "deployment": d})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.notify(ctx, site.Deploy.NotifyURL, strings.TrimSpace(string(secret)), body); err != nil {
		slog.Warn("deployment notification failed", "domain", d.Domain, "err", err)
	}
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// postSigned sends a signed JSON event. It refuses private and loopback
// addresses so a notification URL can't be used to reach internal services.
func postSigned(ctx context.Context, url, secret string, body []byte) error {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsUnspecified() {
				return nil, fmt.Errorf("%s is a private address", host)
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	client := &http.Client{Timeout: 10 * time.Second, Transport: tr}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Ike-Deploy")
	req.Header.Set("X-Ike-Signature", sign(secret, body))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
