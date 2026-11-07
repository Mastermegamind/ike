// Package migrate restores cPanel backups (cpmove-<user>.tar.gz) into Ike:
// the account, its websites and files, databases, mailboxes with their mail
// and passwords, forwarders, cron jobs and DNS zones.
//
// Everything from the archive is written by the account's own Linux user
// (files), the vmail user (mail) or the database's own limited MariaDB user
// (SQL), never by root, so a crafted backup can't write outside the account.
package migrate

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/dns"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/mail"
	"github.com/Mastermegamind/ike/agent/internal/runner"
	"github.com/Mastermegamind/ike/agent/internal/sites"
	"github.com/Mastermegamind/ike/agent/internal/validate"
)

type Restorer struct {
	Env   *kit.Env
	Sites *sites.Manager
	Mail  *mail.Manager // nil when mail hosting isn't installed
	DNS   *dns.Manager  // nil when DNS isn't available
}

// RestoreRequest chooses what to bring over. Everything is on by default;
// Skip turns parts off: "files", "databases", "mail", "cron", "dns".
type RestoreRequest struct {
	File    string   `json:"file"`
	Account string   `json:"account"` // default: the plan's suggestion
	Skip    []string `json:"skip"`
	// PointToThisServer rewrites A records that pointed at the old server.
	PointToThisServer bool `json:"point_to_this_server"`
}

// Result is what was restored, including new database credentials (shown
// once, in the job's result).
type Result struct {
	Account   string        `json:"account"`
	Websites  []string      `json:"websites"`
	Aliases   []string      `json:"aliases"`
	Databases []RestoredDB  `json:"databases"`
	Mailboxes []string      `json:"mailboxes"`
	Forwards  int           `json:"forwarders"`
	CronJobs  int           `json:"cron_jobs"`
	Zones     []string      `json:"dns_zones"`
	FilesMB   float64       `json:"files_mb"`
	Warnings  []string      `json:"warnings"`
	Took      time.Duration `json:"took_ns"`
}

type RestoredDB struct {
	From     string `json:"from"`
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	Rewired  string `json:"rewired,omitempty"` // config file updated with the new credentials
}

// meta is the small text files the restore needs, read in one pass.
type meta struct {
	shadow    map[string]map[string]string // domain → user → hash
	quota     map[string]map[string]int64  // domain → user → bytes
	valiases  map[string]string            // domain → file
	cron      string
	zones     map[string]string // domain → zone file
	oldIP     string
	mailDirs  map[string]map[string]bool // domain → users with mail on disk
	databases map[string]bool
}

func (r *Restorer) open(file string) (*tar.Reader, func(), error) {
	p := r.Env.P(filepath.Join(r.Env.Cfg.ImportsDir, file))
	f, err := os.Open(p)
	if err != nil {
		return nil, nil, err
	}
	var rd io.Reader = bufio.NewReaderSize(f, 1<<20)
	closers := []io.Closer{f}
	if strings.HasSuffix(file, ".gz") {
		gz, err := gzip.NewReader(rd)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("not a gzip file: %w", err)
		}
		closers = append(closers, gz)
		rd = gz
	}
	return tar.NewReader(rd), func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i].Close()
		}
	}, nil
}

