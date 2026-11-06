package sites

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/validate"
)

// Site is one website inside an account.
type Site struct {
	Domain       string       `json:"domain"`
	Account      string       `json:"account"`
	Runtime      string       `json:"runtime"`           // php | node | python | static
	Version      string       `json:"version,omitempty"` // 8.3, 22, 3.12
	WebServer    string       `json:"web_server"`        // nginx | apache | nginx+apache
	DocRoot      string       `json:"doc_root"`
	Port         int          `json:"port,omitempty"` // local port for Node.js/Python apps
	StartCommand string       `json:"start_command,omitempty"`
	Status       string       `json:"status"`               // active | maintenance | suspended
	AllowIPs     []string     `json:"allow_ips,omitempty"`  // see the real site during maintenance
	Staging      bool         `json:"staging,omitempty"`    // a staging copy, hidden from search engines
	StagingOf    string       `json:"staging_of,omitempty"` // the live domain it was copied from
	Health       *HealthCheck `json:"health,omitempty"`     // apps only
	CreatedAt    time.Time    `json:"created_at"`

	// Domains: extra names served by this website.
	Aliases         []string          `json:"aliases,omitempty"`          // other domains, subdomains or *.wildcards
	WWW             string            `json:"www,omitempty"`              // both (default) | www | root | none
	RedirectAliases bool              `json:"redirect_aliases,omitempty"` // aliases 301 to the main domain
	Redirects       []Redirect        `json:"redirects,omitempty"`
	PHP             *PHPSettings      `json:"php,omitempty"`
	SSL             *SSLSettings      `json:"ssl,omitempty"`
	Deploy          *DeployState      `json:"deploy,omitempty"`
	WAF             *WAFSettings      `json:"waf,omitempty"`
	Balancer        *BalancerSettings `json:"balancer,omitempty"`
	Protection      *Protection       `json:"protection,omitempty"`
}

// CreateRequest adds a website to an existing account.
type CreateRequest struct {
	Domain       string `json:"domain"`
	Account      string `json:"account"`
	Runtime      string `json:"runtime,omitempty"`    // default php
	Version      string `json:"version,omitempty"`    // default: server default / newest installed
	WebServer    string `json:"web_server,omitempty"` // default: the admin's choice for this server
	StartCommand string `json:"start_command,omitempty"`
	Port         int    `json:"port,omitempty"` // proxy runtime: the local port to forward to
	// Balancer runtime: the backend pool.
	Balancer *BalancerSettings `json:"balancer,omitempty"`
}

// UpdateRequest changes a website's runtime version, web server or start command.
type UpdateRequest struct {
	Version      string `json:"version,omitempty"`
	WebServer    string `json:"web_server,omitempty"`
	StartCommand string `json:"start_command,omitempty"`
}

type layout struct {
	Domain, User, Runtime, Version, WebServer string
	// Root is the folder the web server serves: public_html, or the live
	// release (current/<public dir>) for deployed websites.
	Root                                  string
	ActivePort                            int    // the port Nginx forwards to (blue-green switches it)
	ActiveUnit                            string // the systemd unit serving now
	Proxied                               bool
	ServerNames                           string // main server block
	RedirectNames                         string // names that redirect to CanonicalHost
	CanonicalHost                         string
	NginxRedirects                        []string // rendered location blocks
	NginxRewrites                         []string // rendered server-level rewrite lines
	ApacheRules                           []string // rendered RewriteRule lines
	AcmeDir                               string
	TLS                                   bool
	CertFile, KeyFile                     string
	ForceHTTPS, HSTS                      bool
	HTTP2, HTTP3, H2Directive             bool
	PHP                                   PHPSettings // effective values (defaults filled in)
	PHPReadTimeout                        int
	BodyLimitMB                           int
	AppEnvExtra                           []string // extra exports for the start script
	AppPath                               string
	ServerList, RedirectList              []string
	ApacheTLS                             bool // Apache serves HTTPS itself (Apache-only stack)
	ApacheRedirectListens                 []string
	Home, SiteDir, DocRoot, AppDir        string
	TmpDir, LogDir, ErrorsDir, PagesDir   string
	Status, StatusPage, ErrorCodes        string
	AllowIPs, AllowIPsRegex               []string
	ErrorCodeList                         []int
	PoolName, PHPSocket                   string
	WebUser, WebGroup, ApacheListen       string
	VhostFile, ApacheFile, PoolFile       string
	StateFile, UnitName, UnitFile         string
	StartScript, RuntimeBin, StartCommand string
	Port                                  int
	IsApp, Staging                        bool
	Slice                                 string
	PHPWorkers, PHPMemoryMB               int
	// ModSecurity: on in Nginx when Nginx serves the site, otherwise in Apache.
	WAFNginx, WAFApache bool
	WAFRulesFile        string
	WAFBlock            bool
	WAFDisabled         []int
	// Password protection, shortest path first
	AuthRules  []authRule
	AuthAnyIPs bool // some protected path lets addresses in without a password
	// Load balancer runtime
	Balanced                                   bool
	NginxUpstream, ApacheBalancer              string
	UpstreamName, UpstreamScheme, UpstreamHost string
}

