package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/panel/auth"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

// ---------------------------------------------------------------- API tokens

const tokenPrefix = "ike_"

// bearerUser signs a request in with an API token: Authorization: Bearer ike_…
func (s *Server) bearerUser(r *http.Request) (user string, tok *store.APIToken) {
	h := r.Header.Get("Authorization")
	t, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || !strings.HasPrefix(t, tokenPrefix) {
		return "", nil
	}
	hash := auth.HashToken(strings.TrimSpace(t))
	_ = s.Store.Update(func(d *store.Data) error {
		for _, x := range d.APITokens {
			if x.Hash == hash && (x.Expires.IsZero() || time.Now().Before(x.Expires)) {
				c := *x
				user, tok = x.Username, &c
				if time.Since(x.LastUsed) > time.Minute {
					x.LastUsed = time.Now().UTC()
					return nil
				}
				return errNoSave
			}
		}
		return errNoSave
	})
	return user, tok
}

var errNoSave = errors.New("nothing to save")

var tokenNameRe = regexp.MustCompile(`^[\p{L}\p{N} ._()-]{1,60}$`)

type tokenView struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	ReadOnly bool              `json:"read_only"`
	Created  time.Time         `json:"created"`
	LastUsed time.Time         `json:"last_used,omitzero"`
	Expires  time.Time         `json:"expires,omitzero"`
	Limited  bool              `json:"limited"`
	Scopes   map[string]string `json:"scopes,omitempty"`
	Accounts []string          `json:"accounts,omitempty"`
	AllowIPs []string          `json:"allow_ips,omitempty"`
}

