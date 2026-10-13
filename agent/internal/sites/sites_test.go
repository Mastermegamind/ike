package sites

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YOUR-ORG/hostpanel/agent/internal/config"
	"github.com/YOUR-ORG/hostpanel/agent/internal/runner"
)

func newTestManager(t *testing.T) (*Manager, *runner.DryRun, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	if err := os.MkdirAll(filepath.Join(root, "etc/php/8.3/fpm/pool.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &runner.DryRun{}
	return NewManager(cfg, r), r, root
}

func TestCreateSite(t *testing.T) {
	m, r, root := newTestManager(t)
	site, err := m.Create(context.Background(), CreateRequest{Domain: "example.com", User: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if site.PHP != "8.3" || site.SSH != SSHNone {
		t.Fatalf("unexpected site: %+v", site)
	}

	vhost, err := os.ReadFile(filepath.Join(root, "etc/nginx/hostpanel/sites/example.com.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"server_name example.com www.example.com;",
		"root /home/example/web/example.com/public_html;",
		"fastcgi_pass unix:/run/php/php8.3-fpm-example.sock;",
		"access_log /var/log/hostpanel/example.com/access.log;",
	} {
		if !strings.Contains(string(vhost), want) {
			t.Errorf("vhost missing %q", want)
		}
	}

	pool, err := os.ReadFile(filepath.Join(root, "etc/php/8.3/fpm/pool.d/hp-example.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[example]", "user = example", "listen.owner = www-data",
		"open_basedir] = /home/example/web/example.com:/home/example/tmp"} {
		if !strings.Contains(string(pool), want) {
			t.Errorf("pool missing %q", want)
		}
	}

	if _, err := os.Stat(filepath.Join(root, "home/example/web/example.com/public_html/index.html")); err != nil {
		t.Errorf("index.html not created: %v", err)
	}
	fi, _ := os.Stat(filepath.Join(root, "home/example"))
	if fi.Mode().Perm() != 0o751 {
		t.Errorf("home mode = %v, want 0751", fi.Mode().Perm())
	}

	cmds := strings.Join(r.History(), "\n")
	for _, want := range []string{"useradd --create-home --home-dir /home/example --shell /usr/sbin/nologin --user-group example",
		"php-fpm8.3 --test", "systemctl reload php8.3-fpm", "nginx -t", "systemctl reload nginx"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("missing command %q in:\n%s", want, cmds)
		}
	}

	list, _ := m.List()
	if len(list) != 1 || list[0].Domain != "example.com" {
		t.Errorf("List() = %+v", list)
	}
}

func TestCreateRejectsDuplicatesAndBadInput(t *testing.T) {
	m, _, _ := newTestManager(t)
	ctx := context.Background()
	if _, err := m.Create(ctx, CreateRequest{Domain: "example.com", User: "example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: "example.com", User: "other"}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate domain: got %v", err)
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: "second.com", User: "example"}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate user: got %v", err)
	}
	for _, req := range []CreateRequest{
		{Domain: "bad domain.com", User: "okuser"},
		{Domain: "ok.com", User: "root"},
		{Domain: "ok.com", User: "okuser", PHP: "8.3;id"},
		{Domain: "ok.com", User: "okuser", PHP: "7.4"}, // not installed
	} {
		if _, err := m.Create(ctx, req); err == nil {
			t.Errorf("expected error for %+v", req)
		}
	}
}

func TestCreateRollsBackWhenNginxTestFails(t *testing.T) {
	m, r, root := newTestManager(t)
	r.FailOn = "nginx -t"
	if _, err := m.Create(context.Background(), CreateRequest{Domain: "broken.com", User: "broken"}); err == nil {
		t.Fatal("expected error")
	}
	for _, p := range []string{
		"etc/nginx/hostpanel/sites/broken.com.conf",
		"etc/php/8.3/fpm/pool.d/hp-broken.conf",
		"var/lib/hostpanel/sites/broken.com.json",
		"var/log/hostpanel/broken.com",
		"home/broken",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("%s should have been rolled back", p)
		}
	}
	if !strings.Contains(strings.Join(r.History(), "\n"), "userdel --remove broken") {
		t.Error("user was not removed during rollback")
	}
}

func TestDeleteSite(t *testing.T) {
	m, r, root := newTestManager(t)
	ctx := context.Background()
	if _, err := m.Create(ctx, CreateRequest{Domain: "gone.com", User: "gone"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "gone.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/nginx/hostpanel/sites/gone.com.conf")); !os.IsNotExist(err) {
		t.Error("vhost still exists")
	}
	if !strings.Contains(strings.Join(r.History(), "\n"), "userdel --remove gone") {
		t.Error("userdel not called")
	}
	if err := m.Delete(ctx, "gone.com", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: got %v", err)
	}
}

func testKey(t *testing.T, comment string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		l := make([]byte, 4)
		binary.BigEndian.PutUint32(l, uint32(len(part)))
		blob = append(append(blob, l...), part...)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " " + comment
}

func TestParsePublicKey(t *testing.T) {
	good := testKey(t, "andre@laptop")
	k, err := ParsePublicKey(good)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Fingerprint, "SHA256:") || k.Comment != "andre@laptop" {
		t.Errorf("unexpected key %+v", k)
	}
	b64 := strings.Fields(good)[1]
	bad := []string{
		"",
		"ssh-ed25519",
		`command="rm -rf /" ` + good,
		"ssh-rsa " + b64, // blob says ed25519
		"ssh-ed25519 not-base64!!",
		good + "\nssh-ed25519 " + b64,
		"ssh-ed25519 " + b64 + " evil;comment",
		"ssh-dss " + b64,
	}
	for _, s := range bad {
		if _, err := ParsePublicKey(s); err == nil {
			t.Errorf("ParsePublicKey(%q) expected error", s)
		}
	}
}

func TestSSHAccessAndKeys(t *testing.T) {
	m, r, root := newTestManager(t)
	ctx := context.Background()
	if _, err := m.Create(ctx, CreateRequest{Domain: "ssh.com", User: "sshuser"}); err != nil {
		t.Fatal(err)
	}

	site, err := m.SetSSHMode(ctx, "ssh.com", SSHSFTP)
	if err != nil || site.SSH != SSHSFTP {
		t.Fatalf("SetSSHMode sftp: %v %+v", err, site)
	}
	if !strings.Contains(strings.Join(r.History(), "\n"), "usermod --shell /usr/sbin/nologin --groups hp-sftp sshuser") {
		t.Error("sftp usermod not called")
	}
	if _, err := m.SetSSHMode(ctx, "ssh.com", "root"); err == nil {
		t.Error("invalid mode accepted")
	}
	if got, _ := m.Get("ssh.com"); got.SSH != SSHSFTP {
		t.Errorf("mode not persisted: %+v", got)
	}

	key := testKey(t, "laptop")
	k, err := m.AddSSHKey("ssh.com", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddSSHKey("ssh.com", key); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate key: %v", err)
	}
	keys, _ := m.ListSSHKeys("ssh.com")
	if len(keys) != 1 || keys[0].Fingerprint != k.Fingerprint {
		t.Fatalf("ListSSHKeys = %+v", keys)
	}
	b, _ := os.ReadFile(filepath.Join(root, "etc/ssh/hostpanel/keys/sshuser"))
	if !strings.Contains(string(b), strings.Fields(key)[1]) {
		t.Error("key not written to authorized keys file")
	}
	if err := m.RemoveSSHKey("ssh.com", k.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if keys, _ := m.ListSSHKeys("ssh.com"); len(keys) != 0 {
		t.Errorf("key not removed: %+v", keys)
	}
}