// siteID is a short, stable, filename-safe id for a domain.
func siteID(domain string) string {
	h := sha256.Sum256([]byte(domain))
	return hex.EncodeToString(h[:4])
}

func (m *Manager) layout(s *Site) layout {
	A := m.accountLayout(s.Account)
	site := filepath.Join(A.WebDir, s.Domain)
	key := s.Account + "-" + siteID(s.Domain)
	L := layout{
		Domain: s.Domain, User: s.Account, Runtime: s.Runtime, Version: s.Version, WebServer: s.WebServer,
		Home:         A.Home,
		SiteDir:      site,
		DocRoot:      filepath.Join(site, "public_html"),
		AppDir:       filepath.Join(site, "app"),
		TmpDir:       A.TmpDir,
		LogDir:       filepath.Join("/var/log/ike", s.Domain),
		ErrorsDir:    filepath.Join(site, "errors"),
		PagesDir:     m.cfg.PagesDir,
		Status:       statusOf(s.Status),
		AllowIPs:     s.AllowIPs,
		WebUser:      m.cfg.WebUser,
		WebGroup:     m.cfg.WebGroup,
		ApacheListen: m.cfg.ApacheListen,
		VhostFile:    filepath.Join(m.cfg.NginxSitesDir, s.Domain+".conf"),
		ApacheFile:   filepath.Join(m.cfg.ApacheSitesDir, s.Domain+".conf"),
		StateFile:    filepath.Join(m.cfg.StateDir, s.Domain+".json"),
		UnitName:     "ike-app-" + key,
		StartScript:  filepath.Join(m.cfg.AppsDir, key+".sh"),
		Port:         s.Port,
		StartCommand: s.StartCommand,
		IsApp:        IsApp(s.Runtime),
		Proxied:      IsProxied(s.Runtime),
		AcmeDir:      m.cfg.AcmeDir,
		Staging:      s.Staging,
		Slice:        "ike-acct-" + s.Account + ".slice",
		PoolName:     key,
	}
	L.UnitFile = filepath.Join("/etc/systemd/system", L.UnitName+".service")
	var lim Limits
	if a, err := m.getAccount(s.Account); err == nil {
		lim = a.Limits
	}
	L.PHPWorkers, L.PHPMemoryMB = lim.phpWorkers(), lim.phpMemoryMB()
	m.applyDeployLayout(s, &L)
	m.applyDomainLayout(s, &L)
	m.applySSLLayout(s, &L)
	m.applyWAFLayout(s, &L)
	m.applyBalancerLayout(s, &L)
	m.applyProtectionLayout(s, &L)
	L.applyPHP(s.PHP, lim)
	if L.RedirectNames != "" && L.WebServer == "apache" {
		L.ApacheRedirectListens = []string{L.ApacheListen}
		if L.ApacheTLS {
			L.ApacheRedirectListens = append(L.ApacheRedirectListens, "*:443")
		}
	}
	// Status page: the website's own copy if it has one, else the server-wide page.
	if L.Status != StatusActive {
		L.StatusPage = filepath.Join(m.cfg.PagesDir, L.Status+".html")
		if L.Status == StatusMaintenance {
			if _, err := os.Stat(m.p(filepath.Join(L.ErrorsDir, "maintenance.html"))); err == nil {
				L.StatusPage = filepath.Join(L.ErrorsDir, "maintenance.html")
			}
		}
	}
	// Nginx answers these itself. Behind a proxy only its own gateway errors are
	// replaced, so an app's or Apache's own 404 pages still show.
	codes := ErrorCodes
	if L.Proxied || L.WebServer == "nginx+apache" {
		codes = []int{502, 504}
	}
	// Apache serves the website itself, so it gets the full set (gateway errors only for apps).
	L.ErrorCodeList = ErrorCodes
	if L.Proxied {
		L.ErrorCodeList = []int{502, 503, 504}
	}
	for _, ip := range L.AllowIPs {
		L.AllowIPsRegex = append(L.AllowIPsRegex, regexp.QuoteMeta(ip))
	}
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = fmt.Sprint(c)
	}
	L.ErrorCodes = strings.Join(parts, " ")
	if s.Runtime == RuntimePHP {
		L.PHPSocket = fmt.Sprintf("/run/php/php%s-fpm-%s.sock", s.Version, key)
		L.PoolFile = filepath.Join("/etc/php", s.Version, "fpm/pool.d", "ike-"+key+".conf")
	}
	if L.IsApp {
		L.RuntimeBin = m.runtimeBin(s.Runtime, s.Version)
		L.AppEnvExtra = m.runtimeEnv(L)
		L.AppPath = runtimePath(L, L.AppDir)
	}
	return L
}

