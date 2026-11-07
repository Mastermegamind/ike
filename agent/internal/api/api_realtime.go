package api

// Real-time media (0.8): a TURN server (coturn) and LiveKit.
//
//	GET    /v1/realtime                        both servers: settings (no secrets), state, URLs, ports
//	PUT    /v1/realtime/turn                   {"enabled","domain","tls","user_quota","total_quota","max_kbps","allow_accounts"}
//	POST   /v1/realtime/turn/credentials       {"user","ttl_hours"} → short-lived TURN login
//	POST   /v1/realtime/turn/secret            new shared secret (old logins stop working)
//	PUT    /v1/realtime/livekit                {"enabled","domain","account","use_turn","empty_timeout","max_participants","webhook_urls","allow_accounts"}
//	POST   /v1/realtime/livekit/keys           {"name","account"} → key and secret, once
//	DELETE /v1/realtime/livekit/keys/{key}
//	POST   /v1/realtime/livekit/token          {"key","room","identity","name","ttl_minutes","can_publish","room_admin"}
//
//	GET    /v1/accounts/{a}/realtime                     LiveKit URL, the account's keys, TURN addresses
//	POST   /v1/accounts/{a}/realtime/turn-credentials    {"user","ttl_hours"}
//	POST   /v1/accounts/{a}/realtime/keys                {"name"}  (when the admin allows it)
//	DELETE /v1/accounts/{a}/realtime/keys/{key}
//	POST   /v1/accounts/{a}/realtime/token               same body as the admin's, with the account's keys

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/datasaver"
	"github.com/Mastermegamind/ike/agent/internal/dns"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/objects"
	"github.com/Mastermegamind/ike/agent/internal/realtime"
	"github.com/Mastermegamind/ike/agent/internal/sites"
)

// realtimeSite makes sure a website forwards domain to LiveKit, and tries to
// get it a certificate. A note explains when HTTPS has to wait.
func (s *Server) realtimeSite(ctx context.Context, account, domain string, port int) (string, error) {
	site, err := s.Sites.Get(domain)
	switch {
	case errors.Is(err, kit.ErrNotFound):
		if site, err = s.Sites.Create(ctx, sites.CreateRequest{Domain: domain, Account: account, Runtime: sites.RuntimeProxy, Port: port}); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	case site.Runtime != sites.RuntimeProxy || site.Port != port:
		return "", fmt.Errorf("%s is already a website for something else; pick another name for LiveKit", domain)
	}
	if site.SSL != nil {
		return "", nil
	}
	if _, err := s.Sites.IssueCertificate(ctx, domain, sites.IssueRequest{}, func(f string, a ...any) { slog.Debug(fmt.Sprintf(f, a...)) }); err != nil {
		return "HTTPS for " + domain + " isn't ready yet (" + err.Error() + "). Point the name at this server, then get a certificate in the website's Domains & HTTPS tab; apps need wss://.", nil
	}
	return "", nil
}

// expiryRoutes report what runs out: registered domains and certificates.
// The panel adds hosting terms and filters by who is asking.
//
//	GET    /v1/expiry/server                        {"domains": [...], "certificates": [...]}
//	PUT    /v1/expiry/domains/{domain}              {"auto_renew","years","days_before","manual_expires"}
//	POST   /v1/expiry/domains/{domain}/renew        {"years"} renew at the registrar now
//	POST   /v1/expiry/domains/{domain}/refresh      ask the registry now
func (s *Server) expiryRoutes(h func(string, handler)) {
	admin := func(fn handler) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if !isAdmin(r) {
				return 0, nil, kit.ErrForbidden
			}
			return fn(w, r)
		}
	}
	h("GET /v1/expiry/server", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		doms, err := s.F.DNS.DomainExpiries()
		return http.StatusOK, map[string]any{"domains": doms, "certificates": s.Sites.Certificates()}, err
	}))
	h("PUT /v1/expiry/domains/{domain}", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b dns.RenewalRequest
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		ren, err := s.F.DNS.SetRenewal(r.PathValue("domain"), b)
		return http.StatusOK, ren, err
	}))
	h("POST /v1/expiry/domains/{domain}/renew", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b struct {
			Years int `json:"years"`
		}
		if r.ContentLength != 0 && !decode(w, r, &b) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		res, err := s.F.DNS.RenewNow(ctx, r.PathValue("domain"), b.Years)
		return http.StatusOK, res, err
	}))
	h("POST /v1/expiry/domains/{domain}/refresh", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		e, err := s.F.DNS.RefreshDomain(ctx, r.PathValue("domain"))
		return http.StatusOK, e, err
	}))
}

// dataSaverRoutes: GET /v1/datasaver, PUT {"enabled","start","end","upload_kbps","download_kbps"}.
func (s *Server) dataSaverRoutes(h func(string, handler)) {
	h("GET /v1/datasaver", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if !isAdmin(r) {
			return 0, nil, kit.ErrForbidden
		}
		return http.StatusOK, s.F.DataSaver.Status(), nil
	})
	h("PUT /v1/datasaver", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if !isAdmin(r) {
			return 0, nil, kit.ErrForbidden
		}
		var b datasaver.Settings
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		st, err := s.F.DataSaver.Set(r.Context(), b)
		return http.StatusOK, st, err
	})
}

