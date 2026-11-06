// Package api serves the agent's JSON API over a unix socket.
//
//	GET    /v1/health
//	GET    /v1/system                          server facts + installed runtimes
//	GET    /v1/services
//	GET    /v1/runtimes                        PHP / Node.js / Python versions and web server choices
//
//	GET    /v1/accounts
//	POST   /v1/accounts                        {"name"}
//	GET    /v1/accounts/{name}
//	DELETE /v1/accounts/{name}?with_sites=true
//	POST   /v1/accounts/{name}/suspend         {"reason"}
//	POST   /v1/accounts/{name}/unsuspend
//	PUT    /v1/accounts/{name}/ssh             {"mode": "none"|"sftp"|"shell"}
//	GET    /v1/accounts/{name}/ssh/keys
//	POST   /v1/accounts/{name}/ssh/keys        {"public_key","access","allow_from","expires"}
//	PUT    /v1/accounts/{name}/ssh/keys?fingerprint=   {"access":"|sftp|sftp-readonly","allow_from":[],"expires":"YYYY-MM-DD"}
//	DELETE /v1/accounts/{name}/ssh/keys?fingerprint=SHA256:...
//	GET    /v1/accounts/{name}/cron
//	POST   /v1/accounts/{name}/cron            {"schedule","command","comment","enabled"}
//	PUT    /v1/accounts/{name}/cron/{id}
//	DELETE /v1/accounts/{name}/cron/{id}
//
//	GET    /v1/sites?account=name
//	POST   /v1/sites                           {"domain","account","runtime","version","web_server","start_command"}
//	GET    /v1/sites/{domain}
//	PATCH  /v1/sites/{domain}                  {"version","web_server","start_command"}
//	DELETE /v1/sites/{domain}?remove_files=true
//	PUT    /v1/sites/{domain}/status           {"status": "active"|"maintenance"|"suspended", "allow_ips": []}
//	GET    /v1/sites/{domain}/app              app state (Node.js/Python)
//	POST   /v1/sites/{domain}/app/{action}     start | stop | restart
//	GET    /v1/sites/{domain}/app/logs?lines=200
//
//	GET    /v1/accounts/{name}/databases
//	POST   /v1/accounts/{name}/databases       {"name","note"} → password shown once
//	POST   /v1/accounts/{name}/databases/{db}/password   new random password
//	DELETE /v1/accounts/{name}/databases/{db}
//
//	GET    /v1/apps                            one-click app catalog
//	POST   /v1/sites/{domain}/apps/wordpress   {"title","admin_user","admin_email","locale"}
//	POST   /v1/sites/{domain}/permissions      reset ownership and modes
//	GET    /v1/sites/{domain}/logs/{kind}?lines=   access | error | php | apache-access | apache-error | app
//
//	GET    /v1/audit?lines=200                 every change made through the agent
//
//	GET    /v1/updates                         pending Ubuntu updates (refreshes package lists)
//	POST   /v1/updates/apply                   {"security_only": bool} → background job
//	GET    /v1/updates/auto                    automatic security update settings
//	PUT    /v1/updates/auto                    {"security_updates","auto_reboot","reboot_time"}
//	POST   /v1/server/reboot                   {"in_minutes": 5}
//	DELETE /v1/server/reboot                   cancel a scheduled reboot
//	GET    /v1/ike/release                     newest Ike release vs running version
//	POST   /v1/ike/update                      download, verify, install, restart → job
//	GET    /v1/jobs, GET /v1/jobs/{id}         background job status and log
//
//	GET    /v1/sites/{domain}/diagnose         health assistant: plain-language findings
//	POST   /v1/sites/{domain}/staging          clone to staging.<domain> → job
//	POST   /v1/sites/{domain}/push             push a staging copy's files to live → job
//	PUT    /v1/sites/{domain}/healthcheck      {"path","interval_sec","fail_threshold","action"} or null
//	GET    /v1/health/events                   app health monitor events
//	GET    /v1/sites/{domain}/env              .env variables
//	PUT    /v1/sites/{domain}/env              [{"key","value"}]
//	PUT    /v1/accounts/{name}/limits          {"cpu_percent","memory_mb","max_processes","disk_mb","php_workers"}
//	GET    /v1/server/metrics                  server CPU, memory, swap, network per interface, disk I/O (+24 h history)
//	GET    /v1/accounts/{name}/sftp-users  website SFTP users (each locked to one website folder)
//	POST   /v1/accounts/{name}/sftp-users  {"name","domain","folder","read_only","password","public_key","expires"} → password once
//	PATCH  /v1/accounts/{name}/sftp-users/{user}  {"password" ("generate"|""),"public_key","keys","read_only","folder","expires"}
//	DELETE /v1/accounts/{name}/sftp-users/{user}
//	GET    /v1/usage                           per-account CPU, memory, disk, bandwidth (+24 h history)
//	GET    /v1/security                        security baseline scan
//	GET    /v1/drift                           managed config files changed by hand
//	POST   /v1/drift/reconcile                 {"paths": [...]} restore them (all when empty)
//	GET    /v1/imports/cpanel/{file}           inspect a cPanel backup in the imports folder
//
//	GET    /v1/firewall                        the firewall in use (ufw|nftables|firewalld), open ports + blocked addresses
//	POST   /v1/firewall/ports                  {"port","proto","comment","from"}
//	DELETE /v1/firewall/ports?port=&proto=[&from=]
//	POST   /v1/firewall/blocks                 {"address","comment"}
//	DELETE /v1/firewall/blocks?address=
//	GET    /v1/firewall/bans                   fail2ban bans
//	DELETE /v1/firewall/bans?jail=&ip=
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/YOUR-ORG/ike/agent/internal/version"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/firewall"
	"github.com/YOUR-ORG/ike/agent/internal/jobs"
	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/sites"
	"github.com/YOUR-ORG/ike/agent/internal/sysinfo"
	"github.com/YOUR-ORG/ike/agent/internal/updates"
)

