// Package config loads the agent configuration written by the installer.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type Config struct {
	// Root is prepended to every managed path. "/" in production; a temp dir in tests.
	Root string `json:"-"`
	// DryRun records commands instead of executing them.
	DryRun bool `json:"-"`

	WebUser       string `json:"web_user"`        // www-data or nginx
	WebGroup      string `json:"web_group"`       // group the web server runs as
	NginxSitesDir string `json:"nginx_sites_dir"` // included from nginx.conf
	HomeBase      string `json:"home_base"`       // usually /home
	StateDir      string `json:"state_dir"`       // site metadata JSON files
	DefaultPHP    string `json:"default_php"`
	RuntimesDir   string `json:"runtimes_dir"`   // Node.js and Python versions: <dir>/<runtime>/<version>/bin
	AppsDir       string `json:"apps_dir"`       // root-owned start scripts for app sites
	AppPortStart  int    `json:"app_port_start"` // first local port handed to apps
	AccountsDir   string `json:"accounts_dir"`
	PagesDir      string `json:"pages_dir"`   // admin-editable starter, status and error pages
	ToolsDir      string `json:"tools_dir"`   // WP-CLI, uv
	ImportsDir    string `json:"imports_dir"` // uploaded cPanel backups
	Repo          string `json:"repo"`        // GitHub "owner/name" Ike updates come from
	CronDir       string `json:"cron_dir"`    // /etc/cron.d
	FirewallState string `json:"firewall_state"`
	// Firewall is the program Ike drives: "ufw" (default), "nftables" or "firewalld".
	Firewall string `json:"firewall"`
	// FirewallRuleset is Ike's nftables table, loaded at boot by ike-firewall.service.
	FirewallRuleset string `json:"firewall_ruleset"`
	// Ports that can never be closed from the panel (SSH, web, panel ports).
	ProtectedPorts []string `json:"protected_ports"`

	// Web server stack chosen by the server admin at install time:
	//   "nginx"  - Nginx only
	//   "apache" - Apache only (listens on port 80)
	//   "hybrid" - Nginx in front; each website picks "nginx" or "nginx+apache"
	//   "caddy"  - Caddy only (Ike still issues the certificates)
	//   "openlitespeed" - OpenLiteSpeed only, PHP through Ike's PHP-FPM pools
	WebStack         string `json:"web_stack"`
	DefaultWebServer string `json:"default_web_server"` // for new websites on a hybrid stack
	ApacheSitesDir   string `json:"apache_sites_dir"`
	ApacheListen     string `json:"apache_listen"`   // "*:80" (apache stack) or "127.0.0.1:8080" (hybrid)
	CaddySitesDir    string `json:"caddy_sites_dir"` // imported from /etc/caddy/Caddyfile
	CaddyConfig      string `json:"caddy_config"`    // the main Caddyfile, for caddy validate
	OLSDir           string `json:"ols_dir"`         // /usr/local/lsws
	OLSSitesDir      string `json:"ols_sites_dir"`   // per-website configs and the included listener file
	// AuthSocket answers Caddy's password checks (forward_auth) for protected folders.
	AuthSocket string `json:"auth_socket"`

	// HTTPS
	AcmeDir        string `json:"acme_dir"`        // webroot for Let's Encrypt HTTP-01 challenges
	LetsEncryptDir string `json:"letsencrypt_dir"` // /etc/letsencrypt
	SSLDir         string `json:"ssl_dir"`         // uploaded certificates
	AdminEmail     string `json:"admin_email"`     // default Let's Encrypt account email

	// PHP tooling
	ComposerPath string `json:"composer_path"`

	// Git deployments
	GitDir        string `json:"git_dir"`         // bare mirrors of deployed repositories (root only)
	GitSecretsDir string `json:"git_secrets_dir"` // deploy keys and tokens (root only)
	DeployDir     string `json:"deploy_dir"`      // deployment history
	DeployLogDir  string `json:"deploy_log_dir"`
	DeployTmpDir  string `json:"deploy_tmp_dir"`

	// Containers
	DockerSocket      string `json:"docker_socket"`
	DockerForAccounts bool   `json:"docker_for_accounts"` // let account owners run containers
	StacksDir         string `json:"stacks_dir"`
	RegistriesFile    string `json:"registries_file"`
	PackPath          string `json:"pack_path"` // Cloud Native Buildpacks CLI
	K3sPath           string `json:"k3s_path"`  // optional Kubernetes (k3s)

	// Server identity
	Hostname   string `json:"hostname"`    // the server's own name (mail, reverse DNS)
	PublicIPv4 string `json:"public_ipv4"` // detected at install time
	PublicIPv6 string `json:"public_ipv6"`
	MasterKey  string `json:"master_key"` // AES key ring for stored credentials

	// DNS (PowerDNS with the bind backend). Empty DNSServer means no local DNS server.
	DNSServer    string `json:"dns_server"`     // "powerdns" or ""
	DNSDir       string `json:"dns_dir"`        // zone state, zone files, secondary zones
	PdnsBindConf string `json:"pdns_bind_conf"` // zone list read by PowerDNS

	// Mail. Empty MailServer means mail hosting was not installed.
	MailServer  string `json:"mail_server"`   // "postfix" or ""
	MailDir     string `json:"mail_dir"`      // /var/mail/vhosts
	MailState   string `json:"mail_state"`    // domains, mailboxes, aliases
	MailConfDir string `json:"mail_conf_dir"` // generated maps for Postfix and Dovecot
	DKIMDir     string `json:"dkim_dir"`
	Webmail     string `json:"webmail"` // "roundcube" or ""
	WebmailDir  string `json:"webmail_dir"`
	SpamFilter  string `json:"spam_filter"` // "rspamd" or ""
	Antivirus   string `json:"antivirus"`   // "clamav" or ""

	// Security
	ModSecurity   bool   `json:"modsecurity"`
	QuarantineDir string `json:"quarantine_dir"`
	ScanDir       string `json:"scan_dir"` // scan state and reports

	// Databases
	Postgres    bool   `json:"postgres"`
	PostgresVer string `json:"postgres_version"`
	Redis       string `json:"redis"` // "redis", "valkey" or ""
	MongoDB     bool   `json:"mongodb"`
	DBBackupDir string `json:"db_backup_dir"`
	DataDir     string `json:"data_dir"` // per-account services (Redis instances)
	StorageFile string `json:"storage_file"`

	// Files
	SnapshotDir string `json:"snapshot_dir"`
	ShareDir    string `json:"share_dir"`

	// Infrastructure
	ClusterDir    string `json:"cluster_dir"`    // CA, server certificates, join tokens
	ClusterListen string `json:"cluster_listen"` // ":2089" when this server is managed remotely
	SpecDir       string `json:"spec_dir"`
	DiskQuotas    bool   `json:"disk_quotas"`
}

