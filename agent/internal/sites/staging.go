package sites

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StagingResult describes a new staging copy.
type StagingResult struct {
	Live      string `json:"live"`
	Staging   string `json:"staging"`
	Database  string `json:"database,omitempty"` // WordPress only: the copied database
	WordPress bool   `json:"wordpress"`
	Note      string `json:"note,omitempty"`
}

func stagingDomain(live string) string { return "staging." + live }

// CreateStaging clones a website to staging.<domain> in the same account:
// same runtime, version and web server, a copy of the files and, for
// WordPress, a copy of the database with URLs rewritten. The copy is hidden
// from search engines. Every copy step runs as the account user.
func (m *Manager) CreateStaging(ctx context.Context, domain string, log func(string, ...any)) (*StagingResult, error) {
	live, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if live.Staging {
		return nil, fmt.Errorf("%s is already a staging copy", domain)
	}
	stage := stagingDomain(domain)
	log("Creating %s", stage)
	s, err := m.Create(ctx, CreateRequest{Domain: stage, Account: live.Account, Runtime: live.Runtime, Version: live.Version,
		WebServer: live.WebServer, StartCommand: live.StartCommand})
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.Staging, s.StagingOf = true, domain
	liveL, stageL := m.layout(live), m.layout(s)
	if err := m.writeJSON(stageL.StateFile, s); err != nil {
		return nil, err
	}
	if stageL.usesNginx() {
		if err := m.writeNginxVhost(ctx, stageL); err != nil {
			return nil, err
		}
	}

	src, dst := liveL.DocRoot, stageL.DocRoot
	if liveL.IsApp {
		src, dst = liveL.AppDir, stageL.AppDir
	}
	asUser := func(stdin string, args ...string) error {
		_, err := m.run.RunInput(ctx, stdin, "runuser", append([]string{"-u", live.Account, "--"}, args...)...)
		return err
	}
	log("Copying files")
	if err := asUser("", "cp", "-a", "--no-dereference", src+"/.", dst+"/"); err != nil {
		return nil, fmt.Errorf("copy files: %w", err)
	}
	res := &StagingResult{Live: domain, Staging: stage}

	_, wpErr := os.Lstat(m.p(filepath.Join(liveL.DocRoot, "wp-config.php")))
	wp := filepath.Join(m.cfg.ToolsDir, "wp")
	if live.Runtime == RuntimePHP && wpErr == nil {
		res.WordPress = true
		php := "/usr/bin/php" + live.Version
		wpAt := func(path, stdin string, args ...string) error {
			return asUser(stdin, append([]string{php, wp, "--path=" + path, "--skip-plugins", "--skip-themes"}, args...)...)
		}
		dump := filepath.Join(m.accountLayout(live.Account).TmpDir, fmt.Sprintf("ike-staging-%d.sql", time.Now().Unix()))
		log("Exporting the live database")
		if err := wpAt(liveL.DocRoot, "", "db", "export", dump); err != nil {
			return nil, fmt.Errorf("export database: %w", err)
		}
		defer func() { _ = asUser("", "rm", "-f", dump) }()
		db, err := m.createDatabase(ctx, live.Account, "stg"+randomHex(2), "Staging "+domain)
		if err != nil {
			return nil, err
		}
		res.Database = db.Name
		log("Pointing the staging copy at its own database %s", db.Name)
		for _, kv := range [][2]string{{"DB_NAME", db.Name}, {"DB_USER", db.User}} {
			if err := wpAt(dst, "", "config", "set", kv[0], kv[1]); err != nil {
				return nil, err
			}
		}
		if err := wpAt(dst, db.Password+"\n", "config", "set", "DB_PASSWORD", "--prompt=value"); err != nil {
			return nil, err
		}
		log("Importing the database copy")
		if err := wpAt(dst, "", "db", "import", dump); err != nil {
			return nil, fmt.Errorf("import database: %w", err)
		}
		log("Replacing %s with %s in the copy", domain, stage)
		if err := wpAt(dst, "", "search-replace", "//"+domain, "//"+stage, "--all-tables-with-prefix", "--skip-columns=guid"); err != nil {
			return nil, err
		}
	} else if !liveL.IsApp {
		res.Note = "Files were copied. Databases other than WordPress's aren't copied; point the staging copy at a test database."
	}
	return res, nil
}

// PushStaging copies a staging website's files over its live website, after
// saving a backup of the live files in the account's tmp folder. Databases are
// never pushed automatically, because live data (orders, users, comments)
// would be lost.
func (m *Manager) PushStaging(ctx context.Context, stagingDomainName string, log func(string, ...any)) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.Get(stagingDomainName)
	if err != nil {
		return "", err
	}
	if !s.Staging || s.StagingOf == "" {
		return "", fmt.Errorf("%s isn't a staging copy", stagingDomainName)
	}
	live, err := m.Get(s.StagingOf)
	if err != nil {
		return "", err
	}
	sL, lL := m.layout(s), m.layout(live)
	src, dst := sL.DocRoot, lL.DocRoot
	if sL.IsApp {
		src, dst = sL.AppDir, lL.AppDir
	}
	asUser := func(args ...string) error {
		_, err := m.run.Run(ctx, "runuser", append([]string{"-u", s.Account, "--"}, args...)...)
		return err
	}
	backup := filepath.Join(m.accountLayout(s.Account).TmpDir, fmt.Sprintf("ike-before-push-%s-%d.tar.gz", live.Domain, time.Now().Unix()))
	log("Backing up the live files to %s", backup)
	if err := asUser("tar", "-czf", backup, "-C", dst, "."); err != nil {
		return "", fmt.Errorf("back up live files: %w", err)
	}
	log("Copying staging files to %s (keeping its wp-config.php and .env)", live.Domain)
	// Config files hold each copy's own database settings, so they're never pushed.
	if err := asUser("rsync", "-a", "--exclude=/wp-config.php", "--exclude=/.env", src+"/", dst+"/"); err != nil {
		return "", fmt.Errorf("copy files: %w", err)
	}
	if lL.IsApp {
		_, _ = m.run.Run(ctx, "systemctl", "restart", lL.UnitName)
	}
	return backup, nil
}
