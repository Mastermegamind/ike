package sites

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// One-click apps. Each installer runs every file-writing step as the account's
// own user (never root), so nothing in the website folder can trick it into
// touching system files.

// CatalogApp describes an installable app.
type CatalogApp struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Runtime     string `json:"runtime"`
	Available   bool   `json:"available"` // false = listed, coming soon
}

var Catalog = []CatalogApp{
	{ID: "wordpress", Name: "WordPress", Runtime: RuntimePHP, Available: true, Description: "Websites, blogs and online shops (WooCommerce)."},
	{ID: "laravel", Name: "Laravel", Runtime: RuntimePHP, Description: "PHP framework for web apps and APIs."},
	{ID: "joomla", Name: "Joomla", Runtime: RuntimePHP, Description: "Content management for larger sites."},
	{ID: "drupal", Name: "Drupal", Runtime: RuntimePHP, Description: "Structured content and multilingual sites."},
	{ID: "prestashop", Name: "PrestaShop", Runtime: RuntimePHP, Description: "Online store."},
	{ID: "nextcloud", Name: "Nextcloud", Runtime: RuntimePHP, Description: "File sync and sharing for teams."},
	{ID: "ghost", Name: "Ghost", Runtime: RuntimeNode, Description: "Publishing and newsletters."},
	{ID: "django", Name: "Django", Runtime: RuntimePython, Description: "Python web framework starter."},
}

// WordPressRequest installs WordPress into a PHP website.
type WordPressRequest struct {
	Title      string `json:"title"`
	AdminUser  string `json:"admin_user"`
	AdminEmail string `json:"admin_email"`
	Locale     string `json:"locale,omitempty"` // e.g. en_US, fr_FR
}

// WordPressResult is returned once; Ike does not keep the admin password.
type WordPressResult struct {
	URL           string      `json:"url"`
	AdminURL      string      `json:"admin_url"`
	AdminUser     string      `json:"admin_user"`
	AdminPassword string      `json:"admin_password"`
	Database      NewDatabase `json:"database"`
}

var (
	wpUserRe   = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)
	wpTitleRe  = regexp.MustCompile(`^[\pL\pN .,'&!?:()-]{1,80}$`)
	wpLocaleRe = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?$`)
)

// InstallWordPress creates a database and installs the latest WordPress with WP-CLI.
func (m *Manager) InstallWordPress(ctx context.Context, domain string, req WordPressRequest) (*WordPressResult, error) {
	if !wpTitleRe.MatchString(req.Title) {
		return nil, fmt.Errorf("site title must be 1-80 letters, numbers or basic punctuation")
	}
	if !wpUserRe.MatchString(req.AdminUser) || strings.EqualFold(req.AdminUser, "admin") {
		return nil, fmt.Errorf("admin username must be 3-32 letters/numbers/._- and not \"admin\"")
	}
	if a, err := mail.ParseAddress(req.AdminEmail); err != nil || a.Address != req.AdminEmail || strings.ContainsAny(req.AdminEmail, " \"'") {
		return nil, fmt.Errorf("enter a valid admin email address")
	}
	if req.Locale == "" {
		req.Locale = "en_US"
	}
	if !wpLocaleRe.MatchString(req.Locale) {
		return nil, fmt.Errorf("invalid language code %q (e.g. en_US)", req.Locale)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if site.Runtime != RuntimePHP {
		return nil, fmt.Errorf("WordPress needs a PHP website; %s uses %s", domain, site.Runtime)
	}
	L := m.layout(site)
	// Only an untouched website: public_html may contain nothing but Ike's starter page.
	entries, err := os.ReadDir(m.p(L.DocRoot))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Name() != "index.php" && e.Name() != "index.html" {
			return nil, fmt.Errorf("public_html already has files (%s); install WordPress into an empty website", e.Name())
		}
	}
	wp := filepath.Join(m.cfg.ToolsDir, "wp")
	if _, err := os.Stat(m.p(wp)); err != nil {
		return nil, fmt.Errorf("WP-CLI is not installed on this server")
	}

	db, err := m.createDatabase(ctx, site.Account, "wp"+randomHex(2), "WordPress "+domain)
	if err != nil {
		return nil, err
	}
	// A failed install removes its database so a retry starts clean.
	cleanup := func() { _ = m.deleteDatabase(context.Background(), site.Account, db.Name) }

	php := "/usr/bin/php" + site.Version
	// Secrets are answered on stdin (WP-CLI --prompt), never passed as arguments.
	asUser := func(stdin string, args ...string) error {
		full := append([]string{"-u", site.Account, "--", php, wp, "--path=" + L.DocRoot, "--skip-plugins", "--skip-themes"}, args...)
		_, err := m.run.RunInput(ctx, stdin, "runuser", full...)
		return err
	}
	for _, f := range []string{"index.php", "index.html"} {
		_, _ = m.run.Run(ctx, "runuser", "-u", site.Account, "--", "rm", "-f", filepath.Join(L.DocRoot, f))
		if m.cfg.DryRun {
			_ = os.Remove(m.p(filepath.Join(L.DocRoot, f)))
		}
	}
	adminPass := randomHex(10)
	url := "http://" + domain
	steps := []struct {
		stdin string
		args  []string
	}{
		{"", []string{"core", "download", "--locale=" + req.Locale}},
		{db.Password + "\n", []string{"config", "create", "--dbname=" + db.Name, "--dbuser=" + db.User,
			"--dbhost=localhost", "--locale=" + req.Locale, "--prompt=dbpass"}},
		{adminPass + "\n", []string{"core", "install", "--url=" + url, "--title=" + req.Title,
			"--admin_user=" + req.AdminUser, "--admin_email=" + req.AdminEmail, "--skip-email", "--prompt=admin_password"}},
	}
	for _, st := range steps {
		s := st.args
		if err := asUser(st.stdin, s...); err != nil {
			cleanup()
			return nil, fmt.Errorf("WordPress %s %s failed: %w", s[0], s[1], redact(err, db.Password, adminPass))
		}
	}
	slog.Info("wordpress installed", "domain", domain, "database", db.Name)
	return &WordPressResult{URL: url, AdminURL: url + "/wp-admin/", AdminUser: req.AdminUser, AdminPassword: adminPass, Database: *db}, nil
}

// redact keeps secrets out of error messages that end up in logs.
func redact(err error, secrets ...string) error {
	msg := err.Error()
	for _, s := range secrets {
		msg = strings.ReplaceAll(msg, s, "********")
	}
	return fmt.Errorf("%s", msg)
}