// relOf strips the archive's top folder (cpmove-user/ or user/).
func relOf(name string) (string, bool) {
	parts := strings.SplitN(strings.TrimPrefix(name, "./"), "/", 2)
	if len(parts) < 2 || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

var ipLineRe = regexp.MustCompile(`(?m)^ip:\s*([0-9.]+)\s*$`)

func (r *Restorer) readMeta(file string) (*meta, error) {
	tr, done, err := r.open(file)
	if err != nil {
		return nil, err
	}
	defer done()
	m := &meta{shadow: map[string]map[string]string{}, quota: map[string]map[string]int64{}, valiases: map[string]string{},
		zones: map[string]string{}, mailDirs: map[string]map[string]bool{}, databases: map[string]bool{}}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the archive is damaged: %w", err)
		}
		rel, ok := relOf(h.Name)
		if !ok {
			continue
		}
		small := h.Typeflag == tar.TypeReg && h.Size < 4<<20
		read := func() string {
			b, _ := io.ReadAll(io.LimitReader(tr, 4<<20))
			return string(b)
		}
		switch {
		case !small:
		case strings.HasPrefix(rel, "homedir/etc/") && strings.HasSuffix(rel, "/shadow"):
			d := strings.ToLower(path.Base(path.Dir(rel)))
			m.shadow[d] = map[string]string{}
			for _, l := range strings.Split(read(), "\n") {
				f := strings.Split(l, ":")
				if len(f) >= 2 && f[0] != "" {
					m.shadow[d][strings.ToLower(f[0])] = f[1]
				}
			}
		case strings.HasPrefix(rel, "homedir/etc/") && strings.HasSuffix(rel, "/quota"):
			d := strings.ToLower(path.Base(path.Dir(rel)))
			m.quota[d] = map[string]int64{}
			for _, l := range strings.Split(read(), "\n") {
				if u, q, ok := strings.Cut(l, ":"); ok {
					n, _ := strconv.ParseFloat(strings.TrimSpace(q), 64)
					m.quota[d][strings.ToLower(u)] = int64(n)
				}
			}
		case strings.HasPrefix(rel, "va/") && !strings.Contains(rel[3:], "/"):
			m.valiases[strings.ToLower(rel[3:])] = read()
		case strings.HasPrefix(rel, "cron/") && !strings.Contains(rel[5:], "/"):
			m.cron = read()
		case strings.HasPrefix(rel, "dnszones/") && strings.HasSuffix(rel, ".db"):
			m.zones[strings.ToLower(strings.TrimSuffix(rel[9:], ".db"))] = read()
		case rel == "userdata/main" || (strings.HasPrefix(rel, "userdata/") && m.oldIP == ""):
			if mm := ipLineRe.FindStringSubmatch(read()); mm != nil {
				m.oldIP = mm[1]
			}
		}
		if strings.HasPrefix(rel, "mysql/") && strings.HasSuffix(rel, ".sql") {
			m.databases[strings.TrimSuffix(path.Base(rel), ".sql")] = true
		}
		// homedir/mail/<domain>/<user>/cur/...
		if p := strings.Split(rel, "/"); len(p) >= 5 && p[0] == "homedir" && p[1] == "mail" && !strings.HasPrefix(p[2], ".") {
			d, u := strings.ToLower(p[2]), strings.ToLower(p[3])
			if m.mailDirs[d] == nil {
				m.mailDirs[d] = map[string]bool{}
			}
			m.mailDirs[d][u] = true
		}
	}
	return m, nil
}

func skips(l []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range l {
		out[s] = true
	}
	return out
}