func viewOf(t *store.APIToken) tokenView {
	return tokenView{t.ID, t.Name, t.ReadOnly, t.Created, t.LastUsed, t.Expires, t.Limited, t.Scopes, t.Accounts, t.AllowIPs}
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	out := []tokenView{}
	s.Store.View(func(d *store.Data) {
		for _, t := range d.APITokens {
			if t.Username == me.Username {
				out = append(out, viewOf(t))
			}
		}
	})
	writeJSON(w, 200, out)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	var req struct {
		Name     string            `json:"name"`
		ReadOnly bool              `json:"read_only"`
		Days     int               `json:"days"` // 0 = never expires
		Limited  bool              `json:"limited"`
		Scopes   map[string]string `json:"scopes"`
		Accounts []string          `json:"accounts"`
		AllowIPs []string          `json:"allow_ips"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !tokenNameRe.MatchString(req.Name) || req.Days < 0 || req.Days > 3650 {
		fail(w, 400, "Give the token a short name; it can last up to 3650 days.")
		return
	}
	if me.Token != nil {
		fail(w, 403, "Create API tokens from the panel, not with another token.")
		return
	}
	if req.Scopes == nil {
		req.Scopes = map[string]string{}
	}
	if err := validScopes(req.Scopes, req.AllowIPs); err != nil {
		fail(w, 400, err.Error())
		return
	}
	acc := s.accessFor(me)
	for _, a := range req.Accounts {
		if !acc.has(a) {
			fail(w, 403, "Account "+a+" isn't one you manage.")
			return
		}
	}
	tok := tokenPrefix + auth.RandomToken(32)
	t := &store.APIToken{ID: kit.RandomHex(5), Hash: auth.HashToken(tok), Name: req.Name, Username: me.Username, ReadOnly: req.ReadOnly, Created: time.Now().UTC(),
		Limited: req.Limited, Accounts: req.Accounts, AllowIPs: req.AllowIPs}
	if req.Limited {
		t.Scopes = req.Scopes
	}
	if req.Days > 0 {
		t.Expires = time.Now().AddDate(0, 0, req.Days).UTC()
	}
	err := s.Store.Update(func(d *store.Data) error {
		n := 0
		for _, x := range d.APITokens {
			if x.Username == me.Username {
				n++
			}
		}
		if n >= 50 {
			return errors.New("at most 50 API tokens per person")
		}
		d.APITokens = append(d.APITokens, t)
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"token": tok, "id": t.ID, "name": t.Name, "read_only": t.ReadOnly, "expires": t.Expires, "limited": t.Limited,
		"scopes": t.Scopes, "accounts": t.Accounts, "allow_ips": t.AllowIPs})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	id := r.PathValue("id")
	err := s.Store.Update(func(d *store.Data) error {
		for i, t := range d.APITokens {
			if t.ID == id && (t.Username == me.Username || me.Role == store.Admin) {
				d.APITokens = append(d.APITokens[:i], d.APITokens[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("token %w", store.ErrNotFound)
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"deleted": id})
}

// ---------------------------------------------------------------- packages

var pkgNameRe = regexp.MustCompile(`^[\p{L}\p{N} ._()+-]{1,60}$`)

func canSeePackage(me *store.Person, p *store.Package) bool {
	return me.Role == store.Admin || p.Owner == me.Username || (me.Role == store.Reseller && p.Owner == "")
}

func (s *Server) listPackages(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	out := []store.Package{}
	usage := map[string]int{}
	s.Store.View(func(d *store.Data) {
		for _, p := range d.Packages {
			if canSeePackage(me, p) {
				out = append(out, *p)
			}
		}
		for _, id := range d.AccountPackage {
			usage[id]++
		}
	})
	type view struct {
		store.Package
		Accounts int `json:"accounts"`
	}
	res := make([]view, 0, len(out))
	for _, p := range out {
		res = append(res, view{p, usage[p.ID]})
	}
	writeJSON(w, 200, res)
}

func checkPackage(p *store.Package) error {
	if !pkgNameRe.MatchString(p.Name) {
		return errors.New("give the package a short name")
	}
	if p.Currency == "" {
		p.Currency = "NGN"
	}
	if !currencyRe.MatchString(p.Currency) {
		return errors.New("currency is a 3-letter code like NGN or USD")
	}
	switch p.Interval {
	case "", "month":
		p.Interval = "month"
	case "year", "none":
	default:
		return errors.New("billing interval is month, year or none")
	}
	if p.Price < 0 || p.Price > 1e12 {
		return errors.New("price is out of range")
	}
	l := p.Limits
	for _, v := range []int{l.Sites, l.Domains, l.Databases, l.EmailDomains, l.Mailboxes, l.DiskMB, l.BandwidthGB, l.CPUPercent, l.MemoryMB, l.Processes, l.PHPWorkers} {
		if v < 0 || v > 10000000 {
			return errors.New("limits must be 0 (unlimited) or a positive number")
		}
	}
	return nil
}

func (s *Server) savePackage(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	var p store.Package
	if !decodeBody(w, r, &p) {
		return
	}
	if err := checkPackage(&p); err != nil {
		fail(w, 400, err.Error())
		return
	}
	id := r.PathValue("id")
	var out store.Package
	err := s.Store.Update(func(d *store.Data) error {
		if id == "" {
			p.ID, p.Created = kit.RandomHex(5), time.Now().UTC()
			if me.Role == store.Reseller {
				p.Owner = me.Username
			} else {
				p.Owner = ""
			}
			if len(d.Packages) >= 500 {
				return errors.New("at most 500 packages")
			}
			d.Packages = append(d.Packages, &p)
			out = p
			return nil
		}
		for _, x := range d.Packages {
			if x.ID == id {
				if me.Role != store.Admin && x.Owner != me.Username {
					return errForbidden
				}
				p.ID, p.Owner, p.Created = x.ID, x.Owner, x.Created
				*x = p
				out = p
				return nil
			}
		}
		return fmt.Errorf("package %w", store.ErrNotFound)
	})
	if err != nil {
		failErr(w, err)
		return
	}
	// re-apply resource limits to every account on the package
	if id != "" {
		var accts []string
		s.Store.View(func(d *store.Data) {
			for a, pid := range d.AccountPackage {
				if pid == id {
					accts = append(accts, a)
				}
			}
		})
		for _, a := range accts {
			_ = s.applyPackageLimits(r.Context(), me, a, &out)
		}
	}
	writeJSON(w, map[bool]int{true: 201, false: 200}[id == ""], out)
}

func (s *Server) deletePackage(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	id := r.PathValue("id")
	err := s.Store.Update(func(d *store.Data) error {
		for _, pid := range d.AccountPackage {
			if pid == id {
				return errors.New("accounts still use this package; move them to another one first")
			}
		}
		for i, x := range d.Packages {
			if x.ID == id {
				if me.Role != store.Admin && x.Owner != me.Username {
					return errForbidden
				}
				d.Packages = append(d.Packages[:i], d.Packages[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("package %w", store.ErrNotFound)
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"deleted": id})
}

// applyPackageLimits sends a package's resource limits to the agent.
func (s *Server) applyPackageLimits(ctx context.Context, me *store.Person, account string, p *store.Package) error {
	l := p.Limits
	body := map[string]int{"cpu_percent": l.CPUPercent, "memory_mb": l.MemoryMB, "max_processes": l.Processes, "disk_mb": l.DiskMB, "php_workers": l.PHPWorkers}
	return s.agentCall(ctx, s.Agent, me, "PUT", "/v1/accounts/"+account+"/limits", body, nil)
}

// setAccountPackage assigns a package to an account.
func (s *Server) setAccountPackage(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	acct := r.PathValue("name")
	var req struct {
		Package string `json:"package"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.accessFor(me).has(acct) || me.Role.Customer() {
		failErr(w, errForbidden)
		return
	}
	pkg, err := s.assignPackage(r.Context(), me, acct, req.Package)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"account": acct, "package": pkg})
}

func (s *Server) assignPackage(ctx context.Context, me *store.Person, acct, pkgID string) (*store.Package, error) {
	var pkg *store.Package
	s.Store.View(func(d *store.Data) {
		for _, p := range d.Packages {
			if p.ID == pkgID && canSeePackage(me, p) {
				c := *p
				pkg = &c
			}
		}
	})
	if pkgID == "" {
		_ = s.Store.Update(func(d *store.Data) error { delete(d.AccountPackage, acct); return nil })
		return nil, nil
	}
	if pkg == nil {
		return nil, fmt.Errorf("package %s %w", pkgID, store.ErrNotFound)
	}
	if err := s.checkResellerAllocation(acct, pkg); err != nil {
		return nil, err
	}
	if err := s.applyPackageLimits(ctx, me, acct, pkg); err != nil {
		return nil, err
	}
	_ = s.Store.Update(func(d *store.Data) error { d.AccountPackage[acct] = pkg.ID; return nil })
	return pkg, nil
}

