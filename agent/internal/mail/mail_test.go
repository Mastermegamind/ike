package mail

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/secrets"
)

type fakeZone struct{ recs []DNSRecord }

func (f *fakeZone) SetManagedTXT(_ context.Context, tag, domain string, recs []DNSRecord) (bool, error) {
	f.recs = recs
	return true, nil
}

func testMail(t *testing.T) (*Manager, *runner.DryRun, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	cfg.MailServer, cfg.SpamFilter, cfg.Antivirus, cfg.Webmail = "postfix", "rspamd", "clamav", "roundcube"
	cfg.PublicIPv4, cfg.Hostname = "203.0.113.10", "server.host.ng"
	run := &runner.DryRun{Responses: map[string]string{
		"postqueue -j": `{"queue_name":"deferred","queue_id":"4BZp1q2Xyz","arrival_time":1760400000,"message_size":2048,"sender":"info@shop.ng","recipients":[{"address":"x@gmail.com","delay_reason":"connect to gmail: Connection timed out"}]}
{"queue_name":"active","queue_id":"5CAq2r3Abc","arrival_time":1760400100,"message_size":900,"sender":"a@other.com","recipients":[{"address":"b@else.com"}]}`,
		"doveadm -f tab quota get": "Quota name\tType\tValue\tLimit\t%\nUser quota\tSTORAGE\t51200\t1048576\t4\nUser quota\tMESSAGE\t321\t-\t0\n",
	}}
	keys, _ := secrets.Open(filepath.Join(root, "k"))
	m := New(&kit.Env{Cfg: cfg, Run: run}, keys)
	return m, run, root
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMailDomainsMailboxesAliases(t *testing.T) {
	m, run, root := testMail(t)
	fz := &fakeZone{}
	m.Zone = fz
	m.OwnerOf = func(d string) string {
		if d == "taken.ng" {
			return "other"
		}
		return ""
	}
	ctx := context.Background()
	if _, err := m.AddDomain(ctx, "shop", "taken.ng"); err == nil {
		t.Fatal("domain of another account accepted")
	}
	if _, err := m.AddDomain(ctx, "shop", "Shop.NG"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddDomain(ctx, "shop", "shop.ng"); !errors.Is(err, kit.ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	// DNS records were published, DKIM included
	var dkim, mx bool
	for _, r := range fz.recs {
		if r.Name == "ike._domainkey" && strings.HasPrefix(r.Content, "v=DKIM1; k=rsa; p=MII") {
			dkim = true
		}
		if r.Type == "MX" && r.Content == "mail" {
			mx = true
		}
	}
	if !dkim || !mx {
		t.Fatalf("dns records: %+v", fz.recs)
	}
	cm, err := m.CreateMailbox(ctx, "shop", "shop.ng", NewMailbox{Local: "Info", Name: "Shop Info", QuotaMB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if cm.Address != "info@shop.ng" || len(cm.Password) != 16 {
		t.Fatalf("%+v", cm)
	}
	if !m.VerifyLogin("info@shop.ng", cm.Password) || m.VerifyLogin("info@shop.ng", "nope") {
		t.Fatal("login check")
	}
	if _, err := m.CreateMailbox(ctx, "shop", "shop.ng", NewMailbox{Local: "sales", Password: "short"}); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := m.CreateMailbox(ctx, "shop", "shop.ng", NewMailbox{Local: "bad..dots", Password: "longenough123"}); err == nil {
		t.Fatal("bad local part accepted")
	}
	if _, err := m.CreateMailbox(ctx, "other", "shop.ng", NewMailbox{Local: "x", Password: "longenough123"}); err == nil {
		t.Fatal("other account added a mailbox")
	}
	if _, err := m.CreateMailbox(ctx, "shop", "shop.ng", NewMailbox{Local: "app", Password: "longenough123", SendOnly: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAliases(ctx, "shop", "shop.ng", []Alias{{From: "sales", To: []string{"info@shop.ng", "Boss@Gmail.com"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAliases(ctx, "shop", "shop.ng", []Alias{{From: "info", To: []string{"x@y.com"}}}); err == nil {
		t.Fatal("alias over a mailbox accepted")
	}
	if _, err := m.UpdateDomain(ctx, "shop", "shop.ng", DomainOptions{Enabled: true, CatchAll: "info@shop.ng"}); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(root, "etc/ike/mail")
	has := func(file string, want ...string) {
		t.Helper()
		body := read(t, filepath.Join(conf, file))
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s missing %q:\n%s", file, w, body)
			}
		}
	}
	has("domains", "shop.ng OK")
	has("mailboxes", "info@shop.ng shop.ng/info/")
	if strings.Contains(read(t, filepath.Join(conf, "mailboxes")), "app@shop.ng") {
		t.Error("send-only mailbox can receive")
	}
	has("aliases", "sales@shop.ng info@shop.ng, boss@gmail.com", "@shop.ng info@shop.ng", "info@shop.ng info@shop.ng")
	has("senders", "info@shop.ng info@shop.ng", "sales@shop.ng info@shop.ng", "app@shop.ng app@shop.ng")
	has("users", "info@shop.ng:{SHA512-CRYPT}$6$", ":5000:5000::/var/mail/vhosts/shop.ng/info::userdb_quota_rule=*:storage=1024M")
	has("dkim_selectors", "shop.ng ike")
	if st, _ := os.Stat(filepath.Join(conf, "users")); st.Mode().Perm() != 0o640 {
		t.Errorf("users file mode %v", st.Mode())
	}
	h := strings.Join(run.History(), "\n")
	for _, w := range []string{"postmap -F lmdb:/etc/ike/mail/sni", "postfix check", "postfix reload", "doveconf -n", "doveadm reload"} {
		if !strings.Contains(h, w) {
			t.Errorf("missing %q", w)
		}
	}
	// forwarding without a copy, and an auto-reply
	if _, err := m.UpdateMailbox(ctx, "shop", "shop.ng", "info", MailboxUpdate{Enabled: true, QuotaMB: 1024, Forward: []string{"me@gmail.com"},
		AutoReply: &AutoReply{Enabled: true, Subject: "Away", Body: "Back Monday.\n.hidden", Until: "2026-12-24"}}); err != nil {
		t.Fatal(err)
	}
	has("aliases", "info@shop.ng me@gmail.com")
	sieve := read(t, filepath.Join(root, "var/mail/vhosts/shop.ng/info/sieve/ike.sieve"))
	for _, w := range []string{`require ["vacation", "date", "relational"];`, `currentdate :value "le" "date" "2026-12-24"`, `:subject "Away"`, "..hidden\n.\n"} {
		if !strings.Contains(sieve, w) {
			t.Errorf("sieve missing %q:\n%s", w, sieve)
		}
	}
	if !strings.Contains(strings.Join(run.History(), "\n"), "sievec") {
		t.Error("sieve not compiled")
	}
	// a Postfix failure rolls the domain file back
	run.FailOn = "postfix check"
	if _, err := m.CreateMailbox(ctx, "shop", "shop.ng", NewMailbox{Local: "later", Password: "longenough123"}); err == nil {
		t.Fatal("expected failure")
	}
	run.FailOn = ""
	d, _ := m.Domain("shop", "shop.ng")
	for _, mb := range d.Mailboxes {
		if mb.Local == "later" {
			t.Fatal("mailbox kept after failed apply")
		}
		if mb.PasswordHash != "" {
			t.Fatal("hash exposed")
		}
	}
	// usage
	d, err = m.RefreshUsage(ctx, "shop", "shop.ng")
	if err != nil || d.Mailboxes[0].Usage == nil || d.Mailboxes[0].Usage.UsedMB != 50 || d.Mailboxes[0].Usage.Messages != 321 {
		t.Fatalf("usage %+v %v", d.Mailboxes[0].Usage, err)
	}
	// queue: account sees only its own messages
	q, err := m.Queue(ctx, "shop")
	if err != nil || len(q) != 1 || q[0].ID != "4BZp1q2Xyz" || !strings.Contains(q[0].Recipients[0].Reason, "timed out") {
		t.Fatalf("queue %+v %v", q, err)
	}
	if err := m.QueueAction(ctx, "shop", "delete", "5CAq2r3Abc"); err == nil {
		t.Fatal("deleted another account's message")
	}
	if err := m.QueueAction(ctx, "shop", "delete", "4BZp1q2Xyz"); err != nil {
		t.Fatal(err)
	}
	if err := m.QueueAction(ctx, "shop", "flush", ""); err == nil {
		t.Fatal("account flushed the queue")
	}
	// webmail vhost
	vh := read(t, filepath.Join(root, "etc/nginx/ike/sites/00-ike-mail.conf"))
	if !strings.Contains(vh, "server_name mail.shop.ng;") || !strings.Contains(vh, "return 302 https://$host:2096$request_uri") {
		t.Fatalf("webmail vhost:\n%s", vh)
	}
	// delete
	if err := m.DeleteMailbox(ctx, "shop", "shop.ng", "app", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(run.History(), "\n"), "rm -rf --one-file-system /var/mail/vhosts/shop.ng/app") {
		t.Fatal("maildir not removed")
	}
	if err := m.RemoveDomain(ctx, "shop", "shop.ng", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, filepath.Join(conf, "domains")), "shop.ng") {
		t.Fatal("domain still listed")
	}
}

func TestMailSettingsRelay(t *testing.T) {
	m, run, root := testMail(t)
	ctx := context.Background()
	s, _ := m.Settings()
	s.Relay = Relay{Provider: "brevo", Username: "me@shop.ng"}
	if _, err := m.UpdateSettings(ctx, SettingsUpdate{Settings: s, RelayPassword: "xsmtpsib-secret"}); err != nil {
		t.Fatal(err)
	}
	pw := read(t, filepath.Join(root, "etc/ike/mail/relay_password"))
	if pw != "[smtp-relay.brevo.com]:587 me@shop.ng:xsmtpsib-secret\n" {
		t.Fatalf("relay map %q", pw)
	}
	stored := read(t, filepath.Join(root, "var/lib/ike/mail/settings.json"))
	if strings.Contains(stored, "xsmtpsib-secret") {
		t.Fatal("relay password stored in plain text")
	}
	h := strings.Join(run.History(), "\n")
	if !strings.Contains(h, "relayhost=[smtp-relay.brevo.com]:587") || !strings.Contains(h, "rspamadm configtest") {
		t.Fatalf("commands:\n%s", h)
	}
	rl := read(t, filepath.Join(root, "etc/rspamd/local.d/ratelimit.conf"))
	if !strings.Contains(rl, `rate = "300 / 1h"`) {
		t.Fatal(rl)
	}
	ps, _ := m.PublicSettings()
	if !ps.HasRelayPassword || ps.Relay.Password != "" {
		t.Fatal("public settings")
	}
	s.SpamThreshold, s.RejectThreshold = 10, 5
	if _, err := m.UpdateSettings(ctx, SettingsUpdate{Settings: s}); err == nil {
		t.Fatal("bad thresholds accepted")
	}
	s.SpamThreshold, s.RejectThreshold = 6, 15
	s.Relay = Relay{Provider: "direct"}
	if _, err := m.UpdateSettings(ctx, SettingsUpdate{Settings: s}); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "etc/ike/mail/relay_password")) != "" {
		t.Fatal("relay password kept after switching to direct")
	}
}

type stubLookup struct{}

func (stubLookup) LookupMX(context.Context, string) ([]*net.MX, error) {
	return []*net.MX{{Host: "mail.shop.ng.", Pref: 10}}, nil
}
func (stubLookup) LookupTXT(_ context.Context, n string) ([]string, error) {
	switch n {
	case "shop.ng":
		return []string{"v=spf1 mx ~all", "google-site-verification=x"}, nil
	case "_dmarc.shop.ng":
		return []string{"v=DMARC1; p=none"}, nil
	}
	return nil, &net.DNSError{IsNotFound: true}
}
func (stubLookup) LookupHost(_ context.Context, h string) ([]string, error) {
	switch h {
	case "mail.shop.ng", "server.host.ng":
		return []string{"203.0.113.10"}, nil
	case "10.113.0.203.bl.spamcop.net":
		return []string{"127.0.0.2"}, nil
	}
	return nil, &net.DNSError{IsNotFound: true}
}
func (stubLookup) LookupAddr(context.Context, string) ([]string, error) {
	return []string{"server.host.ng."}, nil
}

func TestDeliverability(t *testing.T) {
	m, _, _ := testMail(t)
	m.lookup = stubLookup{}
	m.probe = func(context.Context, string) error { return errors.New("timeout") }
	ctx := context.Background()
	if _, err := m.AddDomain(ctx, "shop", "shop.ng"); err != nil {
		t.Fatal(err)
	}
	checks, err := m.Deliverability(ctx, "shop", "shop.ng")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range checks {
		got[c.Record] = c.Status
	}
	want := map[string]string{"MX": "ok", "A": "ok", "SPF": "ok", "DKIM": "missing", "DMARC": "warn", "PTR": "ok", "PORT25": "wrong", "BLOCKLIST": "wrong", "TLS": "warn"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s, want %s", k, got[k], v)
		}
	}
	if checks[0].Record != "MX" {
		t.Error("order")
	}
}

func TestDeliveryLog(t *testing.T) {
	m, _, root := testMail(t)
	_, _ = m.AddDomain(context.Background(), "shop", "shop.ng")
	log := `Oct 14 03:10:01 server postfix/qmgr[812]: 4BZp1q2Xyz: from=<info@shop.ng>, size=2048, nrcpt=1 (queue active)
Oct 14 03:10:02 server postfix/smtp[901]: 4BZp1q2Xyz: to=<ada@gmail.com>, relay=gmail-smtp-in.l.google.com[142.250.1.27]:25, delay=1.2, delays=0.1/0/0.5/0.6, dsn=2.0.0, status=sent (250 2.0.0 OK)
Oct 14 03:11:00 server postfix/smtpd[950]: NOQUEUE: reject: RCPT from unknown[198.51.100.9]: 550 5.1.1 <nobody@shop.ng>: Recipient address rejected: User unknown; from=<spam@bad.biz> to=<nobody@shop.ng> proto=ESMTP helo=<x>
Oct 14 03:12:00 server postfix/smtp[902]: 9XYZ123abc: to=<z@else.com>, relay=none, delay=30, status=deferred (connect timed out)
`
	_ = os.MkdirAll(filepath.Join(root, "var/log"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "var/log/mail.log"), []byte(log), 0o644)
	d, err := m.Deliveries("shop", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 2 || d[0].Status != "rejected" || d[1].Status != "sent" || d[1].From != "info@shop.ng" || !strings.HasPrefix(d[1].Relay, "gmail-smtp-in") {
		t.Fatalf("%+v", d)
	}
	all, _ := m.Deliveries("", "else.com", 0)
	if len(all) != 1 || all[0].Status != "deferred" {
		t.Fatalf("%+v", all)
	}
}
