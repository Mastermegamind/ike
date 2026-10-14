package mail

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// apply writes every lookup table from the domain files and reloads Postfix
// and Dovecot. Callers hold m.mu.
func (m *Manager) apply(ctx context.Context) error {
	all, err := m.AllDomains()
	if err != nil {
		return err
	}
	var domains, mailboxes, aliases, senders, users, sni, selectors []string
	var dovecotSNI strings.Builder
	dovecotSNI.WriteString("# Written by Ike: a certificate for each mail domain.\n")
	for _, d := range all {
		if !d.Enabled {
			continue
		}
		domains = append(domains, d.Domain+" OK")
		selectors = append(selectors, d.Domain+" "+d.Selector)
		owners := map[string][]string{} // address → mailboxes that may send as it
		for _, mb := range d.Mailboxes {
			addr := mb.Local + "@" + d.Domain
			if !mb.Enabled {
				continue
			}
			owners[addr] = append(owners[addr], addr)
			users = append(users, m.userLine(d.Domain, mb))
			if mb.SendOnly {
				continue
			}
			mailboxes = append(mailboxes, addr+" "+d.Domain+"/"+mb.Local+"/")
			switch {
			case len(mb.Forward) > 0 && mb.KeepCopy:
				aliases = append(aliases, addr+" "+addr+", "+strings.Join(mb.Forward, ", "))
			case len(mb.Forward) > 0:
				aliases = append(aliases, addr+" "+strings.Join(mb.Forward, ", "))
			case d.CatchAll != "":
				// with a catch-all, real mailboxes must map to themselves
				aliases = append(aliases, addr+" "+addr)
			}
		}
		for _, a := range d.Aliases {
			addr := a.From + "@" + d.Domain
			aliases = append(aliases, addr+" "+strings.Join(a.To, ", "))
			for _, t := range a.To {
				if strings.HasSuffix(t, "@"+d.Domain) {
					owners[addr] = append(owners[addr], t)
				}
			}
		}
		if d.CatchAll != "" {
			aliases = append(aliases, "@"+d.Domain+" "+d.CatchAll)
		}
		for addr, o := range owners {
			senders = append(senders, addr+" "+strings.Join(o, ", "))
		}
		if d.TLS {
			host := "mail." + d.Domain
			live := filepath.Join(m.env.Cfg.LetsEncryptDir, "live", host)
			key, chain := filepath.Join(live, "privkey.pem"), filepath.Join(live, "fullchain.pem")
			sni = append(sni, host+" "+key+" "+chain)
			if m.dovecot24 {
				fmt.Fprintf(&dovecotSNI, "local_name %s {\n  ssl_server_cert_file = %s\n  ssl_server_key_file = %s\n}\n", host, chain, key)
			} else {
				fmt.Fprintf(&dovecotSNI, "local_name %s {\n  ssl_cert = <%s\n  ssl_key = <%s\n}\n", host, chain, key)
			}
		}
	}
	for _, l := range [][]string{domains, mailboxes, aliases, senders, users, sni, selectors} {
		sort.Strings(l)
	}
	head := "# Written by Ike from the panel; changes here are overwritten.\n"
	files := []struct {
		name  string
		lines []string
		mode  os.FileMode
	}{
		{"domains", domains, 0o644}, {"mailboxes", mailboxes, 0o644}, {"aliases", aliases, 0o644},
		{"senders", senders, 0o644}, {"sni", sni, 0o644}, {"dkim_selectors", selectors, 0o644},
	}
	for _, f := range files {
		if err := writeLines(m.conf(f.name), head, f.lines, f.mode); err != nil {
			return err
		}
	}
	// Dovecot's user file holds password hashes: root and the dovecot group only.
	if err := writeLines(m.conf("users"), "", users, 0o640); err != nil {
		return err
	}
	m.chownGroup(m.conf("users"), "dovecot")
	if err := writeFileIfChanged(m.env.P("/etc/dovecot/conf.d/99-ike-sni.conf"), dovecotSNI.String(), 0o644); err != nil {
		return err
	}
	if _, err := m.env.Run.Run(ctx, "postmap", "-F", "lmdb:"+filepath.Join(m.env.Cfg.MailConfDir, "sni")); err != nil {
		return fmt.Errorf("mail certificates table: %w", err)
	}
	if _, err := m.env.Run.Run(ctx, "postfix", "check"); err != nil {
		return fmt.Errorf("Postfix rejected the new configuration: %w", err)
	}
	if _, err := m.env.Run.Run(ctx, "postfix", "reload"); err != nil {
		return err
	}
	if _, err := m.env.Run.Run(ctx, "doveconf", "-n"); err != nil {
		return fmt.Errorf("Dovecot rejected the new configuration: %w", err)
	}
	_, _ = m.env.Run.Run(ctx, "doveadm", "reload")
	if m.env.Cfg.SpamFilter == "rspamd" {
		_, _ = m.env.Run.Run(ctx, "systemctl", "reload", "rspamd")
	}
	return m.writeWebmailVhost(ctx, all)
}

func (m *Manager) userLine(domain string, mb Mailbox) string {
	home := m.MaildirOf(domain, mb.Local)
	extra := ""
	if mb.QuotaMB > 0 {
		if m.dovecot24 {
			extra = "userdb_quota_storage_size=" + strconv.Itoa(mb.QuotaMB) + "M"
		} else {
			extra = "userdb_quota_rule=*:storage=" + strconv.Itoa(mb.QuotaMB) + "M"
		}
	}
	// user:password:uid:gid:gecos:home:shell:extra
	return fmt.Sprintf("%s@%s:%s:%d:%d::%s::%s", mb.Local, domain, mb.PasswordHash, m.vmailUID, m.vmailGID, home, extra)
}

