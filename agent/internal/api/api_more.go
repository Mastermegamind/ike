package api

// Routes for domains, redirects, PHP, HTTPS, deployments, containers and
// Kubernetes.
//
//	PUT    /v1/sites/{domain}/domains          {"aliases","www","redirect_aliases"}
//	PUT    /v1/sites/{domain}/redirects        [{"kind","match","from","to","code","keep_query"}]
//	GET    /v1/sites/{domain}/protection       password-protected paths and their users (never passwords)
//	PUT    /v1/sites/{domain}/protection/paths [{"path","realm","users","allow_ips"}] ("/" protects the whole website)
//	PUT    /v1/sites/{domain}/protection/users/{user}  {"password"} (empty: one is made up and returned once)
//	DELETE /v1/sites/{domain}/protection/users/{user}
//	PUT    /v1/sites/{domain}/php              pool and php.ini settings
//	POST   /v1/sites/{domain}/composer         {"action","package","dev"} → job
//	POST   /v1/sites/{domain}/ssl              {"email","include_aliases"} Let's Encrypt → job
//	PUT    /v1/sites/{domain}/ssl/custom       {"certificate","private_key"}
//	PATCH  /v1/sites/{domain}/ssl              {"force_https","hsts","http2","http3"}
//	DELETE /v1/sites/{domain}/ssl
//	GET    /v1/ssl                             every certificate with days left
//	GET    /v1/web/features                    Nginx version, HTTP/2 style, HTTP/3
//	GET    /v1/php/{version}/extensions
//	POST   /v1/php/{version}/extensions/{name} install → job
//	DELETE /v1/php/{version}/extensions/{name} remove → job
//
//	GET    /v1/sites/{domain}/deploy           settings, deploy key, webhook path
//	PUT    /v1/sites/{domain}/deploy           connect a repository or image
//	DELETE /v1/sites/{domain}/deploy           disconnect (the live release stays)
//	POST   /v1/sites/{domain}/deploy/key       new deploy key
//	POST   /v1/sites/{domain}/deploy/webhook-secret
//	GET    /v1/sites/{domain}/deployments
//	POST   /v1/sites/{domain}/deployments      {"ref_type","ref","image"} → queued
//	GET    /v1/sites/{domain}/deployments/{id} deployment + build log
//	POST   /v1/sites/{domain}/deployments/{id}/rollback
//	POST   /v1/sites/{domain}/deployments/{id}/cancel
//	GET    /v1/deploy/queue, PUT /v1/deploy/settings {"max_concurrent"}
//	POST   /v1/git/webhook/{domain}            raw Git host webhook (forwarded by the panel)
//
//	GET    /v1/accounts/{name}/containers, POST (spec), GET .../containers/stats
//	POST   /v1/accounts/{name}/containers/{c}/{start|stop|restart|remove}
//	GET    /v1/accounts/{name}/containers/{c}/logs?lines=
//	POST   /v1/accounts/{name}/containers/{c}/exec {"command"}
//	GET    /v1/accounts/{name}/volumes, POST {"name"}, DELETE /v1/accounts/{name}/volumes/{v}
//	GET    /v1/accounts/{name}/networks, POST {"name"}, DELETE /v1/accounts/{name}/networks/{n}
//	GET    /v1/accounts/{name}/registries, POST {"registry","username","password"}, DELETE ?registry=
//	GET    /v1/accounts/{name}/stacks, GET/PUT/DELETE /v1/accounts/{name}/stacks/{s}
//	POST   /v1/accounts/{name}/stacks/{s}/{up|down|restart|pull} → job
//	GET    /v1/accounts/{name}/stacks/{s}/logs
//
//	GET    /v1/docker, POST /v1/docker/prune, DELETE /v1/docker/images?ref=
//	GET    /v1/docker/containers, GET /v1/docker/updates, POST /v1/docker/updates/check
//
//	GET    /v1/kubernetes, GET /v1/kubernetes/workloads, POST /v1/kubernetes/apply {"manifest"}
//	DELETE /v1/kubernetes/{kind}/{ns}/{name}
//	POST   /v1/kubernetes/{kind}/{ns}/{name}/scale {"replicas"}, POST .../restart
//	GET    /v1/kubernetes/pods/{ns}/{pod}/logs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/firewall"
	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/sites"
)