func (s *Server) listAccountPackages(w http.ResponseWriter, r *http.Request) {
	acc := s.accessFor(personFrom(r))
	out := map[string]string{}
	s.Store.View(func(d *store.Data) {
		for a, id := range d.AccountPackage {
			if acc.has(a) {
				out[a] = id
			}
		}
	})
	writeJSON(w, 200, out)
}

func (s *Server) packageOf(account string) *store.Package {
	var pkg *store.Package
	s.Store.View(func(d *store.Data) {
		id := d.AccountPackage[account]
		for _, p := range d.Packages {
			if p.ID == id {
				c := *p
				pkg = &c
			}
		}
	})
	return pkg
}

// ---------------------------------------------------------------- package enforcement

var (
	reSitesCreate   = regexp.MustCompile(`^/v1/sites$`)
	reDomains       = regexp.MustCompile(`^/v1/sites/([^/]+)/domains$`)
	reMariaDB       = regexp.MustCompile(`^/v1/accounts/([^/]+)/databases$`)
	reDBs           = regexp.MustCompile(`^/v1/accounts/([^/]+)/dbs$`)
	reMailDomains   = regexp.MustCompile(`^/v1/accounts/([^/]+)/mail/domains$`)
	reMailboxes     = regexp.MustCompile(`^/v1/accounts/([^/]+)/mail/domains/([^/]+)/mailboxes$`)
	reZones         = regexp.MustCompile(`^/v1/accounts/([^/]+)/dns/zones$`)
	reSSH           = regexp.MustCompile(`^/v1/accounts/([^/]+)/ssh$`)
	reContainers    = regexp.MustCompile(`^/v1/accounts/([^/]+)/(containers|stacks)`)
	reTerminal      = regexp.MustCompile(`^/v1/accounts/([^/]+)/terminal$`)
	reBackupPlans   = regexp.MustCompile(`^/v1/accounts/([^/]+)/backups/plans$`)
	reAccountCreate = regexp.MustCompile(`^/v1/accounts$`)
)

// deniedErr is a 403 whose message stands on its own.
type deniedErr struct{ msg string }

func (e deniedErr) Error() string        { return e.msg }
func (e deniedErr) Is(target error) bool { return target == errForbidden }

func denied(format string, args ...any) error { return deniedErr{fmt.Sprintf(format, args...)} }

func limitErr(one, many string, n int) error {
	what := many
	if n == 1 {
		what = one
	}
	return denied("your hosting package allows %d %s; upgrade it for more", n, what)
}