// Restore runs the import. It is meant to run as a background job; log
// receives progress lines.
func (r *Restorer) Restore(ctx context.Context, req RestoreRequest, log func(string, ...any)) (*Result, error) {
	start := time.Now()
	plan, err := r.Sites.InspectCpanelBackup(req.File)
	if err != nil {
		return nil, err
	}
	skip := skips(req.Skip)
	acct := req.Account
	if acct == "" {
		acct = plan.Account
	}
	if err := validate.Username(acct); err != nil {
		return nil, err
	}
	if _, err := r.Sites.GetAccount(acct); err == nil {
		return nil, fmt.Errorf("account %s %w; choose another name for the import", acct, kit.ErrExists)
	}
	log("Reading %s (cPanel user %s)", req.File, plan.CpanelUser)
	md, err := r.readMeta(req.File)
	if err != nil {
		return nil, err
	}
	res := &Result{Account: acct, Warnings: append([]string{}, plan.Warnings...)}
	res.Warnings = filterWarnings(res.Warnings)

	log("Creating account %s", acct)
	if _, err := r.Sites.CreateAccount(ctx, acct); err != nil {
		return nil, err
	}

	// Websites: main, addon and subdomains become websites; parked domains
	// become aliases of the main website.
	docroots := map[string]string{} // cPanel docroot relative to home → Ike docroot relative to home
	var main *sites.Site
	var aliases []string
	oldHome := "/home/" + plan.CpanelUser
	for _, w := range plan.Websites {
		if w.Kind == "alias" {
			aliases = append(aliases, w.Domain)
			continue
		}
		cr := sites.CreateRequest{Domain: w.Domain, Account: acct}
		if w.PHP != "" && w.PHPOnHere {
			cr.Version = w.PHP
		}
		s, err := r.Sites.Create(ctx, cr)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Website %s was not created: %v", w.Domain, err))
			continue
		}
		log("Website %s created", w.Domain)
		res.Websites = append(res.Websites, w.Domain)
		if w.Kind == "main" {
			main = s
		}
		src := strings.TrimPrefix(strings.TrimPrefix(w.DocRoot, oldHome+"/"), "/")
		if src == "" || strings.HasPrefix(w.DocRoot, "/") && !strings.HasPrefix(w.DocRoot, oldHome+"/") {
			src = "public_html"
			if w.Kind != "main" {
				src = "public_html/" + w.Domain
			}
		}
		dst, _ := filepath.Rel(r.Env.Home(acct), s.DocRoot)
		docroots[path.Clean(src)] = dst
		// remove Ike's starter page so the site's own index wins
		_, _ = r.Env.AsUser(ctx, acct, "", nil, nil, "find", s.DocRoot, "-mindepth", "1", "-maxdepth", "1",
			"(", "-name", "index.html", "-o", "-name", "index.php", ")", "-delete")
	}
	if main != nil && len(aliases) > 0 {
		if _, err := r.Sites.SetDomains(ctx, main.Domain, sites.DomainsRequest{Aliases: aliases}); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Parked domains weren't added to %s: %v", main.Domain, err))
		} else {
			res.Aliases = aliases
		}
	}

	if !skip["files"] {
		log("Copying website files")
		mb, err := r.restoreFiles(ctx, req.File, acct, docroots, log)
		if err != nil {
			return res, fmt.Errorf("copying files: %w", err)
		}
		res.FilesMB = mb
	}
	if !skip["databases"] && len(md.databases) > 0 {
		r.restoreDatabases(ctx, req.File, plan.CpanelUser, acct, md, res, log)
	}
	if !skip["mail"] {
		r.restoreMail(ctx, req.File, acct, md, res, log)
	}
	if !skip["cron"] && md.cron != "" {
		r.restoreCron(acct, plan.CpanelUser, main, md.cron, res, log)
	}
	if !skip["dns"] && len(md.zones) > 0 {
		r.restoreZones(ctx, acct, md, req.PointToThisServer, res, log)
	}
	log("Fixing ownership and permissions")
	for _, d := range res.Websites {
		if _, err := r.Sites.FixPermissions(ctx, d); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Permissions on %s: %v", d, err))
		}
	}
	res.Took = time.Since(start)
	r.Env.Publish("migrate.cpanel.restored", acct, map[string]any{"account": acct, "file": req.File, "websites": len(res.Websites)})
	log("Done in %s", res.Took.Round(time.Second))
	return res, nil
}

func filterWarnings(w []string) []string {
	out := []string{}
	for _, x := range w {
		if !strings.Contains(x, "importing mail arrives") && !strings.Contains(x, "already exists") {
			out = append(out, x)
		}
	}
	return out
}

// Folders in a cPanel home that are cPanel's own and never copied.
var skipHome = []string{"mail", "etc", ".cpanel", ".cphorde", ".trash", "tmp", "logs", "access-logs", "ssl", ".htpasswds",
	"public_ftp", ".spamassassin", ".cagefs", ".softaculous", "cpbackup-exclude.conf", ".lastlogin", ".contactemail"}

