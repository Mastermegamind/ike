package api

// Routes added in 0.5: DNS, mail, security, databases, files, backups,
// infrastructure and the rest of the hosting platform. Account-scoped
// resources live under /v1/accounts/{name}/... so the panel's account checks
// apply to them unchanged; everything under other prefixes is admin only.
//
//	GET    /v1/events?since=&limit=            recent server events (notification centre)
//	POST   /v1/services/{name}/{action}        start | stop | restart | reload | enable | disable
//	GET    /v1/secrets, POST /v1/secrets/rotate
//
//	GET    /v1/dns                             settings (tokens masked), record types, templates
//	PUT    /v1/dns                             settings
//	GET    /v1/dns/zones                       every zone
//	GET    /v1/dns/reverse                     reverse DNS of this server's addresses
//	GET    /v1/dns/registrar, PUT              Namecheap account and contact
//	GET    /v1/dns/registrar/domains
//	POST   /v1/dns/registrar/check             {"domains": [...]}
//	POST   /v1/dns/registrar/register          {"domain","years","account","use_own_nameservers"}
//	POST   /v1/dns/registrar/renew             {"domain","years"}
//	PUT    /v1/dns/registrar/nameservers       {"domain","nameservers"}
//	POST   /v1/dns/registrar/glue              {"host","ip"}
//	POST   /v1/dns/acme, DELETE /v1/dns/acme?domain=   certbot DNS-01 hooks
//
//	GET    /v1/accounts/{a}/dns/zones, POST    {"name","backend","kind","primaries","template","ipv4","ipv6"}
//	GET    /v1/accounts/{a}/dns/zones/{z}, PATCH {"ttl","soa_email","nameservers","also_notify","allow_transfer","dnssec"}, DELETE
//	POST   /v1/accounts/{a}/dns/zones/{z}/records, PUT (replace all)
//	PUT    /v1/accounts/{a}/dns/zones/{z}/records/{id}, DELETE
//	POST   /v1/accounts/{a}/dns/zones/{z}/template   {"template","ipv4","ipv6"}
//	POST   /v1/accounts/{a}/dns/zones/{z}/import     {"zone_file","replace"}
//	GET    /v1/accounts/{a}/dns/zones/{z}/export     BIND zone file (text/plain)
//	GET    /v1/accounts/{a}/dns/zones/{z}/check      propagation
//	POST   /v1/accounts/{a}/dns/zones/{z}/registration  refresh expiry from RDAP
//	POST   /v1/accounts/{a}/dns/zones/{z}/pull       import records from the provider

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/YOUR-ORG/ike/agent/internal/backups"
	"github.com/YOUR-ORG/ike/agent/internal/databases"
	"github.com/YOUR-ORG/ike/agent/internal/dns"
	"github.com/YOUR-ORG/ike/agent/internal/files"
	"github.com/YOUR-ORG/ike/agent/internal/infra"
	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/mail"
	"github.com/YOUR-ORG/ike/agent/internal/malware"
	"github.com/YOUR-ORG/ike/agent/internal/secrets"
	"github.com/YOUR-ORG/ike/agent/internal/sites"
	"github.com/YOUR-ORG/ike/agent/internal/spec"
	"github.com/YOUR-ORG/ike/agent/internal/storage"
	"github.com/YOUR-ORG/ike/agent/internal/sysinfo"
)

// Features holds the 0.5 feature managers.
type Features struct {
	Env     *kit.Env
	Keys    *secrets.Keyring
	DNS     *dns.Manager
	Mail    *mail.Manager
	Malware *malware.Manager
	DB      *databases.Manager
	Files   *files.Manager
	Storage *storage.Manager
	Backups *backups.Manager
	Infra   *infra.Manager
	Spec    *spec.Manager
}

// zoneAdapter lets the mail manager publish records through DNS.
type zoneAdapter struct{ d *dns.Manager }

