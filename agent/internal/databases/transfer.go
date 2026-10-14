package databases

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

func runnerCmd(name string, args, env []string) runner.Command {
	return runner.Command{Name: name, Args: args, Env: env, Timeout: time.Minute}
}

// owns reports whether an account owns a database of an engine.
func (m *Manager) owns(account, engine, name string) error {
	if err := m.env.CheckAccount(account); err != nil {
		return err
	}
	if !strings.HasPrefix(name, account+"_") {
		return fmt.Errorf("%s database %s %w", engine, name, kit.ErrNotFound)
	}
	switch engine {
	case MariaDB:
		if m.MariaDBOf != nil {
			for _, d := range m.MariaDBOf(account) {
				if d == name {
					return nil
				}
			}
		}
	default:
		a, err := m.load(account)
		if err != nil {
			return err
		}
		if _, _, err := m.find(a, engine, name); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%s database %s %w", engine, name, kit.ErrNotFound)
}

// Export streams a gzipped dump of a database to w.
//
//	MariaDB:    mariadb-dump (SQL, with routines, triggers and events)
//	PostgreSQL: pg_dump plain SQL, no owners or privileges (restores anywhere)
//	MongoDB:    mongodump --archive
func (m *Manager) Export(ctx context.Context, account, engine, name string, w io.Writer) error {
	if err := m.owns(account, engine, name); err != nil {
		return err
	}
	gz := gzip.NewWriter(w)
	var c runner.Command
	switch engine {
	case MariaDB:
		c = runner.Command{Name: "mariadb-dump", Args: []string{"--single-transaction", "--quick", "--routines", "--triggers", "--events",
			"--hex-blob", "--default-character-set=utf8mb4", "--", name}}
	case Postgres:
		c = runner.Command{Name: "runuser", Args: []string{"-u", "postgres", "--", "pg_dump", "--no-owner", "--no-privileges", "--clean", "--if-exists", name}}
	case MongoDB:
		pw, err := m.mongoAdmin()
		if err != nil {
			return err
		}
		// mongodump reads the password from a config file; pass it on stdin as YAML.
		c = runner.Command{Name: "mongodump", Args: []string{"--quiet", "--archive", "--host=127.0.0.1", "--port=27017",
			"--username=ike-admin", "--authenticationDatabase=admin", "--config=/dev/stdin", "--db=" + name},
			Stdin: "password: " + yamlQuote(pw) + "\n"}
	default:
		return fmt.Errorf("unknown engine %q", engine)
	}
	c.Stdout, c.Timeout = gz, 6*time.Hour
	if _, err := m.env.Run.Exec(ctx, c); err != nil {
		return err
	}
	return gz.Close()
}

func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// gunzipIfNeeded peeks at the stream and decompresses gzip.
func gunzipIfNeeded(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	head, _ := br.Peek(2)
	if len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		return gzip.NewReader(br)
	}
	return br, nil
}

// Import restores a dump (plain or gzipped) into a database. The SQL client
// runs as the account's Linux user and signs in as a temporary database user
// that can only reach this one database, so a dump can neither touch other
// databases nor run shell commands as root.
func (m *Manager) Import(ctx context.Context, account, engine, name string, r io.Reader) error {
	if err := m.owns(account, engine, name); err != nil {
		return err
	}
	in, err := gunzipIfNeeded(r)
	if err != nil {
		return err
	}
	tmp := "ike_imp_" + kit.RandomHex(5)
	pass := kit.RandomHex(16)
	home := m.env.Home(account)
	baseEnv := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	switch engine {
	case MariaDB:
		create := fmt.Sprintf("CREATE USER '%s'@'localhost' IDENTIFIED BY '%s'; GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'localhost';", tmp, pass, name, tmp)
		if _, err := m.env.Run.RunInput(ctx, create, "mariadb", "--batch"); err != nil {
			return err
		}
		defer func() {
			_, _ = m.env.Run.RunInput(context.Background(), fmt.Sprintf("DROP USER IF EXISTS '%s'@'localhost';", tmp), "mariadb", "--batch")
		}()
		_, err = m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", account, "--", "mariadb",
			"--user=" + tmp, "--default-character-set=utf8mb4", "--binary-mode", "--", name},
			Env: append(baseEnv, "MYSQL_PWD="+pass), StdinReader: in, Timeout: 6 * time.Hour})
	case Postgres:
		a, lerr := m.load(account)
		if lerr != nil {
			return lerr
		}
		d, _, ferr := m.find(a, engine, name)
		if ferr != nil {
			return ferr
		}
		// A member of the owner role, signing in over TCP with a password.
		if err := m.psql(ctx, "", fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' IN ROLE %s; GRANT CONNECT ON DATABASE %s TO %s;",
			qi(tmp), pass, qi(d.Owner), qi(name), qi(tmp))); err != nil {
			return err
		}
		defer func() {
			_ = m.psql(context.Background(), "", fmt.Sprintf("REASSIGN OWNED BY %s TO %s; DROP OWNED BY %s; DROP ROLE IF EXISTS %s;", qi(tmp), qi(d.Owner), qi(tmp), qi(tmp)))
		}()
		pre := strings.NewReader(fmt.Sprintf("SET ROLE %s;\n", qi(d.Owner)))
		_, err = m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", account, "--", "psql", "-X", "-q",
			"-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-U", tmp, "-d", name},
			Env: append(baseEnv, "PGPASSWORD="+pass), StdinReader: io.MultiReader(pre, in), Timeout: 6 * time.Hour})
	case MongoDB:
		if err := m.mongoCreateUser(ctx, name, tmp, pass, "dbOwner"); err != nil {
			return err
		}
		defer func() {
			_ = m.mongoEval(context.Background(), fmt.Sprintf(`db.getSiblingDB(%q).dropUser(%q)`, name, tmp))
		}()
		// mongorestore reads the archive on stdin, so the password goes in a
		// config file only the account can read.
		cfg := home + "/.ike-mongorestore-" + kit.RandomHex(4) + ".yaml"
		if _, err := m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", account, "--", "install", "-m", "0600", "/dev/stdin", cfg},
			Stdin: "password: " + yamlQuote(pass) + "\n", Env: baseEnv}); err != nil {
			return err
		}
		defer func() { _, _ = m.env.Run.Run(context.Background(), "rm", "-f", cfg) }()
		_, err = m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", account, "--", "mongorestore", "--quiet",
			"--archive", "--host=127.0.0.1", "--port=27017", "--username=" + tmp, "--authenticationDatabase=" + name,
			"--config=" + cfg, "--nsInclude=" + name + ".*", "--drop"},
			Env: baseEnv, StdinReader: in, Timeout: 6 * time.Hour})
	default:
		return fmt.Errorf("unknown engine %q", engine)
	}
	if err == nil {
		m.env.Publish("database.imported", name, map[string]any{"account": account, "engine": engine})
	}
	return err
}