// mapPath decides where a homedir path goes: into a website's document root
// (longest matching cPanel docroot wins, so addon domains inside
// public_html go to their own site), or into ~/cpanel-files.
func mapPath(rel string, docroots map[string]string) (string, bool) {
	top := strings.SplitN(rel, "/", 2)[0]
	for _, s := range skipHome {
		if top == s {
			return "", false
		}
	}
	best := ""
	for src := range docroots {
		if (rel == src || strings.HasPrefix(rel, src+"/")) && len(src) > len(best) {
			best = src
		}
	}
	if best != "" {
		return path.Join(docroots[best], strings.TrimPrefix(rel, best)), true
	}
	return path.Join("cpanel-files", rel), true
}

// safeName rejects paths that could leave the destination.
func safeName(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\x00") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// restoreFiles streams the homedir into the account through a tar process
// running as the account user.
func (r *Restorer) restoreFiles(ctx context.Context, file, acct string, docroots map[string]string, log func(string, ...any)) (float64, error) {
	home := r.Env.Home(acct)
	if _, err := r.Env.Run.Run(ctx, "install", "-d", "-o", acct, "-g", acct, "-m", "0750", filepath.Join(home, "cpanel-files")); err != nil {
		return 0, err
	}
	tr, done, err := r.open(file)
	if err != nil {
		return 0, err
	}
	defer done()
	pr, pw := io.Pipe()
	var bytes int64
	go func() {
		tw := tar.NewWriter(pw)
		var werr error
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				werr = err
				break
			}
			rel, ok := relOf(h.Name)
			if !ok || !strings.HasPrefix(rel, "homedir/") {
				continue
			}
			rel = path.Clean(strings.TrimPrefix(rel, "homedir/"))
			if rel == "." || !safeName(rel) {
				continue
			}
			dst, ok := mapPath(rel, docroots)
			if !ok || !safeName(dst) {
				continue
			}
			switch h.Typeflag {
			case tar.TypeReg, tar.TypeDir, tar.TypeSymlink:
			default:
				continue // no devices, FIFOs or hard links
			}
			nh := &tar.Header{Name: dst, Typeflag: h.Typeflag, Mode: h.Mode & 0o777, Size: h.Size, ModTime: h.ModTime, Linkname: h.Linkname}
			if h.Typeflag == tar.TypeDir {
				nh.Name += "/"
				nh.Mode |= 0o700
			}
			if h.Typeflag == tar.TypeReg {
				nh.Mode |= 0o600
			}
			if err := tw.WriteHeader(nh); err != nil {
				werr = err
				break
			}
			if h.Typeflag == tar.TypeReg {
				n, err := io.Copy(tw, tr)
				bytes += n
				if err != nil {
					werr = err
					break
				}
			}
		}
		if werr == nil {
			werr = tw.Close()
		}
		pw.CloseWithError(werr)
	}()
	_, err = r.Env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", acct, "--", "tar", "-x", "-f", "-", "-C", home,
		"--no-same-owner", "--no-same-permissions", "--delay-directory-restore"},
		Env: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}, StdinReader: pr, Timeout: 6 * time.Hour})
	_ = pr.Close()
	mb := float64(bytes) / (1 << 20)
	log("Copied %.1f MB of files", mb)
	return mb, err
}

var dbSafeRe = regexp.MustCompile(`[^a-z0-9]`)

// definerRe strips DEFINER clauses: they name the old server's users and
// need privileges a limited user doesn't have.
var definerRe = regexp.MustCompile("DEFINER\\s*=\\s*(`[^`]*`|'[^']*'|[^@\\s]+)@(`[^`]*`|'[^']*'|[^\\s*]+)")

type sqlFilter struct {
	br  *bufio.Reader
	buf []byte
}

func (f *sqlFilter) Read(p []byte) (int, error) {
	for len(f.buf) == 0 {
		line, err := f.br.ReadBytes('\n')
		if len(line) > 0 {
			if strings.Contains(string(line), "DEFINER") {
				line = definerRe.ReplaceAll(line, nil)
			}
			f.buf = line
		}
		if err != nil {
			if len(f.buf) == 0 {
				return 0, err
			}
			break
		}
	}
	n := copy(p, f.buf)
	f.buf = f.buf[n:]
	return n, nil
}