func (z zoneAdapter) SetManagedTXT(ctx context.Context, tag, domain string, recs []mail.DNSRecord) (bool, error) {
	zone := z.d.ZoneFor(domain)
	if zone == nil {
		return false, nil
	}
	var out []dns.Record
	for _, r := range recs {
		name := r.Name
		if rel := dns.RelativeTo(domain, zone.Name); rel != "@" {
			if name == "@" {
				name = rel
			} else {
				name = name + "." + rel
			}
		}
		content := r.Content
		if (r.Type == "MX" || r.Type == "CNAME" || r.Type == "SRV") && content == "mail" && domain != zone.Name {
			content = "mail." + domain + "."
		}
		out = append(out, dns.Record{Name: name, Type: r.Type, Content: content, Priority: r.Priority, Port: r.Port, Weight: r.Weight})
	}
	return z.d.SetManaged(ctx, tag+":"+domain, domain, out)
}

// Setup builds the feature managers. Call it before Handler.
func (s *Server) Setup() error {
	cfg := s.Config
	env := &kit.Env{Cfg: cfg, Run: s.Runner, Accounts: s.Sites, Events: kit.NewBus()}
	keys, err := secrets.Open(env.P(cfg.MasterKey))
	if err != nil {
		return err
	}
	f := &Features{Env: env, Keys: keys}
	f.DNS = dns.New(env, keys)
	sites.DNSValidation = f.DNS.CanIssueWildcard
	f.Mail = mail.New(env, keys)
	f.Mail.Zone = zoneAdapter{f.DNS}
	f.Mail.OwnerOf = s.domainOwner
	f.Malware = malware.New(env)
	f.Malware.Accounts = s.accountNames
	f.Files = files.New(env)
	f.Storage = storage.New(env, keys)
	f.DB = databases.New(env)
	f.DB.MariaDBOf = func(account string) []string {
		dbs, _ := s.Sites.Databases(account)
		out := make([]string, 0, len(dbs))
		for _, d := range dbs {
			out = append(out, d.Name)
		}
		return out
	}
	mail.NginxH2Directive = func() bool { return s.Sites.Features().H2Directive }
	dns.DKIMRecords = func(domain string) []dns.Record {
		name, val, err := f.Mail.DKIMRecord(domain)
		if err != nil {
			return nil
		}
		return []dns.Record{{Name: name, Type: "TXT", Content: val}}
	}
	s.F = f
	f.Backups = backups.New(env, keys, f.Storage, s.backupSources())
	s.setupInfra(f)
	s.Sites.OnAccountDelete = func(ctx context.Context, account string) {
		_ = f.DB.DeleteAccount(ctx, account)
		if doms, err := f.Mail.Domains(account); err == nil {
			for _, d := range doms {
				_ = f.Mail.RemoveDomain(ctx, account, d.Domain.Domain, true)
			}
		}
		if zones, err := f.DNS.List(account); err == nil {
			for _, z := range zones {
				_ = f.DNS.Delete(ctx, account, z.Name)
			}
		}
		f.Env.Publish("account.deleted", account, nil)
	}
	return nil
}

// RunBackground starts the 0.5 background loops.
func (s *Server) RunBackground(ctx context.Context) {
	if s.F == nil {
		return
	}
	go s.F.DNS.RunExpiryChecks(ctx)
	s.F.Mail.Detect(ctx)
	go s.F.Malware.RunScheduler(ctx)
	go s.F.Backups.RunScheduler(ctx)
	go s.F.Infra.RunMaintenance(ctx)
	go s.F.Infra.RunWebhooks(ctx)
}

func (s *Server) accountNames() []string {
	accts, _ := s.Sites.Accounts()
	out := make([]string, 0, len(accts))
	for _, a := range accts {
		out = append(out, a.Name)
	}
	return out
}

// domainOwner says which account holds a website or DNS zone for a domain.
func (s *Server) domainOwner(domain string) string {
	if site, err := s.Sites.Get(domain); err == nil {
		return site.Account
	}
	if s.F != nil {
		if z := s.F.DNS.ZoneFor(domain); z != nil {
			return z.Account
		}
	}
	return ""
}

