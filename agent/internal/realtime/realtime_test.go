package realtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
)

type fakeFW struct{ open, closed []string }

func (f *fakeFW) AllowFrom(_ context.Context, from, port, proto, comment string) error {
	f.open = append(f.open, port+"/"+proto)
	return nil
}
func (f *fakeFW) Close(_ context.Context, port, proto string) error {
	f.closed = append(f.closed, port+"/"+proto)
	return nil
}

type accounts map[string]bool

func (a accounts) AccountExists(n string) error {
	if !a[n] {
		return kit.ErrNotFound
	}
	return nil
}

func setup(t *testing.T) (*Manager, *runner.DryRun, *fakeFW, *secrets.Keyring, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun, cfg.PublicIPv4 = root, true, "203.0.113.7"
	run := &runner.DryRun{}
	fw := &fakeFW{}
	keys, err := secrets.Open(filepath.Join(root, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(&kit.Env{Cfg: cfg, Run: run, Firewall: fw, Accounts: accounts{"shop": true, "media": true}}, keys)
	m.CertFiles = func(name string) (string, string, bool) {
		return "/etc/letsencrypt/live/" + name + "/fullchain.pem", "/etc/letsencrypt/live/" + name + "/privkey.pem", name == "turn.example.ng"
	}
	return m, run, fw, keys, root
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTurn(t *testing.T) {
	m, run, fw, _, root := setup(t)
	ctx := context.Background()
	if _, err := m.SetTurn(ctx, TurnRequest{Enabled: true, Domain: "not a domain"}); err == nil {
		t.Fatal("bad domain accepted")
	}
	st, err := m.SetTurn(ctx, TurnRequest{Enabled: true, Domain: "turn.example.ng", TLS: true, UserQuota: 10, MaxKbps: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Turn.CertReady || st.Turn.Secret != "" || len(st.Turn.URIs) != 4 || !strings.HasPrefix(st.Turn.URIs[3], "turns:turn.example.ng:5349") {
		t.Fatalf("%+v", st.Turn)
	}
	conf := read(t, filepath.Join(root, "etc/turnserver.conf"))
	for _, want := range []string{"use-auth-secret", "static-auth-secret=", "realm=turn.example.ng", "external-ip=203.0.113.7",
		"min-port=40000", "max-port=49999", "tls-listening-port=5349", "cert=/etc/ike/realtime/turn/fullchain.pem",
		"denied-peer-ip=10.0.0.0-10.255.255.255", "denied-peer-ip=192.168.0.0-192.168.255.255", "denied-peer-ip=127.0.0.0-127.255.255.255",
		"user-quota=10", "max-bps=250000", "no-cli", "no-tlsv1_1"} {
		if !strings.Contains(conf, want) {
			t.Errorf("turnserver.conf missing %q", want)
		}
	}
	secret := ""
	for _, l := range strings.Split(conf, "\n") {
		if v, ok := strings.CutPrefix(l, "static-auth-secret="); ok {
			secret = v
		}
	}
	if len(secret) != 64 {
		t.Fatalf("secret %q", secret)
	}
	// the stored secret is sealed, not plain
	if strings.Contains(read(t, filepath.Join(root, "etc/ike/realtime/realtime.json")), secret) {
		t.Fatal("TURN secret stored in plain text")
	}
	if strings.Contains(strings.Join(run.History(), "\n"), secret) {
		t.Fatal("secret on a command line")
	}
	if !strings.Contains(strings.Join(fw.open, " "), "40000:49999/udp") || !strings.Contains(strings.Join(fw.open, " "), "5349/tcp") {
		t.Fatalf("ports %v", fw.open)
	}

	// credentials verify the way coturn checks them
	c, err := m.TurnCredentials("", "alice", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := hmac.New(sha1.New, []byte(secret))
	h.Write([]byte(c.Username))
	if c.Credential != base64.StdEncoding.EncodeToString(h.Sum(nil)) || !strings.HasSuffix(c.Username, ":alice") {
		t.Fatalf("%+v", c)
	}
	exp, _ := strconv.ParseInt(strings.Split(c.Username, ":")[0], 10, 64)
	if d := time.Until(time.Unix(exp, 0)); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("expiry %v", d)
	}
	if _, err := m.TurnCredentials("", "x", 8*24*time.Hour); err == nil {
		t.Fatal("8-day credential allowed")
	}
	// accounts only when allowed, and their name is in the username
	if _, err := m.TurnCredentials("shop", "app", 0); !errors.Is(err, kit.ErrForbidden) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.SetTurn(ctx, TurnRequest{Enabled: true, Domain: "turn.example.ng", TLS: true, AllowAccounts: true}); err != nil {
		t.Fatal(err)
	}
	c, err = m.TurnCredentials("shop", "app", 0)
	if err != nil || !strings.HasSuffix(c.Username, ":shop:app") || c.TTL != 12*3600 {
		t.Fatalf("%+v %v", c, err)
	}

	// rotating changes the secret in the config
	if _, err := m.RotateTurnSecret(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, filepath.Join(root, "etc/turnserver.conf")), secret) {
		t.Fatal("secret not rotated")
	}

	// switching off stops coturn and closes only what TURN opened
	if _, err := m.SetTurn(ctx, TurnRequest{Enabled: false, Domain: "turn.example.ng"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(run.History(), "\n"), "systemctl disable --now coturn") || len(fw.closed) == 0 {
		t.Fatalf("not stopped: %v", fw.closed)
	}
	if _, err := m.TurnCredentials("", "a", 0); err == nil {
		t.Fatal("credentials while off")
	}
}

func TestTurnWithoutCertificate(t *testing.T) {
	m, _, _, _, root := setup(t)
	st, err := m.SetTurn(context.Background(), TurnRequest{Enabled: true, Domain: "relay.example.ng", TLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Notes) == 0 || !strings.Contains(strings.Join(st.Notes, " "), "certificate") {
		t.Fatalf("no note: %v", st.Notes)
	}
	conf := read(t, filepath.Join(root, "etc/turnserver.conf"))
	if strings.Contains(conf, "tls-listening-port") || !strings.Contains(conf, "no-tls") {
		t.Fatal("TLS configured without a certificate")
	}
}

func TestLiveKit(t *testing.T) {
	m, run, fw, keys, root := setup(t)
	ctx := context.Background()
	var proxied string
	m.ProxySite = func(_ context.Context, account, domain string, port int) (string, error) {
		proxied = account + " " + domain + " " + strconv.Itoa(port)
		return "", nil
	}
	if _, _, err := m.SetLiveKit(ctx, LiveKitRequest{Enabled: true, Domain: "rtc.example.ng", Account: "nobody"}); err == nil {
		t.Fatal("unknown account accepted")
	}
	if _, _, err := m.SetLiveKit(ctx, LiveKitRequest{Enabled: true, Domain: "rtc.example.ng", Account: "media", UseTurn: true}); err == nil {
		t.Fatal("TURN used while off")
	}
	if _, _, err := m.SetLiveKit(ctx, LiveKitRequest{Enabled: true, Domain: "rtc.example.ng", Account: "media", WebhookURLs: []string{"http://x.ng/hook"}}); err == nil {
		t.Fatal("http webhook accepted")
	}
	if _, err := m.SetTurn(ctx, TurnRequest{Enabled: true, Domain: "turn.example.ng", TLS: true}); err != nil {
		t.Fatal(err)
	}
	st, first, err := m.SetLiveKit(ctx, LiveKitRequest{Enabled: true, Domain: "rtc.example.ng", Account: "media", UseTurn: true,
		WebhookURLs: []string{"https://app.example.ng/livekit"}})
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || !keyIDRe.MatchString(first.Key) || len(first.Secret) != 40 {
		t.Fatalf("first key %+v", first)
	}
	if st.LiveKit.URL != "wss://rtc.example.ng" || st.LiveKit.Keys[0].Secret != "" || proxied != "media rtc.example.ng 7880" {
		t.Fatalf("%+v %q", st.LiveKit, proxied)
	}
	conf := read(t, filepath.Join(root, "etc/livekit/livekit.yaml"))
	for _, want := range []string{"port: 7880", "  - 127.0.0.1", "tcp_port: 7881", "port_range_start: 50000", `node_ip: "203.0.113.7"`,
		"turn_servers:", `host: "turn.example.ng"`, "protocol: tls", "protocol: udp", ":livekit\"", "  " + first.Key + `: "` + first.Secret + `"`,
		"empty_timeout: 300", "api_key: " + first.Key, `- "https://app.example.ng/livekit"`} {
		if !strings.Contains(conf, want) {
			t.Errorf("livekit.yaml missing %q\n%s", want, conf)
		}
	}
	if strings.Contains(read(t, filepath.Join(root, "etc/ike/realtime/realtime.json")), first.Secret) {
		t.Fatal("API secret stored in plain text")
	}
	if !strings.Contains(strings.Join(fw.open, " "), "50000:60000/udp") {
		t.Fatalf("ports %v", fw.open)
	}

	// a join token LiveKit would accept
	tok, err := m.JoinToken(TokenRequest{Room: "class-1", Identity: "ada", Name: "Ada"}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok.Token, ".")
	mac := hmac.New(sha256.New, []byte(first.Secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if len(parts) != 3 || base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
		t.Fatal("bad signature")
	}
	var claims struct {
		Iss   string         `json:"iss"`
		Sub   string         `json:"sub"`
		Exp   int64          `json:"exp"`
		Video map[string]any `json:"video"`
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(raw, &claims)
	if claims.Iss != first.Key || claims.Sub != "ada" || claims.Video["room"] != "class-1" || claims.Video["roomJoin"] != true || claims.Video["canPublish"] != true {
		t.Fatalf("%+v", claims)
	}
	if _, err := m.JoinToken(TokenRequest{Room: "bad room!", Identity: "a"}, "", true); err == nil {
		t.Fatal("bad room accepted")
	}

	// accounts make keys only when allowed, and only see their own
	if _, err := m.CreateKey(ctx, "Shop app", "shop", false); !errors.Is(err, kit.ErrForbidden) {
		t.Fatalf("got %v", err)
	}
	if _, _, err := m.SetLiveKit(ctx, LiveKitRequest{Enabled: true, Domain: "rtc.example.ng", Account: "media", UseTurn: true, AllowAccounts: true}); err != nil {
		t.Fatal(err)
	}
	sk, err := m.CreateKey(ctx, "Shop app", "shop", false)
	if err != nil || sk.Secret == "" || sk.Account != "shop" {
		t.Fatalf("%+v %v", sk, err)
	}
	if !strings.Contains(read(t, filepath.Join(root, "etc/livekit/livekit.yaml")), sk.Key) {
		t.Fatal("new key not in config")
	}
	v, err := m.ForAccount("shop")
	if err != nil || len(v.LiveKitKeys) != 1 || v.LiveKitKeys[0].Key != sk.Key || v.LiveKitKeys[0].Secret != "" || !v.CanMakeKeys {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err := m.JoinToken(TokenRequest{Key: first.Key, Room: "r", Identity: "i"}, "shop", false); err == nil {
		t.Fatal("account used the admin's key")
	}
	if tok, err := m.JoinToken(TokenRequest{Room: "r", Identity: "i"}, "shop", false); err != nil || !strings.Contains(tok.Token, ".") {
		t.Fatalf("own key: %v", err)
	}
	if err := m.DeleteKey(ctx, first.Key, "shop", false); err == nil {
		t.Fatal("account deleted the admin's key")
	}
	if err := m.DeleteKey(ctx, sk.Key, "shop", false); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteKey(ctx, first.Key, "", true); err == nil {
		t.Fatal("deleted the last key")
	}

	// key rotation re-seals and the secrets still open
	if _, err := keys.Rotate(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.JoinToken(TokenRequest{Room: "r", Identity: "i"}, "", true); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
	if !strings.Contains(strings.Join(run.History(), "\n"), "systemctl restart livekit") {
		t.Fatal("livekit not restarted")
	}
	// switching off stops it and closes its ports
	if _, _, err := m.SetLiveKit(ctx, LiveKitRequest{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(fw.closed, " "), "50000:60000/udp") {
		t.Fatalf("closed %v", fw.closed)
	}
}

func TestDeleteAccountRemovesKeys(t *testing.T) {
	m, _, _, _, _ := setup(t)
	ctx := context.Background()
	if _, _, err := m.SetLiveKit(ctx, LiveKitRequest{Enabled: true, Domain: "rtc.example.ng", Account: "media", AllowAccounts: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateKey(ctx, "x", "shop", false); err != nil {
		t.Fatal(err)
	}
	m.DeleteAccount(ctx, "shop")
	st, _ := m.Status(ctx)
	if len(st.LiveKit.Keys) != 1 {
		t.Fatalf("%+v", st.LiveKit.Keys)
	}
}