func (r *Restorer) restoreDatabases(ctx context.Context, file, cpuser, acct string, md *meta, res *Result, log func(string, ...any)) {
	created := map[string]*sites.NewDatabase{}
	used := map[string]bool{}
	var names []string
	for db := range md.databases {
		names = append(names, db)
	}
	sort.Strings(names)
	for _, db := range names {
		suffix := dbSafeRe.ReplaceAllString(strings.ToLower(strings.TrimPrefix(db, cpuser+"_")), "")
		if suffix == "" {
			suffix = "db"
		}
		if len(suffix) > 14 {
			suffix = suffix[:14]
		}
		base := suffix
		for i := 2; used[suffix]; i++ {
			suffix = base + strconv.Itoa(i)
		}
		used[suffix] = true
		nd, err := r.Sites.CreateDatabase(ctx, acct, suffix, "from cPanel "+dbSafeRe.ReplaceAllString(strings.ToLower(db), ""))
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Database %s was not created: %v", db, err))
			continue
		}
		created[db] = nd
		res.Databases = append(res.Databases, RestoredDB{From: db, Name: nd.Name, User: nd.User, Password: nd.Password})
		log("Database %s → %s", db, nd.Name)
	}
	tr, done, err := r.open(file)
	if err != nil {
		res.Warnings = append(res.Warnings, "Databases: "+err.Error())
		return
	}
	defer done()
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		rel, ok := relOf(h.Name)
		if !ok || !strings.HasPrefix(rel, "mysql/") || !strings.HasSuffix(rel, ".sql") || h.Typeflag != tar.TypeReg {
			continue
		}
		nd := created[strings.TrimSuffix(path.Base(rel), ".sql")]
		if nd == nil {
			continue
		}
		log("Importing %s (%.1f MB)", nd.Name, float64(h.Size)/(1<<20))
		_, err = r.Env.Run.Exec(ctx, runner.Command{Name: "mariadb", Args: []string{"--user=" + nd.User, "--host=localhost", "--batch",
			"--default-character-set=utf8mb4", nd.Name},
			Env: []string{"MYSQL_PWD=" + nd.Password, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}, StdinReader: &sqlFilter{br: bufio.NewReaderSize(tr, 1<<20)},
			Timeout: 6 * time.Hour})
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Importing %s stopped with an error: %v", nd.Name, shortErr(err)))
		}
	}
	// Point WordPress at the new database.
	for i, d := range res.Databases {
		for _, domain := range res.Websites {
			s, err := r.Sites.Get(domain)
			if err != nil {
				continue
			}
			cfgPath := filepath.Join(s.DocRoot, "wp-config.php")
			if r.rewireWordPress(ctx, acct, cfgPath, d) {
				res.Databases[i].Rewired = cfgPath
				log("%s now uses %s", cfgPath, d.Name)
			}
		}
	}
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 300 {
		s = s[len(s)-300:]
	}
	return s
}

var wpDefineRe = regexp.MustCompile(`define\(\s*['"](DB_NAME|DB_USER|DB_PASSWORD|DB_HOST)['"]\s*,\s*['"]([^'"]*)['"]\s*\)`)

// rewireWordPress updates wp-config.php when it names the old database.
// The file is read and written by the account user.
func (r *Restorer) rewireWordPress(ctx context.Context, acct, cfgPath string, d RestoredDB) bool {
	out, err := r.Env.AsUser(ctx, acct, "", nil, nil, "cat", cfgPath)
	if err != nil || !strings.Contains(string(out), "'"+d.From+"'") && !strings.Contains(string(out), `"`+d.From+`"`) {
		return false
	}
	text := wpDefineRe.ReplaceAllStringFunc(string(out), func(m string) string {
		k := wpDefineRe.FindStringSubmatch(m)[1]
		v := map[string]string{"DB_NAME": d.Name, "DB_USER": d.User, "DB_PASSWORD": d.Password, "DB_HOST": "localhost"}[k]
		return "define( '" + k + "', '" + v + "' )"
	})
	_, err = r.Env.AsUser(ctx, acct, "", strings.NewReader(text), io.Discard, "tee", cfgPath)
	return err == nil
}