const Version = version.Version

type Server struct {
	routes []string // every pattern, for the API reference (Routes)

	Sites     *sites.Manager
	Metrics   *sysinfo.Sampler // server CPU, memory, swap, network and disk history
	Updates   *updates.Manager
	Jobs      *jobs.Manager
	Firewall  *firewall.Manager
	Runner    runner.Runner
	Config    config.Config
	AuditPath string // JSON lines; empty disables
	F         *Features

	auditMu sync.Mutex
}

func (s *Server) runtimes() map[string][]string {
	return sites.InstalledRuntimes(s.Config.Root, s.Config.RuntimesDir)
}

// long gives changes their own deadline so a dropped client can't leave them half done.
func long(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
}

// Routes lists every route pattern ("GET /v1/sites/{domain}"), after Handler.
func (s *Server) Routes() []string { return s.routes }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(w http.ResponseWriter, r *http.Request) (int, any, error)) {
		s.routes = append(s.routes, pattern)
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			code, v, err := fn(w, r)
			if err != nil {
				writeErr(w, err)
				return
			}
			if code == 0 {
				return // fn already wrote a response
			}
			writeJSON(w, code, v)
		})
	}
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	created := func(v any, err error) (int, any, error) { return http.StatusCreated, v, err }

	// ---- server
	h("GET /v1/health", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"status": "ok", "version": Version}, nil)
	})
	h("GET /v1/system", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(sysinfo.Collect(s.Config.Root, s.runtimes()), nil)
	})
	h("GET /v1/services", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(sysinfo.Services(r.Context(), s.Runner, s.Config.Root, s.Config.WebStack), nil)
	})
	h("GET /v1/runtimes", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		rt := s.runtimes()
		exists := func(p string) bool { _, err := os.Stat(filepath.Join(s.Config.Root, p)); return err == nil }
		return ok(map[string]any{
			"php": sysinfo.PHPVersions(s.Config.Root), "node": rt["node"], "python": rt["python"],
			"java": rt["java"], "go": rt["go"], "dart": rt["dart"],
			"default_php": s.Config.DefaultPHP, "web_stack": s.Config.WebStack, "web_servers": s.Config.WebServers(),
			"composer": exists(s.Config.ComposerPath), "docker": exists(s.Config.DockerSocket),
			"docker_for_accounts": s.Config.DockerForAccounts, "buildpacks": exists(s.Config.PackPath),
			"kubernetes": exists(s.Config.K3sPath), "web": s.Sites.Features(),
		}, nil)
	})

	// ---- accounts
	h("GET /v1/accounts", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Accounts())
	})
	h("POST /v1/accounts", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return created(s.Sites.CreateAccount(ctx, body.Name))
	})
	h("GET /v1/accounts/{name}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.GetAccount(r.PathValue("name")))
	})
	h("DELETE /v1/accounts/{name}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		err := s.Sites.DeleteAccount(ctx, r.PathValue("name"), r.URL.Query().Get("with_sites") == "true")
		return ok(map[string]string{"deleted": r.PathValue("name")}, err)
	})
	h("POST /v1/accounts/{name}/suspend", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Reason string `json:"reason"`
		}
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.SuspendAccount(ctx, r.PathValue("name"), body.Reason))
	})
	h("POST /v1/accounts/{name}/unsuspend", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.UnsuspendAccount(ctx, r.PathValue("name")))
	})
	h("PUT /v1/accounts/{name}/ssh", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Mode string `json:"mode"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(s.Sites.SetSSHMode(r.Context(), r.PathValue("name"), body.Mode))
	})
	// Website SFTP users
	h("GET /v1/accounts/{name}/sftp-users", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.ListSFTPUsers(r.PathValue("name")))
	})
	h("POST /v1/accounts/{name}/sftp-users", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body sites.SFTPUserRequest
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		u, made, err := s.Sites.CreateSFTPUser(r.Context(), r.PathValue("name"), body)
		if err != nil {
			return ok(nil, err)
		}
		return created(map[string]any{"user": u, "password": made, "host": s.Config.Hostname}, nil)
	})
	h("PATCH /v1/accounts/{name}/sftp-users/{user}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body sites.SFTPUserRequest
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		u, made, err := s.Sites.UpdateSFTPUser(r.Context(), r.PathValue("name"), r.PathValue("user"), body)
		if err != nil {
			return ok(nil, err)
		}
		return ok(map[string]any{"user": u, "password": made}, nil)
	})
	h("DELETE /v1/accounts/{name}/sftp-users/{user}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("user")}, s.Sites.DeleteSFTPUser(r.Context(), r.PathValue("name"), r.PathValue("user")))
	})
	h("GET /v1/accounts/{name}/ssh/keys", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.ListSSHKeys(r.PathValue("name")))
	})
	h("POST /v1/accounts/{name}/ssh/keys", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			PublicKey string `json:"public_key"`
			sites.KeyAccess
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(s.Sites.AddSSHKey(r.PathValue("name"), body.PublicKey, body.KeyAccess))
	})
	h("PUT /v1/accounts/{name}/ssh/keys", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body sites.KeyAccess
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(s.Sites.SetKeyAccess(r.PathValue("name"), r.URL.Query().Get("fingerprint"), body))
	})
	h("DELETE /v1/accounts/{name}/ssh/keys", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		fp := r.URL.Query().Get("fingerprint")
		return ok(map[string]string{"removed": fp}, s.Sites.RemoveSSHKey(r.PathValue("name"), fp))
	})
	h("GET /v1/accounts/{name}/cron", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.CronJobs(r.PathValue("name")))
	})
	h("POST /v1/accounts/{name}/cron", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.CronRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		return created(s.Sites.AddCronJob(r.PathValue("name"), req))
	})
	h("PUT /v1/accounts/{name}/cron/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.CronRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		return ok(s.Sites.UpdateCronJob(r.PathValue("name"), r.PathValue("id"), req))
	})
	h("DELETE /v1/accounts/{name}/cron/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("id")}, s.Sites.DeleteCronJob(r.PathValue("name"), r.PathValue("id")))
	})

	// ---- websites
	h("GET /v1/sites", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		list, err := s.Sites.List()
		if err != nil {
			return 0, nil, err
		}
		if acct := r.URL.Query().Get("account"); acct != "" {
			out := []sites.Site{}
			for _, x := range list {
				if x.Account == acct {
					out = append(out, x)
				}
			}
			list = out
		}
		return ok(list, nil)
	})
	h("POST /v1/sites", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.CreateRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return created(s.Sites.Create(ctx, req))
	})
	h("GET /v1/sites/{domain}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Get(r.PathValue("domain")))
	})
	h("PATCH /v1/sites/{domain}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.UpdateRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.Update(ctx, r.PathValue("domain"), req))
	})
	h("DELETE /v1/sites/{domain}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		rm := r.URL.Query().Get("remove_files") == "true"
		err := s.Sites.Delete(ctx, r.PathValue("domain"), rm)
		return ok(map[string]any{"deleted": r.PathValue("domain"), "files_removed": rm}, err)
	})
	h("PUT /v1/sites/{domain}/status", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.SetStatusRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.SetStatus(ctx, r.PathValue("domain"), req))
	})
	h("GET /v1/sites/{domain}/app", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		state, err := s.Sites.AppStatus(r.Context(), r.PathValue("domain"))
		return ok(map[string]string{"state": state}, err)
	})
	h("POST /v1/sites/{domain}/app/{action}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		err := s.Sites.AppAction(r.Context(), r.PathValue("domain"), r.PathValue("action"))
		return ok(map[string]string{"done": r.PathValue("action")}, err)
	})
	h("GET /v1/sites/{domain}/app/logs", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		logs, err := s.Sites.AppLogs(r.Context(), r.PathValue("domain"), n)
		return ok(map[string]string{"logs": logs}, err)
	})

	// ---- databases
	h("GET /v1/accounts/{name}/databases", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Databases(r.PathValue("name")))
	})
	h("POST /v1/accounts/{name}/databases", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct{ Name, Note string }
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(s.Sites.CreateDatabase(r.Context(), r.PathValue("name"), body.Name, body.Note))
	})
	h("POST /v1/accounts/{name}/databases/{db}/password", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.ResetDatabasePassword(r.Context(), r.PathValue("name"), r.PathValue("db")))
	})
	h("DELETE /v1/accounts/{name}/databases/{db}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("db")}, s.Sites.DeleteDatabase(r.Context(), r.PathValue("name"), r.PathValue("db")))
	})

	// ---- apps, permissions, logs
	h("GET /v1/apps", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(sites.Catalog, nil)
	})
	h("POST /v1/sites/{domain}/apps/wordpress", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.WordPressRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return created(s.Sites.InstallWordPress(ctx, r.PathValue("domain"), req))
	})
	h("POST /v1/sites/{domain}/permissions", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.FixPermissions(ctx, r.PathValue("domain")))
	})
	h("GET /v1/sites/{domain}/logs/{kind}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		var text string
		var err error
		if r.PathValue("kind") == "app" {
			text, err = s.Sites.AppLogs(r.Context(), r.PathValue("domain"), n)
		} else {
			text, err = s.Sites.SiteLog(r.PathValue("domain"), r.PathValue("kind"), n)
		}
		return ok(map[string]string{"log": text}, err)
	})
	h("GET /v1/audit", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		return ok(s.readAudit(n))
	})

	// ---- updates and jobs
	h("GET /v1/updates", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Updates.Check(ctx))
	})
	h("POST /v1/updates/apply", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			SecurityOnly bool `json:"security_only"`
		}
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return 0, nil, nil
		}
		j, err := s.Updates.Apply(body.SecurityOnly)
		return http.StatusAccepted, j, err
	})
	h("GET /v1/updates/auto", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Updates.AutoSettings(), nil)
	})
	h("PUT /v1/updates/auto", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body updates.AutoSettings
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(body, s.Updates.SetAutoSettings(body))
	})
	h("POST /v1/server/reboot", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			InMinutes int `json:"in_minutes"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(map[string]int{"reboot_in_minutes": body.InMinutes}, s.Updates.ScheduleReboot(r.Context(), body.InMinutes))
	})
	h("DELETE /v1/server/reboot", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]bool{"cancelled": true}, s.Updates.CancelReboot(r.Context()))
	})
	h("GET /v1/ike/release", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Updates.CheckIke(r.Context()))
	})
	h("POST /v1/ike/update", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		j, err := s.Updates.UpdateIke(r.Context())
		return http.StatusAccepted, j, err
	})
	h("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Jobs.List(), nil)
	})
	h("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		j, found := s.Jobs.Get(r.PathValue("id"))
		if !found {
			return 0, nil, fmt.Errorf("job %w", sites.ErrNotFound)
		}
		return ok(j, nil)
	})

	// ---- health, staging, limits, env, usage, security, drift, imports
	h("GET /v1/sites/{domain}/diagnose", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Diagnose(r.Context(), r.PathValue("domain")))
	})
	h("POST /v1/sites/{domain}/staging", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		d := r.PathValue("domain")
		if _, err := s.Sites.Get(d); err != nil {
			return 0, nil, err
		}
		j, err := s.Jobs.Start("staging:"+d, "Create a staging copy of "+d, 2*time.Hour, func(ctx context.Context, log jobs.Logger) error {
			res, err := s.Sites.CreateStaging(ctx, d, log)
			if err == nil {
				log("Staging copy ready at %s", res.Staging)
				if res.Note != "" {
					log("%s", res.Note)
				}
			}
			return err
		})
		return http.StatusAccepted, j, err
	})
	h("POST /v1/sites/{domain}/push", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		d := r.PathValue("domain")
		j, err := s.Jobs.Start("push:"+d, "Push "+d+" to live", time.Hour, func(ctx context.Context, log jobs.Logger) error {
			backup, err := s.Sites.PushStaging(ctx, d, log)
			if err == nil {
				log("Done. The previous live files are saved in %s", backup)
			}
			return err
		})
		return http.StatusAccepted, j, err
	})
	h("PUT /v1/sites/{domain}/healthcheck", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var hc *sites.HealthCheck
		if !decode(w, r, &hc) {
			return 0, nil, nil
		}
		return ok(s.Sites.SetHealthCheck(r.PathValue("domain"), hc))
	})
	h("GET /v1/health/events", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.HealthEvents(), nil)
	})
	h("GET /v1/sites/{domain}/env", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.GetEnv(r.PathValue("domain")))
	})
	h("PUT /v1/sites/{domain}/env", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var vars []sites.EnvVar
		if !decode(w, r, &vars) {
			return 0, nil, nil
		}
		return ok(map[string]int{"saved": len(vars)}, s.Sites.SetEnv(r.Context(), r.PathValue("domain"), vars))
	})
	h("PUT /v1/accounts/{name}/limits", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var l sites.Limits
		if !decode(w, r, &l) {
			return 0, nil, nil
		}
		return ok(s.Sites.SetLimits(r.Context(), r.PathValue("name"), l))
	})
	h("GET /v1/server/metrics", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if s.Metrics == nil {
			return ok(sysinfo.Metrics{History: []sysinfo.Point{}, Interfaces: []sysinfo.Interface{}}, nil)
		}
		return ok(s.Metrics.Metrics(), nil)
	})
	h("GET /v1/usage", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Usage(), nil)
	})
	h("GET /v1/security", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.SecurityScan(r.Context()), nil)
	})
	h("GET /v1/drift", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.DetectDrift(), nil)
	})
	h("POST /v1/drift/reconcile", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Paths []string `json:"paths"`
		}
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.Reconcile(ctx, body.Paths))
	})
	h("GET /v1/imports/cpanel/{file}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.InspectCpanelBackup(r.PathValue("file")))
	})

	// ---- firewall
	h("GET /v1/firewall", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Firewall.Status())
	})
	h("POST /v1/firewall/ports", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct{ Port, Proto, Comment, From string }
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(s.Firewall.AllowFrom(r.Context(), body.From, body.Port, body.Proto, body.Comment))
	})
	h("DELETE /v1/firewall/ports", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		q := r.URL.Query()
		return ok(map[string]string{"closed": q.Get("port") + "/" + q.Get("proto")},
			s.Firewall.ClosePort(r.Context(), q.Get("port"), q.Get("proto"), q.Get("from")))
	})
	h("POST /v1/firewall/blocks", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct{ Address, Comment string }
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(s.Firewall.BlockAddress(r.Context(), body.Address, body.Comment))
	})
	h("DELETE /v1/firewall/blocks", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		a := r.URL.Query().Get("address")
		return ok(map[string]string{"unblocked": a}, s.Firewall.UnblockAddress(r.Context(), a))
	})
	h("GET /v1/firewall/bans", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Firewall.Bans(r.Context()))
	})
	h("DELETE /v1/firewall/bans", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		q := r.URL.Query()
		return ok(map[string]string{"unbanned": q.Get("ip")}, s.Firewall.Unban(r.Context(), q.Get("jail"), q.Get("ip")))
	})

	s.moreRoutes(func(p string, fn handler) { h(p, fn) })
	s.v5Routes(func(p string, fn handler) { h(p, fn) })
	if s.Firewall != nil {
		sites.SetPublicPortHook(func(ctx context.Context, port int, proto string) {
			_, _ = s.Firewall.OpenPort(ctx, strconv.Itoa(port), proto, "container")
		})
	}
	return s.audit(logRequests(mux))
}

