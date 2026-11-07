package api

// cPanel migration (admin only):
//
//	GET    /v1/quotas                           disk quota status and per-account use
//	GET    /v1/imports                          backups waiting in the imports folder
//	PUT    /v1/imports/cpanel/{file}/upload     the archive as the request body (streamed)
//	POST   /v1/imports/cpanel/{file}/restore    {"account","skip":[...],"point_to_this_server"} → job
//	DELETE /v1/imports/cpanel/{file}

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/migrate"
)

var importFileRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,120}\.tar(\.gz)?$`)

func (s *Server) importPath(name string) (string, error) {
	if !importFileRe.MatchString(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("backup file names look like cpmove-user.tar.gz")
	}
	return filepath.Join(s.Config.Root, s.Config.ImportsDir, name), nil
}

func (s *Server) migrateRoutes(h func(string, handler)) {
	f := s.F
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	adminOnly := func(fn handler) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if !isAdmin(r) {
				return 0, nil, kit.ErrForbidden
			}
			return fn(w, r)
		}
	}
	h("GET /v1/quotas", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(s.Sites.QuotaReport(r.Context()))
	}))
	h("GET /v1/imports", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		dir := filepath.Join(s.Config.Root, s.Config.ImportsDir)
		ents, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return ok([]any{}, nil)
		}
		type item struct {
			File     string    `json:"file"`
			SizeMB   float64   `json:"size_mb"`
			Uploaded time.Time `json:"uploaded"`
		}
		out := []item{}
		for _, e := range ents {
			if !importFileRe.MatchString(e.Name()) {
				continue
			}
			if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
				out = append(out, item{e.Name(), float64(fi.Size()) / (1 << 20), fi.ModTime().UTC()})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Uploaded.After(out[j].Uploaded) })
		return ok(out, err)
	}))
	h("PUT /v1/imports/cpanel/{file}/upload", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		p, err := s.importPath(r.PathValue("file"))
		if err != nil {
			return 0, nil, err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return 0, nil, err
		}
		tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
		if err != nil {
			return 0, nil, err
		}
		defer os.Remove(tmp.Name())
		n, err := io.Copy(tmp, http.MaxBytesReader(w, r.Body, 200<<30))
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return 0, nil, fmt.Errorf("upload stopped after %d MB: %w", n>>20, err)
		}
		if err := os.Chmod(tmp.Name(), 0o600); err != nil {
			return 0, nil, err
		}
		if err := os.Rename(tmp.Name(), p); err != nil {
			return 0, nil, err
		}
		plan, err := s.Sites.InspectCpanelBackup(r.PathValue("file"))
		if err != nil {
			_ = os.Remove(p)
			return 0, nil, err
		}
		return http.StatusCreated, plan, nil
	}))
	h("DELETE /v1/imports/cpanel/{file}", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		p, err := s.importPath(r.PathValue("file"))
		if err != nil {
			return 0, nil, err
		}
		if err := os.Remove(p); err != nil {
			return 0, nil, fmt.Errorf("%s %w", r.PathValue("file"), kit.ErrNotFound)
		}
		return ok(map[string]string{"deleted": r.PathValue("file")}, nil)
	}))
	h("POST /v1/imports/cpanel/{file}/restore", adminOnly(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req migrate.RestoreRequest
		if !decode(w, r, &req) {
			return 0, nil, nil
		}
		req.File = r.PathValue("file")
		if _, err := s.importPath(req.File); err != nil {
			return 0, nil, err
		}
		for _, sk := range req.Skip {
			switch sk {
			case "files", "databases", "mail", "cron", "dns":
			default:
				return 0, nil, fmt.Errorf("skip can list files, databases, mail, cron and dns")
			}
		}
		rs := &migrate.Restorer{Env: f.Env, Sites: s.Sites, Mail: f.Mail, DNS: f.DNS}
		return s.job("cpanel-restore", "Restore "+req.File, 8*time.Hour, func(ctx context.Context, log jobs.Logger) error {
			res, err := rs.Restore(ctx, req, log)
			if res != nil {
				for _, d := range res.Databases {
					log("Database %s (was %s): user %s, password %s", d.Name, d.From, d.User, d.Password)
				}
				for _, wmsg := range res.Warnings {
					log("Note: %s", wmsg)
				}
				log("Restored %d websites, %d databases, %d mailboxes, %d forwarders, %d cron jobs and %d DNS zones into %s",
					len(res.Websites), len(res.Databases), len(res.Mailboxes), res.Forwards, res.CronJobs, len(res.Zones), res.Account)
			}
			return err
		})
	}))
}