func (r *Restorer) restoreMail(ctx context.Context, file, acct string, md *meta, res *Result, log func(string, ...any)) {
	if r.Mail == nil || !r.Mail.Installed() {
		if len(md.shadow) > 0 {
			res.Warnings = append(res.Warnings, "The backup has mailboxes, but email hosting isn't installed on this server; mail was not imported")
		}
		return
	}
	var domains []string
	for d := range md.shadow {
		domains = append(domains, d)
	}
	sort.Strings(domains)
	have := map[string]bool{}
	for _, d := range domains {
		if _, err := r.Mail.AddDomain(ctx, acct, d); err != nil && !errors.Is(err, kit.ErrExists) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Email for %s was not set up: %v", d, err))
			continue
		}
		var users []string
		for u := range md.shadow[d] {
			users = append(users, u)
		}
		sort.Strings(users)
		for _, u := range users {
			q := int(md.quota[d][u] / (1 << 20))
			if err := r.Mail.CreateMailboxHashed(ctx, acct, d, u, md.shadow[d][u], q); err != nil {
				res.Warnings = append(res.Warnings, err.Error())
				continue
			}
			have[u+"@"+d] = true
			res.Mailboxes = append(res.Mailboxes, u+"@"+d)
		}
		log("Email for %s: %d mailboxes (passwords kept)", d, len(users))
		if text, ok := md.valiases[d]; ok {
			n, err := r.restoreForwarders(ctx, acct, d, text)
			res.Forwards += n
			if err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("Forwarders for %s: %v", d, err))
			}
		}
	}
	// Messages: one archive pass, one vmail tar per mailbox.
	tr, done, err := r.open(file)
	if err != nil {
		res.Warnings = append(res.Warnings, "Mail: "+err.Error())
		return
	}
	defer done()
	var cur string
	var pw *io.PipeWriter
	var tw *tar.Writer
	var gz *gzip.Writer
	var wait chan error
	finish := func() {
		if pw == nil {
			return
		}
		_ = tw.Close()
		_ = gz.Close()
		_ = pw.Close()
		if err := <-wait; err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Messages for %s: %v", cur, shortErr(err)))
		}
		pw = nil
	}
	count := 0
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		rel, ok := relOf(h.Name)
		p := strings.Split(rel, "/")
		if !ok || len(p) < 5 || p[0] != "homedir" || p[1] != "mail" {
			continue
		}
		d, u := strings.ToLower(p[2]), strings.ToLower(p[3])
		addr := u + "@" + d
		if !have[addr] {
			continue
		}
		inner := strings.Join(p[4:], "/")
		if !safeName(inner) || (h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir) {
			continue
		}
		if addr != cur {
			finish()
			cur = addr
			var pr *io.PipeReader
			pr, pw = io.Pipe()
			gz = gzip.NewWriter(pw)
			tw = tar.NewWriter(gz)
			wait = make(chan error, 1)
			go func(d, u string, pr *io.PipeReader) {
				err := r.Mail.ImportMailbox(ctx, acct, d, u, pr)
				_ = pr.CloseWithError(err)
				wait <- err
			}(d, u, pr)
		}
		nh := &tar.Header{Name: "Maildir/" + inner, Typeflag: h.Typeflag, Mode: 0o600, Size: h.Size, ModTime: h.ModTime}
		if h.Typeflag == tar.TypeDir {
			nh.Name += "/"
			nh.Mode = 0o700
		}
		if tw.WriteHeader(nh) != nil {
			continue
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = io.Copy(tw, tr)
			count++
		}
	}
	finish()
	if count > 0 {
		log("Imported %d email messages", count)
	}
}