func (L layout) usesNginx() bool  { return L.WebServer != "apache" }
func (L layout) usesApache() bool { return L.WebServer == "apache" || L.WebServer == "nginx+apache" }

// checkWebServer confirms the admin enabled this web server choice on this server.
func (m *Manager) checkWebServer(ws, runtime string) error {
	allowed := m.cfg.WebServers()
	for _, a := range allowed {
		if a == ws {
			if ws == "nginx+apache" && IsProxied(runtime) {
				return fmt.Errorf("apps and proxied websites are served by Nginx directly; use web_server \"nginx\"")
			}
			return nil
		}
	}
	return fmt.Errorf("web server %q is not enabled on this server (available: %s)", ws, strings.Join(allowed, ", "))
}

// Create adds a website to an account. On any failure, everything done so far is undone.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Site, error) {
	if req.Runtime == "" {
		req.Runtime = RuntimePHP
	}
	if req.Version == "" {
		req.Version = m.defaultVersion(req.Runtime)
	}
	if req.Runtime == RuntimeStatic {
		req.Version = ""
	}
	if req.WebServer == "" {
		req.WebServer = m.cfg.WebServers()[0]
		if IsProxied(req.Runtime) && req.WebServer == "nginx+apache" {
			req.WebServer = "nginx"
		}
	}
	if err := validate.Domain(req.Domain); err != nil {
		return nil, err
	}
	if err := validate.Username(req.Account); err != nil {
		return nil, fmt.Errorf("account: %w", err)
	}
	if req.Runtime == RuntimeBalancer {
		if req.Balancer == nil {
			return nil, fmt.Errorf("add the backend servers to balance across")
		}
		if err := req.Balancer.check(); err != nil {
			return nil, err
		}
		if !m.cfg.DryRun {
			if err := req.Balancer.resolvable(ctx); err != nil {
				return nil, err
			}
		}
		if req.WebServer == "nginx+apache" {
			req.WebServer = "nginx"
		}
	} else {
		req.Balancer = nil
	}
	if req.Runtime == RuntimeProxy {
		if req.Port < 1 || req.Port > 65535 {
			return nil, fmt.Errorf("give the local port to forward to (1-65535)")
		}
	} else {
		req.Port = 0
	}
	if err := m.checkRuntime(req.Runtime, req.Version); err != nil {
		return nil, err
	}
	if err := m.checkWebServer(req.WebServer, req.Runtime); err != nil {
		return nil, err
	}
	if IsApp(req.Runtime) {
		if req.StartCommand == "" {
			req.StartCommand = defaultStartCommand(req.Runtime)
		}
		if err := validateStartCommand(req.StartCommand); err != nil {
			return nil, err
		}
	} else {
		req.StartCommand = ""
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.getAccount(req.Account); err != nil {
		return nil, err
	}
	if owner := m.hostOwner(req.Domain, ""); owner != "" {
		return nil, fmt.Errorf("%s is already served by %s", req.Domain, owner)
	}
	site := &Site{
		Domain: req.Domain, Account: req.Account, Runtime: req.Runtime, Version: req.Version,
		WebServer: req.WebServer, StartCommand: req.StartCommand, Status: StatusActive, CreatedAt: time.Now().UTC(),
		Port: req.Port, Balancer: req.Balancer,
	}
	if IsApp(req.Runtime) || req.Runtime == RuntimeDocker {
		if site.Port = m.nextPort(); site.Port == 0 {
			return nil, fmt.Errorf("no free local ports left for apps")
		}
	}
	L := m.layout(site)
	site.DocRoot = L.DocRoot
	if L.IsApp {
		site.DocRoot = L.AppDir
	}
	if req.Runtime == RuntimeProxy || req.Runtime == RuntimeDocker || req.Runtime == RuntimeBalancer {
		site.DocRoot = L.SiteDir
	}
	if _, err := os.Stat(m.p(L.StateFile)); err == nil {
		return nil, fmt.Errorf("website %s %w", req.Domain, ErrExists)
	}
	for _, f := range []string{L.VhostFile, L.ApacheFile} {
		if _, err := os.Stat(m.p(f)); err == nil {
			return nil, fmt.Errorf("a web server config for %s %w", req.Domain, ErrExists)
		}
	}

	var undo undoStack
	fail := func(step string, err error) (*Site, error) {
		slog.Error("site create failed, rolling back", "domain", req.Domain, "step", step, "err", err)
		undo.run()
		return nil, fmt.Errorf("%s: %w", step, err)
	}

	// 1. Folders.
	//    web/<domain>      0750 a:web
	//    public_html       2750 a:web   setgid so new files keep the web group
	//    app               0750 a:a     Node.js/Python code (reached through the proxy only)
	//    logs              0750 a:a     PHP/app error logs written by the account user
	dirs := []dirSpec{
		{path: L.SiteDir, mode: 0o750, webGroup: true},
		{path: L.DocRoot, mode: 0o750 | os.ModeSetgid, webGroup: true},
		{path: L.ErrorsDir, mode: 0o750 | os.ModeSetgid, webGroup: true},
		{path: filepath.Join(L.SiteDir, "logs"), mode: 0o750},
	}
	if L.IsApp {
		dirs = append(dirs, dirSpec{path: L.AppDir, mode: 0o750})
	}
	undo.push(func() { _ = os.RemoveAll(m.p(L.SiteDir)) })
	if err := m.mkdirs(req.Account, dirs); err != nil {
		return fail("create folders", err)
	}
	for _, f := range m.starterFiles(L) {
		if err := os.MkdirAll(filepath.Dir(m.p(f.path)), 0o750); err != nil {
			return fail("write starter file", err)
		}
		if err := writeFile(m.p(f.path), f.body, 0o640); err != nil {
			return fail("write starter file", err)
		}
		if err := m.chown(f.path, req.Account, !L.IsApp); err != nil {
			return fail("set ownership", err)
		}
		if dir := filepath.Dir(f.path); dir != L.AppDir && dir != L.DocRoot {
			if err := m.chown(dir, req.Account, false); err != nil {
				return fail("set ownership", err)
			}
		}
	}
	if err := m.copyErrorPages(L); err != nil {
		return fail("write error pages", err)
	}

	// 2. Log folder owned by root, outside the account's reach.
	if err := os.MkdirAll(m.p(L.LogDir), 0o750); err != nil {
		return fail("create log folder", err)
	}
	undo.push(func() { _ = os.RemoveAll(m.p(L.LogDir)) })

	// 3. Runtime.
	switch {
	case req.Runtime == RuntimePHP:
		undo.push(func() { m.removePool(context.Background(), L) })
		if err := m.writePool(ctx, L); err != nil {
			return fail("set up PHP-FPM", err)
		}
	case L.IsApp:
		if err := m.prepareApp(ctx, L); err != nil {
			return fail("prepare the "+runtimeNames[req.Runtime]+" app", err)
		}
		undo.push(func() { m.removeApp(context.Background(), L) })
		if err := m.writeApp(ctx, L, true); err != nil {
			return fail("start app", err)
		}
	}

	// 4. Web servers: Apache first (when used) so Nginx never proxies to a missing vhost.
	if L.usesApache() {
		undo.push(func() { m.removeApacheVhost(context.Background(), L) })
		if err := m.writeApacheVhost(ctx, L); err != nil {
			return fail("configure Apache", err)
		}
	}
	if L.usesNginx() {
		undo.push(func() { m.removeNginxVhost(context.Background(), L) })
		if err := m.writeNginxVhost(ctx, L); err != nil {
			return fail("configure Nginx", err)
		}
	}

	// 5. Save.
	if err := m.writeJSON(L.StateFile, site); err != nil {
		return fail("save website", err)
	}
	slog.Info("site created", "domain", site.Domain, "account", site.Account, "runtime", site.Runtime,
		"version", site.Version, "web_server", site.WebServer)
	return site, nil
}

