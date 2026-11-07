package api

// Infrastructure routes.
//
//	GET    /v1/inventory                       hardware, disks, network, asset details
//	PUT    /v1/inventory/asset                 {"tag","owner","location","provider","purchase_date","renewal_date","monthly_cost","notes"}
//	GET    /v1/maintenance, POST {"title","start","end","sites","updates","reboot"}, DELETE /v1/maintenance/{id}
//	GET    /v1/webhooks, POST {"url","events","enabled"} → secret once, PUT /v1/webhooks/{id}, DELETE
//	POST   /v1/webhooks/{id}/test, GET /v1/webhooks/deliveries
//	POST   /v1/spec/plan                       {"spec", "variables"} → changes
//	POST   /v1/spec/apply                      {"spec", "variables"} → job
//	GET    /v1/spec/export                     this server as a spec
//	GET    /v1/spec/templates, PUT /v1/spec/templates/{name} {"description","spec"}, DELETE
//	POST   /v1/spec/templates/{name}/plan|apply {"variables"}
//	PUT    /v1/sites/{domain}/balancer         load balancer pool

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/infra"
	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/sites"
	"github.com/Mastermegamind/ike/agent/internal/spec"
)

func (s *Server) setupInfra(f *Features) {
	f.Infra = infra.New(f.Env)
	f.Infra.AllSites = func() []string {
		list, _ := s.Sites.List()
		out := make([]string, 0, len(list))
		for _, st := range list {
			out = append(out, st.Domain)
		}
		return out
	}
	var prevMu sync.Mutex
	f.Infra.SetMaintenance = func(ctx context.Context, domains []string, on bool) (map[string]string, error) {
		prevMu.Lock()
		defer prevMu.Unlock()
		prev := map[string]string{}
		var firstErr error
		for _, d := range domains {
			st, err := s.Sites.Get(d)
			if err != nil {
				continue
			}
			if on {
				if st.Status != sites.StatusActive {
					continue // leave suspended or already-maintenance sites alone
				}
				prev[d] = st.Status
				_, err = s.Sites.SetStatus(ctx, d, sites.SetStatusRequest{Status: sites.StatusMaintenance, AllowIPs: st.AllowIPs})
			} else if st.Status == sites.StatusMaintenance {
				_, err = s.Sites.SetStatus(ctx, d, sites.SetStatusRequest{Status: sites.StatusActive})
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return prev, firstErr
	}
	f.Infra.ApplyUpdates = func(ctx context.Context, reboot bool) error {
		if s.Updates == nil {
			return nil
		}
		j, err := s.Updates.Apply(true)
		if err != nil {
			return err
		}
		for {
			time.Sleep(10 * time.Second)
			cur, found := s.Jobs.Get(j.ID)
			if !found || cur.Status != "running" {
				break
			}
		}
		if reboot {
			return s.Updates.ScheduleReboot(ctx, 1)
		}
		return nil
	}
	f.Env.Events.Subscribe(f.Infra.OnEvent)
	f.Spec = spec.New(f.Env)
	f.Spec.Sites, f.Spec.DNS, f.Spec.Mail, f.Spec.DB, f.Spec.FW = s.Sites, f.DNS, f.Mail, f.DB, s.Firewall
}

func (s *Server) infraRoutes(h func(string, handler)) {
	in, sp := s.F.Infra, s.F.Spec
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	admin := func(fn handler) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if !isAdmin(r) {
				return 0, nil, kit.ErrForbidden
			}
			return fn(w, r)
		}
	}
	h("GET /v1/inventory", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) { return ok(in.Collect(r.Context())) }))
	h("PUT /v1/inventory/asset", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var a infra.Asset
		if !decode(w, r, &a) {
			return 0, nil, nil
		}
		return ok(in.SetAsset(a))
	}))
	h("GET /v1/maintenance", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) { return ok(in.Windows()) }))
	h("POST /v1/maintenance", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body infra.Window
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		v, err := in.AddWindow(body, actor(r))
		return http.StatusCreated, v, err
	}))
	h("DELETE /v1/maintenance/{id}", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"cancelled": r.PathValue("id")}, in.CancelWindow(r.Context(), r.PathValue("id")))
	}))
	h("GET /v1/webhooks", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) { return ok(in.Webhooks()) }))
	h("GET /v1/webhooks/deliveries", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) { return ok(in.Deliveries(), nil) }))
	h("POST /v1/webhooks", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body infra.Webhook
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		body.ID = ""
		v, err := in.SaveWebhook(body)
		return http.StatusCreated, v, err
	}))
	h("PUT /v1/webhooks/{id}", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body infra.Webhook
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		body.ID = r.PathValue("id")
		return ok(in.SaveWebhook(body))
	}))
	h("DELETE /v1/webhooks/{id}", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("id")}, in.DeleteWebhook(r.PathValue("id")))
	}))
	h("POST /v1/webhooks/{id}/test", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"queued": "webhook.test"}, in.TestWebhook(r.PathValue("id")))
	}))

	type specBody struct {
		Spec      json.RawMessage   `json:"spec"`
		Variables map[string]string `json:"variables"`
	}
	parse := func(w http.ResponseWriter, r *http.Request) (*spec.Spec, bool, error) {
		var b specBody
		if !decodeBig(w, r, &b, 1<<20) {
			return nil, false, nil
		}
		sp, err := spec.Parse(b.Spec, b.Variables)
		return sp, true, err
	}
	h("POST /v1/spec/plan", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		p, okd, err := parse(w, r)
		if !okd {
			return 0, nil, nil
		}
		if err != nil {
			return 0, nil, err
		}
		return ok(sp.Plan(p))
	}))
	apply := func(p *spec.Spec) (int, any, error) {
		var out []spec.Result
		code, j, err := s.job("spec-apply", "Apply server spec", 2*time.Hour, func(ctx context.Context, log jobs.Logger) error {
			res, err := sp.Apply(ctx, p, log)
			out = res
			for _, r := range res {
				if r.Secrets != nil {
					b, _ := json.Marshal(r.Secrets)
					log("Created %s %s: %s", r.Kind, r.Name, b)
				}
			}
			return err
		})
		_ = out
		return code, j, err
	}
	h("POST /v1/spec/apply", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		p, okd, err := parse(w, r)
		if !okd {
			return 0, nil, nil
		}
		if err != nil {
			return 0, nil, err
		}
		return apply(p)
	}))
	h("GET /v1/spec/export", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) { return ok(sp.Export()) }))
	h("GET /v1/spec/templates", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) { return ok(sp.Templates()) }))
	h("PUT /v1/spec/templates/{name}", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var t spec.Template
		if !decodeBig(w, r, &t, 1<<20) {
			return 0, nil, nil
		}
		t.Name = r.PathValue("name")
		return ok(sp.SaveTemplate(t))
	}))
	h("DELETE /v1/spec/templates/{name}", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("name")}, sp.DeleteTemplate(r.PathValue("name")))
	}))
	h("POST /v1/spec/templates/{name}/{action}", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b struct {
			Variables map[string]string `json:"variables"`
		}
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		t, err := sp.Template(r.PathValue("name"))
		if err != nil {
			return 0, nil, err
		}
		p, err := spec.Parse(t.Spec, b.Variables)
		if err != nil {
			return 0, nil, err
		}
		if r.PathValue("action") == "apply" {
			return apply(p)
		}
		return ok(sp.Plan(p))
	}))
	h("PUT /v1/sites/{domain}/balancer", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b sites.BalancerSettings
		if !decode(w, r, &b) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.SetBalancer(ctx, r.PathValue("domain"), b))
	})
}