// restoreForwarders reads a cPanel valiases file ("from@domain: to, to").
func (r *Restorer) restoreForwarders(ctx context.Context, acct, domain, text string) (int, error) {
	var aliases []mail.Alias
	for _, l := range strings.Split(text, "\n") {
		from, to, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		from = strings.ToLower(strings.TrimSpace(from))
		if from == "*" || !strings.HasSuffix(from, "@"+domain) {
			continue // catch-alls and other domains
		}
		var dest []string
		for _, t := range strings.Split(to, ",") {
			t = strings.TrimSpace(t)
			if strings.Contains(t, "@") && !strings.ContainsAny(t, "|:\"") {
				dest = append(dest, strings.ToLower(t))
			}
		}
		if len(dest) > 0 {
			aliases = append(aliases, mail.Alias{From: strings.TrimSuffix(from, "@"+domain), To: dest})
		}
	}
	if len(aliases) == 0 {
		return 0, nil
	}
	_, err := r.Mail.SetAliases(ctx, acct, domain, aliases)
	if err != nil {
		return 0, err
	}
	return len(aliases), nil
}

var envLineRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*=`)

func (r *Restorer) restoreCron(acct, cpuser string, main *sites.Site, text string, res *Result, log func(string, ...any)) {
	oldHome := "/home/" + cpuser
	newFiles := filepath.Join(r.Env.Home(acct), "cpanel-files")
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || envLineRe.MatchString(l) {
			continue
		}
		var sched, cmd string
		if strings.HasPrefix(l, "@") {
			sched, cmd, _ = strings.Cut(l, " ")
		} else {
			// five schedule fields, then the command with its own spacing
			rest := l
			var f []string
			for len(f) < 5 && rest != "" {
				rest = strings.TrimLeft(rest, " \t")
				i := strings.IndexAny(rest, " \t")
				if i < 0 {
					rest = ""
					break
				}
				f, rest = append(f, rest[:i]), rest[i:]
			}
			cmd = strings.TrimSpace(rest)
			if len(f) < 5 || cmd == "" {
				continue
			}
			sched = strings.Join(f, " ")
		}
		pairs := []string{}
		if main != nil {
			pairs = append(pairs, oldHome+"/public_html", main.DocRoot)
		}
		pairs = append(pairs, oldHome+"/", newFiles+"/")
		cmd = strings.NewReplacer(pairs...).Replace(cmd)
		if _, err := r.Sites.AddCronJob(acct, sites.CronRequest{Schedule: sched, Command: cmd, Comment: "from cPanel"}); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Cron job %q was not added: %v", l, err))
			continue
		}
		res.CronJobs++
	}
	log("Added %d cron jobs", res.CronJobs)
}

func (r *Restorer) restoreZones(ctx context.Context, acct string, md *meta, point bool, res *Result, log func(string, ...any)) {
	if r.DNS == nil {
		res.Warnings = append(res.Warnings, "DNS zones were not imported: no DNS server or provider is set up")
		return
	}
	newIP := r.Env.Cfg.PublicIPv4
	var names []string
	for z := range md.zones {
		names = append(names, z)
	}
	sort.Strings(names)
	for _, z := range names {
		if validate.Domain(z) != nil {
			continue
		}
		text := md.zones[z]
		if point && md.oldIP != "" && newIP != "" {
			text = regexp.MustCompile(`\b`+regexp.QuoteMeta(md.oldIP)+`\b`).ReplaceAllString(text, newIP)
		}
		if _, err := r.DNS.Create(ctx, acct, dns.CreateZone{Name: z}); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("DNS zone %s: %v", z, err))
			continue
		}
		_, skipped, err := r.DNS.Import(ctx, acct, z, text, true)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("DNS zone %s records: %v", z, err))
			continue
		}
		for _, s := range skipped {
			res.Warnings = append(res.Warnings, fmt.Sprintf("DNS zone %s: skipped %s", z, s))
		}
		res.Zones = append(res.Zones, z)
		log("DNS zone %s imported", z)
	}
}