type handler func(w http.ResponseWriter, r *http.Request) (int, any, error)

// isAdmin: the panel says who it acts for; direct socket callers (root) and
// the MCP server act as the server admin.
func isAdmin(r *http.Request) bool {
	role := r.Header.Get("X-Ike-Role")
	return role == "" || role == "admin"
}

func (s *Server) job(kind, title string, timeout time.Duration, fn func(ctx context.Context, log jobs.Logger) error) (int, any, error) {
	j, err := s.Jobs.Start(kind, title, timeout, fn)
	return http.StatusAccepted, j, err
}

func (s *Server) moreRoutes(h func(string, handler)) {
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	created := func(v any, err error) (int, any, error) { return http.StatusCreated, v, err }
	d := func(r *http.Request) string { return r.PathValue("domain") }
	acct := func(r *http.Request) string { return r.PathValue("name") }

	// ---- domains, redirects, PHP
	h("PUT /v1/sites/{domain}/domains", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.DomainsRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.SetDomains(ctx, d(r), req))
	})
	// Password protection
	h("GET /v1/sites/{domain}/protection", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.GetProtection(d(r)))
	})
	h("PUT /v1/sites/{domain}/protection/paths", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var paths []sites.ProtectedPath
		if !decode(w, r, &paths) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.SetProtectedPaths(ctx, d(r), paths))
	})
	h("PUT /v1/sites/{domain}/protection/users/{user}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		made, err := s.Sites.SetAuthUser(ctx, d(r), r.PathValue("user"), body.Password)
		if err != nil {
			return ok(nil, err)
		}
		out := map[string]string{"user": r.PathValue("user")}
		if made != "" {
			out["password"] = made // made up here; shown once
		}
		return ok(out, nil)
	})
	h("DELETE /v1/sites/{domain}/protection/users/{user}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		return ok(map[string]string{"deleted": r.PathValue("user")}, s.Sites.DeleteAuthUser(ctx, d(r), r.PathValue("user")))
	})
	h("PUT /v1/sites/{domain}/redirects", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var rules []sites.Redirect
		if !decode(w, r, &rules) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.SetRedirects(ctx, d(r), rules))
	})
	h("PUT /v1/sites/{domain}/php", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var p sites.PHPSettings
		if !decode(w, r, &p) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.SetPHPSettings(ctx, d(r), p))
	})
	h("POST /v1/sites/{domain}/composer", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.ComposerRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		dom := d(r)
		if _, err := s.Sites.Get(dom); err != nil {
			return 0, nil, err
		}
		return s.job("composer:"+dom, "composer "+req.Action+" on "+dom, 35*time.Minute, func(ctx context.Context, log jobs.Logger) error {
			return s.Sites.RunComposer(ctx, dom, req, log)
		})
	})
	h("GET /v1/php/{version}/extensions", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.PHPExtensions(r.Context(), r.PathValue("version")))
	})
	phpExt := func(install bool) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			v, name := r.PathValue("version"), r.PathValue("name")
			verb := map[bool]string{true: "Install", false: "Remove"}[install]
			return s.job("php-ext:"+v, verb+" PHP "+v+" "+name, 20*time.Minute, func(ctx context.Context, log jobs.Logger) error {
				return s.Sites.ChangePHPExtension(ctx, v, name, install, log)
			})
		}
	}
	h("POST /v1/php/{version}/extensions/{name}", phpExt(true))
	h("DELETE /v1/php/{version}/extensions/{name}", phpExt(false))

	// ---- HTTPS
	h("POST /v1/sites/{domain}/ssl", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.IssueRequest
		if r.ContentLength != 0 && !decode(w, r, &req) {
			return 0, nil, nil
		}
		dom := d(r)
		if _, err := s.Sites.Get(dom); err != nil {
			return 0, nil, err
		}
		return s.job("ssl:"+dom, "Get an HTTPS certificate for "+dom, 10*time.Minute, func(ctx context.Context, log jobs.Logger) error {
			_, err := s.Sites.IssueCertificate(ctx, dom, req, log)
			return err
		})
	})
	h("PUT /v1/sites/{domain}/ssl/custom", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.CustomCertRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.SetCustomCertificate(ctx, d(r), req))
	})
	h("PATCH /v1/sites/{domain}/ssl", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var o sites.SSLOptions
		if !decode(w, r, &o) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		site, err := s.Sites.UpdateSSL(ctx, d(r), o)
		if err == nil && o.HTTP3 != nil && *o.HTTP3 && s.Firewall != nil {
			// HTTP/3 runs over UDP.
			if _, ferr := s.Firewall.OpenPort(ctx, "443", "udp", "HTTP/3"); ferr != nil && !errors.Is(ferr, firewall.ErrExists) {
				return 0, nil, fmt.Errorf("HTTP/3 is on, but opening 443/udp in the firewall failed: %w", ferr)
			}
		}
		return ok(site, err)
	})
	h("DELETE /v1/sites/{domain}/ssl", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.RemoveSSL(ctx, d(r)))
	})
	h("GET /v1/ssl", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Certificates(), nil)
	})
	h("GET /v1/web/features", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Features(), nil)
	})

	// ---- deployments
	h("GET /v1/sites/{domain}/deploy", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.DeployInfo(d(r)))
	})
	h("PUT /v1/sites/{domain}/deploy", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.DeploySetup
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(s.Sites.ConfigureDeploy(ctx, d(r), req))
	})
	h("DELETE /v1/sites/{domain}/deploy", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.DisconnectDeploy(r.Context(), d(r)))
	})
	h("POST /v1/sites/{domain}/deploy/key", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.RegenerateDeployKey(r.Context(), d(r)))
	})
	h("POST /v1/sites/{domain}/deploy/webhook-secret", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.RegenerateWebhookSecret(d(r)))
	})
	h("GET /v1/sites/{domain}/deployments", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Deployments(d(r)))
	})
	h("POST /v1/sites/{domain}/deployments", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.DeployRequest
		if r.ContentLength != 0 && !decode(w, r, &req) {
			return 0, nil, nil
		}
		req.Trigger, req.Actor = "manual", actor(r)
		if req.Actor == "ai:mcp" || r.Header.Get("X-Ike-Trigger") == "api" {
			req.Trigger = "api"
		}
		dep, err := s.Sites.Deploy(d(r), req)
		return http.StatusAccepted, dep, err
	})
	h("GET /v1/sites/{domain}/deployments/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		dep, log, err := s.Sites.Deployment(d(r), r.PathValue("id"))
		return ok(map[string]any{"deployment": dep, "log": log}, err)
	})
	h("POST /v1/sites/{domain}/deployments/{id}/rollback", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		dep, err := s.Sites.Rollback(d(r), r.PathValue("id"), actor(r))
		return http.StatusAccepted, dep, err
	})
	h("POST /v1/sites/{domain}/deployments/{id}/cancel", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"cancelled": r.PathValue("id")}, s.Sites.CancelDeployment(d(r), r.PathValue("id")))
	})
	h("GET /v1/deploy/queue", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.DeployQueue(), nil)
	})
	h("PUT /v1/deploy/settings", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var st sites.DeploySettings
		if !decode(w, r, &st) {
			return 0, nil, nil
		}
		return ok(s.Sites.SetDeploySettings(st))
	})
	h("POST /v1/git/webhook/{domain}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		if err != nil {
			return 0, nil, fmt.Errorf("webhook body too large")
		}
		res, err := s.Sites.HandleGitWebhook(r.Context(), d(r), r.Header, body)
		if errors.Is(err, sites.ErrUnauthorized) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "signature doesn't match the webhook secret"})
			return 0, nil, nil
		}
		return ok(res, err)
	})

	// ---- account containers, volumes, networks, registries, stacks
	h("GET /v1/accounts/{name}/containers", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Containers(r.Context(), acct(r)))
	})
	h("GET /v1/accounts/{name}/containers/stats", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.ContainerStatsFor(r.Context(), acct(r)))
	})
	h("POST /v1/accounts/{name}/containers", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var spec sites.ContainerSpec
		if !decode(w, r, &spec) {
			return 0, nil, nil
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 20*time.Minute)
		defer cancel()
		return created(s.Sites.CreateContainer(ctx, acct(r), spec, isAdmin(r)))
	})
	h("POST /v1/accounts/{name}/containers/{c}/{action}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		if r.PathValue("action") == "exec" {
			var body struct {
				Command string `json:"command"`
			}
			if !decode(w, r, &body) {
				return 0, nil, nil
			}
			out, err := s.Sites.ContainerExec(ctx, acct(r), r.PathValue("c"), body.Command)
			return ok(map[string]string{"output": out}, err)
		}
		return ok(map[string]string{"done": r.PathValue("action")}, s.Sites.ContainerAction(ctx, acct(r), r.PathValue("c"), r.PathValue("action")))
	})
	h("GET /v1/accounts/{name}/containers/{c}/logs", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		out, err := s.Sites.ContainerLogs(r.Context(), acct(r), r.PathValue("c"), n)
		return ok(map[string]string{"log": out}, err)
	})
	h("GET /v1/accounts/{name}/volumes", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Volumes(r.Context(), acct(r)))
	})
	h("POST /v1/accounts/{name}/volumes", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(s.Sites.CreateVolume(r.Context(), acct(r), body.Name))
	})
	h("DELETE /v1/accounts/{name}/volumes/{v}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("v")}, s.Sites.RemoveVolume(r.Context(), acct(r), r.PathValue("v")))
	})
	h("GET /v1/accounts/{name}/networks", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Networks(r.Context(), acct(r)))
	})
	h("POST /v1/accounts/{name}/networks", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		n, err := s.Sites.CreateNetwork(r.Context(), acct(r), body.Name)
		return created(map[string]string{"network": n}, err)
	})
	h("DELETE /v1/accounts/{name}/networks/{n}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("n")}, s.Sites.RemoveNetwork(r.Context(), acct(r), r.PathValue("n")))
	})
	h("GET /v1/accounts/{name}/registries", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Registries(acct(r)))
	})
	h("POST /v1/accounts/{name}/registries", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body sites.RegistryLogin
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(map[string]string{"registry": body.Registry}, s.Sites.AddRegistry(r.Context(), acct(r), body))
	})
	h("DELETE /v1/accounts/{name}/registries", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		reg := r.URL.Query().Get("registry")
		return ok(map[string]string{"removed": reg}, s.Sites.RemoveRegistry(r.Context(), acct(r), reg))
	})
	h("GET /v1/accounts/{name}/stacks", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Stacks(r.Context(), acct(r)))
	})
	h("GET /v1/accounts/{name}/stacks/{s}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.GetStack(r.Context(), acct(r), r.PathValue("s")))
	})
	h("PUT /v1/accounts/{name}/stacks/{s}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req sites.StackRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		a, st, admin := acct(r), r.PathValue("s"), isAdmin(r)
		return s.job("stack:"+a+"/"+st, "Start stack "+st, 30*time.Minute, func(ctx context.Context, log jobs.Logger) error {
			return s.Sites.SaveStack(ctx, a, st, req, admin, log)
		})
	})
	h("POST /v1/accounts/{name}/stacks/{s}/{action}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		a, st, action := acct(r), r.PathValue("s"), r.PathValue("action")
		return s.job("stack:"+a+"/"+st, "Stack "+st+": "+action, 30*time.Minute, func(ctx context.Context, log jobs.Logger) error {
			return s.Sites.StackAction(ctx, a, st, action, log)
		})
	})
	h("DELETE /v1/accounts/{name}/stacks/{s}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		vol := r.URL.Query().Get("volumes") == "true"
		return ok(map[string]any{"deleted": r.PathValue("s"), "volumes_removed": vol}, s.Sites.DeleteStack(ctx, acct(r), r.PathValue("s"), vol))
	})
	h("GET /v1/accounts/{name}/stacks/{s}/logs", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		out, err := s.Sites.StackLogs(r.Context(), acct(r), r.PathValue("s"), n)
		return ok(map[string]string{"log": out}, err)
	})

	// ---- Docker host (admin)
	h("GET /v1/docker", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.DockerOverview(r.Context()))
	})
	h("GET /v1/docker/containers", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.Containers(r.Context(), ""))
	})
	h("POST /v1/docker/prune", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := long(r)
		defer cancel()
		out, err := s.Sites.PruneDocker(ctx)
		return ok(map[string]string{"output": out}, err)
	})
	h("DELETE /v1/docker/images", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ref := r.URL.Query().Get("ref")
		return ok(map[string]string{"removed": ref}, s.Sites.RemoveImage(r.Context(), ref))
	})
	h("GET /v1/docker/updates", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.ImageUpdates(), nil)
	})
	h("POST /v1/docker/updates/check", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return s.job("image-updates", "Check container images for updates", 15*time.Minute, func(ctx context.Context, log jobs.Logger) error {
			list, err := s.Sites.CheckImageUpdates(ctx)
			for _, u := range list {
				if u.UpdateAvailable {
					log("Update available: %s", u.Image)
				}
			}
			log("Checked %d images", len(list))
			return err
		})
	})

	// ---- Kubernetes (admin)
	h("GET /v1/kubernetes", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.KubeOverview(r.Context()), nil)
	})
	h("GET /v1/kubernetes/workloads", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.KubeWorkloads(r.Context()))
	})
	h("POST /v1/kubernetes/apply", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Manifest string `json:"manifest"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		out, err := s.Sites.KubeApply(r.Context(), body.Manifest)
		return ok(map[string]string{"output": out}, err)
	})
	h("DELETE /v1/kubernetes/{kind}/{ns}/{n}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("n")}, s.Sites.KubeDelete(r.Context(), r.PathValue("kind"), r.PathValue("ns"), r.PathValue("n")))
	})
	h("POST /v1/kubernetes/{kind}/{ns}/{n}/{action}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		kind, ns, n := r.PathValue("kind"), r.PathValue("ns"), r.PathValue("n")
		switch r.PathValue("action") {
		case "scale":
			var body struct {
				Replicas int `json:"replicas"`
			}
			if !decode(w, r, &body) {
				return 0, nil, nil
			}
			return ok(map[string]int{"replicas": body.Replicas}, s.Sites.KubeScale(r.Context(), kind, ns, n, body.Replicas))
		case "restart":
			return ok(map[string]string{"restarted": n}, s.Sites.KubeRestart(r.Context(), kind, ns, n))
		}
		return 0, nil, fmt.Errorf("action must be scale or restart")
	})
	h("GET /v1/kubernetes/pods/{ns}/{pod}/logs", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		out, err := s.Sites.KubeLogs(r.Context(), r.PathValue("ns"), r.PathValue("pod"), n)
		return ok(map[string]string{"log": out}, err)
	})
}

func actor(r *http.Request) string {
	a := r.Header.Get("X-Ike-Actor")
	if len(a) > 64 {
		a = a[:64]
	}
	if a == "" {
		a = "local"
	}
	return a
}
