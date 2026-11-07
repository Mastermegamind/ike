package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
)

// TestRealMail drives a real Postfix and Dovecot configured by the installer's
// install_mail: receive over SMTP, authenticated submission, IMAP login,
// forwarders and auto-reply scripts.
// IKE_REAL_MAIL=1 go test ./internal/mail -run RealMail
func TestRealMail(t *testing.T) {
	if os.Getenv("IKE_REAL_MAIL") == "" {
		t.Skip("set IKE_REAL_MAIL=1 on a machine set up by install_mail")
	}
	cfg := config.Default()
	cfg.Root, cfg.MailServer, cfg.Hostname, cfg.PublicIPv4 = "/", "postfix", "server.test.ng", "127.0.0.1"
	keys, err := secrets.Open(filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(&kit.Env{Cfg: cfg, Run: runner.Exec{}}, keys)
	ctx := context.Background()
	m.Detect(ctx)
	_ = m.RemoveDomain(ctx, "", "test.ng", true)
	if _, err := m.AddDomain(ctx, "shop", "test.ng"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.RemoveDomain(ctx, "", "test.ng", true) }()
	pw := "correct-horse-battery"
	if _, err := m.CreateMailbox(ctx, "shop", "test.ng", NewMailbox{Local: "info", Password: pw, QuotaMB: 50}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAliases(ctx, "shop", "test.ng", []Alias{{From: "sales", To: []string{"info@test.ng"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateMailbox(ctx, "shop", "test.ng", "info", MailboxUpdate{Enabled: true, QuotaMB: 50, KeepCopy: true,
		AutoReply: &AutoReply{Enabled: true, Subject: "Away", Body: "Back soon", Until: "2099-01-01"}}); err != nil {
		t.Fatal(err)
	}

	// 1. inbound mail on port 25 to the alias
	if err := sendMail("ada@example.org", []string{"sales@test.ng"},
		[]byte("From: ada@example.org\r\nTo: sales@test.ng\r\nSubject: hello\r\n\r\nhi there\r\n")); err != nil {
		t.Fatalf("inbound: %v", err)
	}
	// unknown recipient is rejected at SMTP time
	if err := sendMail("ada@example.org", []string{"nobody@test.ng"}, []byte("Subject: x\r\n\r\nx\r\n")); err == nil {
		t.Fatal("mail to an unknown user was accepted")
	}
	waitFor(t, "/var/mail/vhosts/test.ng/info/Maildir/new", "hi there")

	// 2. only this machine may relay without signing in
	if out, _ := (runner.Exec{}).Run(ctx, "postconf", "-h", "mynetworks"); strings.TrimSpace(string(out)) != "127.0.0.0/8 [::ffff:127.0.0.0]/104 [::1]/128" {
		t.Fatalf("mynetworks: %s", out)
	}

	// 3. submission with STARTTLS and the mailbox password
	c, err := smtp.Dial("127.0.0.1:587")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StartTLS(&tls.Config{InsecureSkipVerify: true, ServerName: "server.test.ng"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Auth(smtp.PlainAuth("", "info@test.ng", "wrong-password", "127.0.0.1")); err == nil {
		t.Fatal("wrong password accepted")
	}
	c.Close()
	c, _ = smtp.Dial("127.0.0.1:587")
	_ = c.StartTLS(&tls.Config{InsecureSkipVerify: true})
	if err := c.Auth(smtp.PlainAuth("", "info@test.ng", pw, "127.0.0.1")); err != nil {
		t.Fatalf("submission auth: %v", err)
	}
	if err := c.Mail("ceo@bigbank.com"); err == nil {
		if err := c.Rcpt("info@test.ng"); err == nil {
			t.Fatal("sent as an address the mailbox doesn't own")
		}
	}
	c.Reset()
	if err := c.Mail("sales@test.ng"); err != nil {
		t.Fatalf("sending as an alias it receives: %v", err)
	}
	if err := c.Rcpt("info@test.ng"); err != nil {
		t.Fatal(err)
	}
	w, _ := c.Data()
	fmt.Fprint(w, "From: sales@test.ng\r\nTo: info@test.ng\r\nSubject: internal\r\n\r\nsubmitted\r\n")
	_ = w.Close()
	c.Quit()
	waitFor(t, "/var/mail/vhosts/test.ng/info/Maildir/new", "submitted")

	// 4. IMAP over TLS
	conn, err := tls.Dial("tcp", "127.0.0.1:993", &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	_, _ = r.ReadString('\n')
	fmt.Fprintf(conn, "a1 LOGIN info@test.ng %s\r\n", pw)
	line := readTagged(r, "a1")
	if !strings.HasPrefix(line, "a1 OK") {
		t.Fatalf("IMAP login: %s", line)
	}
	fmt.Fprintf(conn, "a2 STATUS INBOX (MESSAGES)\r\n")
	if l := readTagged(r, "a2"); !strings.HasPrefix(l, "a2 OK") {
		t.Fatalf("STATUS: %s", l)
	}
	fmt.Fprintf(conn, "a3 GETQUOTAROOT INBOX\r\n")
	if l := readTagged(r, "a3"); !strings.HasPrefix(l, "a3 OK") {
		t.Fatalf("quota: %s", l)
	}
	// 5. usage via doveadm
	d, err := m.RefreshUsage(ctx, "shop", "test.ng")
	if err != nil || d.Mailboxes[0].Usage == nil || d.Mailboxes[0].Usage.Messages < 2 {
		t.Fatalf("usage: %+v %v", d.Mailboxes[0].Usage, err)
	}
	// 6. disabling the mailbox blocks IMAP
	if _, err := m.UpdateMailbox(ctx, "shop", "test.ng", "info", MailboxUpdate{Enabled: false, QuotaMB: 50}); err != nil {
		t.Fatal(err)
	}
	conn2, _ := tls.Dial("tcp", "127.0.0.1:993", &tls.Config{InsecureSkipVerify: true})
	r2 := bufio.NewReader(conn2)
	_, _ = r2.ReadString('\n')
	fmt.Fprintf(conn2, "b1 LOGIN info@test.ng %s\r\n", pw)
	if l := readTagged(r2, "b1"); strings.HasPrefix(l, "b1 OK") {
		t.Fatal("disabled mailbox could sign in")
	}
	conn2.Close()

	// 7. settings: relay and Rspamd files pass the real config checks
	if os.Getenv("IKE_REAL_RSPAMD") != "" {
		m.env.Cfg.SpamFilter, m.env.Cfg.Antivirus = "rspamd", "clamav"
	}
	s, _ := m.Settings()
	s.Relay = Relay{Provider: "mailgun", Username: "postmaster@mg.test.ng"}
	s.Antivirus, s.Greylisting = true, true
	if _, err := m.UpdateSettings(ctx, SettingsUpdate{Settings: s, RelayPassword: "key-123"}); err != nil {
		t.Fatal(err)
	}
	if out, _ := (runner.Exec{}).Run(ctx, "postconf", "-h", "relayhost"); strings.TrimSpace(string(out)) != "[smtp.mailgun.org]:587" {
		t.Fatalf("relayhost %s", out)
	}
	s.Relay = Relay{Provider: "direct"}
	if _, err := m.UpdateSettings(ctx, SettingsUpdate{Settings: s}); err != nil {
		t.Fatal(err)
	}
}

func sendMail(from string, to []string, msg []byte) error {
	c, err := smtp.Dial("127.0.0.1:25")
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.StartTLS(&tls.Config{InsecureSkipVerify: true}); err != nil {
		return err
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	_, _ = w.Write(msg)
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func readTagged(r *bufio.Reader, tag string) string {
	for {
		l, err := r.ReadString('\n')
		if err != nil || strings.HasPrefix(l, tag+" ") {
			return l
		}
	}
}

func waitFor(t *testing.T, dir, text string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			if strings.Contains(string(b), text) {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	out, _ := runner.Exec{}.Run(context.Background(), "tail", "-30", "/var/log/mail.log")
	t.Fatalf("%q not delivered to %s\n%s", text, dir, out)
}

var _ = net.Dial
