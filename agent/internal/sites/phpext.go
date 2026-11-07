package sites

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// PHPExtension is one optional PHP extension, installed per PHP version.
type PHPExtension struct {
	Name        string `json:"name"`
	Package     string `json:"package"`
	Description string `json:"description"`
	Installed   bool   `json:"installed"`
}

// phpExtensions is the allow-list: only these packages can be installed or
// removed from the panel. Values are descriptions.
var phpExtensions = map[string]string{
	"amqp": "RabbitMQ client", "apcu": "In-memory user cache", "ast": "Abstract syntax tree (static analysis tools)",
	"bcmath": "Arbitrary precision maths", "bz2": "bzip2 compression", "curl": "HTTP requests",
	"decimal": "Exact decimal arithmetic", "ds": "Efficient data structures", "gd": "Image processing (GD)",
	"gmp": "Big integer maths", "grpc": "gRPC client", "igbinary": "Compact serializer",
	"imagick": "Image processing (ImageMagick)", "imap": "IMAP mail", "intl": "Internationalization, number and date formatting",
	"ldap": "LDAP directory", "mailparse": "Email parsing", "maxminddb": "GeoIP lookups", "mbstring": "Multibyte strings",
	"memcached": "Memcached client", "mongodb": "MongoDB driver", "msgpack": "MessagePack serializer",
	"mysql": "MySQL and MariaDB (mysqli, PDO)", "odbc": "ODBC databases", "opcache": "Bytecode cache (faster PHP)",
	"pgsql": "PostgreSQL (pgsql, PDO)", "protobuf": "Protocol Buffers", "readline": "Interactive shell",
	"redis": "Redis and Valkey client", "soap": "SOAP web services", "sqlite3": "SQLite", "ssh2": "SSH and SFTP client",
	"swoole": "Async server (Swoole)", "tidy": "HTML cleanup", "uuid": "UUID generation", "xdebug": "Debugger and profiler (not for production)",
	"xml": "XML, DOM and XSL", "xmlrpc": "XML-RPC", "yaml": "YAML parsing", "zip": "ZIP archives", "zstd": "Zstandard compression",
}

// PHPExtensions lists the optional extensions for a PHP version and whether
// each is installed.
func (m *Manager) PHPExtensions(ctx context.Context, version string) ([]PHPExtension, error) {
	if err := m.checkRuntime(RuntimePHP, version); err != nil {
		return nil, err
	}
	out, _ := m.run.Run(ctx, "dpkg-query", "-W", "-f", "${Package} ${Status}\\n", "php"+version+"-*")
	installed := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) >= 4 && f[3] == "installed" && f[1] == "install" {
			installed[f[0]] = true
		}
	}
	list := make([]PHPExtension, 0, len(phpExtensions))
	for name, desc := range phpExtensions {
		pkg := "php" + version + "-" + name
		list = append(list, PHPExtension{Name: name, Package: pkg, Description: desc, Installed: installed[pkg]})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// ChangePHPExtension installs or removes one extension, then reloads PHP-FPM.
// It runs as a job.
func (m *Manager) ChangePHPExtension(ctx context.Context, version, name string, install bool, log func(string, ...any)) error {
	if err := m.checkRuntime(RuntimePHP, version); err != nil {
		return err
	}
	if _, ok := phpExtensions[name]; !ok {
		return fmt.Errorf("%q isn't an extension Ike can manage", name)
	}
	pkg := "php" + version + "-" + name
	env := []string{"DEBIAN_FRONTEND=noninteractive", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	args := []string{"remove", "-y", pkg}
	if install {
		args = []string{"install", "-y", "--no-install-recommends", pkg}
		log("Installing %s", pkg)
	} else {
		log("Removing %s", pkg)
	}
	if _, err := m.run.Exec(ctx, runner.Command{Name: "apt-get", Args: args, Env: env, Output: logWriter(log)}); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "php-fpm"+version, "--test"); err != nil {
		return fmt.Errorf("PHP %s's configuration is broken after the change: %w", version, err)
	}
	_, err := m.run.Run(ctx, "systemctl", "reload", "php"+version+"-fpm")
	if err == nil {
		log("PHP %s reloaded", version)
	}
	return err
}
