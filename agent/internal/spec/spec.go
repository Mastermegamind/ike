// Package spec is infrastructure as code for one Ike server: a JSON document
// that declares accounts, websites, databases, DNS zones, email, cron jobs
// and open ports. Plan shows what would change; Apply makes it so. Specs
// only add and update (nothing is deleted unless it is in the spec's
// "remove" list), so applying the same spec twice changes nothing.
//
// Templates are saved specs with ${variables}, for setting up the same kind
// of site again and again.
package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Mastermegamind/ike/agent/internal/databases"
	"github.com/Mastermegamind/ike/agent/internal/dns"
	"github.com/Mastermegamind/ike/agent/internal/firewall"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/mail"
	"github.com/Mastermegamind/ike/agent/internal/sites"
)

// Spec is the desired state.
type Spec struct {
	Variables map[string]string `json:"variables,omitempty"`
	Accounts  []AccountSpec     `json:"accounts,omitempty"`
	Sites     []SiteSpec        `json:"sites,omitempty"`
	Databases []DatabaseSpec    `json:"databases,omitempty"`
	DNS       []ZoneSpec        `json:"dns,omitempty"`
	Mail      []MailSpec        `json:"mail,omitempty"`
	Cron      []CronSpec        `json:"cron,omitempty"`
	Firewall  []PortSpec        `json:"firewall,omitempty"`
}

type AccountSpec struct {
	Name   string        `json:"name"`
	SSH    string        `json:"ssh,omitempty"` // none | sftp | shell
	Limits *sites.Limits `json:"limits,omitempty"`
}

type SiteSpec struct {
	Domain       string   `json:"domain"`
	Account      string   `json:"account"`
	Runtime      string   `json:"runtime,omitempty"`
	Version      string   `json:"version,omitempty"`
	WebServer    string   `json:"web_server,omitempty"`
	StartCommand string   `json:"start_command,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
	WWW          string   `json:"www,omitempty"`
	HTTPS        bool     `json:"https,omitempty"` // request a Let's Encrypt certificate
}

type DatabaseSpec struct {
	Account string `json:"account"`
	Engine  string `json:"engine"` // mariadb | postgres | mongodb
	Name    string `json:"name"`   // suffix after <account>_
}

type ZoneSpec struct {
	Account  string       `json:"account"`
	Name     string       `json:"name"`
	Template string       `json:"template,omitempty"`
	Records  []dns.Record `json:"records,omitempty"`
}

type MailSpec struct {
	Account   string        `json:"account"`
	Domain    string        `json:"domain"`
	Mailboxes []MailboxSpec `json:"mailboxes,omitempty"`
	Aliases   []mail.Alias  `json:"aliases,omitempty"`
}

type MailboxSpec struct {
	Local   string `json:"local"`
	QuotaMB int    `json:"quota_mb,omitempty"`
}

type CronSpec struct {
	Account  string `json:"account"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Comment  string `json:"comment,omitempty"`
}

