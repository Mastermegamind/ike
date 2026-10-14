package mail

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Lookup is the DNS the deliverability checks use.
type Lookup interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

type netLookup struct{}

func (netLookup) LookupMX(ctx context.Context, n string) ([]*net.MX, error) {
	return net.DefaultResolver.LookupMX(ctx, n)
}
func (netLookup) LookupTXT(ctx context.Context, n string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, n)
}
func (netLookup) LookupHost(ctx context.Context, n string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, n)
}
func (netLookup) LookupAddr(ctx context.Context, a string) ([]string, error) {
	return net.DefaultResolver.LookupAddr(ctx, a)
}

func dialProbe(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 6 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// Check is one deliverability check with a plain-language fix.
type Check struct {
	Record   string `json:"record"` // MX | SPF | DKIM | DMARC | PTR | A | PORT25 | BLOCKLIST | TLS
	Host     string `json:"host"`
	Type     string `json:"type"`
	Expected string `json:"expected"`
	Found    string `json:"found,omitempty"`
	Status   string `json:"status"` // ok | missing | wrong | warn
	Hint     string `json:"hint"`
}

// Blocklists checked for this server's IPv4 address.
var Blocklists = []string{"zen.spamhaus.org", "bl.spamcop.net", "b.barracudacentral.org", "dnsbl.sorbs.net"}

// Deliverability checks everything that decides whether this domain's mail
// is accepted elsewhere and whether mail reaches it.
func (m *Manager) Deliverability(ctx context.Context, account, domain string) ([]Check, error) {
	d, err := m.get(account, domain)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	set, _ := m.Settings()
	ip4, ip6, host := m.env.Cfg.PublicIPv4, m.env.Cfg.PublicIPv6, strings.ToLower(m.env.Cfg.Hostname)
	mailHost := "mail." + d.Domain
	ours := func(addrs []string) bool {
		for _, a := range addrs {
			if a == ip4 || (ip6 != "" && net.ParseIP(a).Equal(net.ParseIP(ip6))) {
				return true
			}
		}
		return false
	}
	var mu sync.Mutex
	var out []Check
	add := func(c Check) { mu.Lock(); out = append(out, c); mu.Unlock() }
	var wg sync.WaitGroup
	run := func(fn func()) { wg.Add(1); go func() { defer wg.Done(); fn() }() }

	run(func() { // A record for mail.<domain>
		c := Check{Record: "A", Host: mailHost, Type: "A", Expected: ip4}
		addrs, err := m.lookup.LookupHost(ctx, mailHost)
		switch {
		case err != nil || len(addrs) == 0:
			c.Status, c.Hint = "missing", "Add an A record for mail."+d.Domain+" pointing to "+ip4+"."
		case !ours(addrs):
			c.Status, c.Found, c.Hint = "wrong", strings.Join(addrs, ", "), "mail."+d.Domain+" points elsewhere. Change it to "+ip4+"."
		default:
			c.Status, c.Found, c.Hint = "ok", strings.Join(addrs, ", "), "Mail clients and other servers can find this server."
		}
		add(c)
	})
	run(func() { // MX
		c := Check{Record: "MX", Host: d.Domain, Type: "MX", Expected: "10 " + mailHost}
		mx, err := m.lookup.LookupMX(ctx, d.Domain)
		if err != nil || len(mx) == 0 {
			c.Status, c.Hint = "missing", "Add an MX record: priority 10, mail server "+mailHost+". Without it nobody can send mail to "+d.Domain+"."
			add(c)
			return
		}
		var found []string
		good := false
		for _, x := range mx {
			h := strings.TrimSuffix(strings.ToLower(x.Host), ".")
			found = append(found, fmt.Sprintf("%d %s", x.Pref, h))
			if addrs, err := m.lookup.LookupHost(ctx, h); err == nil && ours(addrs) {
				good = true
			}
		}
		c.Found = strings.Join(found, ", ")
		if good {
			c.Status, c.Hint = "ok", "Mail for "+d.Domain+" comes to this server."
		} else {
			c.Status, c.Hint = "wrong", "MX points to another mail service ("+c.Found+"). Mail sent to "+d.Domain+" won't arrive in mailboxes here."
		}
		add(c)
	})
	run(func() { // SPF
		c := Check{Record: "SPF", Host: d.Domain, Type: "TXT", Expected: "v=spf1 mx a ip4:" + ip4 + " ~all"}
		txt, _ := m.lookup.LookupTXT(ctx, d.Domain)
		var spf []string
		for _, t := range txt {
			if strings.HasPrefix(strings.ToLower(t), "v=spf1") {
				spf = append(spf, t)
			}
		}
		switch {
		case len(spf) == 0:
			c.Status, c.Hint = "missing", "Add a TXT record on "+d.Domain+": "+c.Expected+". Without it Gmail and Outlook treat your mail as suspicious."
		case len(spf) > 1:
			c.Status, c.Found, c.Hint = "wrong", strings.Join(spf, " | "), "There are two SPF records; receivers then ignore both. Merge them into one."
		default:
			c.Found = spf[0]
			s := " " + strings.ToLower(spf[0]) + " "
			okIP := strings.Contains(s, " mx ") || strings.Contains(s, " a ") || strings.Contains(s, "ip4:"+ip4+" ") || strings.Contains(s, "ip4:"+ip4+"/")
			if set.Relay.Provider != "direct" && relaySPF[set.Relay.Provider] != "" && !strings.Contains(s, relaySPF[set.Relay.Provider]) {
				c.Status, c.Hint = "wrong", "Mail goes out through "+set.Relay.Provider+"; add include:"+relaySPF[set.Relay.Provider]+" to the SPF record."
			} else if okIP {
				c.Status, c.Hint = "ok", "Receivers know this server may send for "+d.Domain+"."
			} else {
				c.Status, c.Hint = "wrong", "The SPF record doesn't include this server. Add ip4:"+ip4+" (or mx) to it."
			}
			if strings.Contains(s, "+all") {
				c.Status, c.Hint = "wrong", "+all lets anyone send as "+d.Domain+". Use ~all."
			}
		}
		add(c)
	})
	run(func() { // DKIM
		name, val, err := m.DKIMRecord(d.Domain)
		c := Check{Record: "DKIM", Host: name + "." + d.Domain, Type: "TXT", Expected: val}
		if err != nil {
			c.Status, c.Hint = "missing", "No DKIM key exists for this domain yet."
			add(c)
			return
		}
		txt, _ := m.lookup.LookupTXT(ctx, name+"."+d.Domain)
		got := strings.Join(txt, "")
		want := val[strings.Index(val, "p=")+2:]
		switch {
		case got == "":
			c.Status, c.Hint = "missing", "Add the TXT record "+name+" with the value shown. It proves your mail wasn't altered."
		case !strings.Contains(strings.ReplaceAll(got, " ", ""), want):
			c.Status, c.Found, c.Hint = "wrong", trim(got, 80), "The published DKIM key doesn't match this server's key. Replace it with the value shown."
		default:
			c.Status, c.Hint = "ok", "Outgoing mail is signed and receivers can verify it."
		}
		add(c)
	})
	run(func() { // DMARC
		c := Check{Record: "DMARC", Host: "_dmarc." + d.Domain, Type: "TXT", Expected: "v=DMARC1; p=" + set.DMARCPolicy + "; rua=mailto:postmaster@" + d.Domain}
		txt, _ := m.lookup.LookupTXT(ctx, "_dmarc."+d.Domain)
		got := ""
		for _, t := range txt {
			if strings.HasPrefix(strings.ToUpper(t), "V=DMARC1") {
				got = t
			}
		}
		switch {
		case got == "":
			c.Status, c.Hint = "missing", "Add a DMARC TXT record on _dmarc."+d.Domain+". Gmail and Yahoo require it from bulk senders."
		case strings.Contains(strings.ReplaceAll(strings.ToLower(got), " ", ""), "p=none"):
			c.Status, c.Found, c.Hint = "warn", got, "DMARC is in report-only mode (p=none). Once SPF and DKIM pass, switch to quarantine."
		default:
			c.Status, c.Found, c.Hint = "ok", got, "Receivers know what to do with mail that fails checks."
		}
		add(c)
	})
	run(func() { // PTR
		c := Check{Record: "PTR", Host: ip4, Type: "PTR", Expected: orStr(host, mailHost)}
		names, err := m.lookup.LookupAddr(ctx, ip4)
		if err != nil || len(names) == 0 {
			c.Status, c.Hint = "missing", "This server's IP has no reverse DNS. Set it to "+c.Expected+" in your VPS provider's control panel; many receivers reject mail without it."
			add(c)
			return
		}
		ptr := strings.TrimSuffix(strings.ToLower(names[0]), ".")
		c.Found = ptr
		fwd, _ := m.lookup.LookupHost(ctx, ptr)
		if !ours(fwd) {
			c.Status, c.Hint = "wrong", ptr+" doesn't point back to "+ip4+". Add an A record for it, or change the reverse DNS."
		} else {
			c.Status, c.Hint = "ok", "Reverse DNS matches."
		}
		add(c)
	})
	run(func() { // outbound port 25
		c := Check{Record: "PORT25", Host: "gmail-smtp-in.l.google.com:25", Type: "TCP", Expected: "open"}
		if set.Relay.Provider != "direct" {
			c.Status, c.Hint = "ok", "Mail goes out through "+set.Relay.Provider+", so port 25 doesn't matter."
		} else if err := m.probe(ctx, "gmail-smtp-in.l.google.com:25"); err != nil {
			c.Status, c.Found = "wrong", "blocked"
			c.Hint = "Your VPS provider blocks outgoing port 25, so this server can't deliver mail itself. Ask them to unblock it, or set up a relay (Brevo, Mailgun, SES) in mail settings."
		} else {
			c.Status, c.Found, c.Hint = "ok", "open", "This server can deliver mail directly."
		}
		add(c)
	})
	run(func() { // blocklists
		if ip4 == "" {
			return
		}
		p := strings.Split(ip4, ".")
		rev := p[3] + "." + p[2] + "." + p[1] + "." + p[0]
		var listed []string
		for _, bl := range Blocklists {
			if a, err := m.lookup.LookupHost(ctx, rev+"."+bl); err == nil && len(a) > 0 && strings.HasPrefix(a[0], "127.0.0.") {
				listed = append(listed, bl)
			}
		}
		c := Check{Record: "BLOCKLIST", Host: ip4, Type: "DNSBL", Expected: "not listed"}
		if len(listed) > 0 {
			c.Status, c.Found = "wrong", strings.Join(listed, ", ")
			c.Hint = "This server's IP is on a spam blocklist. Find out why on the list's website, fix it, then request removal; until then use a relay."
		} else {
			c.Status, c.Found, c.Hint = "ok", "not listed", "Not on the common spam blocklists."
		}
		add(c)
	})
	run(func() {
		c := Check{Record: "TLS", Host: mailHost, Type: "certificate", Expected: "Let's Encrypt certificate"}
		if d.TLS {
			c.Status, c.Hint = "ok", "Mail clients connect to "+mailHost+" without certificate warnings."
		} else {
			c.Status, c.Hint = "warn", "Get a certificate for "+mailHost+" so mail apps don't show warnings."
		}
		add(c)
	})
	wg.Wait()
	order := map[string]int{"MX": 0, "A": 1, "SPF": 2, "DKIM": 3, "DMARC": 4, "PTR": 5, "PORT25": 6, "BLOCKLIST": 7, "TLS": 8}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && order[out[j].Record] < order[out[j-1].Record]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
