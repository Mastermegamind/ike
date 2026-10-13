package sites

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/validate"
)

// Database is a MariaDB database owned by an account. Its name and user are
// prefixed with the account name ("shop_store") so accounts never collide.
type Database struct {
	Name      string    `json:"name"`
	User      string    `json:"user"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// NewDatabase is returned once, at creation; the password is not stored by Ike.
type NewDatabase struct {
	Database
	Password string `json:"password"`
	Host     string `json:"host"`
}

var dbSuffixRe = regexp.MustCompile(`^[a-z0-9]{1,16}$`)

const maxDatabases = 100

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sqlExec runs SQL as the database root over the local unix socket. Every value
// interpolated here is either validated against [a-z0-9_] or random hex.
func (m *Manager) sqlExec(ctx context.Context, sql string) error {
	// SQL goes through stdin so passwords never show up in the process list.
	_, err := m.run.RunInput(ctx, sql, "mariadb", "--batch")
	return err
}

// CreateDatabase creates "<account>_<suffix>" with a user of the same name and a
// random password, granted access to that database only.
func (m *Manager) CreateDatabase(ctx context.Context, account, suffix, note string) (*NewDatabase, error) {
	if err := validate.Username(account); err != nil {
		return nil, err
	}
	if !dbSuffixRe.MatchString(suffix) {
		return nil, fmt.Errorf("database name must be 1-16 lowercase letters or digits")
	}
	if !cronComment.MatchString(note) {
		return nil, fmt.Errorf("note may only contain letters, numbers and basic punctuation")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createDatabase(ctx, account, suffix, note)
}

func (m *Manager) createDatabase(ctx context.Context, account, suffix, note string) (*NewDatabase, error) {
	a, err := m.getAccount(account)
	if err != nil {
		return nil, err
	}
	name := account + "_" + suffix
	if len(name) > 64 {
		return nil, fmt.Errorf("database name is too long")
	}
	for _, d := range a.Databases {
		if d.Name == name {
			return nil, fmt.Errorf("database %s %w", name, ErrExists)
		}
	}
	if len(a.Databases) >= maxDatabases {
		return nil, fmt.Errorf("an account can have at most %d databases", maxDatabases)
	}
	// MariaDB user names are limited to 80 chars; ours are at most 33.
	pass := randomHex(16)
	sql := fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; "+
		"CREATE USER '%s'@'localhost' IDENTIFIED BY '%s'; "+
		"GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'localhost'; FLUSH PRIVILEGES;",
		name, name, pass, name, name)
	if err := m.sqlExec(ctx, sql); err != nil {
		_ = m.sqlExec(context.Background(), fmt.Sprintf("DROP USER IF EXISTS '%s'@'localhost'; DROP DATABASE IF EXISTS `%s`;", name, name))
		return nil, fmt.Errorf("create database: %w", err)
	}
	db := Database{Name: name, User: name, Note: note, CreatedAt: time.Now().UTC()}
	a.Databases = append(a.Databases, db)
	if err := m.saveAccount(a); err != nil {
		return nil, err
	}
	slog.Info("database created", "account", account, "database", name)
	return &NewDatabase{Database: db, Password: pass, Host: "localhost"}, nil
}

// ResetDatabasePassword sets and returns a new random password.
func (m *Manager) ResetDatabasePassword(ctx context.Context, account, name string) (*NewDatabase, error) {
	if err := validate.Username(account); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.getAccount(account)
	if err != nil {
		return nil, err
	}
	for _, d := range a.Databases {
		if d.Name == name {
			pass := randomHex(16)
			if err := m.sqlExec(ctx, fmt.Sprintf("ALTER USER '%s'@'localhost' IDENTIFIED BY '%s';", d.User, pass)); err != nil {
				return nil, err
			}
			return &NewDatabase{Database: d, Password: pass, Host: "localhost"}, nil
		}
	}
	return nil, fmt.Errorf("database %s %w", name, ErrNotFound)
}

// DeleteDatabase drops a database and its user. This cannot be undone.
func (m *Manager) DeleteDatabase(ctx context.Context, account, name string) error {
	if err := validate.Username(account); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deleteDatabase(ctx, account, name)
}

func (m *Manager) deleteDatabase(ctx context.Context, account, name string) error {
	a, err := m.getAccount(account)
	if err != nil {
		return err
	}
	out := a.Databases[:0]
	var found *Database
	for i, d := range a.Databases {
		if d.Name == name {
			found = &a.Databases[i]
			continue
		}
		out = append(out, d)
	}
	if found == nil {
		return fmt.Errorf("database %s %w", name, ErrNotFound)
	}
	if err := m.sqlExec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`; DROP USER IF EXISTS '%s'@'localhost';", found.Name, found.User)); err != nil {
		return err
	}
	a.Databases = out
	slog.Info("database deleted", "account", account, "database", name)
	return m.saveAccount(a)
}

// Databases lists an account's databases.
func (m *Manager) Databases(account string) ([]Database, error) {
	a, err := m.GetAccount(account)
	if err != nil {
		return nil, err
	}
	if a.Databases == nil {
		return []Database{}, nil
	}
	return a.Databases, nil
}
