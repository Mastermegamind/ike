// Package api serves the agent's JSON API over a unix socket.
//
//	GET    /v1/health
//	GET    /v1/system
//	GET    /v1/services
//	GET    /v1/sites
//	GET    /v1/sites/{domain}
//	POST   /v1/sites                 {"domain","user","php"}
//	DELETE /v1/sites/{domain}?remove_files=true
//	PUT    /v1/sites/{domain}/ssh             {"mode": "none"|"sftp"|"shell"}
//	GET    /v1/sites/{domain}/ssh/keys
//	POST   /v1/sites/{domain}/ssh/keys        {"public_key"}
//	DELETE /v1/sites/{domain}/ssh/keys?fingerprint=SHA256:...
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/sites"
	"github.com/YOUR-ORG/ike/agent/internal/sysinfo"
)

const Version = "0.1.0"

type Server struct {
	Sites  *sites.Manager
	Runner runner.Runner
	Root   string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
	})
	mux.HandleFunc("GET /v1/system", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, sysinfo.Collect(s.Root))
	})
	mux.HandleFunc("GET /v1/services", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, sysinfo.Services(r.Context(), s.Runner, s.Root))
	})
	mux.HandleFunc("GET /v1/sites", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Sites.List()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	})
	mux.HandleFunc("GET /v1/sites/{domain}", func(w http.ResponseWriter, r *http.Request) {
		site, err := s.Sites.Get(r.PathValue("domain"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, site)
	})
	mux.HandleFunc("POST /v1/sites", func(w http.ResponseWriter, r *http.Request) {
		var req sites.CreateRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		// Detach from the client so a dropped connection can't interrupt a half-done change.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer cancel()
		site, err := s.Sites.Create(ctx, req)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, site)
	})
	mux.HandleFunc("DELETE /v1/sites/{domain}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer cancel()
		removeFiles := r.URL.Query().Get("remove_files") == "true"
		if err := s.Sites.Delete(ctx, r.PathValue("domain"), removeFiles); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("domain"), "files_removed": removeFiles})
	})
	mux.HandleFunc("PUT /v1/sites/{domain}/ssh", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mode string `json:"mode"`
		}
		if !decode(w, r, &body) {
			return
		}
		site, err := s.Sites.SetSSHMode(r.Context(), r.PathValue("domain"), body.Mode)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, site)
	})
	mux.HandleFunc("GET /v1/sites/{domain}/ssh/keys", func(w http.ResponseWriter, r *http.Request) {
		keys, err := s.Sites.ListSSHKeys(r.PathValue("domain"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, keys)
	})
	mux.HandleFunc("POST /v1/sites/{domain}/ssh/keys", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PublicKey string `json:"public_key"`
		}
		if !decode(w, r, &body) {
			return
		}
		k, err := s.Sites.AddSSHKey(r.PathValue("domain"), body.PublicKey)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, k)
	})
	mux.HandleFunc("DELETE /v1/sites/{domain}/ssh/keys", func(w http.ResponseWriter, r *http.Request) {
		fp := r.URL.Query().Get("fingerprint")
		if err := s.Sites.RemoveSSHKey(r.PathValue("domain"), fp); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"removed": fp})
	})
	return logRequests(mux)
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
	case errors.Is(err, sites.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, sites.ErrExists):
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