// checkPackageLimits enforces the account's hosting package (for everyone
// but server admins) and resellers' totals before a request reaches the agent.
func (s *Server) checkPackageLimits(ctx context.Context, p *store.Person, method, apiPath string, body []byte) error {
	if p.Role == store.Admin {
		return nil
	}
	count := func(path string) int {
		var list []json.RawMessage
		if s.Agent.Get(ctx, path, &list) != nil {
			return 0
		}
		return len(list)
	}
	switch {
	case method == "POST" && reSitesCreate.MatchString(apiPath):
		var req struct {
			Account string `json:"account"`
		}
		_ = json.Unmarshal(body, &req)
		if pkg := s.packageOf(req.Account); pkg != nil && pkg.Limits.Sites > 0 {
			if n := count("/v1/sites?account=" + req.Account); n >= pkg.Limits.Sites {
				return limitErr("website", "websites", pkg.Limits.Sites)
			}
		}
		return s.checkResellerSites(ctx, p, req.Account)
	case method == "PUT" && reDomains.MatchString(apiPath):
		domain := reDomains.FindStringSubmatch(apiPath)[1]
		acct, _ := s.siteAccount(ctx, domain)
		var req struct {
			Aliases []string `json:"aliases"`
		}
		_ = json.Unmarshal(body, &req)
		if pkg := s.packageOf(acct); pkg != nil && pkg.Limits.Domains > 0 && len(req.Aliases) > pkg.Limits.Domains {
			return limitErr("extra domain per website", "extra domains per website", pkg.Limits.Domains)
		}
		return s.checkResellerDomains(ctx, acct, domain, len(req.Aliases))
	case method == "POST" && reMariaDB.MatchString(apiPath):
		acct := reMariaDB.FindStringSubmatch(apiPath)[1]
		if pkg := s.packageOf(acct); pkg != nil && pkg.Limits.Databases > 0 {
			var dbs struct {
				MariaDB   []string          `json:"mariadb"`
				Databases []json.RawMessage `json:"databases"`
			}
			_ = s.Agent.Get(ctx, "/v1/accounts/"+acct+"/dbs", &dbs)
			if len(dbs.MariaDB)+len(dbs.Databases) >= pkg.Limits.Databases {
				return limitErr("database", "databases", pkg.Limits.Databases)
			}
		}
	case method == "POST" && reDBs.MatchString(apiPath):
		acct := reDBs.FindStringSubmatch(apiPath)[1]
		if pkg := s.packageOf(acct); pkg != nil && pkg.Limits.Databases > 0 {
			var dbs struct {
				MariaDB   []string          `json:"mariadb"`
				Databases []json.RawMessage `json:"databases"`
			}
			_ = s.Agent.Get(ctx, "/v1/accounts/"+acct+"/dbs", &dbs)
			if len(dbs.MariaDB)+len(dbs.Databases) >= pkg.Limits.Databases {
				return limitErr("database", "databases", pkg.Limits.Databases)
			}
		}
	case method == "POST" && reMailDomains.MatchString(apiPath):
		acct := reMailDomains.FindStringSubmatch(apiPath)[1]
		if pkg := s.packageOf(acct); pkg != nil && pkg.Limits.EmailDomains > 0 && count("/v1/accounts/"+acct+"/mail/domains") >= pkg.Limits.EmailDomains {
			return limitErr("email domain", "email domains", pkg.Limits.EmailDomains)
		}
	case method == "POST" && reMailboxes.MatchString(apiPath):
		acct := reMailboxes.FindStringSubmatch(apiPath)[1]
		if pkg := s.packageOf(acct); pkg != nil && pkg.Limits.Mailboxes > 0 {
			var doms []struct {
				Mailboxes []json.RawMessage `json:"mailboxes"`
			}
			_ = s.Agent.Get(ctx, "/v1/accounts/"+acct+"/mail/domains", &doms)
			n := 0
			for _, d := range doms {
				n += len(d.Mailboxes)
			}
			if n >= pkg.Limits.Mailboxes {
				return limitErr("mailbox", "mailboxes", pkg.Limits.Mailboxes)
			}
		}
	case method == "POST" && reZones.MatchString(apiPath):
		acct := reZones.FindStringSubmatch(apiPath)[1]
		if pkg := s.packageOf(acct); pkg != nil && pkg.Limits.Domains > 0 && count("/v1/accounts/"+acct+"/dns/zones") >= pkg.Limits.Domains {
			return limitErr("DNS zone", "DNS zones", pkg.Limits.Domains)
		}
		return s.checkResellerDomains(ctx, acct, "", 0)
	case method == "PUT" && reSSH.MatchString(apiPath):
		acct := reSSH.FindStringSubmatch(apiPath)[1]
		if pkg := s.packageOf(acct); pkg != nil && !pkg.Limits.Shell && strings.Contains(string(body), `"shell"`) {
			return denied("your hosting package doesn't include shell access")
		}
	case reTerminal.MatchString(apiPath):
		acct := reTerminal.FindStringSubmatch(apiPath)[1]
		if pkg := s.packageOf(acct); pkg != nil && !pkg.Limits.Shell {
			return denied("your hosting package doesn't include the terminal")
		}
	case method != "GET" && reContainers.MatchString(apiPath):
		acct := reContainers.FindStringSubmatch(apiPath)[1]
		if pkg := s.packageOf(acct); pkg != nil && !pkg.Limits.Containers {
			return denied("your hosting package doesn't include containers")
		}
	case method == "POST" && reBackupPlans.MatchString(apiPath):
		acct := reBackupPlans.FindStringSubmatch(apiPath)[1]
		if pkg := s.packageOf(acct); pkg != nil && !pkg.Limits.Backups {
			return denied("your hosting package doesn't include your own backup plans")
		}
	case method == "POST" && reAccountCreate.MatchString(apiPath) && p.Role == store.Reseller:
		return nil // MaxAccounts is checked in authorize
	}
	return nil
}

// checkResellerSites enforces a reseller's total website quota.
func (s *Server) checkResellerSites(ctx context.Context, p *store.Person, account string) error {
	reseller := ""
	limit := 0
	s.Store.View(func(d *store.Data) {
		reseller = d.AccountOf[account]
		if r, err := d.Person(reseller); err == nil {
			limit = r.MaxSites
		}
	})
	if reseller == "" || limit == 0 {
		return nil
	}
	var sites []struct {
		Account string `json:"account"`
	}
	if s.Agent.Get(ctx, "/v1/sites", &sites) != nil {
		return nil
	}
	n := 0
	s.Store.View(func(d *store.Data) {
		for _, st := range sites {
			if d.AccountOf[st.Account] == reseller {
				n++
			}
		}
	})
	if n >= limit {
		return denied("the reseller plan allows %d websites in total", limit)
	}
	return nil
}

// resellerOf returns the reseller who owns an account and that reseller.
func (s *Server) resellerOf(account string) *store.Person {
	var out *store.Person
	s.Store.View(func(d *store.Data) {
		if r, err := d.Person(d.AccountOf[account]); err == nil && r.Role == store.Reseller {
			c := *r
			out = &c
		}
	})
	return out
}

