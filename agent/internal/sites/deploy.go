package sites

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/validate"
)

// DeploySetup connects a website to a Git repository or a container image.
type DeploySetup struct {
	Type           string           `json:"type"` // native | dockerfile | buildpack | image
	Git            *GitSource       `json:"git,omitempty"`
	Token          string           `json:"token,omitempty"` // token auth; empty keeps the saved one
	Image          string           `json:"image,omitempty"`
	Strategy       string           `json:"strategy,omitempty"`
	PublicDir      string           `json:"public_dir,omitempty"`
	SharedPaths    []string         `json:"shared_paths,omitempty"`
	BuildCommand   string           `json:"build_command,omitempty"`
	ReleaseCommand string           `json:"release_command,omitempty"`
	ContainerPort  int              `json:"container_port,omitempty"`
	HealthPath     string           `json:"health_path,omitempty"`
	KeepReleases   int              `json:"keep_releases,omitempty"`
	NotifyURL      string           `json:"notify_url,omitempty"`
	Previews       *PreviewSettings `json:"previews,omitempty"`
}

// DeployInfo is what the panel shows on a website's Deploy tab.
type DeployInfo struct {
	Deploy        *DeployState `json:"deploy"`
	DeployKey     string       `json:"deploy_key,omitempty"` // public key to add to the repository
	HasToken      bool         `json:"has_token"`
	WebhookPath   string       `json:"webhook_path"`             // relative to the panel's public URL
	WebhookSecret string       `json:"webhook_secret,omitempty"` // shown when created or regenerated
	Instructions  []string     `json:"instructions"`
}

var (
	gitHTTPSRe   = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?/[A-Za-z0-9._~/-]+$`)
	gitSCPRe     = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9._~/-]+$`)
	gitSSHRe     = regexp.MustCompile(`^ssh://[A-Za-z0-9._-]+@[A-Za-z0-9.-]+(:[0-9]{1,5})?/[A-Za-z0-9._~/-]+$`)
	branchRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	tagPatternRe = regexp.MustCompile(`^[A-Za-z0-9*][A-Za-z0-9._/*-]{0,199}$`)
	commitRe     = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	imageRe      = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]{1,5})?(/[a-z0-9]+([._-][a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(@sha256:[0-9a-f]{64})?$`)
	userRe       = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,100}$`)
	tokenRe      = regexp.MustCompile(`^[\x21-\x7e]{8,512}$`)
)

func gitHost(u string) string {
	switch {
	case strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "ssh://"):
		if p, err := url.Parse(u); err == nil {
			return p.Hostname()
		}
	case gitSCPRe.MatchString(u):
		_, rest, _ := strings.Cut(u, "@")
		host, _, _ := strings.Cut(rest, ":")
		return host
	}
	return ""
}

func detectProvider(u string) string {
	switch h := gitHost(u); {
	case h == "github.com":
		return "github"
	case h == "gitlab.com" || strings.HasPrefix(h, "gitlab."):
		return "gitlab"
	case h == "bitbucket.org":
		return "bitbucket"
	}
	return "other"
}

func checkRef(refType, ref string) error {
	switch refType {
	case "branch":
		if !branchRe.MatchString(ref) || strings.Contains(ref, "..") || strings.HasSuffix(ref, ".lock") || strings.HasSuffix(ref, "/") {
			return fmt.Errorf("%q isn't a valid branch name", ref)
		}
	case "tag":
		if !tagPatternRe.MatchString(ref) || strings.Contains(ref, "..") {
			return fmt.Errorf("%q isn't a valid tag or tag pattern (like v*)", ref)
		}
	case "commit":
		if !commitRe.MatchString(ref) {
			return fmt.Errorf("%q isn't a commit hash", ref)
		}
	default:
		return fmt.Errorf("ref type must be branch, tag or commit")
	}
	return nil
}

