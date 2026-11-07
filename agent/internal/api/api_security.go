package api

// Security routes.
//
//	PUT    /v1/sites/{domain}/waf              {"mode": "off|detect|block", "disabled_rules": [942100], "profile": "wordpress"}
//	GET    /v1/sites/{domain}/waf/events?limit=
//	GET    /v1/security/waf                    installed?, app profiles
//	GET    /v1/security/malware                schedule, every report, quarantine (admin)
//	PUT    /v1/security/malware/schedule       {"enabled","frequency","hour","weekday","auto_quarantine"}
//	POST   /v1/accounts/{a}/malware/scans      {"path"} → job
//	GET    /v1/accounts/{a}/malware/scans, GET .../scans/{id}
//	POST   /v1/accounts/{a}/malware/scans/{id}/quarantine   {"findings": [ids]} (empty = all)
//	GET    /v1/accounts/{a}/quarantine
//	POST   /v1/accounts/{a}/quarantine/{id}/restore, DELETE /v1/accounts/{a}/quarantine/{id}

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/malware"
	"github.com/Mastermegamind/ike/agent/internal/sites"
)

func (s *Server) securityRoutes(h func(string, handler)) {
	mw := s.F.Malware
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	admin := func(fn handler) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if !isAdmin(r) {
				return 0, nil, kit.ErrForbidden
			}
			return fn(w, r)
		}
	}
	h("PUT /v1/sites/{domain}/waf", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body sites.WAFSettings
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.SetWAF(ctx, r.PathValue("domain"), body))
	})
	h("GET /v1/sites/{domain}/waf/events", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		return ok(s.Sites.WAFEvents(r.PathValue("domain"), limit))
	})
	h("GET /v1/security/waf", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]any{"installed": s.Config.ModSecurity, "profiles": sites.WAFProfiles}, nil)
	})
	h("GET /v1/security/malware", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		sch, err := mw.Schedule()
		if err != nil {
			return 0, nil, err
		}
		reps, err := mw.Reports("")
		if err != nil {
			return 0, nil, err
		}
		q, err := mw.Quarantined("")
		return ok(map[string]any{"schedule": sch, "reports": reps, "quarantine": q, "clamav": mw.ClamAVAvailable()}, err)
	}))
	h("PUT /v1/security/malware/schedule", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body malware.Schedule
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(mw.SetSchedule(body))
	}))
	h("POST /v1/accounts/{a}/malware/scans", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Path string `json:"path"`
		}
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return 0, nil, nil
		}
		acct := pathAcct(r)
		if err := s.F.Env.CheckAccount(acct); err != nil {
			return 0, nil, err
		}
		return s.job("malware:"+acct, "Malware scan of "+acct, 8*time.Hour, func(ctx context.Context, log jobs.Logger) error {
			_, err := mw.Scan(ctx, acct, body.Path, false, log)
			return err
		})
	})
	h("GET /v1/accounts/{a}/malware/scans", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(mw.Reports(pathAcct(r)))
	})
	h("GET /v1/accounts/{a}/malware/scans/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(mw.Report(pathAcct(r), r.PathValue("id")))
	})
	h("POST /v1/accounts/{a}/malware/scans/{id}/quarantine", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Findings []string `json:"findings"`
		}
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(mw.Quarantine(pathAcct(r), r.PathValue("id"), body.Findings))
	})
	h("GET /v1/accounts/{a}/quarantine", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(mw.Quarantined(pathAcct(r)))
	})
	h("POST /v1/accounts/{a}/quarantine/{id}/restore", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"restored": r.PathValue("id")}, mw.Restore(pathAcct(r), r.PathValue("id")))
	})
	h("DELETE /v1/accounts/{a}/quarantine/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("id")}, mw.Delete(pathAcct(r), r.PathValue("id")))
	})
}