type PortSpec struct {
	Port    string `json:"port"`
	Proto   string `json:"proto,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// Change is one step of a plan.
type Change struct {
	Action string `json:"action"` // create | update | none
	Kind   string `json:"kind"`   // account | site | database | dns-zone | dns-record | mail-domain | mailbox | mail-aliases | cron | port | https
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// Result is one applied change; generated passwords are shown once.
type Result struct {
	Change
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Secrets any    `json:"secrets,omitempty"`
}

type Manager struct {
	env   *kit.Env
	Sites *sites.Manager
	DNS   *dns.Manager
	Mail  *mail.Manager
	DB    *databases.Manager
	FW    *firewall.Manager
}

func New(env *kit.Env) *Manager { return &Manager{env: env} }

var varRe = regexp.MustCompile(`\$\{([a-z_][a-z0-9_]*)\}`)

// Expand replaces ${name} with variables (from the spec, overridden by vars).
func Expand(raw []byte, vars map[string]string) ([]byte, error) {
	var head struct {
		Variables map[string]string `json:"variables"`
	}
	_ = json.Unmarshal(raw, &head)
	all := map[string]string{}
	for k, v := range head.Variables {
		all[k] = v
	}
	for k, v := range vars {
		all[k] = v
	}
	var missing []string
	out := varRe.ReplaceAllFunc(raw, func(m []byte) []byte {
		name := string(varRe.FindSubmatch(m)[1])
		v, ok := all[name]
		if !ok {
			missing = append(missing, name)
			return m
		}
		b, _ := json.Marshal(v)
		return b[1 : len(b)-1] // JSON-escaped, without quotes
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("set these variables: %s", strings.Join(uniq(missing), ", "))
	}
	return out, nil
}

func uniq(l []string) []string {
	var out []string
	for i, s := range l {
		if i == 0 || s != l[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// Parse reads a spec (after variables are expanded).
func Parse(raw []byte, vars map[string]string) (*Spec, error) {
	b, err := Expand(raw, vars)
	if err != nil {
		return nil, err
	}
	var s Spec
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("spec: %w", err)
	}
	return &s, nil
}

// Plan compares the spec with the server.
func (m *Manager) Plan(s *Spec) ([]Change, error) {
	var out []Change
	accts := map[string]sites.Account{}
	if list, err := m.Sites.Accounts(); err == nil {
		for _, a := range list {
			accts[a.Name] = a
		}
	}
	for _, a := range s.Accounts {
		cur, ok := accts[a.Name]
		switch {
		case !ok:
			out = append(out, Change{"create", "account", a.Name, ""})
		case a.SSH != "" && a.SSH != cur.SSH:
			out = append(out, Change{"update", "account", a.Name, "SSH " + cur.SSH + " → " + a.SSH})
		case a.Limits != nil && *a.Limits != cur.Limits:
			out = append(out, Change{"update", "account", a.Name, "limits"})
		default:
			out = append(out, Change{"none", "account", a.Name, ""})
		}
	}
	for _, st := range s.Sites {
		cur, err := m.Sites.Get(st.Domain)
		switch {
		case err != nil:
			out = append(out, Change{"create", "site", st.Domain, orStr(st.Runtime, "php") + " in " + st.Account})
		case cur.Account != st.Account:
			out = append(out, Change{"update", "site", st.Domain, "belongs to " + cur.Account + "; can't move it between accounts"})
		default:
			var d []string
			if st.Version != "" && st.Version != cur.Version {
				d = append(d, "version "+cur.Version+" → "+st.Version)
			}
			if st.Aliases != nil && !sameSet(st.Aliases, cur.Aliases) || st.WWW != "" && st.WWW != cur.WWW {
				d = append(d, "domains")
			}
			if len(d) > 0 {
				out = append(out, Change{"update", "site", st.Domain, strings.Join(d, ", ")})
			} else {
				out = append(out, Change{"none", "site", st.Domain, ""})
			}
		}
		if st.HTTPS && (err != nil || cur.SSL == nil) {
			out = append(out, Change{"create", "https", st.Domain, "Let's Encrypt certificate"})
		}
	}
	for _, d := range s.Databases {
		name := d.Account + "_" + d.Name
		exists := false
		switch d.Engine {
		case "mariadb", "":
			if dbs, err := m.Sites.Databases(d.Account); err == nil {
				for _, x := range dbs {
					exists = exists || x.Name == name
				}
			}
		default:
			if m.DB != nil {
				for _, x := range m.DB.Names(d.Account) {
					exists = exists || x == d.Engine+":"+name
				}
			}
		}
		out = append(out, Change{map[bool]string{true: "none", false: "create"}[exists], "database", orStr(d.Engine, "mariadb") + ":" + name, ""})
	}
	for _, z := range s.DNS {
		cur, err := m.DNS.Get("", z.Name)
		if err != nil {
			out = append(out, Change{"create", "dns-zone", z.Name, orStr(z.Template, "empty")})
			for _, r := range z.Records {
				out = append(out, Change{"create", "dns-record", z.Name, r.Type + " " + r.Name + " " + r.Content})
			}
			continue
		}
		out = append(out, Change{"none", "dns-zone", z.Name, ""})
		for _, r := range z.Records {
			rr := r
			if rr.Validate() != nil {
				out = append(out, Change{"create", "dns-record", z.Name, "invalid: " + r.Type + " " + r.Name})
				continue
			}
			found := false
			for _, o := range cur.Records {
				found = found || dns.SameIn(o, rr, z.Name)
			}
			out = append(out, Change{map[bool]string{true: "none", false: "create"}[found], "dns-record", z.Name, rr.Type + " " + rr.Name + " " + rr.Content})
		}
	}
	for _, md := range s.Mail {
		cur, err := m.Mail.Domain("", md.Domain)
		if err != nil {
			out = append(out, Change{"create", "mail-domain", md.Domain, ""})
		} else {
			out = append(out, Change{"none", "mail-domain", md.Domain, ""})
		}
		for _, mb := range md.Mailboxes {
			exists := false
			if cur != nil {
				for _, x := range cur.Mailboxes {
					exists = exists || x.Local == strings.ToLower(mb.Local)
				}
			}
			out = append(out, Change{map[bool]string{true: "none", false: "create"}[exists], "mailbox", mb.Local + "@" + md.Domain, ""})
		}
		if len(md.Aliases) > 0 {
			out = append(out, Change{"update", "mail-aliases", md.Domain, fmt.Sprintf("%d forwarders", len(md.Aliases))})
		}
	}
	for _, c := range s.Cron {
		exists := false
		if jobs, err := m.Sites.CronJobs(c.Account); err == nil {
			for _, j := range jobs {
				exists = exists || (j.Schedule == c.Schedule && j.Command == c.Command)
			}
		}
		out = append(out, Change{map[bool]string{true: "none", false: "create"}[exists], "cron", c.Account, c.Schedule + " " + c.Command})
	}
	if len(s.Firewall) > 0 && m.FW != nil {
		st, _ := m.FW.Status()
		for _, p := range s.Firewall {
			proto := orStr(p.Proto, "tcp")
			exists := false
			if st != nil {
				for _, x := range st.Ports {
					exists = exists || (x.Port == p.Port && x.Proto == proto)
				}
			}
			out = append(out, Change{map[bool]string{true: "none", false: "create"}[exists], "port", p.Port + "/" + proto, p.Comment})
		}
	}
	return out, nil
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[strings.ToLower(x)] = true
	}
	for _, x := range b {
		if !m[strings.ToLower(x)] {
			return false
		}
	}
	return true
}

// Apply makes the server match the spec, in dependency order, and reports
// each step. It keeps going after a failed step that later steps don't need.
func (m *Manager) Apply(ctx context.Context, s *Spec, log func(string, ...any)) ([]Result, error) {
	plan, err := m.Plan(s)
	if err != nil {
		return nil, err
	}
	var results []Result
	add := func(c Change, err error, secrets any) {
		r := Result{Change: c, OK: err == nil, Secrets: secrets}
		if err != nil {
			r.Error = err.Error()
			log("%s %s %s: %v", c.Action, c.Kind, c.Name, err)
		} else if c.Action != "none" {
			log("%s %s %s", c.Action, c.Kind, c.Name)
		}
		results = append(results, r)
	}
	idx := 0
	next := func() Change { c := plan[idx]; idx++; return c }
	for _, a := range s.Accounts {
		c := next()
		switch c.Action {
		case "create":
			_, err := m.Sites.CreateAccount(ctx, a.Name)
			if err == nil && a.SSH != "" && a.SSH != "none" {
				_, err = m.Sites.SetSSHMode(ctx, a.Name, a.SSH)
			}
			if err == nil && a.Limits != nil {
				_, err = m.Sites.SetLimits(ctx, a.Name, *a.Limits)
			}
			add(c, err, nil)
		case "update":
			var err error
			if a.SSH != "" {
				_, err = m.Sites.SetSSHMode(ctx, a.Name, a.SSH)
			}
			if err == nil && a.Limits != nil {
				_, err = m.Sites.SetLimits(ctx, a.Name, *a.Limits)
			}
			add(c, err, nil)
		default:
			add(c, nil, nil)
		}
	}
	for _, st := range s.Sites {
		c := next()
		var err error
		switch c.Action {
		case "create":
			_, err = m.Sites.Create(ctx, sites.CreateRequest{Domain: st.Domain, Account: st.Account, Runtime: st.Runtime, Version: st.Version,
				WebServer: st.WebServer, StartCommand: st.StartCommand})
			if err == nil && (len(st.Aliases) > 0 || st.WWW != "") {
				_, err = m.Sites.SetDomains(ctx, st.Domain, sites.DomainsRequest{Aliases: st.Aliases, WWW: st.WWW})
			}
		case "update":
			if strings.Contains(c.Detail, "can't move") {
				err = fmt.Errorf("%s", c.Detail)
				break
			}
			cur, _ := m.Sites.Get(st.Domain)
			if st.Version != "" && cur != nil && st.Version != cur.Version {
				_, err = m.Sites.Update(ctx, st.Domain, sites.UpdateRequest{Version: st.Version})
			}
			if err == nil && strings.Contains(c.Detail, "domains") {
				aliases := st.Aliases
				if aliases == nil && cur != nil {
					aliases = cur.Aliases
				}
				_, err = m.Sites.SetDomains(ctx, st.Domain, sites.DomainsRequest{Aliases: aliases, WWW: st.WWW})
			}
		}
		add(c, err, nil)
		if st.HTTPS && idx < len(plan) && plan[idx].Kind == "https" {
			hc := next()
			if err == nil {
				_, herr := m.Sites.IssueCertificate(ctx, st.Domain, sites.IssueRequest{}, log)
				add(hc, herr, nil)
			} else {
				add(hc, fmt.Errorf("skipped: the website wasn't created"), nil)
			}
		}
	}
	for _, d := range s.Databases {
		c := next()
		if c.Action != "create" {
			add(c, nil, nil)
			continue
		}
		switch d.Engine {
		case "mariadb", "":
			nd, err := m.Sites.CreateDatabase(ctx, d.Account, d.Name, "")
			add(c, err, nd)
		default:
			cred, err := m.DB.Create(ctx, d.Account, databases.CreateRequest{Engine: d.Engine, Name: d.Name})
			add(c, err, cred)
		}
	}
	for _, z := range s.DNS {
		c := next()
		if c.Action == "create" {
			_, err := m.DNS.Create(ctx, z.Account, dns.CreateZone{Name: z.Name, Template: z.Template})
			add(c, err, nil)
		} else {
			add(c, nil, nil)
		}
		for _, r := range z.Records {
			rc := next()
			if rc.Action == "create" {
				_, err := m.DNS.AddRecord(ctx, z.Account, z.Name, r)
				add(rc, err, nil)
			} else {
				add(rc, nil, nil)
			}
		}
	}
	for _, md := range s.Mail {
		c := next()
		if c.Action == "create" {
			_, err := m.Mail.AddDomain(ctx, md.Account, md.Domain)
			add(c, err, nil)
		} else {
			add(c, nil, nil)
		}
		for _, mb := range md.Mailboxes {
			mc := next()
			if mc.Action == "create" {
				cm, err := m.Mail.CreateMailbox(ctx, md.Account, md.Domain, mail.NewMailbox{Local: mb.Local, QuotaMB: mb.QuotaMB})
				add(mc, err, cm)
			} else {
				add(mc, nil, nil)
			}
		}
		if len(md.Aliases) > 0 {
			ac := next()
			_, err := m.Mail.SetAliases(ctx, md.Account, md.Domain, md.Aliases)
			add(ac, err, nil)
		}
	}
	for _, cj := range s.Cron {
		c := next()
		if c.Action == "create" {
			_, err := m.Sites.AddCronJob(cj.Account, sites.CronRequest{Schedule: cj.Schedule, Command: cj.Command, Comment: cj.Comment})
			add(c, err, nil)
		} else {
			add(c, nil, nil)
		}
	}
	if len(s.Firewall) > 0 && m.FW != nil {
		for _, p := range s.Firewall {
			c := next()
			if c.Action == "create" {
				_, err := m.FW.OpenPort(ctx, p.Port, orStr(p.Proto, "tcp"), p.Comment)
				add(c, err, nil)
			} else {
				add(c, nil, nil)
			}
		}
	}
	m.env.Publish("spec.applied", m.env.Cfg.Hostname, map[string]any{"changes": countChanges(results)})
	return results, nil
}

func countChanges(rs []Result) int {
	n := 0
	for _, r := range rs {
		if r.Action != "none" && r.OK {
			n++
		}
	}
	return n
}

// ---- templates ----

// Template is a saved spec.
type Template struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Spec        json.RawMessage `json:"spec"`
	Variables   []string        `json:"variables"` // found in the spec
}

var tmplNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

func (m *Manager) dir() string { return m.env.P(m.env.Cfg.SpecDir) }

func (m *Manager) Templates() ([]Template, error) {
	ents, err := os.ReadDir(m.dir())
	if os.IsNotExist(err) {
		return []Template{}, nil
	}
	out := []Template{}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var t Template
		if kit.ReadJSON(filepath.Join(m.dir(), e.Name()), &t) == nil {
			out = append(out, t)
		}
	}
	return out, err
}

func (m *Manager) SaveTemplate(t Template) (Template, error) {
	if !tmplNameRe.MatchString(t.Name) {
		return t, fmt.Errorf("template names are lowercase letters, digits and dashes")
	}
	if len(t.Description) > 300 || len(t.Spec) > 512<<10 {
		return t, fmt.Errorf("description or spec too long")
	}
	// it must parse with every variable set to a placeholder
	vars := map[string]string{}
	for _, mm := range varRe.FindAllSubmatch(t.Spec, -1) {
		vars[string(mm[1])] = "example"
	}
	if _, err := Parse(t.Spec, vars); err != nil {
		return t, err
	}
	t.Variables = []string{}
	for v := range vars {
		t.Variables = append(t.Variables, v)
	}
	sort.Strings(t.Variables)
	return t, kit.WriteJSON(filepath.Join(m.dir(), t.Name+".json"), t)
}

func (m *Manager) Template(name string) (*Template, error) {
	if !tmplNameRe.MatchString(name) {
		return nil, fmt.Errorf("template %s %w", name, kit.ErrNotFound)
	}
	var t Template
	if err := kit.ReadJSON(filepath.Join(m.dir(), name+".json"), &t); err != nil {
		return nil, fmt.Errorf("template %s %w", name, kit.ErrNotFound)
	}
	return &t, nil
}

func (m *Manager) DeleteTemplate(name string) error {
	if !tmplNameRe.MatchString(name) {
		return fmt.Errorf("template %s %w", name, kit.ErrNotFound)
	}
	return os.Remove(filepath.Join(m.dir(), name+".json"))
}

// Export describes the current server as a spec (accounts, sites, databases,
// zones, mail, cron), to copy to another server or keep in Git.
func (m *Manager) Export() (*Spec, error) {
	s := &Spec{}
	accts, err := m.Sites.Accounts()
	if err != nil {
		return nil, err
	}
	for _, a := range accts {
		lim := a.Limits
		s.Accounts = append(s.Accounts, AccountSpec{Name: a.Name, SSH: a.SSH, Limits: &lim})
		for _, d := range a.Databases {
			s.Databases = append(s.Databases, DatabaseSpec{Account: a.Name, Engine: "mariadb", Name: strings.TrimPrefix(d.Name, a.Name+"_")})
		}
		if m.DB != nil {
			for _, n := range m.DB.Names(a.Name) {
				eng, name, _ := strings.Cut(n, ":")
				s.Databases = append(s.Databases, DatabaseSpec{Account: a.Name, Engine: eng, Name: strings.TrimPrefix(name, a.Name+"_")})
			}
		}
		for _, c := range a.Cron {
			s.Cron = append(s.Cron, CronSpec{Account: a.Name, Schedule: c.Schedule, Command: c.Command, Comment: c.Comment})
		}
	}
	list, _ := m.Sites.List()
	for _, st := range list {
		s.Sites = append(s.Sites, SiteSpec{Domain: st.Domain, Account: st.Account, Runtime: st.Runtime, Version: st.Version, WebServer: st.WebServer,
			StartCommand: st.StartCommand, Aliases: st.Aliases, WWW: st.WWW, HTTPS: st.SSL != nil})
	}
	if zones, err := m.DNS.All(); err == nil {
		for _, z := range zones {
			zs := ZoneSpec{Account: z.Account, Name: z.Name}
			for _, r := range z.Records {
				if r.Managed == "" {
					r.ID = ""
					zs.Records = append(zs.Records, r)
				}
			}
			s.DNS = append(s.DNS, zs)
		}
	}
	if doms, err := m.Mail.Domains(""); err == nil {
		for _, d := range doms {
			ms := MailSpec{Account: d.Account, Domain: d.Domain.Domain, Aliases: d.Aliases}
			for _, mb := range d.Mailboxes {
				ms.Mailboxes = append(ms.Mailboxes, MailboxSpec{Local: mb.Local, QuotaMB: mb.QuotaMB})
			}
			s.Mail = append(s.Mail, ms)
		}
	}
	return s, nil
}
