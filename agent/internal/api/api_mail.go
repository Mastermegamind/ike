package api

// Email routes.
//
//	GET    /v1/mail, PUT                       server mail settings (relay, limits, spam), relay presets
//	GET    /v1/mail/domains                    every email domain
//	GET    /v1/mail/queue, POST /v1/mail/queue/{action}?id=   flush | retry | hold | release | delete
//	GET    /v1/mail/log?search=&limit=          delivery log
//
//	GET    /v1/accounts/{a}/mail/domains, POST {"domain"}
//	GET    /v1/accounts/{a}/mail/domains/{d}, PATCH {"enabled","catch_all"}, DELETE ?delete_mail=true
//	POST   /v1/accounts/{a}/mail/domains/{d}/mailboxes            {"local","name","password","quota_mb","send_only"}
//	PUT    /v1/accounts/{a}/mail/domains/{d}/mailboxes/{local}    {"name","password","quota_mb","enabled","forward","keep_copy","auto_reply"}
//	DELETE /v1/accounts/{a}/mail/domains/{d}/mailboxes/{local}?delete_mail=true
//	GET    /v1/accounts/{a}/mail/domains/{d}/mailboxes/{local}/export   .tar.gz
//	POST   /v1/accounts/{a}/mail/domains/{d}/mailboxes/{local}/import   .tar.gz body
//	PUT    /v1/accounts/{a}/mail/domains/{d}/aliases              [{"from","to"}]
//	GET    /v1/accounts/{a}/mail/domains/{d}/records              DNS records mail needs
//	POST   /v1/accounts/{a}/mail/domains/{d}/dns                  add them to the zone here
//	GET    /v1/accounts/{a}/mail/domains/{d}/checks               deliverability
//	POST   /v1/accounts/{a}/mail/domains/{d}/tls                  certificate for mail.<d> → job
//	POST   /v1/accounts/{a}/mail/domains/{d}/dkim/rotate
//	POST   /v1/accounts/{a}/mail/domains/{d}/usage                refresh mailbox usage
//	GET    /v1/accounts/{a}/mail/queue, POST /v1/accounts/{a}/mail/queue/{action}?id=
//	GET    /v1/accounts/{a}/mail/log?search=

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/mail"
)