// checkResellerDomains enforces a reseller's total of domains: every
// website's extra domains plus every DNS zone across their accounts. site and
// newAliases describe a pending domains change (its old aliases don't count);
// an empty site means one more DNS zone.
func (s *Server) checkResellerDomains(ctx context.Context, account, site string, newAliases int) error {
	r := s.resellerOf(account)
	if r == nil || r.MaxDomains == 0 {
		return nil
	}
	var mine map[string]bool
	s.Store.View(func(d *store.Data) {
		mine = map[string]bool{}
		for a, owner := range d.AccountOf {
			if owner == r.Username {
				mine[a] = true
			}
		}
	})
	var sites []struct {
		Domain  string   `json:"domain"`
		Account string   `json:"account"`
		Aliases []string `json:"aliases"`
	}
	_ = s.Agent.Get(ctx, "/v1/sites", &sites)
	n := newAliases
	if site == "" {
		n = 1
	}
	for _, st := range sites {
		if mine[st.Account] && st.Domain != site {
			n += len(st.Aliases)
		}
	}
	for a := range mine {
		var zones []json.RawMessage
		if s.Agent.Get(ctx, "/v1/accounts/"+a+"/dns/zones", &zones) == nil {
			n += len(zones)
		}
	}
	if n > r.MaxDomains {
		return denied("the reseller plan allows %d domains in total (extra website domains and DNS zones)", r.MaxDomains)
	}
	return nil
}

// checkResellerAllocation stops a reseller from handing out more disk or
// bandwidth in packages than their own plan has.
func (s *Server) checkResellerAllocation(account string, pkg *store.Package) error {
	r := s.resellerOf(account)
	if r == nil || (r.MaxDiskMB == 0 && r.MaxBandwidthGB == 0) {
		return nil
	}
	disk, bw := pkg.Limits.DiskMB, pkg.Limits.BandwidthGB
	if (r.MaxDiskMB > 0 && disk == 0) || (r.MaxBandwidthGB > 0 && bw == 0) {
		return denied("your reseller plan has a disk and bandwidth total, so packages need their own disk and bandwidth limits")
	}
	s.Store.View(func(d *store.Data) {
		for a, owner := range d.AccountOf {
			if owner != r.Username || a == account {
				continue
			}
			for _, p := range d.Packages {
				if p.ID == d.AccountPackage[a] {
					disk += p.Limits.DiskMB
					bw += p.Limits.BandwidthGB
				}
			}
		}
	})
	if r.MaxDiskMB > 0 && disk > r.MaxDiskMB {
		return denied("this would hand out %d MB of disk; your reseller plan has %d MB", disk, r.MaxDiskMB)
	}
	if r.MaxBandwidthGB > 0 && bw > r.MaxBandwidthGB {
		return denied("this would hand out %d GB of bandwidth a month; your reseller plan has %d GB", bw, r.MaxBandwidthGB)
	}
	return nil
}

// ---------------------------------------------------------------- bandwidth

// trackBandwidth adds the newest readings to this month's totals and
// suspends accounts over their package's monthly bandwidth.
func (s *Server) TrackBandwidth(ctx context.Context, now time.Time) {
	var usage []struct {
		Account   string  `json:"account"`
		TodayMB   float64 `json:"bandwidth_today_mb"`
		Suspended bool    `json:"suspended"`
	}
	if s.Agent.Get(ctx, "/v1/usage", &usage) != nil {
		return
	}
	day, month := now.UTC().Format("2006-01-02"), now.UTC().Format("2006-01")
	totals := map[string]float64{}
	_ = s.Store.Update(func(d *store.Data) error {
		if d.Bandwidth[month] == nil {
			d.Bandwidth[month] = map[string]float64{}
		}
		for _, u := range usage {
			last := d.BWDays[u.Account]
			add := u.TodayMB
			if last.Day == day {
				add = u.TodayMB - last.LastMB
				if add < 0 {
					add = u.TodayMB // counter reset
				}
			}
			d.Bandwidth[month][u.Account] += add
			d.BWDays[u.Account] = store.BWDay{Day: day, LastMB: u.TodayMB}
			totals[u.Account] = d.Bandwidth[month][u.Account]
		}
		// keep a year of history
		var months []string
		for m := range d.Bandwidth {
			months = append(months, m)
		}
		sort.Strings(months)
		for len(months) > 13 {
			delete(d.Bandwidth, months[0])
			months = months[1:]
		}
		return nil
	})
	admin := &store.Person{Username: "ike-bandwidth", Role: store.Admin}
	for _, u := range usage {
		pkg := s.packageOf(u.Account)
		var suspendedFor string
		s.Store.View(func(d *store.Data) { suspendedFor = d.BWSuspended[u.Account] })
		switch {
		case pkg != nil && pkg.Limits.BandwidthGB > 0 && totals[u.Account] > float64(pkg.Limits.BandwidthGB)*1024 && !u.Suspended && suspendedFor == "":
			reason := fmt.Sprintf("Monthly bandwidth of %d GB used up", pkg.Limits.BandwidthGB)
			if s.agentCall(ctx, s.Agent, admin, "POST", "/v1/accounts/"+u.Account+"/suspend", map[string]string{"reason": reason}, nil) == nil {
				_ = s.Store.Update(func(d *store.Data) error { d.BWSuspended[u.Account] = month; return nil })
				if s.Notifier != nil {
					s.Notifier.Send(ctx, u.Account+": bandwidth limit reached", reason+". The account is suspended until next month or until its package is upgraded.")
				}
			}
		case suspendedFor != "" && suspendedFor != month:
			// new month: lift bandwidth suspensions
			if s.agentCall(ctx, s.Agent, admin, "POST", "/v1/accounts/"+u.Account+"/unsuspend", map[string]string{}, nil) == nil {
				_ = s.Store.Update(func(d *store.Data) error { delete(d.BWSuspended, u.Account); return nil })
			}
		}
	}
}

