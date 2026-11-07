// Package databases adds PostgreSQL, Redis/Valkey and MongoDB to hosting
// accounts, and the tools around every engine: connection diagnostics,
// streamed import and export, MariaDB's process list and slow query log, and
// replication between servers. MariaDB databases themselves are created by the
// sites package (they predate this one).
//
// Names follow the MariaDB convention: "<account>_<name>", so accounts never
// collide and ownership is visible in the name.
package databases

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// Engines Ike can host.
const (
	MariaDB  = "mariadb"
	Postgres = "postgres"
	Redis    = "redis"
	MongoDB  = "mongodb"
)

// DB is one PostgreSQL or MongoDB database.
type DB struct {
	Engine    string    `json:"engine"`
	Name      string    `json:"name"`
	Owner     string    `json:"owner"` // login role / user
	Note      string    `json:"note,omitempty"`
	Roles     []Role    `json:"roles,omitempty"` // extra users
	CreatedAt time.Time `json:"created_at"`
	SizeMB    float64   `json:"size_mb,omitempty"`
}

// Role is an extra login on a database.
type Role struct {
	Name   string `json:"name"`
	Access string `json:"access"` // read | write
}

// RedisInstance is an account's private Redis, Valkey or Dragonfly server.
type RedisInstance struct {
	Engine    string    `json:"engine,omitempty"` // redis | valkey | dragonfly ("" = the server's Redis)
	Socket    string    `json:"socket"`
	MemoryMB  int       `json:"memory_mb"`
	Policy    string    `json:"policy"` // allkeys-lru | volatile-lru | noeviction ...
	Persist   bool      `json:"persist"`
	CreatedAt time.Time `json:"created_at"`
}

// AccountDBs is what one account has beyond MariaDB.
type AccountDBs struct {
	Account string         `json:"account"`
	DBs     []DB           `json:"databases"`
	Redis   *RedisInstance `json:"redis,omitempty"`
}

