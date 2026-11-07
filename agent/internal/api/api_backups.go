package api

// Backup routes.
//
//	GET    /v1/backups                          every plan (admin)
//	POST   /v1/backups/recover                  {"target","repository","password","snapshot"} → job (new server)
//	GET    /v1/accounts/{a}/backups, POST       plans of one account
//	(admin plans use /v1/backups/plans; account plans /v1/accounts/{a}/backups/plans)
//	POST   .../plans                            create → recovery key once
//	PUT    .../plans/{id}, DELETE .../plans/{id}
//	POST   .../plans/{id}/run                   → job
//	POST   .../plans/{id}/check                 integrity check → job
//	GET    .../plans/{id}/snapshots
//	GET    .../plans/{id}/snapshots/{snap}/browse?path=
//	POST   .../plans/{id}/restore               {"snapshot","paths","databases","overwrite"} → job
//	GET    .../plans/{id}/recovery-key          repository and password (admin plans)

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/backups"
	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/kit"
)

func (s *Server) backupSources() backups.Sources {
	f := s.F
	return backups.Sources{
		SiteDir: func(domain string) (string, string, error) {
			site, err := s.Sites.Get(domain)
			if err != nil {
				return "", "", err
			}
			return filepath.Join(s.Config.HomeBase, site.Account, "web", site.Domain), site.Account, nil
		},
		Databases: func(account string) []string {
			accts := []string{account}
			if account == "" {
				accts = s.accountNames()
			}
			var out []string
			for _, a := range accts {
				if dbs, err := s.Sites.Databases(a); err == nil {
					for _, d := range dbs {
						out = append(out, "mariadb:"+d.Name)
					}
				}
				out = append(out, f.DB.Names(a)...)
			}
			return out
		},
		Dump: func(ctx context.Context, engine, name string, w *os.File) error {
			acct, _, _ := strings.Cut(name, "_")
			return f.DB.Export(ctx, acct, engine, name, w)
		},
		MailDirs: func(account string) []string {
			if !f.Mail.Installed() {
				return nil
			}
			if account == "" {
				return []string{s.Config.MailDir}
			}
			doms, _ := f.Mail.Domains(account)
			var out []string
			for _, d := range doms {
				out = append(out, filepath.Join(s.Config.MailDir, d.Domain.Domain))
			}
			return out
		},
		ImportDump: func(ctx context.Context, account, engine, name string, file *os.File) error {
			return f.DB.Import(ctx, account, engine, name, file)
		},
		AfterServerRestore: func(ctx context.Context, log func(string, ...any)) error {
			for _, a := range s.accountNames() {
				log("Account %s", a)
				if err := s.Sites.EnsureAccountUser(ctx, a); err != nil {
					return err
				}
			}
			log("Writing web server, PHP and app configuration")
			if _, err := s.Sites.RebuildAll(ctx); err != nil {
				log("Some configuration couldn't be rebuilt: %v", err)
			}
			if err := f.Mail.Rebuild(ctx); err != nil {
				log("Mail configuration: %v", err)
			}
			if err := f.DNS.RepublishAll(ctx); err != nil {
				log("DNS zones: %v", err)
			}
			return nil
		},
	}
}

func (s *Server) backupRoutes(h func(string, handler)) {
	b := s.F.Backups
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	admin := func(fn handler) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if !isAdmin(r) {
				return 0, nil, kit.ErrForbidden
			}
			return fn(w, r)
		}
	}
	h("POST /v1/backups/recover", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body backups.RecoverRequest
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return s.job("backup-recover", "Restore this server from a backup", 48*time.Hour, func(ctx context.Context, log jobs.Logger) error {
			return b.Recover(ctx, body, log)
		})
	}))
	routes := func(prefix string, acct func(*http.Request) string, wrap func(handler) handler) {
		id := func(r *http.Request) string { return r.PathValue("id") }
		h("GET "+prefix, wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) { return ok(b.Plans(acct(r))) }))
		h("POST "+prefix, wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			var body backups.PlanRequest
			if !decode(w, r, &body) {
				return 0, nil, nil
			}
			body.ID = ""
			v, err := b.Save(r.Context(), acct(r), actor(r), body)
			return http.StatusCreated, v, err
		}))
		h("PUT "+prefix+"/{id}", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			var body backups.PlanRequest
			if !decode(w, r, &body) {
				return 0, nil, nil
			}
			body.ID = id(r)
			return ok(b.Save(r.Context(), acct(r), actor(r), body))
		}))
		h("DELETE "+prefix+"/{id}", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			return ok(map[string]string{"deleted": id(r)}, b.Delete(acct(r), id(r)))
		}))
		h("POST "+prefix+"/{id}/run", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			a, pid := acct(r), id(r)
			return s.job("backup:"+pid, "Backup", 24*time.Hour, func(ctx context.Context, log jobs.Logger) error {
				_, err := b.Run(ctx, a, pid, log)
				return err
			})
		}))
		h("POST "+prefix+"/{id}/check", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			a, pid := acct(r), id(r)
			return s.job("backup-check:"+pid, "Backup integrity check", 24*time.Hour, func(ctx context.Context, log jobs.Logger) error {
				return b.Check(ctx, a, pid, log)
			})
		}))
		h("GET "+prefix+"/{id}/snapshots", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			return ok(b.Snapshots(r.Context(), acct(r), id(r)))
		}))
		h("GET "+prefix+"/{id}/snapshots/{snap}/browse", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			return ok(b.Browse(r.Context(), acct(r), id(r), r.PathValue("snap"), r.URL.Query().Get("path")))
		}))
		h("POST "+prefix+"/{id}/restore", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			var body backups.RestoreRequest
			if !decode(w, r, &body) {
				return 0, nil, nil
			}
			a, pid := acct(r), id(r)
			return s.job("restore:"+pid, "Restore from backup", 24*time.Hour, func(ctx context.Context, log jobs.Logger) error {
				return b.Restore(ctx, a, pid, body, log)
			})
		}))
		h("GET "+prefix+"/{id}/recovery-key", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if acct(r) != "" && !isAdmin(r) {
				return 0, nil, errors.New("ask the server admin for this plan's recovery key")
			}
			return ok(b.RecoveryKey(acct(r), id(r)))
		}))
	}
	routes("/v1/backups/plans", func(*http.Request) string { return "" }, admin)
	routes("/v1/accounts/{a}/backups/plans", pathAcct, func(fn handler) handler { return fn })
}
