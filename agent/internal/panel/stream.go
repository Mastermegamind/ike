package panel

import (
	"bufio"
	"context"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/panel/store"
)

// isStream reports whether a proxied request carries a file instead of JSON:
// downloads, exports, uploads and imports are streamed both ways without
// being held in memory.
func isStream(method, apiPath string) bool {
	switch method {
	case http.MethodGet:
		return strings.HasSuffix(apiPath, "/download") || strings.HasSuffix(apiPath, "/export")
	case http.MethodPut:
		return strings.HasSuffix(apiPath, "/files/upload")
	case http.MethodPost:
		return strings.HasSuffix(apiPath, "/import")
	}
	return false
}

func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// streamProxy forwards a file transfer after the usual authorization (done
// on the path alone; the body is the file).
func (s *Server) streamProxy(w http.ResponseWriter, r *http.Request, p *store.Person, apiPath string) {
	if _, _, err := s.authorize(r.Context(), p, r.Method, apiPath, nil); err != nil {
		failErr(w, err)
		return
	}
	if err := s.checkPackageLimits(r.Context(), p, r.Method, apiPath, nil); err != nil {
		failErr(w, err)
		return
	}
	target := s.Agent.BaseURL + apiPath
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	var body io.Reader
	if r.Method != http.MethodGet {
		body = r.Body
	}
	req, _ := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	req.ContentLength = r.ContentLength
	req.Header.Set("Content-Type", "application/octet-stream")
	s.actorHeaders(req, p)
	resp, err := s.Agent.Stream.Do(req)
	if err != nil {
		fail(w, 502, "The server agent isn't answering. Check that ike-agent is running.")
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Disposition", "Content-Length", "X-Content-Type-Options"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(flushWriter{w}, resp.Body)
}

type flushWriter struct{ w http.ResponseWriter }

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

func (s *Server) actorHeaders(req *http.Request, p *store.Person) {
	actor := p.Username
	if p.Role == store.Reseller {
		actor = "reseller:" + p.Username
	}
	req.Header.Set("X-Ike-Actor", actor)
	req.Header.Set("X-Ike-Role", string(p.Role))
}

// sameOrigin stops other websites from opening a terminal with the user's
// cookie (browsers don't apply CORS to WebSockets).
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// wsTunnel authorizes a WebSocket request, forwards the handshake to the
// agent and then copies bytes both ways until either side closes.
func (s *Server) wsTunnel(w http.ResponseWriter, r *http.Request, p *store.Person, apiPath string) {
	if !sameOrigin(r) {
		fail(w, 403, "Open the terminal from the panel itself.")
		return
	}
	if _, _, err := s.authorize(r.Context(), p, r.Method, apiPath, nil); err != nil {
		failErr(w, err)
		return
	}
	if s.Agent.Dial == nil {
		fail(w, 502, "The terminal isn't available.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ac, err := s.Agent.Dial(ctx)
	if err != nil {
		fail(w, 502, "The server agent isn't answering.")
		return
	}
	target := apiPath
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, _ := http.NewRequest(http.MethodGet, "http://ike-agent"+target, nil)
	for _, h := range []string{"Upgrade", "Connection", "Sec-WebSocket-Key", "Sec-WebSocket-Version"} {
		req.Header.Set(h, r.Header.Get(h))
	}
	s.actorHeaders(req, p)
	if err := req.Write(ac); err != nil {
		ac.Close()
		fail(w, 502, "The server agent isn't answering.")
		return
	}
	br := bufio.NewReader(ac)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		ac.Close()
		fail(w, 502, "The server agent isn't answering.")
		return
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer ac.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(b)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		ac.Close()
		fail(w, 500, "The connection can't be upgraded.")
		return
	}
	cc, cbuf, err := hj.Hijack()
	if err != nil {
		ac.Close()
		return
	}
	_ = cc.SetDeadline(time.Time{})
	fmt.Fprintf(cc, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", resp.Header.Get("Sec-WebSocket-Accept"))
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(ac, cbuf); done <- struct{}{} }()
	go func() { _, _ = io.Copy(cc, br); done <- struct{}{} }()
	<-done
	cc.Close()
	ac.Close()
}

// ---- public share links: /s/<token> ----

var sharePage = template.Must(template.New("share").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Name}}</title><meta name="robots" content="noindex">
<style>
:root{--bg:#f6f7f9;--card:#fff;--fg:#14171c;--muted:#5d6672;--accent:#2357d6;--line:#dde1e7;--bad:#b42318}
@media (prefers-color-scheme:dark){:root{--bg:#111418;--card:#1a1e24;--fg:#e9edf2;--muted:#9aa4b1;--accent:#7aa2ff;--line:#2b313a;--bad:#ff8a80}}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;padding:16px;box-sizing:border-box}
main{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:28px;max-width:420px;width:100%;box-sizing:border-box}
h1{font-size:1.15rem;margin:0 0 4px;word-break:break-word}p{color:var(--muted);margin:0 0 20px;font-size:.92rem}
input{width:100%;box-sizing:border-box;padding:11px 12px;border:1px solid var(--line);border-radius:9px;background:transparent;color:inherit;font:inherit;margin-bottom:12px}
button{width:100%;padding:12px;border:0;border-radius:9px;background:var(--accent);color:#fff;font:inherit;font-weight:600;cursor:pointer}
.err{color:var(--bad);font-size:.9rem;margin:-4px 0 12px}
</style></head><body><main>
{{if .Gone}}<h1>Link unavailable</h1><p>This link has expired, reached its download limit, or was removed. Ask the sender for a new one.</p>
{{else}}<h1>{{.Name}}</h1><p>Shared with you{{if .Folder}} as a compressed folder{{end}}. Available until {{.Expires}}.</p>
<form method="post" action="">
{{if .NeedsPassword}}<label for="pw" style="display:block;font-size:.9rem;margin-bottom:6px">Password</label><input id="pw" name="password" type="password" autocomplete="off" required autofocus>{{end}}
{{if .Error}}<div class="err" role="alert">{{.Error}}</div>{{end}}
<button type="submit">Download</button></form>{{end}}
</main></body></html>`))

// shareLink shows a share's page (GET) and streams the file (POST).
func (s *Server) shareLink(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.webhookLimiter.allow("share:" + ip) {
		http.Error(w, "Too many requests. Wait a minute and try again.", http.StatusTooManyRequests)
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Referrer-Policy", "no-referrer")
	var info struct {
		Name          string    `json:"name"`
		Folder        bool      `json:"folder"`
		NeedsPassword bool      `json:"needs_password"`
		ExpiresAt     time.Time `json:"expires_at"`
	}
	data := map[string]any{}
	if err := s.Agent.Get(r.Context(), "/v1/shares/"+url.PathEscape(token), &info); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = sharePage.Execute(w, map[string]any{"Gone": true, "Name": "Link unavailable"})
		return
	}
	data["Name"], data["Folder"], data["NeedsPassword"] = info.Name, info.Folder, info.NeedsPassword
	data["Expires"] = info.ExpiresAt.Format("2 Jan 2006, 15:04 UTC")
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, s.Agent.BaseURL+"/v1/shares/"+url.PathEscape(token)+"/download", nil)
		req.Header.Set("X-Share-Password", r.PostForm.Get("password"))
		req.Header.Set("X-Ike-Actor", "share:"+ip)
		resp, err := s.Agent.Stream.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			for _, h := range []string{"Content-Type", "Content-Disposition", "X-Content-Type-Options"} {
				w.Header().Set(h, resp.Header.Get(h))
			}
			_, _ = io.Copy(flushWriter{w}, resp.Body)
			return
		}
		if resp != nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusForbidden {
				data["Error"] = "That password isn't right."
			} else {
				data["Gone"] = true
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	_ = sharePage.Execute(w, data)
}
