package api

// File manager, snapshots, share links, storage targets and the web terminal.
//
//	GET    /v1/accounts/{a}/files?path=                   folder listing
//	GET    /v1/accounts/{a}/files/content?path=           {"content"} for the editor (≤ 2 MB text)
//	PUT    /v1/accounts/{a}/files/content?path=           {"content","overwrite"}
//	GET    /v1/accounts/{a}/files/download?path=          file, or folder as .tar.gz
//	PUT    /v1/accounts/{a}/files/upload?path=&overwrite=true   raw body
//	POST   /v1/accounts/{a}/files/mkdir|move|copy|delete|chmod|archive|extract
//	GET    /v1/accounts/{a}/files/search?path=&name=&content=
//	GET    /v1/accounts/{a}/files/sizes?path=             folder sizes
//	GET    /v1/accounts/{a}/snapshots, POST {"keep"} → job
//	GET    /v1/accounts/{a}/snapshots/{id}/files?path=, POST .../{id}/restore {"path","overwrite"}, DELETE .../{id}
//	GET    /v1/accounts/{a}/shares, POST {"path","expires_hours","password","max_downloads"}, DELETE .../{token}
//	GET    /v1/shares/{token}, GET /v1/shares/{token}/download?password=   (public through the panel)
//	GET    /v1/storage, POST, PUT /v1/storage/{id}, DELETE, POST .../{id}/check, GET .../{id}/browse?prefix=
//	GET    /v1/storage/sftp-key
//	GET    /v1/accounts/{a}/storage (same, per account), POST, PUT, DELETE, check, browse
//	POST   /v1/accounts/{a}/storage/{id}/upload {"path","key"} → job (file → bucket)
//	GET    /v1/accounts/{a}/terminal?cols=&rows=           WebSocket: the account's shell
//	GET    /v1/accounts/{a}/containers/{c}/terminal?shell=sh|bash   WebSocket: docker exec
//	GET    /v1/terminal                                   WebSocket: root shell (admin)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/files"
	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/storage"
	"github.com/Mastermegamind/ike/agent/internal/terminal"
)