// Credentials are shown once.
type Credentials struct {
	Engine   string `json:"engine"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	URL      string `json:"url"`
}

type Manager struct {
	env   *kit.Env
	mu    sync.Mutex
	locks map[string]*sync.Mutex
	// MariaDBOf lists an account's MariaDB databases (from the sites package).
	MariaDBOf func(account string) []string
	// MongoAdmin reads the MongoDB admin password; replaced in tests.
	mongoAdmin func() (string, error)
}

func New(env *kit.Env) *Manager {
	m := &Manager{env: env, locks: map[string]*sync.Mutex{}}
	m.mongoAdmin = m.readMongoAdmin
	return m
}

func (m *Manager) lock(account string) func() {
	m.mu.Lock()
	l, ok := m.locks[account]
	if !ok {
		l = &sync.Mutex{}
		m.locks[account] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (m *Manager) stateFile(account string) string {
	return m.env.P(filepath.Join(m.env.Cfg.DataDir, "databases", account+".json"))
}

func (m *Manager) load(account string) (*AccountDBs, error) {
	if err := m.env.CheckAccount(account); err != nil {
		return nil, err
	}
	var a AccountDBs
	err := kit.ReadJSON(m.stateFile(account), &a)
	if err != nil && err != kit.ErrNotFound {
		return nil, err
	}
	a.Account = account
	if a.DBs == nil {
		a.DBs = []DB{}
	}
	return &a, nil
}

func (m *Manager) save(a *AccountDBs) error { return kit.WriteJSON(m.stateFile(a.Account), a) }

// Engines reports which engines this server has.
type Engines struct {
	MariaDB  bool   `json:"mariadb"`
	Postgres bool   `json:"postgres"`
	Redis    string `json:"redis"` // redis | valkey | "": the server-wide engine
	// Caches are the engines an account can pick for its private instance.
	Caches  []string `json:"caches"`
	MongoDB bool     `json:"mongodb"`
}

func (m *Manager) Engines() Engines {
	return Engines{MariaDB: m.MariaDBOf != nil, Postgres: m.env.Cfg.Postgres, Redis: m.env.Cfg.Redis,
		Caches: m.env.Cfg.Caches(), MongoDB: m.env.Cfg.MongoDB}
}

// List returns an account's databases of every engine.
func (m *Manager) List(account string) (map[string]any, error) {
	a, err := m.load(account)
	if err != nil {
		return nil, err
	}
	var maria []string
	if m.MariaDBOf != nil {
		maria = m.MariaDBOf(account)
	}
	return map[string]any{"engines": m.Engines(), "mariadb": maria, "databases": a.DBs, "redis": a.Redis}, nil
}

var suffixRe = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
var noteRe = regexp.MustCompile(`^[\p{L}\p{N} .,:;'()/_-]{0,120}$`)

// CreateRequest adds a PostgreSQL or MongoDB database.
type CreateRequest struct {
	Engine string `json:"engine"`
	Name   string `json:"name"` // suffix after "<account>_"
	Note   string `json:"note"`
}

func (m *Manager) Create(ctx context.Context, account string, req CreateRequest) (*Credentials, error) {
	if !suffixRe.MatchString(req.Name) {
		return nil, fmt.Errorf("name must be 1-16 lowercase letters or digits")
	}
	if !noteRe.MatchString(req.Note) {
		return nil, fmt.Errorf("note has unusual characters")
	}
	unlock := m.lock(account)
	defer unlock()
	a, err := m.load(account)
	if err != nil {
		return nil, err
	}
	name := account + "_" + req.Name
	for _, d := range a.DBs {
		if d.Name == name && d.Engine == req.Engine {
			return nil, fmt.Errorf("database %s %w", name, kit.ErrExists)
		}
	}
	if len(a.DBs) >= 100 {
		return nil, fmt.Errorf("an account can have at most 100 of these databases")
	}
	pass := kit.RandomHex(16)
	var cred *Credentials
	switch req.Engine {
	case Postgres:
		if !m.env.Cfg.Postgres {
			return nil, fmt.Errorf("PostgreSQL %w", kit.ErrNotInstalled)
		}
		if err := m.pgCreate(ctx, name, pass); err != nil {
			return nil, err
		}
		cred = pgCred(name, name, pass)
	case MongoDB:
		if !m.env.Cfg.MongoDB {
			return nil, fmt.Errorf("MongoDB %w", kit.ErrNotInstalled)
		}
		if err := m.mongoCreateUser(ctx, name, name, pass, "readWrite"); err != nil {
			return nil, err
		}
		cred = mongoCred(name, name, pass)
	default:
		return nil, fmt.Errorf("engine must be postgres or mongodb (MariaDB databases are under Databases)")
	}
	a.DBs = append(a.DBs, DB{Engine: req.Engine, Name: name, Owner: name, Note: req.Note, CreatedAt: kit.Now()})
	if err := m.save(a); err != nil {
		return nil, err
	}
	m.env.Publish("database.created", name, map[string]any{"account": account, "engine": req.Engine})
	return cred, nil
}

func (m *Manager) find(a *AccountDBs, engine, name string) (*DB, int, error) {
	for i := range a.DBs {
		if a.DBs[i].Engine == engine && a.DBs[i].Name == name {
			return &a.DBs[i], i, nil
		}
	}
	return nil, -1, fmt.Errorf("%s database %s %w", engine, name, kit.ErrNotFound)
}

// ResetPassword sets a new password for a database's owner (or one of its roles).
func (m *Manager) ResetPassword(ctx context.Context, account, engine, name, role string) (*Credentials, error) {
	unlock := m.lock(account)
	defer unlock()
	a, err := m.load(account)
	if err != nil {
		return nil, err
	}
	d, _, err := m.find(a, engine, name)
	if err != nil {
		return nil, err
	}
	user := d.Owner
	if role != "" {
		found := false
		for _, r := range d.Roles {
			found = found || r.Name == role
		}
		if !found {
			return nil, fmt.Errorf("user %s %w", role, kit.ErrNotFound)
		}
		user = role
	}
	pass := kit.RandomHex(16)
	switch engine {
	case Postgres:
		if err := m.psql(ctx, "", fmt.Sprintf("ALTER ROLE %s PASSWORD '%s';", qi(user), pass)); err != nil {
			return nil, err
		}
		return pgCred(name, user, pass), nil
	case MongoDB:
		js := fmt.Sprintf(`db.getSiblingDB(%q).changeUserPassword(%q, %q)`, name, user, pass)
		if err := m.mongoEval(ctx, js); err != nil {
			return nil, err
		}
		return mongoCred(name, user, pass), nil
	}
	return nil, fmt.Errorf("unknown engine")
}

// AddRole adds an extra login with read or read/write access.
func (m *Manager) AddRole(ctx context.Context, account, engine, name, roleSuffix, access string) (*Credentials, error) {
	if !suffixRe.MatchString(roleSuffix) {
		return nil, fmt.Errorf("user name must be 1-16 lowercase letters or digits")
	}
	if access != "read" && access != "write" {
		return nil, fmt.Errorf("access is read or write")
	}
	unlock := m.lock(account)
	defer unlock()
	a, err := m.load(account)
	if err != nil {
		return nil, err
	}
	d, _, err := m.find(a, engine, name)
	if err != nil {
		return nil, err
	}
	user := account + "_" + roleSuffix
	if user == d.Owner {
		return nil, fmt.Errorf("that is the owner's name")
	}
	for _, r := range d.Roles {
		if r.Name == user {
			return nil, fmt.Errorf("user %s %w", user, kit.ErrExists)
		}
	}
	if len(d.Roles) >= 20 {
		return nil, fmt.Errorf("at most 20 extra users per database")
	}
	pass := kit.RandomHex(16)
	var cred *Credentials
	switch engine {
	case Postgres:
		if err := m.pgAddRole(ctx, d, user, pass, access); err != nil {
			return nil, err
		}
		cred = pgCred(name, user, pass)
	case MongoDB:
		r := "read"
		if access == "write" {
			r = "readWrite"
		}
		if err := m.mongoCreateUser(ctx, name, user, pass, r); err != nil {
			return nil, err
		}
		cred = mongoCred(name, user, pass)
	}
	d.Roles = append(d.Roles, Role{Name: user, Access: access})
	return cred, m.save(a)
}

func (m *Manager) RemoveRole(ctx context.Context, account, engine, name, user string) error {
	unlock := m.lock(account)
	defer unlock()
	a, err := m.load(account)
	if err != nil {
		return err
	}
	d, _, err := m.find(a, engine, name)
	if err != nil {
		return err
	}
	idx := -1
	for i, r := range d.Roles {
		if r.Name == user {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("user %s %w", user, kit.ErrNotFound)
	}
	switch engine {
	case Postgres:
		err = m.psql(ctx, name, fmt.Sprintf("REASSIGN OWNED BY %s TO %s; DROP OWNED BY %s; DROP ROLE IF EXISTS %s;", qi(user), qi(d.Owner), qi(user), qi(user)))
	case MongoDB:
		err = m.mongoEval(ctx, fmt.Sprintf(`db.getSiblingDB(%q).dropUser(%q)`, name, user))
	}
	if err != nil {
		return err
	}
	d.Roles = append(d.Roles[:idx], d.Roles[idx+1:]...)
	return m.save(a)
}

// Delete drops a database and its users.
func (m *Manager) Delete(ctx context.Context, account, engine, name string) error {
	unlock := m.lock(account)
	defer unlock()
	a, err := m.load(account)
	if err != nil {
		return err
	}
	d, i, err := m.find(a, engine, name)
	if err != nil {
		return err
	}
	switch engine {
	case Postgres:
		var sql strings.Builder
		fmt.Fprintf(&sql, "DROP DATABASE IF EXISTS %s WITH (FORCE);\n", qi(d.Name))
		for _, r := range d.Roles {
			fmt.Fprintf(&sql, "DROP ROLE IF EXISTS %s;\n", qi(r.Name))
		}
		fmt.Fprintf(&sql, "DROP ROLE IF EXISTS %s;\n", qi(d.Owner))
		err = m.psql(ctx, "", sql.String())
	case MongoDB:
		err = m.mongoEval(ctx, fmt.Sprintf(`const d = db.getSiblingDB(%q); d.dropAllUsers(); d.dropDatabase()`, name))
	}
	if err != nil {
		return err
	}
	a.DBs = append(a.DBs[:i], a.DBs[i+1:]...)
	m.env.Publish("database.deleted", name, map[string]any{"account": account, "engine": engine})
	return m.save(a)
}

// DeleteAccount removes everything an account has (called when the account goes).
func (m *Manager) DeleteAccount(ctx context.Context, account string) error {
	a, err := m.load(account)
	if err != nil {
		return err
	}
	for _, d := range append([]DB(nil), a.DBs...) {
		_ = m.Delete(ctx, account, d.Engine, d.Name)
	}
	if a.Redis != nil {
		_ = m.RemoveRedis(ctx, account)
	}
	return nil
}

// RefreshSizes measures database sizes.
func (m *Manager) RefreshSizes(ctx context.Context, account string) ([]DB, error) {
	unlock := m.lock(account)
	defer unlock()
	a, err := m.load(account)
	if err != nil {
		return nil, err
	}
	for i := range a.DBs {
		d := &a.DBs[i]
		switch d.Engine {
		case Postgres:
			out, err := m.psqlQuery(ctx, "", fmt.Sprintf("SELECT pg_database_size('%s')", d.Name))
			if err == nil {
				var b float64
				fmt.Sscan(strings.TrimSpace(out), &b)
				d.SizeMB = float64(int(b/1048576*10)) / 10
			}
		case MongoDB:
			out, err := m.mongoOut(ctx, fmt.Sprintf(`print(db.getSiblingDB(%q).stats().storageSize)`, d.Name))
			if err == nil {
				var b float64
				fmt.Sscan(strings.TrimSpace(out), &b)
				d.SizeMB = float64(int(b/1048576*10)) / 10
			}
		}
	}
	sort.Slice(a.DBs, func(i, j int) bool { return a.DBs[i].Name < a.DBs[j].Name })
	return a.DBs, m.save(a)
}

// ---- PostgreSQL ----

// qi quotes a PostgreSQL identifier. Names are validated to [a-z0-9_] first,
// so this is belt and braces.
func qi(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func pgCred(db, user, pass string) *Credentials {
	return &Credentials{Engine: Postgres, Database: db, User: user, Password: pass, Host: "127.0.0.1", Port: 5432,
		URL: fmt.Sprintf("postgresql://%s:%s@127.0.0.1:5432/%s", user, pass, db)}
}

// psql runs SQL as the postgres superuser, through stdin so passwords never
// reach the process list.
func (m *Manager) psql(ctx context.Context, db, sql string) error {
	args := []string{"-u", "postgres", "--", "psql", "-X", "-q", "-v", "ON_ERROR_STOP=1"}
	if db != "" {
		args = append(args, "-d", db)
	}
	_, err := m.env.Run.RunInput(ctx, sql, "runuser", args...)
	return err
}

func (m *Manager) psqlQuery(ctx context.Context, db, sql string) (string, error) {
	args := []string{"-u", "postgres", "--", "psql", "-X", "-At", "-v", "ON_ERROR_STOP=1"}
	if db != "" {
		args = append(args, "-d", db)
	}
	out, err := m.env.Run.RunInput(ctx, sql, "runuser", args...)
	return string(out), err
}

func (m *Manager) pgCreate(ctx context.Context, name, pass string) error {
	// CREATE DATABASE can't run in a transaction block with other statements,
	// so run them one by one and clean up on failure.
	steps := []string{
		fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE CONNECTION LIMIT 100;", qi(name), pass),
		fmt.Sprintf("CREATE DATABASE %s OWNER %s ENCODING 'UTF8' TEMPLATE template0;", qi(name), qi(name)),
		fmt.Sprintf("REVOKE ALL ON DATABASE %s FROM PUBLIC; GRANT CONNECT, TEMPORARY ON DATABASE %s TO %s;", qi(name), qi(name), qi(name)),
	}
	for _, s := range steps {
		if err := m.psql(ctx, "", s); err != nil {
			_ = m.psql(context.Background(), "", fmt.Sprintf("DROP DATABASE IF EXISTS %s; DROP ROLE IF EXISTS %s;", qi(name), qi(name)))
			return fmt.Errorf("create PostgreSQL database: %w", err)
		}
	}
	// PostgreSQL 15+ no longer lets everyone create in public; make the owner own it.
	return m.psql(ctx, name, fmt.Sprintf("ALTER SCHEMA public OWNER TO %s;", qi(name)))
}

func (m *Manager) pgAddRole(ctx context.Context, d *DB, user, pass, access string) error {
	if err := m.psql(ctx, "", fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE; GRANT CONNECT ON DATABASE %s TO %s;",
		qi(user), pass, qi(d.Name), qi(user))); err != nil {
		return err
	}
	grant := "SELECT"
	seq := "SELECT"
	if access == "write" {
		grant, seq = "SELECT, INSERT, UPDATE, DELETE", "USAGE, SELECT, UPDATE"
	}
	sql := fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %[1]s;
GRANT %[2]s ON ALL TABLES IN SCHEMA public TO %[1]s;
GRANT %[3]s ON ALL SEQUENCES IN SCHEMA public TO %[1]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[4]s IN SCHEMA public GRANT %[2]s ON TABLES TO %[1]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[4]s IN SCHEMA public GRANT %[3]s ON SEQUENCES TO %[1]s;`, qi(user), grant, seq, qi(d.Owner))
	if err := m.psql(ctx, d.Name, sql); err != nil {
		_ = m.psql(context.Background(), "", fmt.Sprintf("DROP ROLE IF EXISTS %s;", qi(user)))
		return err
	}
	return nil
}

// ---- MongoDB ----

func mongoCred(db, user, pass string) *Credentials {
	return &Credentials{Engine: MongoDB, Database: db, User: user, Password: pass, Host: "127.0.0.1", Port: 27017,
		URL: fmt.Sprintf("mongodb://%s:%s@127.0.0.1:27017/%s?authSource=%s", user, pass, db, db)}
}

func (m *Manager) readMongoAdmin() (string, error) {
	var c struct {
		Password string `json:"admin_password"`
	}
	if err := kit.ReadJSON(m.env.P("/etc/ike/mongodb.json"), &c); err != nil {
		return "", fmt.Errorf("MongoDB admin credentials missing: %w", err)
	}
	return c.Password, nil
}

// mongoOut runs JavaScript as the MongoDB admin. The connection string with
// the password goes through stdin, never the command line.
func (m *Manager) mongoOut(ctx context.Context, js string) (string, error) {
	pw, err := m.mongoAdmin()
	if err != nil {
		return "", err
	}
	script := fmt.Sprintf("db = connect(%q);\n%s\n", "mongodb://ike-admin:"+pw+"@127.0.0.1:27017/admin", js)
	out, err := m.env.Run.Exec(ctx, runner.Command{Name: "mongosh", Args: []string{"--nodb", "--quiet"}, Stdin: script, Timeout: time.Minute})
	return string(out), err
}

func (m *Manager) mongoEval(ctx context.Context, js string) error {
	_, err := m.mongoOut(ctx, js)
	return err
}

func (m *Manager) mongoCreateUser(ctx context.Context, dbName, user, pass, role string) error {
	return m.mongoEval(ctx, fmt.Sprintf(`db.getSiblingDB(%q).createUser({user: %q, pwd: %q, roles: [{role: %q, db: %q}]})`, dbName, user, pass, role, dbName))
}

// Names lists an account's PostgreSQL and MongoDB databases as "engine:name".
func (m *Manager) Names(account string) []string {
	a, err := m.load(account)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(a.DBs))
	for _, d := range a.DBs {
		out = append(out, d.Engine+":"+d.Name)
	}
	return out
}