// WebServers lists the per-website choices this stack allows, default first.
func (c Config) WebServers() []string {
	switch c.WebStack {
	case "apache":
		return []string{"apache"}
	case "caddy":
		return []string{"caddy"}
	case "openlitespeed":
		return []string{"openlitespeed"}
	case "hybrid":
		if c.DefaultWebServer == "nginx" {
			return []string{"nginx", "nginx+apache"}
		}
		return []string{"nginx+apache", "nginx"}
	default:
		return []string{"nginx"}
	}
}

func Default() Config {
	return Config{
		Root:            "/",
		WebUser:         "www-data",
		WebGroup:        "www-data",
		NginxSitesDir:   "/etc/nginx/ike/sites",
		HomeBase:        "/home",
		StateDir:        "/var/lib/ike/sites",
		DefaultPHP:      "8.3",
		RuntimesDir:     "/opt/ike/runtimes",
		AppsDir:         "/etc/ike/apps",
		AppPortStart:    3001,
		AccountsDir:     "/var/lib/ike/accounts",
		PagesDir:        "/etc/ike/pages",
		ToolsDir:        "/opt/ike/bin",
		ImportsDir:      "/var/lib/ike/imports",
		Repo:            "YOUR-ORG/ike",
		CronDir:         "/etc/cron.d",
		FirewallState:   "/var/lib/ike/firewall.json",
		Firewall:        "ufw",
		FirewallRuleset: "/etc/ike/nftables.nft",
		ProtectedPorts:  []string{"22/tcp", "80/tcp", "443/tcp"},
		WebStack:        "nginx",
		ApacheSitesDir:  "/etc/apache2/ike/sites",
		ApacheListen:    "127.0.0.1:8080",
		CaddySitesDir:   "/etc/caddy/ike",
		CaddyConfig:     "/etc/caddy/Caddyfile",
		OLSDir:          "/usr/local/lsws",
		OLSSitesDir:     "/usr/local/lsws/conf/ike",
		AuthSocket:      "/run/ike-auth/auth.sock",
		AcmeDir:         "/var/lib/ike/acme",
		LetsEncryptDir:  "/etc/letsencrypt",
		SSLDir:          "/etc/ike/ssl",
		ComposerPath:    "/usr/local/bin/composer",
		GitDir:          "/var/lib/ike/git",
		GitSecretsDir:   "/etc/ike/git",
		DeployDir:       "/var/lib/ike/deploys",
		DeployLogDir:    "/var/log/ike/deploys",
		DeployTmpDir:    "/var/lib/ike/deploy-tmp",
		DockerSocket:    "/var/run/docker.sock",
		StacksDir:       "/var/lib/ike/stacks",
		RegistriesFile:  "/etc/ike/registries.json",
		PackPath:        "/usr/local/bin/pack",
		K3sPath:         "/usr/local/bin/k3s",
		MasterKey:       "/etc/ike/master.key",
		DNSDir:          "/var/lib/ike/dns",
		PdnsBindConf:    "/etc/powerdns/ike-zones.conf",
		MailDir:         "/var/mail/vhosts",
		MailState:       "/var/lib/ike/mail",
		MailConfDir:     "/etc/ike/mail",
		DKIMDir:         "/var/lib/rspamd/dkim",
		WebmailDir:      "/var/lib/roundcube/public_html",
		QuarantineDir:   "/var/lib/ike/quarantine",
		ScanDir:         "/var/lib/ike/scans",
		PostgresVer:     "16",
		DBBackupDir:     "/var/backups/ike/databases",
		DataDir:         "/var/lib/ike/data",
		StorageFile:     "/etc/ike/storage.json",
		SnapshotDir:     "/var/backups/ike/snapshots",
		ShareDir:        "/var/lib/ike/shares",
		ClusterDir:      "/etc/ike/cluster",
		SpecDir:         "/var/lib/ike/specs",
	}
}

// Load reads a JSON config file on top of the defaults. A missing file is not an error.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}
