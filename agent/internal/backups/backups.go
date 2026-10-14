// Package backups runs scheduled, incremental, encrypted backups with restic:
// the whole server, one account, one website, databases or email, to local
// disks, SFTP or S3-compatible storage (Amazon S3, Backblaze B2, Wasabi, R2,
// Spaces, MinIO). restic deduplicates (every run after the first only stores
// what changed), encrypts with AES-256 before anything leaves the server, and
// checks repository integrity. Database dumps are made just before each run.
package backups

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/secrets"
	"github.com/YOUR-ORG/ike/agent/internal/storage"
	"github.com/YOUR-ORG/ike/agent/internal/validate"
)

// Plan is one backup job.
type Plan struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Scope   string `json:"scope"` // server | account | site | databases | mail
	Account string `json:"account,omitempty"`
	Site    string `json:"site,omitempty"` // site scope: the domain
	Target  string `json:"target"`         // storage target ID
	Enabled bool   `json:"enabled"`

	Schedule  Schedule  `json:"schedule"`
	Retention Retention `json:"retention"`
	// Databases to dump: empty = all of the scope's databases.
	Databases []string `json:"databases,omitempty"`
	Excludes  []string `json:"excludes,omitempty"` // glob patterns
	// CheckDays: verify a sample of stored data every N days (0 = never).
	CheckDays int `json:"check_days"`

	Password  string    `json:"password,omitempty"` // restic repository password, sealed
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`

	Last      *RunResult `json:"last,omitempty"`
	LastCheck *RunResult `json:"last_check,omitempty"`
}

type Schedule struct {
	Frequency string `json:"frequency"` // hourly | daily | weekly | manual
	Hour      int    `json:"hour"`
	Minute    int    `json:"minute"`
	Weekday   int    `json:"weekday"`
}

type Retention struct {
	KeepLast    int `json:"keep_last"`
	KeepHourly  int `json:"keep_hourly"`
	KeepDaily   int `json:"keep_daily"`
	KeepWeekly  int `json:"keep_weekly"`
	KeepMonthly int `json:"keep_monthly"`
}

type RunResult struct {
	Status     string    `json:"status"` // ok | failed | running
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Snapshot   string    `json:"snapshot,omitempty"`
	AddedBytes int64     `json:"added_bytes,omitempty"`
	TotalBytes int64     `json:"total_bytes,omitempty"`
	Files      int64     `json:"files,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Sources is what the backups package needs from the rest of the agent.
type Sources struct {
	// AccountSites lists an account's website folders (absolute).
	AccountSites func(account string) []string
	// SiteDir returns a website's folder and account.
	SiteDir func(domain string) (dir, account string, err error)
	// Databases lists an account's databases as "engine:name" ("" = all accounts).
	Databases func(account string) []string
	// Dump writes a database dump (gzipped) to w.
	Dump func(ctx context.Context, engine, name string, w *os.File) error
	// MailDirs lists an account's mail folders (absolute; "" = all).
	MailDirs func(account string) []string
	// Restore imports a dump into a database.
	ImportDump func(ctx context.Context, account, engine, name string, f *os.File) error
	// AfterServerRestore recreates users and configs after a full restore.
	AfterServerRestore func(ctx context.Context, log func(string, ...any)) error
}

type Manager struct {
	env     *kit.Env
	keys    *secrets.Keyring
	targets *storage.Manager
	src     Sources
	store   kit.Store[[]Plan]
	mu      sync.Mutex
	running map[string]bool
}

func New(env *kit.Env, keys *secrets.Keyring, targets *storage.Manager, src Sources) *Manager {
	m := &Manager{env: env, keys: keys, targets: targets, src: src, running: map[string]bool{}}
	m.store.Path = env.P("/var/lib/ike/backups/plans.json")
	if keys != nil {
		keys.Register("backups", m.reseal)
	}
	return m
}