func (s *Server) mailRoutes(h func(string, handler)) {
	m := s.F.Mail
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	created := func(v any, err error) (int, any, error) { return http.StatusCreated, v, err }
	admin := func(fn handler) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if !isAdmin(r) {
				return 0, nil, kit.ErrForbidden
			}
			return fn(w, r)
		}
	}
	h("GET /v1/mail", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		set, err := m.PublicSettings()
		out := map[string]any{"installed": set.Installed, "webmail": set.Webmail, "webmail_port": set.WebmailPort}
		if isAdmin(r) {
			out["settings"], out["relay_presets"] = set, mail.RelayPresets
		}
		return ok(out, err)
	})
	h("PUT /v1/mail", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body mail.SettingsUpdate
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(m.UpdateSettings(ctx, body))
	}))
	h("GET /v1/mail/domains", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(m.Domains(""))
	}))
	queue := func(account func(*http.Request) string) {
		prefix := "/v1/mail"
		if account != nil {
			prefix = "/v1/accounts/{a}/mail"
		}
		acct := func(r *http.Request) string {
			if account == nil {
				return ""
			}
			return account(r)
		}
		wrap := func(fn handler) handler {
			if account == nil {
				return admin(fn)
			}
			return fn
		}
		h("GET "+prefix+"/queue", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			return ok(m.Queue(r.Context(), acct(r)))
		}))
		h("POST "+prefix+"/queue/{action}", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			id := r.URL.Query().Get("id")
			return ok(map[string]string{"action": r.PathValue("action"), "id": id}, m.QueueAction(r.Context(), acct(r), r.PathValue("action"), id))
		}))
		h("GET "+prefix+"/log", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			return ok(m.Deliveries(acct(r), r.URL.Query().Get("search"), limit))
		}))
	}
	queue(nil)
	queue(pathAcct)

	base := "/v1/accounts/{a}/mail/domains"
	dom := func(r *http.Request) string { return r.PathValue("d") }
	h("GET "+base, func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(m.Domains(pathAcct(r)))
	})
	h("POST "+base, func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Domain string `json:"domain"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return created(m.AddDomain(ctx, pathAcct(r), body.Domain))
	})
	h("GET "+base+"/{d}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(m.Domain(pathAcct(r), dom(r)))
	})
	h("PATCH "+base+"/{d}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body mail.DomainOptions
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(m.UpdateDomain(r.Context(), pathAcct(r), dom(r), body))
	})
	h("DELETE "+base+"/{d}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		del := r.URL.Query().Get("delete_mail") == "true"
		return ok(map[string]string{"deleted": dom(r)}, m.RemoveDomain(r.Context(), pathAcct(r), dom(r), del))
	})
	h("POST "+base+"/{d}/mailboxes", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body mail.NewMailbox
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(m.CreateMailbox(r.Context(), pathAcct(r), dom(r), body))
	})
	h("PUT "+base+"/{d}/mailboxes/{local}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body mail.MailboxUpdate
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(m.UpdateMailbox(r.Context(), pathAcct(r), dom(r), r.PathValue("local"), body))
	})
	h("DELETE "+base+"/{d}/mailboxes/{local}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		del := r.URL.Query().Get("delete_mail") == "true"
		return ok(map[string]string{"deleted": r.PathValue("local") + "@" + dom(r)},
			m.DeleteMailbox(r.Context(), pathAcct(r), dom(r), r.PathValue("local"), del))
	})
	h("GET "+base+"/{d}/mailboxes/{local}/export", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		local := r.PathValue("local")
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+local+"@"+dom(r)+`.tar.gz"`)
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
		defer cancel()
		if err := m.ExportMailbox(ctx, pathAcct(r), dom(r), local, w); err != nil {
			return 0, nil, err
		}
		return 0, nil, nil
	})
	h("POST "+base+"/{d}/mailboxes/{local}/import", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Hour)
		defer cancel()
		body := http.MaxBytesReader(w, r.Body, 50<<30)
		return ok(map[string]string{"restored": r.PathValue("local") + "@" + dom(r)},
			m.ImportMailbox(ctx, pathAcct(r), dom(r), r.PathValue("local"), body))
	})
	h("PUT "+base+"/{d}/aliases", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body []mail.Alias
		if !decodeBig(w, r, &body, 1<<20) {
			return 0, nil, nil
		}
		return ok(m.SetAliases(r.Context(), pathAcct(r), dom(r), body))
	})
	h("GET "+base+"/{d}/records", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if _, err := m.Domain(pathAcct(r), dom(r)); err != nil {
			return 0, nil, err
		}
		return ok(m.Records(dom(r)))
	})
	h("POST "+base+"/{d}/dns", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		added, err := m.PublishDNS(ctx, pathAcct(r), dom(r))
		return ok(map[string]bool{"added": added}, err)
	})
	h("GET "+base+"/{d}/checks", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(m.Deliverability(r.Context(), pathAcct(r), dom(r)))
	})
	h("POST "+base+"/{d}/tls", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		acct, d := pathAcct(r), dom(r)
		if _, err := m.Domain(acct, d); err != nil {
			return 0, nil, err
		}
		useDNS := s.F.DNS.CanIssueWildcard("mail." + d)
		return s.job("mail-tls:"+d, "Certificate for mail."+d, 10*time.Minute, func(ctx context.Context, log jobs.Logger) error {
			_, err := m.IssueTLS(ctx, acct, d, useDNS, log)
			return err
		})
	})
	h("POST "+base+"/{d}/dkim/rotate", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		return ok(m.RotateDKIM(ctx, pathAcct(r), dom(r)))
	})
	h("POST "+base+"/{d}/usage", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(m.RefreshUsage(r.Context(), pathAcct(r), dom(r)))
	})
}
