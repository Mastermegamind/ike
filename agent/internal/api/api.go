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
//	POST   /v1/accounts/{name}/ssh/keys        {"public_key"}
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
//	GET    /v1/firewall                        open ports + blocked addresses
//	POST   /v1/firewall/ports                  {"port","proto","comment"}
//	DELETE /v1/firewall/ports?port=&proto=
//	POST   /v1/firewall/blocks                 {"address","comment"}
//	DELETE /v1/firewall/blocks?address=
//	GET    /v1/firewall/bans                   fail2ban bans
//	DELETE /v1/firewall/bans?jail=&ip=
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/firewall"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/sites"
	"github.com/YOUR-ORG/ike/agent/internal/sysinfo"
)

const Version = "0.2.0"

type Server struct {
	Sites     *sites.Manager
	Firewall  *firewall.Manager
	Runner    runner.Runner
	Config    config.Config
	AuditPath string // JSON lines; empty disables

	auditMu sync.Mutex
}

func (s *Server) runtimes() map[string][]string {
	return sites.InstalledRuntimes(s.Config.Root, s.Config.RuntimesDir)
}

// long gives changes their own deadline so a dropped client can't leave them half done.
func long(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(w http.ResponseWriter, r *http.Request) (int, any, error)) {
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
		return ok(map[string]any{
			"php": sysinfo.PHPVersions(s.Config.Root), "node": rt["node"], "python": rt["python"],
			"default_php": s.Config.DefaultPHP, "web_stack": s.Config.WebStack, "web_servers": s.Config.WebServers(),
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
	h("GET /v1/accounts/{name}/ssh/keys", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.ListSSHKeys(r.PathValue("name")))
	})
	h("POST /v1/accounts/{name}/ssh/keys", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			PublicKey string `json:"public_key"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(s.Sites.AddSSHKey(r.PathValue("name"), body.PublicKey))
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

	// ---- firewall
	h("GET /v1/firewall", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Firewall.Status())
	})
	h("POST /v1/firewall/ports", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct{ Port, Proto, Comment string }
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(s.Firewall.OpenPort(r.Context(), body.Port, body.Proto, body.Comment))
	})
	h("DELETE /v1/firewall/ports", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		q := r.URL.Query()
		return ok(map[string]string{"closed": q.Get("port") + "/" + q.Get("proto")},
			s.Firewall.ClosePort(r.Context(), q.Get("port"), q.Get("proto")))
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
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
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
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, sites.ErrNotFound), errors.Is(err, firewall.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, sites.ErrExists), errors.Is(err, firewall.ErrExists):
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