// Update switches a website's runtime version (PHP 8.2 → 8.4, Node.js 20 → 22),
// web server (nginx ↔ nginx+apache) and/or app start command.
func (m *Manager) Update(ctx context.Context, domain string, req UpdateRequest) (*Site, error) {
	if err := validate.Domain(domain); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.get(domain)
	if err != nil {
		return nil, err
	}
	old := *site
	if req.Version != "" && req.Version != site.Version {
		if site.Runtime == RuntimeStatic {
			return nil, fmt.Errorf("static websites have no runtime version")
		}
		if err := m.checkRuntime(site.Runtime, req.Version); err != nil {
			return nil, err
		}
		site.Version = req.Version
	}
	if req.WebServer != "" && req.WebServer != site.WebServer {
		if err := m.checkWebServer(req.WebServer, site.Runtime); err != nil {
			return nil, err
		}
		site.WebServer = req.WebServer
	}
	if req.StartCommand != "" {
		if !IsApp(site.Runtime) {
			return nil, fmt.Errorf("only app websites (Node.js, Python, Java, Go, Dart) have a start command")
		}
		if err := validateStartCommand(req.StartCommand); err != nil {
			return nil, err
		}
		site.StartCommand = req.StartCommand
	}
	if site.Version == old.Version && site.WebServer == old.WebServer && site.StartCommand == old.StartCommand {
		return site, nil
	}
	oldL, newL := m.layout(&old), m.layout(site)

	var undo undoStack
	fail := func(step string, err error) (*Site, error) {
		undo.run()
		return nil, fmt.Errorf("%s: %w", step, err)
	}
	// Runtime first: bring up the new pool/app before web servers point at it.
	switch {
	case site.Runtime == RuntimePHP && site.Version != old.Version:
		undo.push(func() { m.removePool(context.Background(), newL) })
		if err := m.writePool(ctx, newL); err != nil {
			return fail("set up PHP "+site.Version, err)
		}
	case IsApp(site.Runtime) && (site.Version != old.Version || site.StartCommand != old.StartCommand):
		undo.push(func() { _ = m.writeApp(context.Background(), oldL, false) })
		if err := m.writeApp(ctx, newL, false); err != nil {
			return fail("restart app", err)
		}
	}

	// Rewrite web server configs for the new shape; on failure restore the old ones.
	undo.push(func() {
		if oldL.usesApache() {
			_ = m.writeApacheVhost(context.Background(), oldL)
		} else {
			m.removeApacheVhost(context.Background(), newL)
		}
		if oldL.usesNginx() {
			_ = m.writeNginxVhost(context.Background(), oldL)
		}
	})
	if newL.usesApache() {
		if err := m.writeApacheVhost(ctx, newL); err != nil {
			return fail("configure Apache", err)
		}
	}
	if newL.usesNginx() {
		if err := m.writeNginxVhost(ctx, newL); err != nil {
			return fail("configure Nginx", err)
		}
	}
	if oldL.usesApache() && !newL.usesApache() {
		m.removeApacheVhost(ctx, oldL)
	}
	if site.Runtime == RuntimePHP && site.Version != old.Version {
		m.removePool(ctx, oldL)
	}

	if err := m.writeJSON(newL.StateFile, site); err != nil {
		return nil, err
	}
	slog.Info("site updated", "domain", domain, "version", site.Version, "web_server", site.WebServer)
	return site, nil
}