func (s *Server) bandwidth(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	acc := s.accessFor(me)
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	type row struct {
		Account   string  `json:"account"`
		UsedMB    float64 `json:"used_mb"`
		LimitGB   int     `json:"limit_gb"`
		Suspended bool    `json:"suspended_for_bandwidth"`
		PackageID string  `json:"package_id,omitempty"`
	}
	out := []row{}
	s.Store.View(func(d *store.Data) {
		for a, mb := range d.Bandwidth[month] {
			if !acc.has(a) {
				continue
			}
			r := row{Account: a, UsedMB: float64(int(mb*10)) / 10, PackageID: d.AccountPackage[a], Suspended: d.BWSuspended[a] != ""}
			for _, p := range d.Packages {
				if p.ID == r.PackageID {
					r.LimitGB = p.Limits.BandwidthGB
				}
			}
			out = append(out, r)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].UsedMB > out[j].UsedMB })
	writeJSON(w, 200, map[string]any{"month": month, "accounts": out})
}

// ---------------------------------------------------------------- subscriptions

func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	acc := s.accessFor(personFrom(r))
	out := []store.Subscription{}
	s.Store.View(func(d *store.Data) {
		for _, x := range d.Subscriptions {
			if acc.has(x.Account) {
				out = append(out, *x)
			}
		}
	})
	writeJSON(w, 200, out)
}