func attachment(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" || strings.HasPrefix(ct, "text/html") || strings.Contains(ct, "javascript") || strings.Contains(ct, "svg") {
		ct = "application/octet-stream" // never let a download render as a page
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) fileRoutes(h func(string, handler)) {
	fm := s.F.Files
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	created := func(v any, err error) (int, any, error) { return http.StatusCreated, v, err }
	q := func(r *http.Request, k string) string { return r.URL.Query().Get(k) }
	base := "/v1/accounts/{a}/files"

	h("GET "+base, func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(fm.List(r.Context(), pathAcct(r), q(r, "path")))
	})
	h("GET "+base+"/content", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		c, err := fm.ReadText(r.Context(), pathAcct(r), q(r, "path"))
		return ok(map[string]string{"path": q(r, "path"), "content": c}, err)
	})
	h("PUT "+base+"/content", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Content   string `json:"content"`
			Overwrite bool   `json:"overwrite"`
		}
		if !decodeBig(w, r, &body, files.MaxEdit+(64<<10)) {
			return 0, nil, nil
		}
		if len(body.Content) > files.MaxEdit {
			return 0, nil, errors.New("the editor saves files up to 2 MB; upload larger files")
		}
		return ok(map[string]string{"saved": q(r, "path")}, fm.Write(r.Context(), pathAcct(r), q(r, "path"), strings.NewReader(body.Content), body.Overwrite))
	})
	h("GET "+base+"/download", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		p, acct := q(r, "path"), pathAcct(r)
		ents, err := fm.List(r.Context(), acct, path.Dir(p))
		if err != nil {
			return 0, nil, err
		}
		isDir := false
		found := false
		for _, e := range ents {
			if e.Name == path.Base(p) {
				found, isDir = true, e.Type == "dir"
			}
		}
		if !found {
			return 0, nil, fmt.Errorf("%s %w", p, kit.ErrNotFound)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Hour)
		defer cancel()
		if isDir {
			attachment(w, path.Base(p)+".tar.gz")
			return 0, nil, fm.DownloadFolder(ctx, acct, p, w)
		}
		attachment(w, path.Base(p))
		return 0, nil, fm.Read(ctx, acct, p, w)
	})
	h("PUT "+base+"/upload", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 6*time.Hour)
		defer cancel()
		body := http.MaxBytesReader(w, r.Body, 50<<30)
		return created(map[string]string{"uploaded": q(r, "path")}, fm.Write(ctx, pathAcct(r), q(r, "path"), body, q(r, "overwrite") == "true"))
	})
	type opBody struct {
		Path      string   `json:"path"`
		Paths     []string `json:"paths"`
		From      []string `json:"from"`
		To        string   `json:"to"`
		Rename    bool     `json:"rename"`
		Mode      string   `json:"mode"`
		Recursive bool     `json:"recursive"`
		Dir       string   `json:"dir"`
		Items     []string `json:"items"`
		Name      string   `json:"name"`
		Format    string   `json:"format"`
		Into      string   `json:"into"`
	}
	h("POST "+base+"/{op}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var b opBody
		if !decodeBig(w, r, &b, 1<<20) {
			return 0, nil, nil
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Hour)
		defer cancel()
		a := pathAcct(r)
		var err error
		switch r.PathValue("op") {
		case "mkdir":
			err = fm.Mkdir(ctx, a, b.Path)
		case "move":
			err = fm.Move(ctx, a, b.From, b.To, b.Rename)
		case "copy":
			err = fm.Copy(ctx, a, b.From, b.To)
		case "delete":
			err = fm.Delete(ctx, a, b.Paths)
		case "chmod":
			err = fm.Chmod(ctx, a, b.Paths, b.Mode, b.Recursive)
		case "archive":
			err = fm.Archive(ctx, a, b.Dir, b.Items, b.Name, b.Format)
		case "extract":
			err = fm.Extract(ctx, a, b.Path, b.Into)
		default:
			return 0, nil, fmt.Errorf("unknown file operation %q", r.PathValue("op"))
		}
		return ok(map[string]string{"done": r.PathValue("op")}, err)
	})
	h("GET "+base+"/search", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(fm.Search(r.Context(), pathAcct(r), q(r, "path"), q(r, "name"), q(r, "content")))
	})
	h("GET "+base+"/sizes", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(fm.FolderSizes(r.Context(), pathAcct(r), q(r, "path")))
	})

	// snapshots
	h("GET /v1/accounts/{a}/snapshots", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(fm.Snapshots(pathAcct(r)))
	})
	h("POST /v1/accounts/{a}/snapshots", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Keep int `json:"keep"`
		}
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return 0, nil, nil
		}
		a := pathAcct(r)
		if err := s.F.Env.CheckAccount(a); err != nil {
			return 0, nil, err
		}
		return s.job("snapshot:"+a, "Snapshot of "+a, 6*time.Hour, func(ctx context.Context, log jobs.Logger) error {
			_, err := fm.TakeSnapshot(ctx, a, body.Keep, log)
			return err
		})
	})
	h("GET /v1/accounts/{a}/snapshots/{id}/files", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(fm.SnapshotList(r.Context(), pathAcct(r), r.PathValue("id"), q(r, "path")))
	})
	h("POST /v1/accounts/{a}/snapshots/{id}/restore", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Path      string `json:"path"`
			Overwrite bool   `json:"overwrite"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 6*time.Hour)
		defer cancel()
		return ok(map[string]string{"restored": body.Path}, fm.RestoreFromSnapshot(ctx, pathAcct(r), r.PathValue("id"), body.Path, body.Overwrite))
	})
	h("DELETE /v1/accounts/{a}/snapshots/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("id")}, fm.DeleteSnapshot(r.Context(), pathAcct(r), r.PathValue("id")))
	})

	// share links
	h("GET /v1/accounts/{a}/shares", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(fm.Shares(pathAcct(r)))
	})
	h("POST /v1/accounts/{a}/shares", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body files.ShareRequest
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(fm.CreateShare(r.Context(), pathAcct(r), actor(r), body))
	})
	h("DELETE /v1/accounts/{a}/shares/{token}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": "link"}, fm.DeleteShare(pathAcct(r), r.PathValue("token")))
	})
	h("GET /v1/shares/{token}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(fm.ShareInfo(r.PathValue("token")))
	})
	h("GET /v1/shares/{token}/download", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Hour)
		defer cancel()
		pw := r.Header.Get("X-Share-Password")
		err := fm.ServeShare(ctx, r.PathValue("token"), pw, func(name string) { attachment(w, name) }, w)
		if err != nil && w.Header().Get("Content-Disposition") != "" {
			return 0, nil, nil // already streaming; the download is cut short
		}
		return 0, nil, err
	})

	s.storageRoutes(h, "/v1/storage", func(*http.Request) string { return "" }, true)
	s.storageRoutes(h, "/v1/accounts/{a}/storage", pathAcct, false)

	// web terminal
	h("GET /v1/accounts/{a}/terminal", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		a := pathAcct(r)
		acct, err := s.Sites.GetAccount(a)
		if err != nil {
			return 0, nil, err
		}
		if acct.SSH != "shell" && !isAdmin(r) {
			return 0, nil, errors.New("the terminal needs shell access: turn it on under the account's SSH settings")
		}
		if acct.Suspended {
			return 0, nil, errors.New("the account is suspended")
		}
		cmd := exec.Command("runuser", "-l", a, "-s", "/bin/bash")
		cmd.Env = []string{"TERM=xterm-256color", "LANG=C.UTF-8"}
		return s.terminal(w, r, cmd, "account:"+a)
	})
	h("GET /v1/accounts/{a}/containers/{c}/terminal", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		name, err := s.Sites.OwnContainer(r.Context(), pathAcct(r), r.PathValue("c"))
		if err != nil {
			return 0, nil, err
		}
		shell := "/bin/sh"
		if q(r, "shell") == "bash" {
			shell = "/bin/bash"
		}
		cmd := exec.Command("docker", "exec", "-it", "-e", "TERM=xterm-256color", name, shell)
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		return s.terminal(w, r, cmd, "container:"+name)
	})
	h("GET /v1/terminal", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if !isAdmin(r) {
			return 0, nil, kit.ErrForbidden
		}
		cmd := exec.Command("/bin/bash", "-l")
		cmd.Dir = "/root"
		cmd.Env = []string{"TERM=xterm-256color", "LANG=C.UTF-8", "HOME=/root", "USER=root",
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
		return s.terminal(w, r, cmd, "server")
	})
}

func (s *Server) terminal(w http.ResponseWriter, r *http.Request, cmd *exec.Cmd, what string) (int, any, error) {
	if s.Config.DryRun {
		return 0, nil, errors.New("the terminal is not available in dry-run mode")
	}
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	ws, err := terminal.Upgrade(w, r)
	if err != nil {
		return 0, nil, nil // Upgrade already answered
	}
	s.F.Env.Publish("terminal.opened", what, map[string]any{"by": actor(r)})
	s.auditLine(r, "terminal "+what)
	_ = terminal.Serve(context.WithoutCancel(r.Context()), ws, cmd, cols, rows)
	return 0, nil, nil
}

func (s *Server) storageRoutes(h func(string, handler), prefix string, acct func(*http.Request) string, adminOnly bool) {
	st := s.F.Storage
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	wrap := func(fn handler) handler {
		if !adminOnly {
			return fn
		}
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if !isAdmin(r) {
				return 0, nil, kit.ErrForbidden
			}
			return fn(w, r)
		}
	}
	h("GET "+prefix, wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(st.List(acct(r)))
	}))
	h("POST "+prefix, wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var t storage.Target
		if !decode(w, r, &t) {
			return 0, nil, nil
		}
		t.ID = ""
		v, err := st.Save(r.Context(), acct(r), t)
		return http.StatusCreated, v, err
	}))
	h("PUT "+prefix+"/{id}", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var t storage.Target
		if !decode(w, r, &t) {
			return 0, nil, nil
		}
		t.ID = r.PathValue("id")
		return ok(st.Save(r.Context(), acct(r), t))
	}))
	h("DELETE "+prefix+"/{id}", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("id")}, st.Delete(acct(r), r.PathValue("id")))
	}))
	h("POST "+prefix+"/{id}/check", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]bool{"ok": true}, st.Check(r.Context(), acct(r), r.PathValue("id")))
	}))
	h("GET "+prefix+"/{id}/browse", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		t, err := st.Get(acct(r), r.PathValue("id"))
		if err != nil {
			return 0, nil, err
		}
		if !t.IsS3() {
			return 0, nil, errors.New("only S3-compatible storage can be browsed")
		}
		prefix := strings.TrimLeft(r.URL.Query().Get("prefix"), "/")
		if t.Prefix != "" && !strings.HasPrefix(prefix, t.Prefix) {
			prefix = t.Prefix + "/" + prefix
		}
		return ok(t.S3().List(r.Context(), prefix, 1000))
	}))
	if adminOnly {
		h("GET "+prefix+"/sftp-key", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			k, err := st.SFTPKey()
			return ok(map[string]string{"public_key": k}, err)
		}))
		return
	}
	h("POST "+prefix+"/{id}/upload", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Path string `json:"path"`
			Key  string `json:"key"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		a := acct(r)
		t, err := st.Get(a, r.PathValue("id"))
		if err != nil {
			return 0, nil, err
		}
		if !t.IsS3() {
			return 0, nil, errors.New("only S3-compatible storage takes single files")
		}
		key := strings.Trim(body.Key, "/")
		if key == "" {
			key = path.Base(body.Path)
		}
		if t.Prefix != "" {
			key = t.Prefix + "/" + key
		}
		return s.job("s3-upload:"+a, "Copy "+body.Path+" to "+t.Name, 6*time.Hour, func(ctx context.Context, log jobs.Logger) error {
			return s.uploadToS3(ctx, a, body.Path, t, key, log)
		})
	})
}

func (s *Server) uploadToS3(ctx context.Context, account, p string, t *storage.Target, key string, log func(string, ...any)) error {
	ents, err := s.F.Files.List(ctx, account, path.Dir(p))
	if err != nil {
		return err
	}
	var size int64 = -1
	for _, e := range ents {
		if e.Name == path.Base(p) && e.Type == "file" {
			size = e.Size
		}
	}
	if size < 0 {
		return fmt.Errorf("%s isn't a file; pack folders into an archive first", p)
	}
	log("Uploading %s (%d MB) to %s/%s", p, size>>20, t.Bucket, key)
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(s.F.Files.Read(ctx, account, p, pw)) }()
	if err := t.S3().Put(ctx, key, pr, size); err != nil {
		pr.CloseWithError(err)
		return err
	}
	log("Done")
	return nil
}

// auditLine records actions that don't go through a changing HTTP method
// (opening a terminal is a GET).
func (s *Server) auditLine(r *http.Request, what string) {
	if s.AuditPath == "" {
		return
	}
	line, _ := json.Marshal(map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "actor": actor(r), "method": "OPEN",
		"path": r.URL.Path, "query": what, "status": 101, "result": "ok"})
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if f, err := os.OpenFile(s.AuditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}
}
