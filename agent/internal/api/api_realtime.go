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

	"github.com/Mastermegamind/ike/agent/internal/kit"
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
