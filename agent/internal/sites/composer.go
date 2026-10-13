package sites

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

// ComposerRequest runs Composer in a PHP website's project folder.
type ComposerRequest struct {
	Action  string `json:"action"`            // install | update | require | remove | dump-autoload
	Package string `json:"package,omitempty"` // require/remove: vendor/name, optionally :constraint
	Dev     bool   `json:"dev,omitempty"`     // include development packages
}

var composerPkgRe = regexp.MustCompile(`^[a-z0-9]([_.-]?[a-z0-9]+)*/[a-z0-9](([_.]|-{1,2})?[a-z0-9]+)*(:[A-Za-z0-9.*^~|<>=@,-]{1,40})?$`)

// composerDir finds the folder with composer.json: the website folder, or
// its public_html for projects uploaded there.
func (m *Manager) composerDir(L layout) (string, error) {
	for _, d := range []string{L.SiteDir, L.DocRoot} {
		if _, err := os.Lstat(m.p(filepath.Join(d, "composer.json"))); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("no composer.json in %s or %s", L.SiteDir, L.DocRoot)
}

// RunComposer runs one Composer command as the account user. It runs as a job.
func (m *Manager) RunComposer(ctx context.Context, domain string, req ComposerRequest, log func(string, ...any)) error {
	site, err := m.Get(domain)
	if err != nil {
		return err
	}
	if site.Runtime != RuntimePHP {
		return fmt.Errorf("%s doesn't use PHP", domain)
	}
	if _, err := os.Stat(m.p(m.cfg.ComposerPath)); err != nil {
		return fmt.Errorf("Composer isn't installed on this server")
	}
	args := []string{m.cfg.ComposerPath}
	switch req.Action {
	case "install", "update":
		args = append(args, req.Action, "--prefer-dist", "--optimize-autoloader")
		if !req.Dev {
			args = append(args, "--no-dev")
		}
	case "require", "remove":
		if !composerPkgRe.MatchString(req.Package) {
			return fmt.Errorf("package must look like vendor/name or vendor/name:^2.0")
		}
		args = append(args, req.Action, req.Package)
		if req.Dev {
			args = append(args, "--dev")
		}
	case "dump-autoload":
		args = append(args, "dump-autoload", "--optimize")
	default:
		return fmt.Errorf("action must be install, update, require, remove or dump-autoload")
	}
	L := m.layout(site)
	dir, err := m.composerDir(L)
	if err != nil && req.Action != "require" {
		return err
	}
	if dir == "" {
		dir = L.SiteDir
	}
	args = append(args, "--no-interaction", "--no-progress", "--working-dir="+dir)
	env := m.userEnv(L, dir, "COMPOSER_HOME="+L.Home+"/.composer", "COMPOSER_CACHE_DIR="+L.Home+"/.cache/composer",
		"COMPOSER_NO_INTERACTION=1")
	log("composer %s in %s (PHP %s)", req.Action, dir, site.Version)
	_, err = m.run.Exec(ctx, runner.Command{
		Name: "runuser", Args: append([]string{"-u", site.Account, "--", "/usr/bin/php" + site.Version}, args...),
		Env: env, Dir: m.p(dir), Output: logWriter(log), Timeout: 30 * time.Minute,
	})
	return err
}