func (s *Server) createSubscription(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	var req struct {
		Account   string    `json:"account"`
		PackageID string    `json:"package_id"`
		Price     *int64    `json:"price"` // default: the package's
		FirstDue  time.Time `json:"first_due"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if me.Role.Customer() || !s.accessFor(me).has(req.Account) {
		failErr(w, errForbidden)
		return
	}
	sub, err := s.subscribe(me, req.Account, req.PackageID, req.Price, req.FirstDue)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 201, sub)
}

func (s *Server) subscribe(me *store.Person, account, pkgID string, price *int64, first time.Time) (*store.Subscription, error) {
	var sub *store.Subscription
	err := s.Store.Update(func(d *store.Data) error {
		var pkg *store.Package
		for _, p := range d.Packages {
			if p.ID == pkgID && canSeePackage(me, p) {
				pkg = p
			}
		}
		if pkg == nil {
			return fmt.Errorf("package %w", store.ErrNotFound)
		}
		if pkg.Interval == "none" {
			return errors.New("this package isn't billed on a schedule")
		}
		for _, x := range d.Subscriptions {
			if x.Account == account && x.Status == "active" {
				return errors.New("this account already has an active subscription; cancel it first")
			}
		}
		if first.IsZero() {
			first = time.Now().UTC()
		}
		sub = &store.Subscription{ID: kit.RandomHex(5), Account: account, PackageID: pkg.ID, Price: pkg.Price, Currency: pkg.Currency,
			Interval: pkg.Interval, NextDue: first.UTC(), Status: "active", CreatedBy: me.Username, CreatedAt: time.Now().UTC()}
		if price != nil {
			if *price < 0 {
				return errors.New("price can't be negative")
			}
			sub.Price = *price
		}
		d.Subscriptions = append(d.Subscriptions, sub)
		return nil
	})
	return sub, err
}

func (s *Server) cancelSubscription(w http.ResponseWriter, r *http.Request) {
	acc := s.accessFor(personFrom(r))
	id := r.PathValue("id")
	err := s.Store.Update(func(d *store.Data) error {
		for _, x := range d.Subscriptions {
			if x.ID == id && acc.has(x.Account) {
				x.Status = "cancelled"
				return nil
			}
		}
		return fmt.Errorf("subscription %w", store.ErrNotFound)
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"cancelled": id})
}

// RunSubscriptions issues invoices for subscriptions that are due (a few
// days ahead, so customers can pay before the due date) and moves their
// next due date on. Overdue invoices are handled by EnforceOverdue.
func (s *Server) RunSubscriptions(ctx context.Context, now time.Time) {
	const lead = 7 * 24 * time.Hour
	var created []store.Invoice
	_ = s.Store.Update(func(d *store.Data) error {
		for _, sub := range d.Subscriptions {
			if sub.Status != "active" || now.Add(lead).Before(sub.NextDue) {
				continue
			}
			pkgName := sub.PackageID
			for _, p := range d.Packages {
				if p.ID == sub.PackageID {
					pkgName = p.Name
				}
			}
			period := sub.NextDue.Format("Jan 2006")
			if sub.Interval == "year" {
				period = sub.NextDue.Format("2006") + "–" + sub.NextDue.AddDate(1, 0, 0).Format("2006")
			}
			inv := &store.Invoice{ID: d.NewInvoiceID(), Account: sub.Account, Description: pkgName + " hosting, " + period,
				Currency: sub.Currency, Amount: sub.Price, Status: "unpaid", DueAt: sub.NextDue, CreatedAt: now.UTC(), CreatedBy: "subscription:" + sub.ID}
			d.Invoices = append(d.Invoices, inv)
			sub.LastInvoice = inv.ID
			if sub.Interval == "year" {
				sub.NextDue = sub.NextDue.AddDate(1, 0, 0)
			} else {
				sub.NextDue = sub.NextDue.AddDate(0, 1, 0)
			}
			created = append(created, *inv)
		}
		return nil
	})
	for _, inv := range created {
		if s.Notifier != nil {
			s.Notifier.Send(ctx, "Invoice "+inv.ID+" for "+inv.Account, fmt.Sprintf("%s: %s due %s", inv.Description, formatMoney(inv.Amount, inv.Currency), inv.DueAt.Format("2 Jan 2006")))
		}
	}
}

// ---------------------------------------------------------------- branding

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var logoRe = regexp.MustCompile(`^data:image/(png|svg\+xml|webp|jpeg);base64,[A-Za-z0-9+/=]+$`)
var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// branding returns the look for this request: a reseller's when the panel is
// opened at one of their hostnames or by one of their customers, otherwise
// the admin's.
func (s *Server) brandingFor(r *http.Request) *store.Branding {
	host := strings.ToLower(r.Host)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	user, _ := s.sessionUser(r)
	var out *store.Branding
	s.Store.View(func(d *store.Data) {
		for owner, b := range d.Branding {
			for _, h := range b.Hostnames {
				if h == host && owner != "" {
					c := *b
					out = &c
					return
				}
			}
		}
		if user != "" {
			if p, err := d.Person(user); err == nil {
				key := ""
				switch {
				case p.Role == store.Reseller:
					key = p.Username
				case p.Reseller != "":
					key = p.Reseller
				}
				if b, ok := d.Branding[key]; ok && key != "" {
					c := *b
					out = &c
					return
				}
			}
		}
		if b, ok := d.Branding[""]; ok {
			c := *b
			out = &c
		}
	})
	if out == nil {
		out = &store.Branding{ProductName: "Ike", PrimaryColor: ""}
	}
	return out
}

func (s *Server) getBranding(w http.ResponseWriter, r *http.Request) {
	b := s.brandingFor(r)
	b.Hostnames = nil
	writeJSON(w, 200, b)
}

func (s *Server) getMyBranding(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	key := ""
	if me.Role == store.Reseller {
		key = me.Username
	}
	var b store.Branding
	s.Store.View(func(d *store.Data) {
		if x, ok := d.Branding[key]; ok {
			b = *x
		}
	})
	writeJSON(w, 200, b)
}

func (s *Server) putBranding(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	if me.Role.Customer() {
		failErr(w, errForbidden)
		return
	}
	var b store.Branding
	if !decodeBody(w, r, &b) {
		return
	}
	b.ProductName = strings.TrimSpace(b.ProductName)
	if b.ProductName == "" || len(b.ProductName) > 40 || strings.ContainsAny(b.ProductName, "<>\"") {
		fail(w, 400, "Product name is 1-40 characters.")
		return
	}
	if b.PrimaryColor != "" && !colorRe.MatchString(b.PrimaryColor) {
		fail(w, 400, "Colour looks like #2357d6.")
		return
	}
	if b.Logo != "" && (len(b.Logo) > 280_000 || !logoRe.MatchString(b.Logo)) {
		fail(w, 400, "The logo must be a PNG, SVG, WebP or JPEG under 200 KB.")
		return
	}
	if b.Logo != "" && strings.HasPrefix(b.Logo, "data:image/svg") {
		fail(w, 400, "Upload the logo as PNG or WebP; SVG files can carry scripts.")
		return
	}
	if b.SupportEmail != "" {
		if err := store.ValidateEmail(b.SupportEmail); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	if b.SupportURL != "" && !strings.HasPrefix(b.SupportURL, "https://") {
		fail(w, 400, "Support link must start with https://")
		return
	}
	if len(b.Hostnames) > 10 {
		fail(w, 400, "At most 10 panel addresses.")
		return
	}
	for i, h := range b.Hostnames {
		b.Hostnames[i] = strings.ToLower(strings.TrimSpace(h))
		if !hostRe.MatchString(b.Hostnames[i]) {
			fail(w, 400, h+" isn't a host name.")
			return
		}
	}
	key := ""
	if me.Role == store.Reseller {
		key = me.Username
	} else if b.HidePoweredBy {
		// fine for the admin
	}
	err := s.Store.Update(func(d *store.Data) error {
		for owner, x := range d.Branding {
			if owner == key {
				continue
			}
			for _, h := range x.Hostnames {
				for _, nh := range b.Hostnames {
					if h == nh {
						return fmt.Errorf("%s is already used by another brand", h)
					}
				}
			}
		}
		d.Branding[key] = &b
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, b)
}

// ---------------------------------------------------------------- notifications

type notification struct {
	ID      int64          `json:"id"`
	Time    time.Time      `json:"time"`
	Type    string         `json:"type"`
	Subject string         `json:"subject"`
	Level   string         `json:"level"`
	Data    map[string]any `json:"data,omitempty"`
	Unread  bool           `json:"unread"`
}

// notifications shows server events the person may see: everything for
// admins, events about their own accounts for everyone else.
func (s *Server) notifications(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	acc := s.accessFor(me)
	var events []notification
	if err := s.Agent.Get(r.Context(), "/v1/events?limit=500", &events); err != nil {
		failErr(w, err)
		return
	}
	var lastRead int64
	s.Store.View(func(d *store.Data) { lastRead = d.NotifRead[me.Username] })
	out := []notification{}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if !acc.all {
			a, _ := e.Data["account"].(string)
			if a == "" || !acc.has(a) {
				continue
			}
		}
		e.Unread = e.ID > lastRead
		out = append(out, e)
		if len(out) >= 100 {
			break
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) markNotificationsRead(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	var req struct {
		UpTo int64 `json:"up_to"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	_ = s.Store.Update(func(d *store.Data) error {
		if req.UpTo > d.NotifRead[me.Username] {
			d.NotifRead[me.Username] = req.UpTo
		}
		return nil
	})
	writeJSON(w, 200, map[string]int64{"read_up_to": req.UpTo})
}

// LatestEventID is the newest event the agent has (0 when none).
func (s *Server) LatestEventID(ctx context.Context) int64 {
	var events []notification
	if s.Agent.Get(ctx, "/v1/events?since=0&limit=1", &events) != nil || len(events) == 0 {
		return 0
	}
	return events[len(events)-1].ID
}

// ForwardEvents sends serious server events (backups failing, malware, mail
// problems) to the admin's alert channels. Returns the newest event ID seen.
func (s *Server) ForwardEvents(ctx context.Context, since int64) int64 {
	var events []notification
	if s.Agent.Get(ctx, fmt.Sprintf("/v1/events?since=%d&limit=200", since), &events) != nil {
		return since
	}
	for _, e := range events {
		if e.ID > since {
			since = e.ID
		}
		if s.Notifier == nil {
			continue
		}
		if title, detail, ok := updateAlert(e); ok {
			s.Notifier.Send(ctx, title, detail)
		} else if e.Level == "bad" {
			detail := e.Type
			if msg, ok := e.Data["error"].(string); ok {
				detail += ": " + msg
			}
			s.Notifier.Send(ctx, e.Subject+": "+strings.ReplaceAll(e.Type, ".", " "), detail)
		}
	}
	return since
}

// ---------------------------------------------------------------- SSO

var ssoPage = template.Must(template.New("sso").Parse(`<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=/">
<title>Signing in…</title><p style="font:16px system-ui;margin:40px">Signing you in… <a href="/">Continue</a></p>`))

// ssoLogin signs a person in with a one-time link made by a billing system.
func (s *Server) ssoLogin(w http.ResponseWriter, r *http.Request) {
	hash := auth.HashToken(r.PathValue("token"))
	user := ""
	_ = s.Store.Update(func(d *store.Data) error {
		keep := d.SSOTokens[:0]
		for _, t := range d.SSOTokens {
			if t.Hash == hash && time.Now().Before(t.Expires) {
				user = t.Username
				continue
			}
			if time.Now().Before(t.Expires) {
				keep = append(keep, t)
			}
		}
		d.SSOTokens = keep
		return nil
	})
	if user == "" {
		http.Error(w, "This sign-in link has expired. Go back and try again.", http.StatusGone)
		return
	}
	s.newSession(w, r, user)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_ = ssoPage.Execute(w, nil)
}

// updateAlert words the update events admins hear about on their alert
// channels: new versions waiting for approval, and how updates went.
func updateAlert(e notification) (title, detail string, ok bool) {
	str := func(k string) string { v, _ := e.Data[k].(string); return v }
	num := func(k string) int { v, _ := e.Data[k].(float64); return int(v) }
	switch e.Type {
	case "ike.update.available":
		return "Ike " + str("latest") + " is available", "This server runs Ike " + str("current") + ". Open Updates in the panel to read what's new and install it. Ike keeps the current version and goes back by itself if the new one isn't healthy.", true
	case "ike.updated":
		return "Ike updated to " + str("to"), "Updated from " + str("from") + " and checked: the agent and panel are healthy.", true
	case "ike.rolled_back":
		return "Ike is back on " + str("to"), "Went back from " + str("from") + " as asked.", true
	case "ike.update.failed":
		return "Ike update to " + str("to") + " didn't go through", str("reason") + ". This server still runs Ike " + str("from") + ".", true
	case "server.updates.security":
		return fmt.Sprintf("%d security updates are waiting", num("security")), fmt.Sprintf("%d package updates in all, %d of them security fixes. Open Updates in the panel to install them.", num("packages"), num("security")), true
	case "server.updates.available":
		return fmt.Sprintf("%d package updates are waiting", num("packages")), "Open Updates in the panel to review and install them.", true
	case "server.updates.failed":
		return "Package updates failed", str("error"), true
	case "server.release.available":
		return "Ubuntu " + str("available") + " is available", "This server runs Ubuntu " + str("current") + ". Moving to a new release needs a snapshot first; see Updates in the panel.", true
	}
	return "", "", false
}
