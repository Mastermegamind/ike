package databases

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

func ioPipe() (*io.PipeReader, *io.PipeWriter) { return io.Pipe() }

func cmdOut(name string, args, env []string, w io.Writer) runner.Command {
	return runner.Command{Name: name, Args: args, Env: env, Stdout: w, Timeout: 12 * time.Hour}
}

func cmdIn(name string, args []string, r io.Reader) runner.Command {
	return runner.Command{Name: name, Args: args, StdinReader: r, Timeout: 12 * time.Hour}
}

// Check is one diagnostic result.
type Check struct {
	Title    string `json:"title"`
	Severity string `json:"severity"` // ok | info | warn | bad
	Detail   string `json:"detail"`
	Fix      string `json:"fix,omitempty"`
}

// DiagnoseRequest optionally includes the password the app uses, to test
// that it can sign in. It is used once and not stored.
type DiagnoseRequest struct {
	Engine   string `json:"engine"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	// PHP is the website's PHP version, to check the right extension.
	PHP string `json:"php"`
}

// Diagnose explains why an app might not reach its database.
func (m *Manager) Diagnose(ctx context.Context, account string, r DiagnoseRequest) ([]Check, error) {
	if err := m.owns(account, r.Engine, r.Database); err != nil {
		return nil, err
	}
	if r.User == "" {
		r.User = r.Database
	}
	if !strings.HasPrefix(r.User, account+"_") || !userRe.MatchString(r.User) {
		return nil, fmt.Errorf("user must be one of this account's database users")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out []Check
	add := func(c Check) { out = append(out, c) }
	service := map[string]string{MariaDB: "mariadb", Postgres: "postgresql", MongoDB: "mongod"}[r.Engine]
	state, _ := m.env.Run.Run(ctx, "systemctl", "is-active", service)
	if s := strings.TrimSpace(string(state)); s == "inactive" || s == "failed" || s == "deactivating" {
		add(Check{Title: "The database server isn't running", Severity: "bad", Detail: service + " is " + s + ".",
			Fix: "Ask the server admin to start it under Server → Services, then check its log."})
		return out, nil
	}
	add(Check{Title: "Database server is running", Severity: "ok", Detail: service + " is active."})

	switch r.Engine {
	case MariaDB:
		rows, err := m.sqlRows(ctx, "SELECT @@max_connections, (SELECT COUNT(*) FROM information_schema.PROCESSLIST), @@version;")
		if err == nil && len(rows) == 1 && len(rows[0]) >= 3 {
			maxc, _ := strconv.Atoi(rows[0][0])
			cur, _ := strconv.Atoi(rows[0][1])
			c := Check{Title: "Connections", Severity: "ok", Detail: fmt.Sprintf("%d of %d connections in use (MariaDB %s).", cur, maxc, rows[0][2])}
			if maxc > 0 && cur*100/maxc > 85 {
				c.Severity, c.Title = "warn", "Almost out of connections"
				c.Fix = "An app is holding connections open. Look at the process list for long-running queries."
			}
			add(c)
		}
		rows, err = m.sqlRows(ctx, fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='localhost';", r.User))
		if err == nil && len(rows) == 1 && rows[0][0] == "0" {
			add(Check{Title: "User doesn't exist", Severity: "bad", Detail: r.User + "@localhost isn't a MariaDB user.",
				Fix: "Reset the database password; Ike recreates the user."})
		}
		rows, _ = m.sqlRows(ctx, fmt.Sprintf("SELECT ROUND(SUM(data_length+index_length)/1048576,1), COUNT(*) FROM information_schema.TABLES WHERE table_schema='%s';", r.Database))
		if len(rows) == 1 && len(rows[0]) == 2 {
			add(Check{Title: "Database contents", Severity: "info", Detail: fmt.Sprintf("%s tables, %s MB.", rows[0][1], strings.Replace(rows[0][0], "NULL", "0", 1))})
		}
		if r.Password != "" {
			_, err := m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", account, "--", "mariadb", "--user=" + r.User,
				"--batch", "-e", "SELECT 1", "--", r.Database}, Env: []string{"MYSQL_PWD=" + r.Password, "PATH=/usr/bin:/bin", "HOME=" + m.env.Home(account)}})
			add(signIn(err, "localhost"))
		}
	case Postgres:
		if q, err := m.psqlQuery(ctx, "", "SELECT current_setting('max_connections')::int, (SELECT count(*) FROM pg_stat_activity), version();"); err == nil {
			f := strings.Split(strings.TrimSpace(q), "|")
			if len(f) >= 3 {
				maxc, _ := strconv.Atoi(f[0])
				cur, _ := strconv.Atoi(f[1])
				c := Check{Title: "Connections", Severity: "ok", Detail: fmt.Sprintf("%d of %d connections in use.", cur, maxc)}
				if maxc > 0 && cur*100/maxc > 85 {
					c.Severity, c.Title, c.Fix = "warn", "Almost out of connections", "Use a connection pool (for example PgBouncer) or close idle connections."
				}
				add(c)
			}
		}
		if r.Password != "" {
			_, err := m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", account, "--", "psql", "-X", "-At",
				"-h", "127.0.0.1", "-U", r.User, "-d", r.Database, "-c", "SELECT 1"},
				Env: []string{"PGPASSWORD=" + r.Password, "PATH=/usr/bin:/bin", "HOME=" + m.env.Home(account)}})
			add(signIn(err, "127.0.0.1:5432"))
		}
	case MongoDB:
		if r.Password != "" {
			script := fmt.Sprintf("db = connect(%q); print(db.runCommand({ping: 1}).ok)",
				fmt.Sprintf("mongodb://%s:%s@127.0.0.1:27017/%s?authSource=%s", r.User, r.Password, r.Database, r.Database))
			_, err := m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", account, "--", "mongosh", "--nodb", "--quiet"},
				Stdin: script, Env: []string{"PATH=/usr/bin:/bin", "HOME=" + m.env.Home(account)}})
			add(signIn(err, "127.0.0.1:27017"))
		}
	}
	if phpRe.MatchString(r.PHP) {
		ext := map[string]string{MariaDB: "pdo_mysql", Postgres: "pdo_pgsql", MongoDB: "mongodb"}[r.Engine]
		ini := filepath.Join("/etc/php", r.PHP, "mods-available", strings.TrimPrefix(ext, "pdo_")+".ini")
		if r.Engine == MongoDB {
			ini = filepath.Join("/etc/php", r.PHP, "mods-available", "mongodb.ini")
		}
		if _, err := os.Stat(m.env.P(ini)); err != nil && !m.env.Cfg.DryRun {
			add(Check{Title: "PHP can't talk to this database", Severity: "bad", Detail: "The " + ext + " extension isn't installed for PHP " + r.PHP + ".",
				Fix: "Install it under Server → PHP extensions."})
		} else {
			add(Check{Title: "PHP extension present", Severity: "ok", Detail: ext + " is available for PHP " + r.PHP + "."})
		}
	}
	return out, nil
}

var phpRe = regexp.MustCompile(`^[5-9]\.[0-9]$`)

func signIn(err error, where string) Check {
	if err == nil {
		return Check{Title: "Signing in works", Severity: "ok", Detail: "The user and password connect to " + where + "."}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Access denied"), strings.Contains(msg, "password authentication failed"), strings.Contains(msg, "Authentication failed"):
		return Check{Title: "Wrong password", Severity: "bad", Detail: "The database rejected the password.",
			Fix: "Copy the password into your app's settings again, or reset it here and update the app."}
	case strings.Contains(msg, "Unknown database"), strings.Contains(msg, "does not exist"):
		return Check{Title: "Database not found", Severity: "bad", Detail: "The database name doesn't exist.", Fix: "Check the database name in your app's settings."}
	case strings.Contains(msg, "Can't connect"), strings.Contains(msg, "Connection refused"), strings.Contains(msg, "ECONNREFUSED"):
		return Check{Title: "Can't connect", Severity: "bad", Detail: "Nothing answered at " + where + ".", Fix: "Use host " + where + " in your app."}
	}
	return Check{Title: "Signing in failed", Severity: "bad", Detail: lastLine(msg)}
}

func lastLine(s string) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	return l[len(l)-1]
}