func writeLines(path, head string, lines []string, mode os.FileMode) error {
	body := head + strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}
	return writeFileIfChanged(path, body, mode)
}

func writeFileIfChanged(path, body string, mode os.FileMode) error {
	if b, err := os.ReadFile(path); err == nil && string(b) == body {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ike-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	_ = tmp.Chmod(mode)
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (m *Manager) chownGroup(path, group string) {
	if m.env.Cfg.DryRun {
		return
	}
	if g, err := user.LookupGroup(group); err == nil {
		gid, _ := strconv.Atoi(g.Gid)
		_ = os.Chown(path, 0, gid)
	}
}

// ---- Sieve (auto-replies) ----

func sieveQuote(s string) string {
	// Sieve multi-line strings: "text:\n...\n.\n", with leading dots doubled.
	var b strings.Builder
	b.WriteString("text:\n")
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if strings.HasPrefix(l, ".") {
			l = "." + l
		}
		b.WriteString(l + "\n")
	}
	b.WriteString(".\n")
	return b.String()
}

func sieveString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// SieveScript renders a mailbox's filter (auto-reply).
func SieveScript(addr string, a *AutoReply) string {
	if a == nil || !a.Enabled {
		return "# Written by Ike. No auto-reply.\n"
	}
	var b strings.Builder
	b.WriteString("# Written by Ike: auto-reply.\nrequire [\"vacation\", \"date\", \"relational\"];\n")
	var conds []string
	if a.From != "" {
		conds = append(conds, `currentdate :value "ge" "date" `+sieveString(a.From))
	}
	if a.Until != "" {
		conds = append(conds, `currentdate :value "le" "date" `+sieveString(a.Until))
	}
	vac := fmt.Sprintf("vacation :days %d :subject %s :addresses [%s] %s;\n", a.Days, sieveString(a.Subject), sieveString(addr), sieveQuote(a.Body))
	if len(conds) > 0 {
		// no indentation: it would end up in the message body
		b.WriteString("if allof (" + strings.Join(conds, ", ") + ") {\n" + vac + "}\n")
	} else {
		b.WriteString(vac)
	}
	return b.String()
}

func (m *Manager) writeSieve(ctx context.Context, domain string, mb Mailbox) error {
	home := m.MaildirOf(domain, mb.Local)
	path := m.env.P(filepath.Join(home, "sieve", "ike.sieve"))
	if err := writeFileIfChanged(path, SieveScript(mb.Local+"@"+domain, mb.AutoReply), 0o600); err != nil {
		return err
	}
	link := m.env.P(filepath.Join(home, ".dovecot.sieve"))
	_ = os.Remove(link)
	if err := os.Symlink("sieve/ike.sieve", link); err != nil {
		return err
	}
	if !m.env.Cfg.DryRun {
		for _, p := range []string{filepath.Dir(path), path, link} {
			_ = os.Lchown(p, m.vmailUID, m.vmailGID)
		}
		_ = os.Lchown(m.env.P(home), m.vmailUID, m.vmailGID)
	}
	// compile now so a mistake shows up here, not as a lost message
	if _, err := m.env.Run.Run(ctx, "sievec", path); err != nil {
		return fmt.Errorf("auto-reply could not be saved: %w", err)
	}
	return nil
}

// ---- DKIM ----

func (m *Manager) dkimKeyPath(d *Domain) string {
	return filepath.Join(m.env.Cfg.DKIMDir, d.Domain+"."+d.Selector+".key")
}

// writeDKIMKey makes a 2048-bit RSA key for Rspamd to sign with.
func (m *Manager) writeDKIMKey(ctx context.Context, d *Domain) error {
	path := m.env.P(m.dkimKeyPath(d))
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o640); err != nil {
		return err
	}
	m.chownGroup(path, "_rspamd")
	return nil
}

// DKIMRecord returns the TXT record name and value for a domain's key.
func (m *Manager) DKIMRecord(domain string) (name, value string, err error) {
	d, err := m.load(domain)
	if err != nil {
		return "", "", err
	}
	b, err := os.ReadFile(m.env.P(m.dkimKeyPath(d)))
	if err != nil {
		return "", "", err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return "", "", fmt.Errorf("DKIM key is unreadable")
	}
	key, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return "", "", err
	}
	pub, _ := x509.MarshalPKIXPublicKey(key.(*rsa.PrivateKey).Public())
	return d.Selector + "._domainkey", "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(pub), nil
}

// RotateDKIM makes a new key under a new selector. The old key's record
// stays published until the next DNS update so mail in flight still verifies.
func (m *Manager) RotateDKIM(ctx context.Context, account, domain string) (*PublicDomain, error) {
	var oldPath string
	p, err := m.change(ctx, account, domain, func(d *Domain) error {
		oldPath = m.dkimKeyPath(d)
		d.Selector = "ike" + kitDate()
		return m.writeDKIMKey(ctx, d)
	})
	if err != nil {
		return nil, err
	}
	_, _ = m.PublishDNS(ctx, account, domain)
	_ = os.Remove(m.env.P(oldPath))
	return p, nil
}

func kitDate() string { return time.Now().UTC().Format("20060102") }