func (g *GitSource) check() error {
	g.URL = strings.TrimSpace(g.URL)
	switch {
	case gitHTTPSRe.MatchString(g.URL), gitSCPRe.MatchString(g.URL), gitSSHRe.MatchString(g.URL):
	default:
		return fmt.Errorf("repository must be an https:// URL or an SSH address like git@github.com:owner/repo.git")
	}
	if strings.Contains(g.URL, "..") {
		return fmt.Errorf("repository URL can't contain ..")
	}
	if g.Provider == "" {
		g.Provider = detectProvider(g.URL)
	}
	switch g.Provider {
	case "github", "gitlab", "bitbucket", "other":
	default:
		return fmt.Errorf("provider must be github, gitlab, bitbucket or other")
	}
	if g.RefType == "" {
		g.RefType = "branch"
	}
	if g.Ref == "" && g.RefType == "branch" {
		g.Ref = "main"
	}
	if err := checkRef(g.RefType, g.Ref); err != nil {
		return err
	}
	if g.Subdir != "" {
		if err := validate.RelPath(g.Subdir); err != nil {
			return fmt.Errorf("subfolder: %w", err)
		}
	}
	if g.Auth == "" {
		g.Auth = "none"
	}
	switch g.Auth {
	case "none":
	case "deploy_key":
		if strings.HasPrefix(g.URL, "https://") {
			return fmt.Errorf("deploy keys work with SSH addresses (git@host:owner/repo.git); use a token for https:// URLs")
		}
	case "token":
		if !strings.HasPrefix(g.URL, "https://") {
			return fmt.Errorf("tokens work with https:// URLs; use a deploy key for SSH addresses")
		}
		if g.Username != "" && !userRe.MatchString(g.Username) {
			return fmt.Errorf("invalid username")
		}
	default:
		return fmt.Errorf("auth must be none, deploy_key or token")
	}
	return nil
}

// validateCommand: build and release commands run as the account user inside
// a sandbox, so their content is the user's business; they must be one line.
func validateCommand(what, cmd string) error {
	if cmd == "" {
		return nil
	}
	if len(cmd) > 1000 {
		return fmt.Errorf("%s must be under 1000 characters", what)
	}
	for _, r := range cmd {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s must be one line (join steps with &&)", what)
		}
	}
	return nil
}

