package sites

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

func TestSFTPUsers(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "shop")
	if _, err := m.Create(ctx, CreateRequest{Domain: "shop.ng", Account: "shop"}); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(root, "etc/ssh"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "etc/ssh/sshd_config"), []byte("Port 22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Commands = nil

	for _, bad := range []struct {
		req  SFTPUserRequest
		want string
	}{
		{SFTPUserRequest{Name: "Designer", Domain: "shop.ng"}, "lowercase"},
		{SFTPUserRequest{Name: "waytoolongname1", Domain: "shop.ng"}, "1-12"},
		{SFTPUserRequest{Name: "ok", Domain: "other.ng"}, "not found"},
		{SFTPUserRequest{Name: "ok", Domain: "shop.ng", Folder: ptr("../../etc")}, "not a folder"},
		{SFTPUserRequest{Name: "ok", Domain: "shop.ng", Folder: ptr("nope")}, "has no folder"},
		{SFTPUserRequest{Name: "ok", Domain: "shop.ng", Password: ptr("short")}, "10-200"},
		{SFTPUserRequest{Name: "ok", Domain: "shop.ng", Expires: ptr("2020-01-01")}, "already passed"},
	} {
		if _, _, err := m.CreateSFTPUser(ctx, "shop", bad.req); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%+v: want %q, got %v", bad.req, bad.want, err)
		}
	}

	u, pw, err := m.CreateSFTPUser(ctx, "shop", SFTPUserRequest{Name: "designer", Domain: "shop.ng", Expires: ptr("2099-12-31")})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "shop_designer" || pw == "" || !u.Password || u.Folder != "public_html" {
		t.Fatalf("user %+v, password %q", u, pw)
	}
	joined := strings.Join(r.Commands, "\n")
	for _, want := range []string{
		"groupadd --force --system ike-sftpsite",
		"useradd --non-unique --uid 1001 --gid 1001 --no-create-home --home-dir /shop.ng --shell /usr/sbin/nologin --groups ike-sftpsite --comment Ike SFTP for shop.ng --expiredate 2099-12-31 shop_designer",
		"chpasswd --encrypted",
		"systemctl enable --now var-lib-ike-sftp-shop_designer-shop.ng.mount",
		"sshd -t",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing command %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, pw) {
		t.Fatal("the password appeared in a command line")
	}
	for _, in := range r.Stdin {
		if strings.HasPrefix(in, "shop_designer:") && (!strings.Contains(in, ":$6$") || strings.Contains(in, pw)) {
			t.Fatalf("chpasswd input should be a $6$ hash, got %q", in)
		}
	}
	unit := read(t, root, "etc/systemd/system/var-lib-ike-sftp-shop_designer-shop.ng.mount")
	has(t, "mount unit", unit, "What=/home/shop/web/shop.ng/public_html", "Where=/var/lib/ike/sftp/shop_designer/shop.ng", "Options=bind\n")
	sshd := read(t, root, "etc/ssh/sshd_config")
	has(t, "sshd_config", sshd, "Port 22\n", "Match Group ike-sftpsite\n    ChrootDirectory /var/lib/ike/sftp/%u\n    ForceCommand internal-sftp\n",
		"Match Group ike-sftpsite-ro\n", "ForceCommand internal-sftp -R\n", "PermitTTY no")
	// The block is only added once.
	if _, _, err := m.CreateSFTPUser(ctx, "shop", SFTPUserRequest{Name: "viewer", Domain: "shop.ng", ReadOnly: ptr(true), Folder: ptr(""),
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGxEWh2wV7jm2b0b7nKgbG3o3fJ3u4Qe5d9Ff0b6m9fX v@laptop"}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(read(t, root, "etc/ssh/sshd_config"), "Match Group ike-sftpsite\n"); n != 1 {
		t.Fatalf("sshd block written %d times", n)
	}
	has(t, "read-only unit", read(t, root, "etc/systemd/system/var-lib-ike-sftp-shop_viewer-shop.ng.mount"), "What=/home/shop/web/shop.ng\n", "Options=bind,ro")
	has(t, "keys", read(t, root, "etc/ssh/ike/keys/shop_viewer"), "restrict ssh-ed25519 AAAAC3")
	if _, _, err := m.CreateSFTPUser(ctx, "shop", SFTPUserRequest{Name: "viewer", Domain: "shop.ng"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate: %v", err)
	}
	list, _ := m.ListSFTPUsers("shop")
	if len(list) != 2 || len(list[1].Keys) != 1 || list[1].Keys[0].Fingerprint == "" {
		t.Fatalf("list = %+v", list)
	}

	// A key-only user can't drop its last way in.
	if _, _, err := m.UpdateSFTPUser(ctx, "shop", "shop_viewer", SFTPUserRequest{Keys: []string{}}); err == nil {
		t.Fatal("removing the only key of a user without a password was allowed")
	}
	// Suspension expires every SFTP user; reactivation restores each one's own date.
	r.Commands = nil
	if _, err := m.SuspendAccount(ctx, "shop", "unpaid"); err != nil {
		t.Fatal(err)
	}
	has(t, "suspend", strings.Join(r.Commands, "\n"), "usermod --expiredate 1 shop_designer", "usermod --expiredate 1 shop_viewer")
	r.Commands = nil
	if _, err := m.UnsuspendAccount(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	has(t, "unsuspend", strings.Join(r.Commands, "\n"), "usermod --expiredate 2099-12-31 shop_designer", "usermod --expiredate  shop_viewer")

	// Deleting the website removes its SFTP users, never the website's files through the jail.
	r.Commands = nil
	if err := m.Delete(ctx, "shop.ng", false); err != nil {
		t.Fatal(err)
	}
	has(t, "delete", strings.Join(r.Commands, "\n"), "userdel --force shop_designer", "userdel --force shop_viewer",
		"systemctl disable --now var-lib-ike-sftp-shop_designer-shop.ng.mount")
	if left, _ := m.ListSFTPUsers("shop"); len(left) != 0 {
		t.Fatalf("users left: %+v", left)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/ssh/ike/keys/shop_viewer")); !os.IsNotExist(err) {
		t.Fatal("key file left behind")
	}
}

func TestMountUnitName(t *testing.T) {
	for in, want := range map[string]string{
		"/var/lib/ike/sftp/shop_designer/shop.ng":       "var-lib-ike-sftp-shop_designer-shop.ng.mount",
		"/var/lib/ike/sftp/shop_x/my-site.com.ng":       `var-lib-ike-sftp-shop_x-my\x2dsite.com.ng.mount`,
		"/var/lib/ike/sftp/shop_x/xn--d1acufc.xn--p1ai": `var-lib-ike-sftp-shop_x-xn\x2d\x2dd1acufc.xn\x2d\x2dp1ai.mount`,
	} {
		if got := mountUnit(in); got != want {
			t.Errorf("mountUnit(%q) = %q, want %q", in, got, want)
		}
	}
	if out, err := exec.Command("systemd-escape", "--path", "--suffix=mount", "/var/lib/ike/sftp/shop_x/my-site.com.ng").Output(); err == nil {
		if got := strings.TrimSpace(string(out)); got != mountUnit("/var/lib/ike/sftp/shop_x/my-site.com.ng") {
			t.Errorf("systemd-escape says %q", got)
		}
	}
}

// sysRunner runs real commands, standing in for systemd (not running in a
// container): mount units become plain bind mounts; other systemctl calls
// and web server reloads do nothing.
type sysRunner struct{ real runner.Exec }

func (s sysRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return s.Exec(ctx, runner.Command{Name: name, Args: args})
}
func (s sysRunner) RunInput(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
	return s.Exec(ctx, runner.Command{Name: name, Args: args, Stdin: stdin})
}
func (s sysRunner) Exec(ctx context.Context, c runner.Command) ([]byte, error) {
	switch c.Name {
	case "systemctl":
		if len(c.Args) == 3 && strings.HasSuffix(c.Args[2], ".mount") {
			b, err := os.ReadFile("/etc/systemd/system/" + c.Args[2])
			if c.Args[0] == "enable" {
				if err != nil {
					return nil, err
				}
				kv := map[string]string{}
				for _, l := range strings.Split(string(b), "\n") {
					if k, v, ok := strings.Cut(l, "="); ok {
						kv[k] = v
					}
				}
				if out, err := exec.Command("mount", "--bind", kv["What"], kv["Where"]).CombinedOutput(); err != nil {
					return out, err
				}
				if strings.Contains(kv["Options"], "ro") {
					return exec.Command("mount", "-o", "remount,bind,ro", kv["Where"]).CombinedOutput()
				}
				return nil, nil
			}
			where := "/" + strings.ReplaceAll(strings.TrimSuffix(c.Args[2], ".mount"), "-", "/")
			_ = exec.Command("umount", where).Run()
		}
		if len(c.Args) == 2 && c.Args[0] == "stop" && strings.HasSuffix(c.Args[1], ".mount") {
			where := "/" + strings.ReplaceAll(strings.TrimSuffix(c.Args[1], ".mount"), "-", "/")
			_ = exec.Command("umount", where).Run()
		}
		return nil, nil
	case "nginx", "apachectl", "apache2ctl", "setquota", "quotaon", "chage":
		return nil, nil
	}
	return s.real.Exec(ctx, c)
}

// TestRealSFTPUsers creates real Linux users, bind mounts and an sshd on
// port 2222, then signs in with sftp. Throwaway machines only:
//
//	IKE_REAL_SFTP=1 go test ./internal/sites -run RealSFTPUsers
func TestRealSFTPUsers(t *testing.T) {
	if os.Getenv("IKE_REAL_SFTP") != "1" {
		t.Skip("set IKE_REAL_SFTP=1 on a throwaway machine with openssh-server and sshpass")
	}
	_ = os.Remove("/etc/nginx/ike/sites/sftpt.test.conf")
	_ = os.Remove("/etc/apache2/ike/sites/sftpt.test.conf")
	for _, d := range []string{"/var/lib/ike/sites", "/var/lib/ike/sftp"} {
		_ = exec.Command("sh", "-c", "umount "+d+"/*/* 2>/dev/null; true").Run()
		_ = os.RemoveAll(d)
	}
	_ = exec.Command("userdel", "--force", "sftpt_designer").Run()
	_ = exec.Command("userdel", "--force", "sftpt_viewer").Run()
	_ = exec.Command("userdel", "--force", "--remove", "sftpt").Run()
	_ = exec.Command("groupadd", "--force", "--system", "www-data").Run()
	_ = os.MkdirAll("/run/sshd", 0o755) // systemd makes it on a real server

	cfg := config.Default()
	cfg.Root, cfg.DryRun = "/", false
	base := t.TempDir()
	_ = os.Chmod(filepath.Dir(base), 0o755)
	cfg.AccountsDir, cfg.CronDir = filepath.Join(base, "acc"), filepath.Join(base, "cron")
	_ = os.MkdirAll(cfg.AccountsDir, 0o700)
	m := NewManager(cfg, sysRunner{})
	if err := m.EnsurePages(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateAccount(ctx, "sftpt"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		m.deleteSFTPUsersFor(ctx, "sftpt", "")
		_ = exec.Command("userdel", "--force", "--remove", "sftpt").Run()
	}()
	if _, err := m.Create(ctx, CreateRequest{Domain: "sftpt.test", Account: "sftpt", Runtime: "static"}); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Get("sftpt.test")
	docroot := m.layout(s).DocRoot
	_ = os.WriteFile(filepath.Join(docroot, "index.html"), []byte("hello"), 0o644)
	_ = exec.Command("chown", "sftpt:sftpt", filepath.Join(docroot, "index.html")).Run()
	_ = os.WriteFile(filepath.Join(m.layout(s).SiteDir, "secret.txt"), []byte("outside public_html"), 0o600)
	_ = exec.Command("chown", "sftpt:sftpt", filepath.Join(m.layout(s).SiteDir, "secret.txt")).Run()

	pwUser, pw, err := m.CreateSFTPUser(ctx, "sftpt", SFTPUserRequest{Name: "designer", Domain: "sftpt.test"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "viewer@test", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	if _, _, err := m.CreateSFTPUser(ctx, "sftpt", SFTPUserRequest{Name: "viewer", Domain: "sftpt.test", ReadOnly: ptr(true), Folder: ptr(""), PublicKey: string(pub)}); err != nil {
		t.Fatal(err)
	}

	// sshd with Ubuntu's defaults plus Ike's block.
	hostKey := filepath.Join(dir, "host")
	_ = exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostKey).Run()
	_ = os.MkdirAll("/run/sshd", 0o755)
	conf := fmt.Sprintf("Port 2222\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s/sshd.pid\nUsePAM yes\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nSubsystem sftp internal-sftp\n\n%s",
		hostKey, dir, SFTPSiteSSHBlock(keysDir))
	_ = os.WriteFile(filepath.Join(dir, "sshd_config"), []byte(conf), 0o600)
	if out, err := exec.Command("/usr/sbin/sshd", "-t", "-f", filepath.Join(dir, "sshd_config")).CombinedOutput(); err != nil {
		t.Fatalf("sshd -t: %v %s", err, out)
	}
	sshd := exec.Command("/usr/sbin/sshd", "-D", "-e", "-f", filepath.Join(dir, "sshd_config"))
	var logs strings.Builder
	sshd.Stderr = &logs
	if err := sshd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sshd.Process.Kill(); _ = sshd.Wait() }()
	time.Sleep(700 * time.Millisecond)

	// sftp signs in with the password (when key is "") or the key, and runs commands.
	sftp := func(userName, password, key, commands string) (string, error) {
		args := []string{"-P", "2222", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR"}
		var cmd *exec.Cmd
		if key == "" {
			args = append(args, "-o", "PubkeyAuthentication=no", "-o", "PreferredAuthentications=password", userName+"@127.0.0.1")
			cmd = exec.Command("sshpass", append([]string{"-p", password, "sftp"}, args...)...)
		} else {
			args = append(args, "-i", key, "-o", "IdentitiesOnly=yes", "-o", "PasswordAuthentication=no", userName+"@127.0.0.1")
			cmd = exec.Command("sftp", args...)
		}
		cmd.Stdin = strings.NewReader(commands)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	upload := filepath.Join(dir, "new.html")
	_ = os.WriteFile(upload, []byte("from the designer"), 0o644)

	// Password login: lands in the website folder, can upload, can't climb out.
	out, err := sftp(pwUser.Name, pw, "", "pwd\nls\nput "+upload+" new.html\ncd /\nls -a\ncd ..\nls -a\nget ../secret.txt "+dir+"/stolen\n")
	if err != nil && !strings.Contains(out, "index.html") {
		t.Fatalf("password sftp: %v\n%s\nsshd:\n%s", err, out, logs.String())
	}
	if !strings.Contains(out, "Remote working directory: /sftpt.test") || !strings.Contains(out, "index.html") {
		t.Fatalf("designer should start in the website folder:\n%s\nsshd:\n%s", out, logs.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "stolen")); err == nil {
		t.Fatal("the designer read a file outside public_html")
	}
	st, err := os.Stat(filepath.Join(docroot, "new.html"))
	if err != nil {
		t.Fatalf("upload didn't land in public_html: %v\n%s", err, out)
	}
	acct, _ := user.Lookup("sftpt")
	if uid := fmt.Sprint(st.Sys().(*syscall.Stat_t).Uid); uid != acct.Uid {
		t.Fatalf("uploaded file owned by uid %s, want the account's %s", uid, acct.Uid)
	}
	// Wrong password is refused.
	if out, err := sftp(pwUser.Name, "not the password", "", "ls\n"); err == nil {
		t.Fatalf("wrong password signed in:\n%s", out)
	}

	// Key login, read-only, whole website folder.
	out, _ = sftp("sftpt_viewer", "", key, "ls\nget secret.txt "+dir+"/viewer-copy\nput "+upload+" public_html/nope.html\nrm public_html/index.html\n")
	if b, _ := os.ReadFile(filepath.Join(dir, "viewer-copy")); string(b) != "outside public_html" {
		t.Fatalf("read-only user couldn't download from the whole website folder:\n%s\nsshd:\n%s", out, logs.String())
	}
	if _, err := os.Stat(filepath.Join(docroot, "nope.html")); err == nil {
		t.Fatal("read-only user uploaded a file")
	}
	if _, err := os.Stat(filepath.Join(docroot, "index.html")); err != nil {
		t.Fatal("read-only user deleted a file")
	}
	// No shell, even when asked.
	shell := exec.Command("sshpass", "-p", pw, "ssh", "-p", "2222", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "PubkeyAuthentication=no", pwUser.Name+"@127.0.0.1", "id")
	if out, _ := shell.CombinedOutput(); strings.Contains(string(out), "uid=") {
		t.Fatalf("designer got a shell:\n%s", out)
	}
	// Suspension blocks sign-in; reactivation restores it.
	if _, err := m.SuspendAccount(ctx, "sftpt", "test"); err != nil {
		t.Fatal(err)
	}
	if out, err := sftp(pwUser.Name, pw, "", "ls\n"); err == nil && strings.Contains(out, "index.html") {
		t.Fatalf("suspended account's SFTP user signed in:\n%s", out)
	}
	if _, err := m.UnsuspendAccount(ctx, "sftpt"); err != nil {
		t.Fatal(err)
	}
	if out, _ := sftp(pwUser.Name, pw, "", "ls\n"); !strings.Contains(out, "index.html") {
		t.Fatalf("after reactivation:\n%s\nsshd:\n%s", out, logs.String())
	}
	// Deleting the user unmounts the jail and leaves the website's files alone.
	if err := m.DeleteSFTPUser(ctx, "sftpt", pwUser.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(docroot, "index.html")); err != nil {
		t.Fatal("deleting the SFTP user removed website files")
	}
	if _, err := os.Stat("/var/lib/ike/sftp/" + pwUser.Name); !os.IsNotExist(err) {
		t.Fatal("jail left behind")
	}
}
