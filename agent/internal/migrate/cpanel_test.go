package migrate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/dns"
	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/mail"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/secrets"
	"github.com/YOUR-ORG/ike/agent/internal/sites"
)

type entry struct {
	name, body string
	dir        bool
	link       string
}

func writeBackup(t *testing.T, path string, entries []entry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: "cpmove-adaeze/" + e.name, Mode: 0o644, Size: int64(len(e.body)), ModTime: time.Now(), Typeflag: tar.TypeReg}
		switch {
		case e.dir:
			h.Typeflag, h.Size, h.Mode, h.Name = tar.TypeDir, 0, 0o755, h.Name+"/"
		case e.link != "":
			h.Typeflag, h.Size, h.Linkname = tar.TypeSymlink, 0, e.link
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
}

// names lists the member names of a (possibly gzipped) tar stream.
func names(t *testing.T, data string, gz bool) []string {
	t.Helper()
	var r io.Reader = strings.NewReader(data)
	if gz {
		g, err := gzip.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		r = g
	}
	tr := tar.NewReader(r)
	var out []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		out = append(out, h.Name)
	}
	sort.Strings(out)
	return out
}

func TestCpanelRestore(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	cfg.MailServer, cfg.DNSServer, cfg.PublicIPv4, cfg.Hostname = "postfix", "powerdns", "203.0.113.10", "server.host.ng"
	_ = os.MkdirAll(filepath.Join(root, "etc/php/8.1/fpm/pool.d"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "etc/php/8.3/fpm/pool.d"), 0o755)
	run := &runner.DryRun{Responses: map[string]string{
		"runuser -u adaeze -- cat /home/adaeze/web/adaeze.ng/public_html/wp-config.php": "<?php\ndefine( 'DB_NAME', 'adaeze_wp1' );\ndefine('DB_USER', 'adaeze_wpuser');\ndefine('DB_PASSWORD', 'old-secret');\ndefine('DB_HOST', 'localhost');\n",
	}}
	keys, _ := secrets.Open(filepath.Join(root, "k"))
	sm := sites.NewManager(cfg, run)
	env := &kit.Env{Cfg: cfg, Run: run, Accounts: sm, Events: kit.NewBus()}
	rs := &Restorer{Env: env, Sites: sm, Mail: mail.New(env, keys), DNS: dns.New(env, keys)}

	imports := filepath.Join(root, cfg.ImportsDir)
	_ = os.MkdirAll(imports, 0o700)
	writeBackup(t, filepath.Join(imports, "cpmove-adaeze.tar.gz"), []entry{
		{name: "userdata/main", body: "---\naddon_domains:\n  shopng.com: shop.adaeze.ng\nmain_domain: adaeze.ng\nparked_domains:\n  - adaeze.com\nsub_domains:\n  - shop.adaeze.ng\n  - blog.adaeze.ng\n"},
		{name: "userdata/adaeze.ng", body: "documentroot: /home/adaeze/public_html\nphpversion: ea-php81\nip: 198.51.100.4\n"},
		{name: "userdata/shopng.com", body: "documentroot: /home/adaeze/public_html/shopng.com\nphpversion: ea-php83\n"},
		{name: "userdata/blog.adaeze.ng", body: "documentroot: /home/adaeze/public_html/blog\n"},
		{name: "homedir", dir: true},
		{name: "homedir/public_html/index.php", body: "<?php echo 'main';"},
		{name: "homedir/public_html/wp-config.php", body: "<?php // old"},
		{name: "homedir/public_html/shopng.com/index.html", body: "shop"},
		{name: "homedir/public_html/blog/index.html", body: "blog"},
		{name: "homedir/public_html/evil", link: "/etc/shadow"},
		{name: "homedir/private/app.env", body: "SECRET=1"},
		{name: "homedir/.cpanel/datastore", body: "skip"},
		{name: "homedir/../../etc/passwd", body: "nope"},
		{name: "homedir/etc/adaeze.ng/passwd", body: "info:x:1001:1001::/home/adaeze/mail/adaeze.ng/info:/bin/false\nsales:x:1001:1001::/home/adaeze/mail/adaeze.ng/sales:/bin/false\n"},
		{name: "homedir/etc/adaeze.ng/shadow", body: "info:$6$abcdefgh$" + strings.Repeat("A", 86) + ":19000::::::\nsales:$1$xyz12345$abcdefghijklmnopqrstuv:19000::::::\n"},
		{name: "homedir/etc/adaeze.ng/quota", body: "info:1073741824\nsales:524288000\n"},
		{name: "homedir/mail/adaeze.ng/info/cur/1700000000.M1.host:2,S", body: "Subject: hello\n\nhi"},
		{name: "homedir/mail/adaeze.ng/info/new/1700000001.M2.host", body: "Subject: new\n\nx"},
		{name: "homedir/mail/adaeze.ng/sales/cur/1700000002.M3.host:2,", body: "Subject: order\n\ny"},
		{name: "va/adaeze.ng", body: "orders@adaeze.ng: sales@adaeze.ng, boss@gmail.com\n*: :fail: No such user here\npipe@adaeze.ng: |/home/adaeze/script.php\n"},
		{name: "cron/adaeze", body: "MAILTO=\"a@b.ng\"\nSHELL=/bin/bash\n*/15 * * * * /usr/local/bin/php /home/adaeze/public_html/wp-cron.php\n0 3 * * * /home/adaeze/private/backup.sh\n"},
		{name: "mysql/adaeze_wp1.create", body: "CREATE DATABASE `adaeze_wp1`;"},
		{name: "mysql/adaeze_wp1.sql", body: "CREATE TABLE t (id int);\n/*!50013 DEFINER=`adaeze_wpuser`@`localhost` SQL SECURITY DEFINER */\nINSERT INTO t VALUES (1);\n"},
		{name: "dnszones/adaeze.ng.db", body: "$TTL 14400\nadaeze.ng. 86400 IN SOA ns1.old.com. admin.old.com. ( 1 3600 1800 1209600 86400 )\nadaeze.ng. IN NS ns1.old.com.\nadaeze.ng. 14400 IN A 198.51.100.4\nwww 14400 IN CNAME adaeze.ng.\nadaeze.ng. IN MX 0 adaeze.ng.\n"},
	})

	var logs []string
	res, err := rs.Restore(context.Background(), RestoreRequest{File: "cpmove-adaeze.tar.gz", PointToThisServer: true},
		func(f string, a ...any) { logs = append(logs, f) })
	if err != nil {
		t.Fatalf("restore: %v\n%v", err, logs)
	}
	sort.Strings(res.Websites)
	if strings.Join(res.Websites, ",") != "adaeze.ng,blog.adaeze.ng,shopng.com" || strings.Join(res.Aliases, ",") != "adaeze.com" {
		t.Fatalf("websites %v aliases %v", res.Websites, res.Aliases)
	}
	if s, _ := sm.Get("adaeze.ng"); s.Version != "8.1" {
		t.Errorf("PHP version not kept: %s", s.Version)
	}
	// files: one user tar with mapped paths, nothing outside the account
	var fileTar string
	hist := run.History()
	for i, c := range hist {
		if strings.HasPrefix(c, "runuser -u adaeze -- tar -x -f - -C /home/adaeze") {
			fileTar = run.Stdin[stdinIndex(hist, i)]
		}
	}
	got := names(t, fileTar, false)
	want := []string{"cpanel-files/private/app.env", "web/adaeze.ng/public_html/evil", "web/adaeze.ng/public_html/index.php",
		"web/adaeze.ng/public_html/wp-config.php", "web/blog.adaeze.ng/public_html/index.html", "web/shopng.com/public_html/index.html"}
	for _, w := range want {
		if !contains(got, w) {
			t.Errorf("file %s missing from %v", w, got)
		}
	}
	for _, g := range got {
		if strings.Contains(g, "..") || strings.Contains(g, ".cpanel") || strings.Contains(g, "/mail/") || strings.HasPrefix(g, "web/adaeze.ng/public_html/shopng.com") {
			t.Errorf("file should not be copied there: %s", g)
		}
	}
	// databases: created, imported as the limited user, DEFINER removed, WordPress rewired
	if len(res.Databases) != 1 || res.Databases[0].Name != "adaeze_wp1" || res.Databases[0].Rewired == "" {
		t.Fatalf("databases %+v", res.Databases)
	}
	joined := strings.Join(hist, "\n")
	if !strings.Contains(joined, "mariadb --user=adaeze_wp1 --host=localhost --batch --default-character-set=utf8mb4 adaeze_wp1") {
		t.Fatalf("import not run as the database user:\n%s", joined)
	}
	foundSQL := false
	for _, in := range run.Stdin {
		if strings.Contains(in, "INSERT INTO t VALUES (1)") {
			foundSQL = true
			if strings.Contains(in, "DEFINER=") {
				t.Errorf("DEFINER kept: %s", in)
			}
		}
		if strings.Contains(in, "define( 'DB_PASSWORD', '") && strings.Contains(in, "old-secret") {
			t.Errorf("old password kept in wp-config")
		}
	}
	if !foundSQL {
		t.Fatal("SQL not streamed")
	}
	// mail: mailboxes with old hashes and quotas, messages as vmail, forwarders
	if len(res.Mailboxes) != 2 || res.Forwards != 1 {
		t.Fatalf("mail %v forwards %d warnings %v", res.Mailboxes, res.Forwards, res.Warnings)
	}
	dom, err := rs.Mail.Domain("adaeze", "adaeze.ng")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, cfg.MailConfDir, "users"))
	if !strings.Contains(string(b), "info@adaeze.ng:{CRYPT}$6$abcdefgh$") {
		t.Errorf("hash not kept in users file:\n%s", b)
	}
	_ = dom
	mailTars := 0
	for i, c := range hist {
		if strings.HasPrefix(c, "runuser -u vmail -- tar -xzf -") {
			mailTars++
			n := names(t, run.Stdin[stdinIndex(hist, i)], true)
			for _, x := range n {
				if !strings.HasPrefix(x, "Maildir/") {
					t.Errorf("mail member %s", x)
				}
			}
		}
	}
	if mailTars != 2 {
		t.Errorf("%d mailbox imports", mailTars)
	}
	// cron: paths rewritten, env lines skipped
	acct, _ := sm.GetAccount("adaeze")
	if len(acct.Cron) != 2 || !strings.Contains(acct.Cron[0].Command, "/home/adaeze/web/adaeze.ng/public_html/wp-cron.php") ||
		!strings.Contains(acct.Cron[1].Command, "/home/adaeze/cpanel-files/private/backup.sh") {
		t.Fatalf("cron %+v", acct.Cron)
	}
	// DNS: zone imported, A record pointed at this server
	z, err := rs.DNS.Get("adaeze", "adaeze.ng")
	if err != nil {
		t.Fatal(err)
	}
	okA := false
	for _, r := range z.Records {
		if r.Type == "A" && r.Name == "@" && r.Content == "203.0.113.10" {
			okA = true
		}
		if r.Content == "198.51.100.4" {
			t.Error("old IP kept")
		}
	}
	if !okA {
		t.Fatalf("records %+v", z.Records)
	}
	// running it again refuses to overwrite the account
	if _, err := rs.Restore(context.Background(), RestoreRequest{File: "cpmove-adaeze.tar.gz"}, func(string, ...any) {}); err == nil {
		t.Fatal("second restore into the same account")
	}
}

// stdinIndex maps a command's position to its entry in DryRun.Stdin.
func stdinIndex(hist []string, i int) int {
	n := -1
	for j := 0; j <= i; j++ {
		if strings.HasSuffix(hist[j], " <stdin>") {
			n++
		}
	}
	return n
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

var _ = bytes.NewReader