func (s *DeploySetup) check(site *Site) error {
	switch s.Type {
	case "native":
		if !IsApp(site.Runtime) && site.Runtime != RuntimePHP && site.Runtime != RuntimeStatic {
			return fmt.Errorf("%s websites deploy with a Dockerfile, a buildpack or an image", site.Runtime)
		}
	case "dockerfile", "buildpack", "image":
		if site.Runtime != RuntimeDocker {
			return fmt.Errorf("container deployments need a website created with the docker runtime")
		}
	default:
		return fmt.Errorf("type must be native, dockerfile, buildpack or image")
	}
	if s.Type == "image" {
		s.Git = nil
		if !imageRe.MatchString(s.Image) {
			return fmt.Errorf("image must look like nginx:1.27 or ghcr.io/owner/app:latest")
		}
	} else {
		s.Image = ""
		if s.Git == nil {
			return fmt.Errorf("choose the Git repository to deploy from")
		}
		if err := s.Git.check(); err != nil {
			return err
		}
	}
	if s.Token != "" && !tokenRe.MatchString(s.Token) {
		return fmt.Errorf("the token has characters a token can't have")
	}
	if s.Strategy == "" {
		s.Strategy = "restart"
		if site.Runtime == RuntimeDocker {
			s.Strategy = "blue-green"
		}
	}
	switch s.Strategy {
	case "restart":
		if site.Runtime == RuntimeDocker {
			return fmt.Errorf("containers always switch blue-green")
		}
	case "blue-green":
		if !IsApp(site.Runtime) && site.Runtime != RuntimeDocker {
			return fmt.Errorf("PHP and static websites already switch releases instantly; blue-green is for apps")
		}
	default:
		return fmt.Errorf("strategy must be restart or blue-green")
	}
	if s.PublicDir != "" {
		if site.Runtime != RuntimePHP && site.Runtime != RuntimeStatic {
			return fmt.Errorf("a public folder only applies to PHP and static websites")
		}
		if err := validate.RelPath(s.PublicDir); err != nil {
			return fmt.Errorf("public folder: %w", err)
		}
	}
	if len(s.SharedPaths) > 20 {
		return fmt.Errorf("at most 20 shared paths")
	}
	for _, p := range s.SharedPaths {
		if err := validate.RelPath(p); err != nil {
			return fmt.Errorf("shared path: %w", err)
		}
	}
	if err := validateCommand("build command", s.BuildCommand); err != nil {
		return err
	}
	if err := validateCommand("release command", s.ReleaseCommand); err != nil {
		return err
	}
	if site.Runtime == RuntimeDocker {
		if s.ContainerPort == 0 {
			s.ContainerPort = 8080
		}
		if s.ContainerPort < 1 || s.ContainerPort > 65535 {
			return fmt.Errorf("container port must be 1-65535")
		}
	} else {
		s.ContainerPort = 0
	}
	if s.HealthPath == "" {
		s.HealthPath = "/"
	}
	if !pathRe.MatchString(s.HealthPath) {
		return fmt.Errorf("health check path must start with /")
	}
	if s.KeepReleases == 0 {
		s.KeepReleases = 5
	}
	if s.KeepReleases < 2 || s.KeepReleases > 30 {
		return fmt.Errorf("keep 2-30 releases")
	}
	if s.NotifyURL != "" {
		u, err := url.Parse(s.NotifyURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(s.NotifyURL) > 500 {
			return fmt.Errorf("the notification URL must be an https:// address")
		}
	}
	if p := s.Previews; p != nil && p.Enabled {
		if s.Git == nil {
			return fmt.Errorf("previews need a Git repository")
		}
		if err := validate.Domain(p.BaseDomain); err != nil {
			return fmt.Errorf("preview domain: %w", err)
		}
		if p.BaseDomain == site.Domain {
			return fmt.Errorf("use a separate domain for previews, like preview.%s (with a *.preview DNS record)", site.Domain)
		}
	}
	return nil
}

func (m *Manager) gitSecrets(s *Site) string {
	return filepath.Join(m.cfg.GitSecretsDir, s.Account+"-"+siteID(s.Domain))
}
func (m *Manager) gitMirror(s *Site) string {
	return filepath.Join(m.cfg.GitDir, s.Account+"-"+siteID(s.Domain)+".git")
}

func randomSecret(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ConfigureDeploy saves how a website deploys. It creates a deploy key and a
// webhook secret the first time.
func (m *Manager) ConfigureDeploy(ctx context.Context, domain string, req DeploySetup) (*DeployInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if site.Deploy != nil && site.Deploy.PreviewOf != "" {
		return nil, fmt.Errorf("%s is a preview of %s; change the settings there", domain, site.Deploy.PreviewOf)
	}
	if err := req.check(site); err != nil {
		return nil, err
	}
	sec := m.gitSecrets(site)
	if req.Git != nil && req.Git.Auth == "token" && req.Token == "" {
		if _, err := os.Stat(m.p(filepath.Join(sec, "token"))); err != nil {
			return nil, fmt.Errorf("paste the access token for this repository")
		}
	}
	if err := os.MkdirAll(m.p(sec), 0o700); err != nil {
		return nil, err
	}
	newSecret := ""
	if _, err := os.Stat(m.p(filepath.Join(sec, "webhook_secret"))); err != nil {
		newSecret = randomSecret(24)
		if err := writeFile(m.p(filepath.Join(sec, "webhook_secret")), []byte(newSecret), 0o600); err != nil {
			return nil, err
		}
	}
	if req.Git != nil && req.Git.Auth == "deploy_key" {
		if _, err := os.Stat(m.p(filepath.Join(sec, "id_ed25519"))); err != nil {
			if err := m.makeDeployKey(ctx, site); err != nil {
				return nil, err
			}
		}
	}
	if req.Git != nil && req.Git.Auth == "token" && req.Token != "" {
		if err := writeFile(m.p(filepath.Join(sec, "token")), []byte(req.Token), 0o600); err != nil {
			return nil, err
		}
	}
	d := site.Deploy
	if d == nil {
		d = &DeployState{}
	}
	if d.Git != nil && req.Git != nil && d.Git.URL != req.Git.URL {
		_ = os.RemoveAll(m.p(m.gitMirror(site))) // a new repository starts a fresh mirror
	}
	d.Type, d.Git, d.Image, d.Strategy = req.Type, req.Git, req.Image, req.Strategy
	d.PublicDir, d.SharedPaths, d.BuildCommand, d.ReleaseCommand = req.PublicDir, req.SharedPaths, req.BuildCommand, req.ReleaseCommand
	d.ContainerPort, d.HealthPath, d.KeepReleases, d.NotifyURL, d.Previews = req.ContainerPort, req.HealthPath, req.KeepReleases, req.NotifyURL, req.Previews
	if d.SharedPaths == nil && site.Runtime != RuntimeDocker {
		d.SharedPaths = []string{".env"}
	}
	if d.Strategy == "blue-green" && d.AltPort == 0 {
		site.Deploy = d // so nextPort sees the site's own port
		if d.AltPort = m.nextPort(); d.AltPort == 0 {
			return nil, fmt.Errorf("no free local ports left")
		}
	}
	old := *site
	site.Deploy = d
	if d.Current != "" {
		// The public folder may have moved: rewrite the web server config.
		if err := m.applyWeb(ctx, site, &old); err != nil {
			return nil, err
		}
	} else if err := m.writeJSON(m.layout(site).StateFile, site); err != nil {
		return nil, err
	}
	info := m.deployInfo(site)
	info.WebhookSecret = newSecret
	return info, nil
}

func (m *Manager) makeDeployKey(ctx context.Context, site *Site) error {
	key := filepath.Join(m.gitSecrets(site), "id_ed25519")
	_ = os.Remove(m.p(key))
	_ = os.Remove(m.p(key + ".pub"))
	_, err := m.run.Run(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "ike-deploy@"+site.Domain, "-f", key)
	return err
}

// RegenerateDeployKey makes a new deploy key; the old one stops working.
func (m *Manager) RegenerateDeployKey(ctx context.Context, domain string) (*DeployInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if site.Deploy == nil || site.Deploy.Git == nil || site.Deploy.Git.Auth != "deploy_key" {
		return nil, fmt.Errorf("%s doesn't use a deploy key", domain)
	}
	if err := m.makeDeployKey(ctx, site); err != nil {
		return nil, err
	}
	return m.deployInfo(site), nil
}

// RegenerateWebhookSecret makes a new secret; update it in the Git host.
func (m *Manager) RegenerateWebhookSecret(domain string) (*DeployInfo, error) {
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if site.Deploy == nil {
		return nil, fmt.Errorf("%s isn't set up for deployments", domain)
	}
	secret := randomSecret(24)
	if err := writeFile(m.p(filepath.Join(m.gitSecrets(site), "webhook_secret")), []byte(secret), 0o600); err != nil {
		return nil, err
	}
	info := m.deployInfo(site)
	info.WebhookSecret = secret
	return info, nil
}

// DeployInfo returns a website's deployment settings.
func (m *Manager) DeployInfo(domain string) (*DeployInfo, error) {
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	return m.deployInfo(site), nil
}

func (m *Manager) deployInfo(site *Site) *DeployInfo {
	info := &DeployInfo{Deploy: site.Deploy, WebhookPath: "/webhooks/git/" + site.Domain, Instructions: []string{}}
	sec := m.gitSecrets(site)
	if b, err := os.ReadFile(m.p(filepath.Join(sec, "id_ed25519.pub"))); err == nil {
		info.DeployKey = strings.TrimSpace(string(b))
	}
	if _, err := os.Stat(m.p(filepath.Join(sec, "token"))); err == nil {
		info.HasToken = true
	}
	if site.Deploy == nil || site.Deploy.Git == nil {
		return info
	}
	g := site.Deploy.Git
	switch g.Provider {
	case "github":
		if g.Auth == "deploy_key" {
			info.Instructions = append(info.Instructions, "In the repository on GitHub: Settings → Deploy keys → Add deploy key. Paste the key; leave write access off.")
		}
		info.Instructions = append(info.Instructions, "Settings → Webhooks → Add webhook. Payload URL: the webhook URL, content type application/json, secret: the webhook secret. Choose 'Just the push event' (add Pull requests for previews).")
	case "gitlab":
		if g.Auth == "deploy_key" {
			info.Instructions = append(info.Instructions, "In the project on GitLab: Settings → Repository → Deploy keys. Paste the key; leave write access off.")
		}
		info.Instructions = append(info.Instructions, "Settings → Webhooks → Add new webhook. URL: the webhook URL, Secret token: the webhook secret. Tick Push events and Tag push events (and Merge request events for previews).")
	case "bitbucket":
		if g.Auth == "deploy_key" {
			info.Instructions = append(info.Instructions, "In the repository on Bitbucket: Repository settings → Access keys → Add key.")
		}
		info.Instructions = append(info.Instructions, "Repository settings → Webhooks → Add webhook. URL: the webhook URL, Secret: the webhook secret. Trigger: Repository push (and pull request events for previews).")
	default:
		info.Instructions = append(info.Instructions, "Have your Git host POST to the webhook URL on each push, signed with the webhook secret (header X-Ike-Signature: sha256=<HMAC of the body>), with a JSON body like {\"ref\":\"refs/heads/main\",\"commit\":\"<sha>\"}.")
	}
	return info
}

// DisconnectDeploy removes the repository link, keys and secrets. The live
// release keeps serving; upload changes over SFTP or connect again.
func (m *Manager) DisconnectDeploy(ctx context.Context, domain string) (*Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if site.Deploy == nil {
		return site, nil
	}
	_ = os.RemoveAll(m.p(m.gitSecrets(site)))
	_ = os.RemoveAll(m.p(m.gitMirror(site)))
	if site.Deploy.Current == "" {
		site.Deploy = nil
	} else {
		site.Deploy.Git, site.Deploy.Previews, site.Deploy.NotifyURL = nil, nil, ""
	}
	return site, m.writeJSON(m.layout(site).StateFile, site)
}

// ------------------------------------------------------------------ history

// Deployment is one deploy attempt.
type Deployment struct {
	ID         string     `json:"id"`
	Domain     string     `json:"domain"`
	Trigger    string     `json:"trigger"` // manual | push | api | rollback | preview
	Actor      string     `json:"actor,omitempty"`
	RefType    string     `json:"ref_type,omitempty"`
	Ref        string     `json:"ref,omitempty"`
	Commit     string     `json:"commit,omitempty"`
	Message    string     `json:"message,omitempty"`
	Author     string     `json:"author,omitempty"`
	Image      string     `json:"image,omitempty"`
	Release    string     `json:"release,omitempty"`
	Status     string     `json:"status"` // queued | building | live | failed | cancelled | superseded | replaced
	Error      string     `json:"error,omitempty"`
	QueuedAt   time.Time  `json:"queued_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	RollbackOf string     `json:"rollback_of,omitempty"`
}

const keepHistory = 100

func (m *Manager) historyFile(domain string) string {
	return filepath.Join(m.cfg.DeployDir, domain+".json")
}

func (m *Manager) logFile(domain, id string) string {
	return filepath.Join(m.cfg.DeployLogDir, domain, id+".log")
}

// Deployments lists a website's deployments, newest first.
func (m *Manager) Deployments(domain string) ([]Deployment, error) {
	if err := validate.Domain(domain); err != nil {
		return nil, err
	}
	m.histMu.Lock()
	defer m.histMu.Unlock()
	return m.loadHistory(domain), nil
}

func (m *Manager) loadHistory(domain string) []Deployment {
	var list []Deployment
	if err := m.readJSON(m.historyFile(domain), &list); err != nil {
		return []Deployment{}
	}
	return list
}

func (m *Manager) saveDeployment(d Deployment) {
	m.histMu.Lock()
	defer m.histMu.Unlock()
	list := m.loadHistory(d.Domain)
	found := false
	for i := range list {
		if list[i].ID == d.ID {
			list[i], found = d, true
		}
	}
	if !found {
		list = append([]Deployment{d}, list...)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].QueuedAt.After(list[j].QueuedAt) })
	if len(list) > keepHistory {
		for _, old := range list[keepHistory:] {
			_ = os.Remove(m.p(m.logFile(d.Domain, old.ID)))
		}
		list = list[:keepHistory]
	}
	_ = m.writeJSON(m.historyFile(d.Domain), list)
}

// Deployment returns one deployment with its build log.
func (m *Manager) Deployment(domain, id string) (*Deployment, string, error) {
	list, err := m.Deployments(domain)
	if err != nil {
		return nil, "", err
	}
	for i := range list {
		if list[i].ID == id {
			b, _ := os.ReadFile(m.p(m.logFile(domain, id)))
			if len(b) > 512<<10 {
				b = append([]byte("…\n"), b[len(b)-512<<10:]...)
			}
			return &list[i], string(b), nil
		}
	}
	return nil, "", fmt.Errorf("deployment %w", ErrNotFound)
}

// removeDeployState deletes deployment history, secrets, mirrors, previews and containers.
func (m *Manager) removeDeployState(ctx context.Context, s *Site, L layout) {
	if s.Deploy == nil {
		return
	}
	_ = os.RemoveAll(m.p(m.gitSecrets(s)))
	_ = os.RemoveAll(m.p(m.gitMirror(s)))
	_ = os.Remove(m.p(m.historyFile(s.Domain)))
	_ = os.RemoveAll(m.p(filepath.Join(m.cfg.DeployLogDir, s.Domain)))
	if s.Runtime == RuntimeDocker {
		key := s.Account + "-" + siteID(s.Domain)
		for _, c := range []string{"a", "b"} {
			_, _ = m.run.Run(ctx, "docker", "rm", "-f", "ike-"+key+"-"+c)
		}
	}
	if L.IsApp {
		bL := m.colorLayout(L, "b", "", 0)
		m.removeApp(ctx, bL)
	}
}

// tagMatches reports whether a tag fits a pattern like v* or release-*.
func tagMatches(pattern, tag string) bool {
	ok, err := path.Match(pattern, tag)
	return err == nil && ok
}

var errNoChange = errors.New("nothing to deploy")

var _ = runner.Command{}
