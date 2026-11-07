package panel

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/cluster"
)

func TestJoinAndRemoteProxy(t *testing.T) {
	e := setup(t)
	e.srv.ClusterDir = filepath.Join(t.TempDir(), "cluster")
	e.srv.CertFingerprint = "AA:BB"
	admin := e.browser(e.admin)
	if c := admin.login("marvellous", "admin-password-1", ""); c != 200 {
		t.Fatal(c)
	}
	var tok struct {
		Token   string `json:"token"`
		Command string `json:"command"`
	}
	if c := admin.do("POST", "/api/v1/servers/join-token", map[string]string{"name": "Lagos 2", "group": "africa"}, &tok); c != 201 || tok.Token == "" {
		t.Fatalf("token %d", c)
	}
	// the new server asks to join
	keyPEM, csrPEM, _ := cluster.NewCSR("join")
	body, _ := json.Marshal(map[string]any{"token": tok.Token, "csr": string(csrPEM), "hostname": "lagos2.example.com"})
	resp, err := http.Post(e.admin.URL+"/cluster/join", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var j struct {
		ServerID string `json:"server_id"`
		Cert     string `json:"cert"`
		CA       string `json:"ca"`
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || json.Unmarshal(b, &j) != nil || j.ServerID == "" {
		t.Fatalf("join %d %s", resp.StatusCode, b)
	}
	// the token works once
	resp, _ = http.Post(e.admin.URL+"/cluster/join", "application/json", bytes.NewReader(body))
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("token reused: %d", resp.StatusCode)
	}
	// the joined server's agent: mutual TLS, panel certificate only
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "agent.key"), keyPEM, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "agent.crt"), []byte(j.Cert), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "ca.crt"), []byte(j.CA), 0o600)
	tc, err := cluster.ServerTLS(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tc)
	if err != nil {
		t.Fatal(err)
	}
	remote := http.NewServeMux()
	remote.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","version":"9.9.9"}`))
	})
	remote.HandleFunc("GET /v1/accounts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"remote-acct","actor":"` + r.Header.Get("X-Ike-Actor") + `"}]`))
	})
	go func() { _ = http.Serve(ln, remote) }()
	defer ln.Close()
	if c := admin.do("PATCH", "/api/v1/servers/"+j.ServerID, map[string]string{"name": "Lagos 2", "group": "africa", "address": ln.Addr().String()}, nil); c != 200 {
		t.Fatalf("patch %d", c)
	}
	var list []map[string]any
	admin.do("GET", "/api/v1/servers", nil, &list)
	if len(list) != 2 || list[1]["status"] != "online" || list[1]["version"] != "9.9.9" {
		t.Fatalf("servers %+v", list)
	}
	// panel requests for that server go over mutual TLS
	req, _ := http.NewRequest("GET", e.admin.URL+"/api/v1/accounts", nil)
	req.Header.Set("X-Ike-Server", j.ServerID)
	resp, err = admin.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`"remote-acct"`)) || !bytes.Contains(b, []byte(`"actor":"marvellous"`)) {
		t.Fatalf("remote proxy %d %s", resp.StatusCode, b)
	}
	// nobody else can talk to the agent port
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	if err == nil {
		_, _ = conn.Write([]byte("GET /v1/accounts HTTP/1.1\r\nHost: x\r\n\r\n"))
		buf := make([]byte, 64)
		n, rerr := conn.Read(buf)
		conn.Close()
		if rerr == nil && bytes.Contains(buf[:n], []byte("200")) {
			t.Fatal("agent answered without a panel certificate")
		}
	}
	// fan-out
	var fan []map[string]any
	if c := admin.do("POST", "/api/v1/fanout", map[string]any{"group": "africa", "servers": []string{"local"}, "method": "GET", "path": "/v1/health"}, &fan); c != 200 || len(fan) != 2 {
		t.Fatalf("fanout %d %+v", c, fan)
	}
	_ = net.IPv4len
}