func pathAcct(r *http.Request) string { return r.PathValue("a") }

func (s *Server) v5Routes(h func(string, handler)) {
	if s.F == nil {
		if err := s.Setup(); err != nil {
			slog.Error("feature setup failed", "err", err)
			return
		}
	}
	f := s.F
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	created := func(v any, err error) (int, any, error) { return http.StatusCreated, v, err }
	adminOnly := func(fn handler) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if !isAdmin(r) {
				return 0, nil, kit.ErrForbidden
			}
			return fn(w, r)
		}
	}

	h("GET /v1/events", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		return ok(f.Env.Events.Since(since, limit), nil)
	})
	h("POST /v1/services/{name}/{action}", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		name, action := r.PathValue("name"), r.PathValue("action")
		err := sysinfo.ControlService(r.Context(), s.Runner, s.Config.Root, s.Config.WebStack, name, action)
		if err == nil {
			f.Env.Publish("service."+action, name, nil)
		}
		return ok(map[string]string{"service": name, "action": action}, err)
	}))
	h("GET /v1/secrets", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(f.Keys.Status(), nil)
	}))
	h("POST /v1/secrets/rotate", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		st, err := f.Keys.Rotate()
		if err == nil {
			f.Env.Publish("secrets.rotated", st.KeyID, nil)
		}
		return ok(st, err)
	}))

	s.mailRoutes(h)
	s.securityRoutes(h)
	s.dbRoutes(h)
	s.fileRoutes(h)
	s.backupRoutes(h)
	s.infraRoutes(h)
	s.migrateRoutes(h)

	// ---- DNS: admin ----
	h("GET /v1/dns", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		set, err := f.DNS.Settings()
		out := map[string]any{"types": dns.Types, "templates": dns.Templates, "local": f.DNS.Local()}
		if isAdmin(r) {
			out["settings"] = set
		} else {
			out["nameservers"] = set.Nameservers
			out["default_backend"] = set.DefaultBackend
		}
		return ok(out, err)
	})
	h("PUT /v1/dns", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body dns.SettingsUpdate
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(f.DNS.UpdateSettings(ctx, body))
	}))
	h("GET /v1/dns/zones", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(f.DNS.All())
	}))
	h("GET /v1/dns/reverse", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(f.DNS.ReverseDNS(r.Context()), nil)
	}))
	h("GET /v1/dns/registrar", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		reg, err := f.DNS.RegistrarSettings()
		if reg == nil && err == nil {
			return ok(map[string]any{"kind": "", "client_ip": s.Config.PublicIPv4}, nil)
		}
		return ok(reg, err)
	}))
	h("PUT /v1/dns/registrar", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body dns.Registrar
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(f.DNS.SetRegistrar(body))
	}))
	h("GET /v1/dns/registrar/domains", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(f.DNS.RegistrarDomains(r.Context()))
	}))
	h("POST /v1/dns/registrar/check", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Domains []string `json:"domains"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(f.DNS.CheckAvailability(r.Context(), body.Domains))
	}))
	h("POST /v1/dns/registrar/register", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body dns.RegisterRequest
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return created(f.DNS.Register(ctx, body))
	}))
	h("POST /v1/dns/registrar/renew", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Domain string `json:"domain"`
			Years  int    `json:"years"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(f.DNS.Renew(ctx, body.Domain, body.Years))
	}))
	h("PUT /v1/dns/registrar/nameservers", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Domain      string   `json:"domain"`
			Nameservers []string `json:"nameservers"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(body, f.DNS.SetRegistrarNameservers(r.Context(), body.Domain, body.Nameservers))
	}))
	h("POST /v1/dns/registrar/glue", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct{ Host, IP string }
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(body, f.DNS.CreateGlue(r.Context(), body.Host, body.IP))
	}))
	h("POST /v1/dns/acme", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct{ Domain, Value string }
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(map[string]string{"domain": body.Domain}, f.DNS.SetACMEChallenge(ctx, body.Domain, body.Value))
	}))
	h("DELETE /v1/dns/acme", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		d := r.URL.Query().Get("domain")
		return ok(map[string]string{"domain": d}, f.DNS.CleanupACME(r.Context(), d))
	}))

	// ---- DNS: per account ----
	base := "/v1/accounts/{a}/dns/zones"
	h("GET "+base, func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(f.DNS.List(pathAcct(r)))
	})
	h("POST "+base, func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body dns.CreateZone
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		if body.Kind == "secondary" && !isAdmin(r) {
			return 0, nil, errors.New("only server admins can add secondary zones")
		}
		ctx, cancel := long(r)
		defer cancel()
		return created(f.DNS.Create(ctx, pathAcct(r), body))
	})
	h("GET "+base+"/{z}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(f.DNS.Get(pathAcct(r), r.PathValue("z")))
	})
	h("PATCH "+base+"/{z}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body dns.ZoneSettings
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		if (len(body.AlsoNotify) > 0 || len(body.AllowTransfer) > 0 || len(body.Nameservers) > 0) && !isAdmin(r) {
			return 0, nil, errors.New("only server admins can change name servers and zone transfers")
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(f.DNS.UpdateZone(ctx, pathAcct(r), r.PathValue("z"), body))
	})
	h("DELETE "+base+"/{z}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		return ok(map[string]string{"deleted": r.PathValue("z")}, f.DNS.Delete(ctx, pathAcct(r), r.PathValue("z")))
	})
	h("POST "+base+"/{z}/records", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body dns.Record
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return created(f.DNS.AddRecord(ctx, pathAcct(r), r.PathValue("z"), body))
	})
	h("PUT "+base+"/{z}/records", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body []dns.Record
		if !decodeBig(w, r, &body, 2<<20) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(f.DNS.ReplaceRecords(ctx, pathAcct(r), r.PathValue("z"), body))
	})
	h("PUT "+base+"/{z}/records/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body dns.Record
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(f.DNS.UpdateRecord(ctx, pathAcct(r), r.PathValue("z"), r.PathValue("id"), body))
	})
	h("DELETE "+base+"/{z}/records/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		return ok(f.DNS.DeleteRecord(ctx, pathAcct(r), r.PathValue("z"), r.PathValue("id")))
	})
	h("POST "+base+"/{z}/template", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct{ Template, IPv4, IPv6 string }
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(f.DNS.ApplyTemplate(ctx, pathAcct(r), r.PathValue("z"), body.Template, body.IPv4, body.IPv6))
	})
	h("POST "+base+"/{z}/import", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			ZoneFile string `json:"zone_file"`
			Replace  bool   `json:"replace"`
		}
		if !decodeBig(w, r, &body, 2<<20) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		z, skipped, err := f.DNS.Import(ctx, pathAcct(r), r.PathValue("z"), body.ZoneFile, body.Replace)
		return ok(map[string]any{"zone": z, "skipped": skipped}, err)
	})
	h("GET "+base+"/{z}/export", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		text, err := f.DNS.Export(pathAcct(r), r.PathValue("z"))
		if err != nil {
			return 0, nil, err
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+r.PathValue("z")+`.zone"`)
		_, _ = w.Write([]byte(text))
		return 0, nil, nil
	})
	h("GET "+base+"/{z}/check", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(f.DNS.Check(r.Context(), pathAcct(r), r.PathValue("z")))
	})
	h("POST "+base+"/{z}/registration", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(f.DNS.RefreshRegistration(r.Context(), pathAcct(r), r.PathValue("z")))
	})
	h("POST "+base+"/{z}/pull", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		return ok(f.DNS.PullFromProvider(ctx, pathAcct(r), r.PathValue("z")))
	})
}

// decodeBig is decode with a larger body limit.
func decodeBig(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	return decodeLimit(w, r, v, limit)
}

var _ = strings.TrimSpace
