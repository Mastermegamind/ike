package panel

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestShareLinkAndStreaming(t *testing.T) {
	e := setup(t)
	admin := e.browser(e.panel)
	if c := admin.login("andre", "admin-password-1", ""); c != 200 {
		t.Fatalf("login %d", c)
	}
	if c := admin.do("POST", "/api/v1/accounts", map[string]string{"name": "shop"}, nil); c != 201 {
		t.Fatalf("account %d", c)
	}
	var sh struct {
		Token   string `json:"token"`
		URLPath string `json:"url_path"`
	}
	if c := admin.do("POST", "/api/v1/accounts/shop/shares", map[string]any{"path": "web/report.pdf", "password": "secret1"}, &sh); c != 201 {
		t.Fatalf("share %d", c)
	}
	// public page, no session
	resp, err := http.Get(e.panel.URL + sh.URLPath)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "report.pdf") || !strings.Contains(string(b), `type="password"`) {
		t.Fatalf("page %d %s", resp.StatusCode, b)
	}
	resp, _ = http.PostForm(e.panel.URL+sh.URLPath, url.Values{"password": {"wrong"}})
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "isn&#39;t right") {
		t.Fatalf("wrong password page: %s", b)
	}
	resp, _ = http.PostForm(e.panel.URL+sh.URLPath, url.Values{"password": {"secret1"}})
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "report.pdf") {
		t.Fatalf("download headers %v", resp.Header)
	}
	resp, _ = http.Get(e.panel.URL + "/s/" + strings.Repeat("a", 32))
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("unknown link %d", resp.StatusCode)
	}
	// streamed download through the authenticated API
	req, _ := http.NewRequest("GET", e.panel.URL+"/api/v1/accounts/shop/dbs/mariadb/shop_x/export", nil)
	resp, err = admin.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("export of unknown db: %d", resp.StatusCode)
	}
}

func TestTerminalTunnel(t *testing.T) {
	e := setup(t)
	admin := e.browser(e.panel)
	admin.login("andre", "admin-password-1", "")
	u, _ := url.Parse(e.panel.URL)
	var cookie string
	for _, c := range admin.c.Jar.Cookies(u) {
		cookie = c.Name + "=" + c.Value
	}
	ws := func(origin string) (int, string) {
		c, err := net.Dial("tcp", u.Host)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_, _ = c.Write([]byte("GET /api/v1/terminal HTTP/1.1\r\nHost: " + u.Host + "\r\nOrigin: " + origin + "\r\nCookie: " + cookie +
			"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: MDEyMzQ1Njc4OWFiY2RlZg==\r\n\r\n"))
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := ws("https://evil.example"); code != 403 {
		t.Fatalf("cross-site terminal: %d", code)
	}
	// dry-run agent refuses terminals; the refusal comes back through the tunnel
	if code, body := ws("http://" + u.Host); code != 400 || !strings.Contains(body, "dry-run") {
		t.Fatalf("tunnel: %d %s", code, body)
	}
}