// Delete removes a website's configuration. With removeFiles, its folder is
// deleted too; the account and its other websites are untouched.
func (m *Manager) Delete(ctx context.Context, domain string, removeFiles bool) error {
	if err := validate.Domain(domain); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.get(domain)
	if err != nil {
		return err
	}
	return m.deleteSite(ctx, site, removeFiles)
}

func (m *Manager) deleteSite(ctx context.Context, site *Site, removeFiles bool) error {
	L := m.layout(site)
	// Website SFTP users first: their jails mount this website's folder.
	m.deleteSFTPUsersFor(ctx, site.Account, site.Domain)
	if L.usesNginx() {
		m.removeNginxVhost(ctx, L)
	}
	if L.usesApache() {
		m.removeApacheVhost(ctx, L)
	}
	switch {
	case site.Runtime == RuntimePHP:
		m.removePool(ctx, L)
	case L.IsApp:
		m.removeApp(ctx, L)
	}
	m.cleanupExtras(ctx, site, L)
	if removeFiles {
		// os.RemoveAll never follows symlinks, so a planted link can't redirect the delete.
		if err := os.RemoveAll(m.p(L.SiteDir)); err != nil {
			return err
		}
		_ = os.RemoveAll(m.p(L.LogDir))
	}
	if err := removeIfExists(m.p(L.StateFile)); err != nil {
		return err
	}
	slog.Info("site deleted", "domain", site.Domain, "files_removed", removeFiles)
	return nil
}