func (m *Manager) reseal(open, seal func(string) (string, error)) error {
	_, err := m.store.Update(func(ps *[]Plan) error {
		for i := range *ps {
			v, err := open((*ps)[i].Password)
			if err != nil {
				return err
			}
			if (*ps)[i].Password, err = seal(v); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (p Plan) public() Plan { p.Password = ""; return p }

// Plans lists plans; account limits to that account's plans.
func (m *Manager) Plans(account string) ([]Plan, error) {
	ps, err := m.store.Load()
	out := []Plan{}
	for _, p := range ps {
		if account == "" || p.Account == account {
			out = append(out, p.public())
		}
	}
	return out, err
}

func (m *Manager) plan(account, id string) (*Plan, error) {
	ps, err := m.store.Load()
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.ID == id && (account == "" || p.Account == account) {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("backup plan %s %w", id, kit.ErrNotFound)
}

var (
	nameRe    = regexp.MustCompile(`^[\p{L}\p{N} ._()-]{1,60}$`)
	excludeRe = regexp.MustCompile(`^[A-Za-z0-9._*/?\[\]-]{1,200}$`)
)

// PlanRequest creates or changes a plan. Account plans may only back up
// their own account, websites and databases.
type PlanRequest struct {
	Plan
	// RecoveryPassword lets an admin set the repository password (to reuse
	// an existing repository); empty generates one.
	RecoveryPassword string `json:"recovery_password"`
}

// SavedPlan returns the plan and, on creation, the recovery key once.
type SavedPlan struct {
	Plan
	RecoveryKey string `json:"recovery_key,omitempty"`
}

func (m *Manager) Save(ctx context.Context, account, actor string, req PlanRequest) (*SavedPlan, error) {
	p := req.Plan
	if account != "" {
		p.Account = account
		if p.Scope == "server" {
			return nil, fmt.Errorf("only server admins can back up the whole server")
		}
	}
	p.Name = strings.TrimSpace(p.Name)
	if !nameRe.MatchString(p.Name) {
		return nil, fmt.Errorf("give the plan a short name")
	}
	switch p.Scope {
	case "server":
		p.Account, p.Site = "", ""
	case "account", "databases", "mail":
		if err := m.env.CheckAccount(p.Account); err != nil {
			return nil, err
		}
		p.Site = ""
	case "site":
		if m.src.SiteDir == nil {
			return nil, fmt.Errorf("websites unavailable")
		}
		_, owner, err := m.src.SiteDir(p.Site)
		if err != nil {
			return nil, err
		}
		if p.Account != "" && owner != p.Account {
			return nil, fmt.Errorf("website %s %w", p.Site, kit.ErrNotFound)
		}
		p.Account = owner
	default:
		return nil, fmt.Errorf("scope is server, account, site, databases or mail")
	}
	if _, err := m.targets.Get(targetOwner(account), p.Target); err != nil {
		if account != "" {
			if _, err2 := m.targets.Get(account, p.Target); err2 != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	s := &p.Schedule
	switch s.Frequency {
	case "hourly", "daily", "weekly", "manual":
	case "":
		s.Frequency = "daily"
	default:
		return nil, fmt.Errorf("frequency is hourly, daily, weekly or manual")
	}
	if s.Hour < 0 || s.Hour > 23 || s.Minute < 0 || s.Minute > 59 || s.Weekday < 0 || s.Weekday > 6 {
		return nil, fmt.Errorf("time is hour 0-23, minute 0-59, weekday 0-6")
	}
	r := &p.Retention
	if *r == (Retention{}) {
		*r = Retention{KeepLast: 3, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6}
	}
	for _, v := range []int{r.KeepLast, r.KeepHourly, r.KeepDaily, r.KeepWeekly, r.KeepMonthly} {
		if v < 0 || v > 1000 {
			return nil, fmt.Errorf("retention counts must be 0-1000")
		}
	}
	if len(p.Excludes) > 50 {
		return nil, fmt.Errorf("at most 50 exclusions")
	}
	for _, e := range p.Excludes {
		if !excludeRe.MatchString(e) {
			return nil, fmt.Errorf("exclusion %q: use paths and * wildcards", e)
		}
	}
	for _, d := range p.Databases {
		if !strings.Contains(d, ":") {
			return nil, fmt.Errorf("databases look like mariadb:shop_wp")
		}
	}
	if p.CheckDays < 0 || p.CheckDays > 90 {
		return nil, fmt.Errorf("check every 0-90 days")
	}
	if req.RecoveryPassword != "" && (len(req.RecoveryPassword) < 12 || account != "") {
		return nil, fmt.Errorf("a recovery password must be at least 12 characters (admins only)")
	}
	var out SavedPlan
	_, err := m.store.Update(func(ps *[]Plan) error {
		idx := -1
		for i, o := range *ps {
			if p.ID != "" && o.ID == p.ID {
				if account != "" && o.Account != account {
					return fmt.Errorf("backup plan %s %w", p.ID, kit.ErrNotFound)
				}
				idx = i
			}
		}
		if idx >= 0 {
			o := (*ps)[idx]
			p.Password, p.CreatedAt, p.CreatedBy, p.Last, p.LastCheck = o.Password, o.CreatedAt, o.CreatedBy, o.Last, o.LastCheck
			(*ps)[idx] = p
			out.Plan = p
			return nil
		}
		if len(*ps) >= 500 {
			return fmt.Errorf("at most 500 backup plans")
		}
		pw := req.RecoveryPassword
		if pw == "" {
			pw = kit.Password(32)
		}
		sealed, err := m.keys.Seal(pw)
		if err != nil {
			return err
		}
		p.ID, p.Password, p.CreatedBy, p.CreatedAt = kit.RandomHex(6), sealed, actor, kit.Now()
		*ps = append(*ps, p)
		out.Plan, out.RecoveryKey = p, pw
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Plan = out.Plan.public()
	return &out, nil
}

func targetOwner(account string) string { return account }

func (m *Manager) Delete(account, id string) error {
	_, err := m.store.Update(func(ps *[]Plan) error {
		for i, o := range *ps {
			if o.ID == id && (account == "" || o.Account == account) {
				*ps = append((*ps)[:i], (*ps)[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("backup plan %s %w", id, kit.ErrNotFound)
	})
	return err
}

// RecoveryKey returns a plan's repository password and location, for safe
// keeping off the server (needed to restore on a new server).
func (m *Manager) RecoveryKey(account, id string) (map[string]string, error) {
	p, err := m.plan(account, id)
	if err != nil {
		return nil, err
	}
	pw, err := m.keys.Unseal(p.Password)
	if err != nil {
		return nil, err
	}
	repo, _, err := m.repo(p)
	if err != nil {
		return nil, err
	}
	return map[string]string{"plan": p.Name, "repository": repo, "password": pw}, nil
}

// ---- restic ----

func (m *Manager) repo(p *Plan) (string, []string, error) {
	sub := "ike/" + strings.ReplaceAll(m.hostname(), "/", "_") + "/" + p.ID
	owner := ""
	if t, err := m.targets.Get(p.Account, p.Target); err == nil && t.Account != "" {
		owner = p.Account
	}
	return m.targets.ResticEnv(owner, p.Target, sub)
}

func (m *Manager) hostname() string {
	if m.env.Cfg.Hostname != "" {
		return m.env.Cfg.Hostname
	}
	h, _ := os.Hostname()
	return h
}

// restic runs restic against a plan's repository.
func (m *Manager) restic(ctx context.Context, p *Plan, out *bytes.Buffer, args ...string) error {
	repo, env, err := m.repo(p)
	if err != nil {
		return err
	}
	pw, err := m.keys.Unseal(p.Password)
	if err != nil {
		return err
	}
	return m.resticRaw(ctx, repo, pw, env, out, args...)
}

func (m *Manager) resticRaw(ctx context.Context, repo, pw string, extra []string, out *bytes.Buffer, args ...string) error {
	env := append([]string{"RESTIC_REPOSITORY=" + repo, "RESTIC_PASSWORD=" + pw, "HOME=/root",
		"PATH=/usr/local/bin:/usr/bin:/bin", "RESTIC_CACHE_DIR=/var/cache/ike/restic"}, extra...)
	var sftp string
	for _, e := range extra {
		if v, ok := strings.CutPrefix(e, "IKE_SFTP_ARGS="); ok {
			sftp = v
		}
	}
	if sftp != "" {
		// restic starts ssh itself; pass the key and port through -o sftp.command
		repoHost := strings.TrimPrefix(repo, "sftp:")
		userHost, _, _ := strings.Cut(repoHost, ":")
		args = append([]string{"-o", "sftp.command=ssh " + sftp + " " + userHost + " -s sftp"}, args...)
	}
	c := runner.Command{Name: "restic", Args: args, Env: env, Timeout: 24 * time.Hour}
	if out != nil {
		c.Stdout = out
	}
	_, err := m.env.Run.Exec(ctx, c)
	return err
}

// ensureRepo creates the repository on first use.
func (m *Manager) ensureRepo(ctx context.Context, p *Plan) error {
	if err := m.restic(ctx, p, nil, "cat", "config"); err == nil {
		return nil
	}
	if err := m.restic(ctx, p, nil, "init"); err != nil && !strings.Contains(err.Error(), "already") {
		return fmt.Errorf("could not create the backup repository: %w", err)
	}
	return nil
}

// ---- running a backup ----

func (m *Manager) stagingDir(p *Plan) string {
	return m.env.P(filepath.Join("/var/backups/ike/staging", p.ID))
}

// paths returns what a plan backs up.
func (m *Manager) paths(p *Plan) []string {
	switch p.Scope {
	case "server":
		ps := []string{"/home", "/etc", "/var/lib/ike", "/var/spool/cron", "/root"}
		for _, extra := range []string{m.env.Cfg.MailDir, m.env.Cfg.DKIMDir, "/var/lib/powerdns", "/etc/letsencrypt", "/opt/ike"} {
			if extra != "" {
				ps = append(ps, extra)
			}
		}
		return ps
	case "account":
		ps := []string{m.env.Home(p.Account)}
		if m.src.MailDirs != nil {
			ps = append(ps, m.src.MailDirs(p.Account)...)
		}
		return ps
	case "site":
		dir, _, err := m.src.SiteDir(p.Site)
		if err == nil {
			return []string{dir}
		}
	case "mail":
		if m.src.MailDirs != nil {
			return m.src.MailDirs(p.Account)
		}
	}
	return nil
}

func (m *Manager) excludes(p *Plan) []string {
	ex := []string{"/var/backups/ike/snapshots", "/var/backups/ike/restore", "/var/backups/ike/recovery-*", "/var/backups/ike/databases", "/var/cache", "/home/*/tmp", "/home/*/.cache", "**/node_modules/.cache", "/var/lib/ike/deploy-tmp",
		"/var/lib/docker", "/var/lib/containerd", "/proc", "/sys", "/var/lib/mysql", "/var/lib/postgresql", "/var/lib/mongodb"}
	return append(ex, p.Excludes...)
}

func (m *Manager) dbList(p *Plan) []string {
	if len(p.Databases) > 0 {
		return p.Databases
	}
	if m.src.Databases == nil {
		return nil
	}
	switch p.Scope {
	case "server":
		return m.src.Databases("")
	case "account", "databases":
		return m.src.Databases(p.Account)
	}
	return nil
}

// Run makes one backup now. log receives progress lines.
func (m *Manager) Run(ctx context.Context, account, id string, log func(string, ...any)) (*RunResult, error) {
	p, err := m.plan(account, id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.running[p.ID] {
		m.mu.Unlock()
		return nil, fmt.Errorf("this backup is already running")
	}
	m.running[p.ID] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.running, p.ID); m.mu.Unlock() }()

	res := &RunResult{Status: "running", StartedAt: kit.Now()}
	m.setLast(p.ID, res, false)
	err = m.run(ctx, p, res, log)
	res.FinishedAt = kit.Now()
	if err != nil {
		res.Status, res.Error = "failed", err.Error()
		log("Backup failed: %v", err)
		m.env.Publish("backup.failed", p.Name, map[string]any{"plan": p.ID, "account": p.Account, "error": err.Error()})
	} else {
		res.Status = "ok"
		m.env.Publish("backup.completed", p.Name, map[string]any{"plan": p.ID, "account": p.Account, "snapshot": res.Snapshot, "added_bytes": res.AddedBytes})
	}
	m.setLast(p.ID, res, false)
	return res, err
}

func (m *Manager) setLast(id string, r *RunResult, check bool) {
	_, _ = m.store.Update(func(ps *[]Plan) error {
		for i := range *ps {
			if (*ps)[i].ID == id {
				c := *r
				if check {
					(*ps)[i].LastCheck = &c
				} else {
					(*ps)[i].Last = &c
				}
			}
		}
		return nil
	})
}

func (m *Manager) run(ctx context.Context, p *Plan, res *RunResult, log func(string, ...any)) error {
	log("Preparing the repository")
	if err := m.ensureRepo(ctx, p); err != nil {
		return err
	}
	paths := m.paths(p)
	stage := m.stagingDir(p)
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	dbs := m.dbList(p)
	if p.Scope != "mail" && p.Scope != "site" || len(p.Databases) > 0 {
		for _, d := range dbs {
			engine, name, _ := strings.Cut(d, ":")
			if !validate.SafeName(name) {
				continue
			}
			log("Dumping %s database %s", engine, name)
			f, err := os.OpenFile(filepath.Join(stage, engine+"-"+name+".dump.gz"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			err = m.src.Dump(ctx, engine, name, f)
			f.Close()
			if err != nil {
				return fmt.Errorf("dumping %s: %w", name, err)
			}
		}
		if p.Scope == "server" {
			// users and grants, so a new server keeps every app's password
			if f, err := os.OpenFile(filepath.Join(stage, "mariadb-users.sql"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
				_, derr := m.env.Run.Exec(ctx, runner.Command{Name: "mariadb-dump", Args: []string{"--system=users", "--insert-ignore"}, Stdout: f, Timeout: time.Hour})
				f.Close()
				if derr != nil {
					log("Couldn't save MariaDB users (is MariaDB installed?): %v", derr)
				}
			}
			if m.env.Cfg.Postgres {
				if f, err := os.OpenFile(filepath.Join(stage, "postgres-globals.sql"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
					_, _ = m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", "postgres", "--", "pg_dumpall", "--globals-only"}, Stdout: f, Timeout: time.Hour})
					f.Close()
				}
			}
		}
	}
	if len(paths) == 0 && len(dbs) == 0 {
		return fmt.Errorf("nothing to back up")
	}
	args := []string{"backup", "--json", "--tag", "ike", "--tag", "plan:" + p.ID, "--host", m.hostname()}
	for _, e := range m.excludes(p) {
		args = append(args, "--exclude", e)
	}
	var existing []string
	for _, pth := range paths {
		if _, err := os.Stat(m.env.P(pth)); err == nil || m.env.Cfg.DryRun {
			existing = append(existing, pth)
		}
	}
	args = append(args, existing...)
	args = append(args, strings.TrimPrefix(stage, strings.TrimSuffix(m.env.Cfg.Root, "/")))
	log("Backing up %s", strings.Join(existing, ", "))
	var out bytes.Buffer
	if err := m.restic(ctx, p, &out, args...); err != nil {
		// exit code 3: some files couldn't be read, but a snapshot was made
		if !strings.Contains(err.Error(), "exit status 3") {
			return err
		}
		log("Some files couldn't be read and were skipped")
	}
	parseSummary(out.Bytes(), res)
	log("Snapshot %s: %d MB new, %d MB in total", res.Snapshot, res.AddedBytes>>20, res.TotalBytes>>20)
	r := p.Retention
	fargs := []string{"forget", "--tag", "plan:" + p.ID, "--host", m.hostname(), "--prune"}
	for _, kv := range []struct {
		flag string
		n    int
	}{{"--keep-last", r.KeepLast}, {"--keep-hourly", r.KeepHourly}, {"--keep-daily", r.KeepDaily}, {"--keep-weekly", r.KeepWeekly}, {"--keep-monthly", r.KeepMonthly}} {
		if kv.n > 0 {
			fargs = append(fargs, kv.flag, strconv.Itoa(kv.n))
		}
	}
	log("Removing backups older than the retention policy")
	if err := m.restic(ctx, p, nil, fargs...); err != nil {
		log("Cleaning up old backups failed (the new backup is fine): %v", err)
	}
	return nil
}

func parseSummary(b []byte, res *RunResult) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var s struct {
			Type      string `json:"message_type"`
			Snapshot  string `json:"snapshot_id"`
			Added     int64  `json:"data_added"`
			Total     int64  `json:"total_bytes_processed"`
			FilesNew  int64  `json:"files_new"`
			FilesChg  int64  `json:"files_changed"`
			FilesSame int64  `json:"files_unmodified"`
		}
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.Type == "summary" {
			res.Snapshot, res.AddedBytes, res.TotalBytes = shortID(s.Snapshot), s.Added, s.Total
			res.Files = s.FilesNew + s.FilesChg + s.FilesSame
		}
	}
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// ---- snapshots, browsing, restore ----

// Snapshot is one backup in a repository.
type Snapshot struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Paths    []string  `json:"paths"`
	Host     string    `json:"host"`
	Tags     []string  `json:"tags"`
	SizeHint int64     `json:"size,omitempty"`
}

func (m *Manager) Snapshots(ctx context.Context, account, id string) ([]Snapshot, error) {
	p, err := m.plan(account, id)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := m.restic(ctx, p, &out, "snapshots", "--json", "--tag", "plan:"+p.ID); err != nil {
		return nil, err
	}
	var raw []struct {
		ShortID string    `json:"short_id"`
		Time    time.Time `json:"time"`
		Paths   []string  `json:"paths"`
		Host    string    `json:"hostname"`
		Tags    []string  `json:"tags"`
		Summary *struct {
			Total int64 `json:"total_bytes_processed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil && out.Len() > 0 {
		return nil, err
	}
	snaps := []Snapshot{}
	for _, r := range raw {
		s := Snapshot{ID: r.ShortID, Time: r.Time, Paths: r.Paths, Host: r.Host, Tags: r.Tags}
		if r.Summary != nil {
			s.SizeHint = r.Summary.Total
		}
		snaps = append(snaps, s)
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time.After(snaps[j].Time) })
	return snaps, nil
}

var snapIDRe = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// Node is one file or folder inside a snapshot.
type Node struct {
	Name  string    `json:"name"`
	Path  string    `json:"path"`
	Type  string    `json:"type"` // file | dir | symlink
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
}

// Browse lists one folder of a snapshot.
func (m *Manager) Browse(ctx context.Context, account, id, snap, dir string) ([]Node, error) {
	p, err := m.plan(account, id)
	if err != nil {
		return nil, err
	}
	if !snapIDRe.MatchString(snap) {
		return nil, fmt.Errorf("snapshot %s %w", snap, kit.ErrNotFound)
	}
	dir = "/" + strings.Trim(dir, "/")
	if err := m.allowed(p, dir); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := m.restic(ctx, p, &out, "ls", "--json", snap, dir); err != nil {
		return nil, err
	}
	nodes := []Node{}
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var n struct {
			Name  string    `json:"name"`
			Path  string    `json:"path"`
			Type  string    `json:"type"`
			Size  int64     `json:"size"`
			MTime time.Time `json:"mtime"`
			Kind  string    `json:"struct_type"`
		}
		if json.Unmarshal(sc.Bytes(), &n) != nil || n.Kind != "node" || n.Path == dir {
			continue
		}
		if filepath.Dir(n.Path) != dir {
			continue // only direct children
		}
		nodes = append(nodes, Node{Name: n.Name, Path: n.Path, Type: n.Type, Size: n.Size, MTime: n.MTime})
		if len(nodes) >= 5000 {
			break
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if (nodes[i].Type == "dir") != (nodes[j].Type == "dir") {
			return nodes[i].Type == "dir"
		}
		return nodes[i].Name < nodes[j].Name
	})
	return nodes, nil
}

// allowed keeps account plans inside the account's own paths.
func (m *Manager) allowed(p *Plan, path string) error {
	if p.Account == "" || path == "/" {
		return nil
	}
	clean := filepath.Clean(path)
	ok := []string{m.env.Home(p.Account), "/var/backups/ike/staging/" + p.ID}
	if m.src.MailDirs != nil {
		ok = append(ok, m.src.MailDirs(p.Account)...)
	}
	for _, o := range ok {
		if clean == o || strings.HasPrefix(clean, o+"/") || strings.HasPrefix(o, clean+"/") {
			return nil
		}
	}
	return fmt.Errorf("%s is outside the account", path)
}

// RestoreRequest says what to bring back.
type RestoreRequest struct {
	Snapshot string   `json:"snapshot"`
	Paths    []string `json:"paths"` // files or folders; empty = everything in the snapshot
	// Databases to import from the snapshot's dumps ("engine:name"); "*" = all.
	Databases []string `json:"databases"`
	// Overwrite replaces newer files; otherwise existing files are kept.
	Overwrite bool `json:"overwrite"`
}

// Restore brings files back to where they were and imports database dumps.
// Files are first restored into a staging folder, then copied into place as
// the account's user (account plans) or root (server plans).
func (m *Manager) Restore(ctx context.Context, account, id string, r RestoreRequest, log func(string, ...any)) error {
	p, err := m.plan(account, id)
	if err != nil {
		return err
	}
	if !snapIDRe.MatchString(r.Snapshot) {
		return fmt.Errorf("snapshot %s %w", r.Snapshot, kit.ErrNotFound)
	}
	if len(r.Paths) > 100 {
		return fmt.Errorf("restore at most 100 paths at a time")
	}
	for _, pth := range r.Paths {
		if !strings.HasPrefix(pth, "/") || strings.Contains(pth, "..") {
			return fmt.Errorf("paths are absolute, like /home/shop/web/shop.ng")
		}
		if err := m.allowed(p, pth); err != nil {
			return err
		}
	}
	tmp := m.env.P(filepath.Join("/var/backups/ike/restore", p.ID+"-"+kit.RandomHex(3)))
	if err := os.MkdirAll(tmp, 0o711); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	args := []string{"restore", r.Snapshot, "--target", tmp}
	includes := r.Paths
	if p.Account != "" && len(includes) == 0 {
		includes = m.paths(p)
	}
	for _, pth := range includes {
		args = append(args, "--include", pth)
	}
	stageRel := "/var/backups/ike/staging/" + p.ID
	wantDB := len(r.Databases) > 0
	if wantDB {
		args = append(args, "--include", stageRel)
	}
	log("Restoring from snapshot %s", r.Snapshot)
	if err := m.restic(ctx, p, nil, args...); err != nil {
		return err
	}
	// copy files into place
	copyRoots := includes
	if len(copyRoots) == 0 {
		copyRoots = m.paths(p)
	}
	for _, root := range copyRoots {
		src := filepath.Join(tmp, root)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		log("Putting back %s", root)
		if err := m.copyBack(ctx, p, src, root, r.Overwrite); err != nil {
			return err
		}
	}
	if wantDB {
		dir := filepath.Join(tmp, stageRel)
		ents, derr := os.ReadDir(dir)
		if derr != nil {
			log("No database dumps in this snapshot")
		}
		for _, e := range ents {
			name := strings.TrimSuffix(e.Name(), ".dump.gz")
			engine, db, ok := strings.Cut(name, "-")
			if !ok || !strings.HasSuffix(e.Name(), ".dump.gz") {
				continue
			}
			if !contains(r.Databases, "*") && !contains(r.Databases, engine+":"+db) {
				continue
			}
			acct := p.Account
			if acct == "" {
				acct, _, _ = strings.Cut(db, "_")
			}
			log("Importing %s database %s", engine, db)
			f, err := os.Open(filepath.Join(dir, e.Name()))
			if err != nil {
				return err
			}
			err = m.src.ImportDump(ctx, acct, engine, db, f)
			f.Close()
			if err != nil {
				return fmt.Errorf("importing %s: %w", db, err)
			}
		}
	}
	m.env.Publish("backup.restored", p.Name, map[string]any{"plan": p.ID, "snapshot": r.Snapshot, "account": p.Account})
	log("Restore finished")
	return nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// copyBack copies restored files to their original place. Account plans copy
// as the account's user so a planted link can't redirect the write.
func (m *Manager) copyBack(ctx context.Context, p *Plan, src, dst string, overwrite bool) error {
	args := []string{"-a"}
	if !overwrite {
		args = append(args, "--ignore-existing")
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if st.IsDir() {
		src, dst = src+"/", dst+"/"
	}
	if p.Account == "" {
		_, err = m.env.Run.Exec(ctx, runner.Command{Name: "rsync", Args: append(args, "--", src, dst), Timeout: 12 * time.Hour})
		return err
	}
	// mail folders belong to vmail, not the account
	usr := p.Account
	if strings.HasPrefix(dst, m.env.Cfg.MailDir+"/") {
		usr = "vmail"
	}
	_, err = m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: append([]string{"-u", usr, "--", "rsync"}, append(args, "--no-owner", "--no-group", "--", src, dst)...), Timeout: 12 * time.Hour})
	return err
}

// ---- integrity checks ----

// Check verifies the repository structure and reads back a sample of the data.
func (m *Manager) Check(ctx context.Context, account, id string, log func(string, ...any)) error {
	p, err := m.plan(account, id)
	if err != nil {
		return err
	}
	res := &RunResult{Status: "running", StartedAt: kit.Now()}
	log("Checking the repository and reading back 5%% of the stored data")
	var out bytes.Buffer
	err = m.restic(ctx, p, &out, "check", "--read-data-subset=5%")
	res.FinishedAt = kit.Now()
	if err != nil {
		res.Status, res.Error = "failed", err.Error()
		m.env.Publish("backup.check.failed", p.Name, map[string]any{"plan": p.ID, "error": err.Error()})
	} else {
		res.Status = "ok"
		log("No errors were found")
	}
	m.setLast(p.ID, res, true)
	return err
}

// ---- scheduler ----

func due(s Schedule, last *RunResult, now time.Time) bool {
	if s.Frequency == "manual" {
		return false
	}
	var lastAt time.Time
	if last != nil {
		lastAt = last.StartedAt
	}
	switch s.Frequency {
	case "hourly":
		return now.Minute() >= s.Minute && now.Sub(lastAt) > 50*time.Minute
	case "daily":
		return (now.Hour() > s.Hour || now.Hour() == s.Hour && now.Minute() >= s.Minute) && now.Sub(lastAt) > 20*time.Hour
	case "weekly":
		return int(now.Weekday()) == s.Weekday && (now.Hour() > s.Hour || now.Hour() == s.Hour && now.Minute() >= s.Minute) && now.Sub(lastAt) > 6*24*time.Hour
	}
	return false
}

// interval is how long a plan may go without a successful backup before
// Ike warns that backups have stopped.
func interval(s Schedule) time.Duration {
	switch s.Frequency {
	case "hourly":
		return 3 * time.Hour
	case "daily":
		return 50 * time.Hour
	case "weekly":
		return 9 * 24 * time.Hour
	}
	return 0
}

// RunScheduler starts due backups and checks, one at a time, and warns about
// plans that haven't succeeded for too long.
func (m *Manager) RunScheduler(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	warned := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			plans, _ := m.store.Load()
			for _, p := range plans {
				if !p.Enabled {
					continue
				}
				if due(p.Schedule, p.Last, now) {
					_, _ = m.Run(ctx, p.Account, p.ID, func(string, ...any) {})
					continue
				}
				if p.CheckDays > 0 && (p.LastCheck == nil || now.Sub(p.LastCheck.StartedAt) > time.Duration(p.CheckDays)*24*time.Hour) && p.Last != nil && p.Last.Status == "ok" {
					_ = m.Check(ctx, p.Account, p.ID, func(string, ...any) {})
				}
				if iv := interval(p.Schedule); iv > 0 && p.Last != nil && p.Last.Status != "running" {
					lastOK := p.Last.FinishedAt
					if p.Last.Status != "ok" {
						lastOK = time.Time{}
						if p.CreatedAt.After(lastOK) {
							lastOK = p.CreatedAt
						}
					}
					if now.Sub(lastOK) > iv && !warned[p.ID] {
						warned[p.ID] = true
						m.env.Publish("backup.missed.warning", p.Name, map[string]any{"plan": p.ID, "account": p.Account})
					} else if now.Sub(lastOK) <= iv {
						delete(warned, p.ID)
					}
				}
			}
		}
	}
}

// ---- off-site recovery ----

// RecoverRequest restores a server backup from another server's repository
// onto this (new) server.
type RecoverRequest struct {
	Target     string `json:"target"`     // storage target ID on this server
	Repository string `json:"repository"` // from the recovery key
	Password   string `json:"password"`
	Snapshot   string `json:"snapshot"` // "latest" or an ID
}

// Recover restores a full-server snapshot: configuration, accounts, websites,
// mail and databases, then rebuilds every managed config.
func (m *Manager) Recover(ctx context.Context, r RecoverRequest, log func(string, ...any)) error {
	if r.Snapshot == "" {
		r.Snapshot = "latest"
	}
	if r.Snapshot != "latest" && !snapIDRe.MatchString(r.Snapshot) {
		return fmt.Errorf("snapshot is latest or an ID")
	}
	if len(r.Password) < 12 {
		return fmt.Errorf("enter the recovery key's password")
	}
	_, env, err := m.targets.ResticEnv("", r.Target, "")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(r.Repository, "s3:") && !strings.HasPrefix(r.Repository, "sftp:") && !strings.HasPrefix(r.Repository, "/") {
		return fmt.Errorf("repository comes from the recovery key (s3:…, sftp:… or a folder)")
	}
	tmp := m.env.P("/var/backups/ike/recovery-" + time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	log("Downloading snapshot %s (this can take a long time)", r.Snapshot)
	if err := m.resticRaw(ctx, r.Repository, r.Password, env, nil, "restore", r.Snapshot, "--target", tmp); err != nil {
		return err
	}
	cp := func(rel string) {
		src := filepath.Join(tmp, rel)
		if _, err := os.Stat(src); err != nil {
			return
		}
		log("Restoring %s", rel)
		_, _ = m.env.Run.Exec(ctx, runner.Command{Name: "rsync", Args: []string{"-a", "--", src + "/", "/" + rel + "/"}, Timeout: 24 * time.Hour})
	}
	// Ike's own state first (accounts, sites, DNS, mail, plans), then data.
	for _, rel := range []string{"var/lib/ike/sites", "var/lib/ike/accounts", "var/lib/ike/dns", "var/lib/ike/mail", "var/lib/ike/data",
		"var/lib/ike/git", "var/lib/ike/deploys", "var/lib/ike/stacks", "etc/ike/ssl", "etc/ike/git", "etc/ike/mail", "etc/ike/redis", "etc/ike/pages",
		"etc/letsencrypt", "var/lib/powerdns", "var/lib/rspamd/dkim", "var/spool/cron"} {
		cp(rel)
	}
	if m.src.AfterServerRestore != nil {
		log("Recreating accounts")
		if err := m.src.AfterServerRestore(ctx, log); err != nil {
			return err
		}
	}
	cp("home")
	cp(strings.TrimPrefix(m.env.Cfg.MailDir, "/"))
	_, _ = m.env.Run.Run(ctx, "chown", "-R", "vmail:vmail", m.env.Cfg.MailDir)
	// databases: users and grants first, then each dump
	stage := ""
	_ = filepath.WalkDir(filepath.Join(tmp, "var/backups/ike/staging"), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && stage == "" && strings.Count(strings.TrimPrefix(p, tmp), "/") == 5 {
			stage = p
		}
		return nil
	})
	if stage != "" {
		if f, err := os.Open(filepath.Join(stage, "mariadb-users.sql")); err == nil {
			log("Restoring MariaDB users")
			_, err := m.env.Run.Exec(ctx, runner.Command{Name: "mariadb", Args: []string{"--batch"}, StdinReader: f, Timeout: time.Hour})
			f.Close()
			if err != nil {
				log("MariaDB users: %v", err)
			}
		}
		if f, err := os.Open(filepath.Join(stage, "postgres-globals.sql")); err == nil {
			_, _ = m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", "postgres", "--", "psql", "-X", "-q"}, StdinReader: f, Timeout: time.Hour})
			f.Close()
		}
		ents, _ := os.ReadDir(stage)
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".dump.gz") {
				continue
			}
			engine, db, _ := strings.Cut(strings.TrimSuffix(e.Name(), ".dump.gz"), "-")
			acct, _, _ := strings.Cut(db, "_")
			log("Importing %s database %s", engine, db)
			if err := m.recreateDB(ctx, engine, db); err != nil {
				log("Creating %s failed: %v", db, err)
				continue
			}
			f, err := os.Open(filepath.Join(stage, e.Name()))
			if err != nil {
				continue
			}
			if err := m.src.ImportDump(ctx, acct, engine, db, f); err != nil {
				log("Importing %s failed: %v", db, err)
			}
			f.Close()
		}
	}
	_ = os.RemoveAll(tmp)
	m.env.Publish("backup.recovered", m.hostname(), map[string]any{"snapshot": r.Snapshot})
	log("Recovery finished. Point your domains' DNS at this server's address if it changed.")
	return nil
}

func (m *Manager) recreateDB(ctx context.Context, engine, db string) error {
	if !validate.SafeName(db) {
		return errors.New("unsafe name")
	}
	switch engine {
	case "mariadb":
		_, err := m.env.Run.RunInput(ctx, fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", db), "mariadb", "--batch")
		return err
	case "postgres":
		_, err := m.env.Run.RunInput(ctx, fmt.Sprintf(`CREATE DATABASE "%s" OWNER "%s";`, db, db), "runuser", "-u", "postgres", "--", "psql", "-X", "-q")
		if err != nil && strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return err
	}
	return nil
}