func (s *Server) realtimeRoutes(h func(string, handler)) {
	rt := s.F.Realtime
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
	type credReq struct {
		User     string `json:"user"`
		TTLHours int    `json:"ttl_hours"`
	}
	cred := func(account string) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			var b credReq
			if r.ContentLength != 0 && !decode(w, r, &b) {
				return 0, nil, nil
			}
			return ok(rt.TurnCredentials(account, b.User, time.Duration(b.TTLHours)*time.Hour))
		}
	}

	h("GET /v1/realtime", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(rt.Status(r.Context()))
	}))
	h("PUT /v1/realtime/turn", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b realtime.TurnRequest
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(rt.SetTurn(ctx, b))
	}))
	h("POST /v1/realtime/turn/credentials", admin(cred("")))
	h("POST /v1/realtime/turn/secret", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		return ok(rt.RotateTurnSecret(ctx))
	}))
	h("PUT /v1/realtime/livekit", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b realtime.LiveKitRequest
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		st, key, err := rt.SetLiveKit(ctx, b)
		return ok(map[string]any{"status": st, "new_key": key}, err)
	}))
	h("POST /v1/realtime/livekit/keys", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b struct {
			Name    string `json:"name"`
			Account string `json:"account"`
		}
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		return created(rt.CreateKey(r.Context(), b.Name, b.Account, true))
	}))
	h("DELETE /v1/realtime/livekit/keys/{key}", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("key")}, rt.DeleteKey(r.Context(), r.PathValue("key"), "", true))
	}))
	h("POST /v1/realtime/livekit/token", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b realtime.TokenRequest
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		return ok(rt.JoinToken(b, "", true))
	}))

	// account owners: only their own keys, and only what the admin allows
	h("GET /v1/accounts/{a}/realtime", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(rt.ForAccount(pathAcct(r)))
	})
	h("POST /v1/accounts/{a}/realtime/turn-credentials", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := s.Sites.AccountExists(pathAcct(r)); err != nil {
			return 0, nil, err
		}
		return cred(pathAcct(r))(w, r)
	})
	h("POST /v1/accounts/{a}/realtime/keys", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		return created(rt.CreateKey(r.Context(), b.Name, pathAcct(r), false))
	})
	h("DELETE /v1/accounts/{a}/realtime/keys/{key}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("key")}, rt.DeleteKey(r.Context(), r.PathValue("key"), pathAcct(r), false))
	})
	h("POST /v1/accounts/{a}/realtime/token", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b realtime.TokenRequest
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		if err := s.Sites.AccountExists(pathAcct(r)); err != nil {
			return 0, nil, err
		}
		return ok(rt.JoinToken(b, pathAcct(r), false))
	})
}

// objectRoutes: S3 storage (Garage).
//
//	GET    /v1/objects, PUT {"domain","account"}           where the S3 endpoint is served (admin)
//	GET    /v1/accounts/{a}/objects                        endpoint, access key id, buckets with usage
//	POST   /v1/accounts/{a}/objects/buckets                {"name","quota_mb"} → bucket (+ credentials the first time)
//	PATCH  /v1/accounts/{a}/objects/buckets/{b}            {"quota_mb"}
//	DELETE /v1/accounts/{a}/objects/buckets/{b}            (empty buckets)
//	POST   /v1/accounts/{a}/objects/key                    new access key → credentials once
func (s *Server) objectRoutes(h func(string, handler)) {
	o := s.F.Objects
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	admin := func(fn handler) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if !isAdmin(r) {
				return 0, nil, kit.ErrForbidden
			}
			return fn(w, r)
		}
	}
	h("GET /v1/objects", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]any{"installed": o.Installed(), "settings": o.GetSettings(), "endpoint": o.Endpoint()}, nil)
	}))
	h("PUT /v1/objects", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b objects.Settings
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		set, note, err := o.SetSettings(ctx, b)
		return ok(map[string]any{"settings": set, "note": note, "endpoint": o.Endpoint()}, err)
	}))
	h("GET /v1/accounts/{a}/objects", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(o.Get(r.Context(), pathAcct(r)))
	})
	h("POST /v1/accounts/{a}/objects/buckets", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b struct {
			Name    string `json:"name"`
			QuotaMB int    `json:"quota_mb"`
		}
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		bucket, cred, err := o.CreateBucket(r.Context(), pathAcct(r), b.Name, b.QuotaMB)
		return http.StatusCreated, map[string]any{"bucket": bucket, "credentials": cred}, err
	})
	h("PATCH /v1/accounts/{a}/objects/buckets/{b}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b struct {
			QuotaMB int `json:"quota_mb"`
		}
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		return ok(o.SetQuota(r.Context(), pathAcct(r), r.PathValue("b"), b.QuotaMB))
	})
	h("DELETE /v1/accounts/{a}/objects/buckets/{b}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("b")}, o.DeleteBucket(r.Context(), pathAcct(r), r.PathValue("b")))
	})
	h("POST /v1/accounts/{a}/objects/key", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(o.NewKey(r.Context(), pathAcct(r)))
	})
}