// List returns all websites sorted by domain.
func (m *Manager) List() ([]Site, error) {
	entries, err := os.ReadDir(m.p(m.cfg.StateDir))
	if errors.Is(err, os.ErrNotExist) {
		return []Site{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Site{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := m.get(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			slog.Warn("skipping unreadable site file", "file", e.Name(), "err", err)
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

// Get returns one website.
func (m *Manager) Get(domain string) (*Site, error) {
	if err := validate.Domain(domain); err != nil {
		return nil, err
	}
	return m.get(domain)
}

func (m *Manager) get(domain string) (*Site, error) {
	var s Site
	if err := m.readJSON(filepath.Join(m.cfg.StateDir, domain+".json"), &s); err != nil {
		if err == ErrNotFound {
			return nil, fmt.Errorf("website %s %w", domain, ErrNotFound)
		}
		return nil, err
	}
	return &s, nil
}

// ------------------------------------------------------------ components ---

func (m *Manager) writePool(ctx context.Context, L layout) error {
	if err := m.render(poolTmpl, L, L.PoolFile, 0o644); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "php-fpm"+L.Version, "--test"); err != nil {
		return err
	}
	_, err := m.run.Run(ctx, "systemctl", "reload", "php"+L.Version+"-fpm")
	return err
}

func (m *Manager) removePool(ctx context.Context, L layout) {
	if L.PoolFile == "" {
		return
	}
	if _, err := os.Stat(m.p(L.PoolFile)); err != nil {
		return
	}
	_ = os.Remove(m.p(L.PoolFile))
	_, _ = m.run.Run(ctx, "systemctl", "reload", "php"+L.Version+"-fpm")
}

// writeApp writes the root-owned start script and systemd unit, then (re)starts the app.
func (m *Manager) writeApp(ctx context.Context, L layout, first bool) error {
	if err := m.render(startScriptTmpl, L, L.StartScript, 0o755); err != nil {
		return err
	}
	if err := m.render(unitTmpl, L, L.UnitFile, 0o644); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if first {
		_, err := m.run.Run(ctx, "systemctl", "enable", "--now", L.UnitName)
		return err
	}
	_, err := m.run.Run(ctx, "systemctl", "restart", L.UnitName)
	return err
}

func (m *Manager) removeApp(ctx context.Context, L layout) {
	_, _ = m.run.Run(ctx, "systemctl", "disable", "--now", L.UnitName)
	_ = os.Remove(m.p(L.UnitFile))
	_ = os.Remove(m.p(L.StartScript))
	_, _ = m.run.Run(ctx, "systemctl", "daemon-reload")
}

func (m *Manager) writeNginxVhost(ctx context.Context, L layout) error {
	if err := m.render(nginxTmpl, L, L.VhostFile, 0o644); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "nginx", "-t"); err != nil {
		return err
	}
	_, err := m.run.Run(ctx, "systemctl", "reload", "nginx")
	return err
}

func (m *Manager) removeNginxVhost(ctx context.Context, L layout) {
	if _, err := os.Stat(m.p(L.VhostFile)); err != nil {
		return
	}
	_ = os.Remove(m.p(L.VhostFile))
	_, _ = m.run.Run(ctx, "systemctl", "reload", "nginx")
}

func (m *Manager) writeApacheVhost(ctx context.Context, L layout) error {
	if err := m.render(apacheTmpl, L, L.ApacheFile, 0o644); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "apache2ctl", "configtest"); err != nil {
		return err
	}
	_, err := m.run.Run(ctx, "systemctl", "reload", "apache2")
	return err
}

func (m *Manager) removeApacheVhost(ctx context.Context, L layout) {
	if _, err := os.Stat(m.p(L.ApacheFile)); err != nil {
		return
	}
	_ = os.Remove(m.p(L.ApacheFile))
	_, _ = m.run.Run(ctx, "systemctl", "reload", "apache2")
}