// statusWriter records the response code for the audit log.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }

// audit appends one JSON line per change (anything but GET). Request bodies are
// not logged, so passwords and keys never reach the audit file. The caller says
// who it acts for in X-Ike-Actor (e.g. "andre", "reseller:acme", "ai:claude").
func (s *Server) audit(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || s.AuditPath == "" {
			h.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		start := time.Now()
		h.ServeHTTP(sw, r)
		actor := r.Header.Get("X-Ike-Actor")
		if len(actor) > 64 {
			actor = actor[:64]
		}
		if actor == "" {
			actor = "local"
		}
		result := "ok"
		if sw.code >= 400 {
			result = "error"
		}
		line, _ := json.Marshal(map[string]any{
			"time": start.UTC().Format(time.RFC3339), "actor": actor, "method": r.Method, "path": r.URL.Path,
			"query": r.URL.RawQuery, "status": sw.code, "result": result, "ms": time.Since(start).Milliseconds(),
		})
		s.auditMu.Lock()
		defer s.auditMu.Unlock()
		f, err := os.OpenFile(s.AuditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			slog.Error("audit log unavailable", "err", err)
			return
		}
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	})
}

func (s *Server) readAudit(lines int) ([]map[string]any, error) {
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	b, err := os.ReadFile(s.AuditPath)
	if errors.Is(err, os.ErrNotExist) || s.AuditPath == "" {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	all := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	out := make([]map[string]any, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- { // newest first
		var e map[string]any
		if json.Unmarshal([]byte(all[i]), &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeLimit(w, r, v, 64<<10)
}

func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// An empty list is [], not null, so clients can always iterate it.
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		_, _ = w.Write([]byte("[]\n"))
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, sites.ErrNotFound), errors.Is(err, firewall.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, sites.ErrExists), errors.Is(err, firewall.ErrExists):
		code = http.StatusConflict
	case errors.Is(err, kit.ErrForbidden):
		code = http.StatusForbidden
	case errors.Is(err, kit.ErrNotInstalled):
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "took", time.Since(start).String())
	})
}
