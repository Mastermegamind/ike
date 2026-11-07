package sites

// PECL extensions and PEAR packages, for what Ubuntu doesn't package.
//
// PECL builds the extension from source for one PHP version (phpize of that
// version), then Ike writes the .ini and turns it on for FPM and the CLI, and
// checks PHP still starts. PEAR packages are PHP libraries shared by every
// website (/usr/share/php). Both are admin-only: they change the server.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

var (
	// "redis", "swoole-5.1.4", "mongodb-1.20.0"
	peclNameRe = regexp.MustCompile(`^([a-z][a-z0-9_]{1,40})(-[0-9]+\.[0-9]+(\.[0-9]+)?([a-z0-9]+)?)?$`)
	// "Mail", "Net_SMTP", "pear/Console_Table-1.3.1", "channel://pear.php.net/Mail-1.5.0"
	pearNameRe = regexp.MustCompile(`^([a-z][a-z0-9.]{1,40}/)?[A-Za-z][A-Za-z0-9_]{1,60}(-[0-9]+\.[0-9]+(\.[0-9]+)?([a-z0-9]+)?)?$`)
)

func pkgEnv() []string {
	return []string{"DEBIAN_FRONTEND=noninteractive", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root"}
}

// ChangePECLExtension builds and turns on (or removes) a PECL extension for
// one PHP version. It runs as a job.
func (m *Manager) ChangePECLExtension(ctx context.Context, version, name string, install bool, log func(string, ...any)) error {
	if err := m.checkRuntime(RuntimePHP, version); err != nil {
		return err
	}
	mm := peclNameRe.FindStringSubmatch(name)
	if mm == nil {
		return fmt.Errorf("%q isn't a PECL package name (like redis or swoole-5.1.4)", name)
	}
	ext := mm[1]
	if _, packaged := phpExtensions[ext]; packaged && install {
		log("Ubuntu packages %s for PHP %s; that's the better choice (it gets security updates). Building it from PECL anyway.", ext, version)
	}
	ini := filepath.Join("/etc/php", version, "mods-available", ext+".ini")
	run := func(name string, args ...string) error {
		_, err := m.run.Exec(ctx, runner.Command{Name: name, Args: args, Env: pkgEnv(), Output: logWriter(log)})
		return err
	}
	if install {
		log("Making sure PHP %s's build tools are installed", version)
		if err := run("apt-get", "install", "-y", "--no-install-recommends", "php-pear", "php"+version+"-dev", "build-essential"); err != nil {
			return err
		}
		log("Building %s for PHP %s", name, version)
		// php_suffix picks phpize8.3 and php-config8.3; answering the
		// prompts with Enter keeps PECL's defaults.
		if _, err := m.run.Exec(ctx, runner.Command{Name: "pecl", Args: []string{"-d", "php_suffix=" + version, "install", "-f", name},
			Env: pkgEnv(), Stdin: strings.Repeat("\n", 20), Output: logWriter(log)}); err != nil {
			return err
		}
		// PECL's own record is shared by every PHP version; forget the
		// package so the next version can build it too. The .so stays.
		_ = run("pecl", "uninstall", "-r", ext)
		if err := writeFile(m.p(ini), []byte("; Built from PECL by Ike\nextension="+ext+".so\n"), 0o644); err != nil {
			return err
		}
		if err := run("phpenmod", "-v", version, "-s", "ALL", ext); err != nil {
			return err
		}
	} else {
		log("Turning %s off for PHP %s", ext, version)
		_ = run("phpdismod", "-v", version, "-s", "ALL", ext)
		_ = os.Remove(m.p(ini))
		if dir, err := m.run.Run(ctx, "php-config"+version, "--extension-dir"); err == nil {
			_ = os.Remove(m.p(filepath.Join(strings.TrimSpace(string(dir)), ext+".so")))
		}
	}
	if _, err := m.run.Run(ctx, "php-fpm"+version, "--test"); err != nil {
		if install {
			_ = run("phpdismod", "-v", version, "-s", "ALL", ext)
		}
		return fmt.Errorf("PHP %s doesn't start with %s, so it was turned off again: %w", version, ext, err)
	}
	_, err := m.run.Run(ctx, "systemctl", "reload", "php"+version+"-fpm")
	if err == nil {
		log("PHP %s reloaded", version)
	}
	return err
}

// ChangePEARPackage installs or removes a PEAR package. It runs as a job.
func (m *Manager) ChangePEARPackage(ctx context.Context, name string, install bool, log func(string, ...any)) error {
	if !pearNameRe.MatchString(name) {
		return fmt.Errorf("%q isn't a PEAR package name (like Mail or Net_SMTP-1.10.1)", name)
	}
	if install {
		if _, err := m.run.Exec(ctx, runner.Command{Name: "apt-get", Args: []string{"install", "-y", "--no-install-recommends", "php-pear"},
			Env: pkgEnv(), Output: logWriter(log)}); err != nil {
			return err
		}
		log("Installing PEAR package %s", name)
		_, err := m.run.Exec(ctx, runner.Command{Name: "pear", Args: []string{"install", "--alldeps", name}, Env: pkgEnv(), Output: logWriter(log)})
		return err
	}
	log("Removing PEAR package %s", name)
	_, err := m.run.Exec(ctx, runner.Command{Name: "pear", Args: []string{"uninstall", name}, Env: pkgEnv(), Output: logWriter(log)})
	return err
}

// PackageTool is a package manager and whether this server has it.
type PackageTool struct {
	Name      string `json:"name"`
	Language  string `json:"language"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Detects   string `json:"detects"` // what a deploy looks for
	Note      string `json:"note,omitempty"`
}

var toolList = []struct{ name, lang, bin, detects, note string }{
	{"npm", "JavaScript", "", "package-lock.json, package.json", "comes with Node.js"},
	{"pnpm", "JavaScript", "", "pnpm-lock.yaml or packageManager", "run through npx, at the version the project pins"},
	{"Yarn", "JavaScript", "", "yarn.lock, .yarnrc.yml or packageManager", "classic and Berry, at the version the project pins"},
	{"Bun", "JavaScript", "", "bun.lock, bun.lockb", "run through npx"},
	{"Composer", "PHP", "composer", "composer.json", ""},
	{"Phive", "PHP", "phive", ".phive/phars.xml", "list trusted GPG key ids in .phive/trusted-keys"},
	{"PECL", "PHP", "pecl", "", "extensions, from PHP extensions"},
	{"PEAR", "PHP", "pear", "", "shared PHP libraries, from PHP extensions"},
	{"pip", "Python", "", "requirements.txt, pyproject.toml", "comes with Python"},
	{"uv", "Python", "uv", "uv.lock", ""},
	{"Poetry", "Python", "poetry", "poetry.lock", ""},
	{"Pipenv", "Python", "pipenv", "Pipfile, Pipfile.lock", ""},
	{"pipx", "Python", "pipx", "", "command-line tools for SSH users"},
	{"Maven", "Java", "mvn", "pom.xml (mvnw is used when present)", ""},
	{"Gradle", "Java", "gradle", "build.gradle (gradlew is used when present)", ""},
	{"Go modules", "Go", "", "go.mod", "comes with Go"},
	{"pub", "Dart", "", "pubspec.yaml", "comes with Dart"},
}

// PackageTools reports the package managers deploys and SSH users can use.
func (m *Manager) PackageTools(ctx context.Context) []PackageTool {
	rts := InstalledRuntimes(m.cfg.Root, m.cfg.RuntimesDir)
	out := []PackageTool{}
	for _, t := range toolList {
		pt := PackageTool{Name: t.name, Language: t.lang, Detects: t.detects, Note: t.note}
		switch {
		case t.bin != "":
			for _, dir := range []string{"/usr/local/bin", "/usr/bin", "/opt/ike/bin"} {
				if _, err := os.Stat(m.p(filepath.Join(dir, t.bin))); err == nil {
					pt.Installed = true
				}
			}
		case t.lang == "JavaScript":
			pt.Installed = len(rts[RuntimeNode]) > 0
		case t.lang == "Python":
			pt.Installed = len(rts[RuntimePython]) > 0
		case t.lang == "Go":
			pt.Installed = len(rts[RuntimeGo]) > 0
		case t.lang == "Dart":
			pt.Installed = len(rts[RuntimeDart]) > 0
		}
		out = append(out, pt)
	}
	return out
}
