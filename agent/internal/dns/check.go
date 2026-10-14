package dns

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Resolver is the part of net.Resolver the checks use.
type Resolver interface {
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupNS(ctx context.Context, name string) ([]*net.NS, error)
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

// netResolver asks one DNS server directly ("1.1.1.1"). Empty means the
// system resolver.
func netResolver(server string) Resolver {
	if server == "" {
		return net.DefaultResolver
	}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		d := net.Dialer{Timeout: 4 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(server, "53"))
	}}
}

// PublicResolvers are asked during propagation checks.
var PublicResolvers = []struct{ Name, Addr string }{
	{"Cloudflare", "1.1.1.1"},
	{"Google", "8.8.8.8"},
	{"Quad9", "9.9.9.9"},
	{"OpenDNS", "208.67.222.222"},
}

// Answer is what one resolver returned for one record set.
type Answer struct {
	Resolver string   `json:"resolver"`
	Values   []string `json:"values"`
	Match    bool     `json:"match"`
	Error    string   `json:"error,omitempty"`
}

type RecordCheck struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Expected []string `json:"expected"`
	Answers  []Answer `json:"answers"`
	// Propagated is true when every resolver returns the expected values.
	Propagated bool   `json:"propagated"`
	Note       string `json:"note,omitempty"`
}

type Propagation struct {
	Zone string `json:"zone"`
	// Delegation: what the parent zone says the name servers are.
	Delegated   []string      `json:"delegated"`
	Expected    []string      `json:"expected_nameservers"`
	Delegation  bool          `json:"delegation_ok"`
	Records     []RecordCheck `json:"records"`
	CheckedAt   time.Time     `json:"checked_at"`
	Explanation string        `json:"explanation"`
}

// lookup returns normalized values for one name and type.
func lookup(ctx context.Context, r Resolver, name, typ string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var out []string
	switch typ {
	case "A", "AAAA":
		nw := "ip4"
		if typ == "AAAA" {
			nw = "ip6"
		}
		ips, err := r.LookupIP(ctx, nw, name)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			out = append(out, ip.String())
		}
	case "CNAME":
		c, err := r.LookupCNAME(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, strings.ToLower(strings.TrimSuffix(c, ".")))
	case "MX":
		mx, err := r.LookupMX(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, x := range mx {
			out = append(out, fmt.Sprintf("%d %s", x.Pref, strings.ToLower(strings.TrimSuffix(x.Host, "."))))
		}
	case "TXT":
		t, err := r.LookupTXT(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, t...)
	case "NS":
		ns, err := r.LookupNS(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, n := range ns {
			out = append(out, strings.ToLower(strings.TrimSuffix(n.Host, ".")))
		}
	default:
		return nil, errSkip
	}
	sort.Strings(out)
	return out, nil
}

var errSkip = fmt.Errorf("not checked")

func expectedValues(rs []Record, zone string) []string {
	var out []string
	for _, r := range rs {
		switch r.Type {
		case "MX":
			out = append(out, fmt.Sprintf("%d %s", r.Priority, strings.TrimSuffix(absTarget(r.Content, zone), ".")))
		case "CNAME", "NS":
			out = append(out, strings.TrimSuffix(absTarget(r.Content, zone), "."))
		default:
			out = append(out, r.Content)
		}
	}
	sort.Strings(out)
	return out
}

// Check asks public resolvers and the parent zone whether the zone's records
// are visible yet. Only A, AAAA, CNAME, MX, TXT and NS are compared.
func (m *Manager) Check(ctx context.Context, account, zone string) (*Propagation, error) {
	z, err := m.Get(account, zone)
	if err != nil {
		return nil, err
	}
	p := &Propagation{Zone: z.Name, CheckedAt: time.Now().UTC(), Expected: z.Nameservers}
	if ns, err := lookup(ctx, m.resolver(PublicResolvers[0].Addr), z.Name, "NS"); err == nil {
		p.Delegated = ns
	}
	p.Delegation = len(p.Delegated) > 0 && sameSet(p.Delegated, lower(z.Nameservers))
	// group records into sets
	type key struct{ name, typ string }
	sets := map[key][]Record{}
	var order []key
	for _, r := range z.Records {
		k := key{r.Name, r.Type}
		if _, ok := sets[k]; !ok {
			order = append(order, k)
		}
		sets[k] = append(sets[k], r)
	}
	if len(order) > 40 {
		order = order[:40]
	}
	p.Records = make([]RecordCheck, len(order))
	var wg sync.WaitGroup
	for i, k := range order {
		i, k := i, k
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := strings.TrimSuffix(FQDN(k.name, z.Name), ".")
			rc := RecordCheck{Name: k.name, Type: k.typ, Expected: expectedValues(sets[k], z.Name), Propagated: true}
			if strings.HasPrefix(k.name, "*") {
				name = strings.Replace(name, "*", "ike-wildcard-check", 1)
			}
			for _, res := range PublicResolvers {
				got, err := lookup(ctx, m.resolver(res.Addr), name, k.typ)
				a := Answer{Resolver: res.Name, Values: got}
				if err == errSkip {
					rc.Note = k.typ + " records are not compared; use dig to check them"
					rc.Propagated = false
					break
				}
				if err != nil {
					a.Error = shortDNSErr(err)
				}
				a.Match = err == nil && containsAll(got, rc.Expected)
				if !a.Match {
					rc.Propagated = false
				}
				rc.Answers = append(rc.Answers, a)
			}
			p.Records[i] = rc
		}()
	}
	wg.Wait()
	switch {
	case !p.Delegation && len(p.Delegated) == 0:
		p.Explanation = "The domain's registrar isn't pointing it at any name servers yet. Set the name servers listed here at your registrar."
	case !p.Delegation:
		p.Explanation = "The registrar points this domain at different name servers (" + strings.Join(p.Delegated, ", ") +
			"). Records here won't be used until you change the name servers at your registrar."
	default:
		ok := 0
		for _, r := range p.Records {
			if r.Propagated {
				ok++
			}
		}
		if ok == len(p.Records) {
			p.Explanation = "Everything is visible worldwide."
		} else {
			p.Explanation = fmt.Sprintf("%d of %d record sets are visible everywhere. Changes can take up to the record's TTL to show up.", ok, len(p.Records))
		}
	}
	return p, nil
}

func shortDNSErr(err error) string {
	if de, ok := err.(*net.DNSError); ok {
		if de.IsNotFound {
			return "no such record"
		}
		if de.IsTimeout {
			return "timed out"
		}
		return de.Err
	}
	return err.Error()
}

func lower(l []string) []string {
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = strings.ToLower(strings.TrimSuffix(s, "."))
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	return containsAll(a, b)
}

func containsAll(have, want []string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[strings.ToLower(h)] = true
	}
	for _, w := range want {
		if !set[strings.ToLower(w)] {
			return false
		}
	}
	return true
}

// ReverseDNS checks this server's PTR records: each public address should
// resolve back to the hostname, and the hostname forward to the address.
type ReverseCheck struct {
	Address  string   `json:"address"`
	PTR      []string `json:"ptr"`
	Hostname string   `json:"hostname"`
	Forward  bool     `json:"forward_confirmed"`
	OK       bool     `json:"ok"`
	Advice   string   `json:"advice"`
}

func (m *Manager) ReverseDNS(ctx context.Context) []ReverseCheck {
	host := strings.ToLower(m.env.Cfg.Hostname)
	r := m.resolver(PublicResolvers[0].Addr)
	var out []ReverseCheck
	for _, ip := range []string{m.env.Cfg.PublicIPv4, m.env.Cfg.PublicIPv6} {
		if ip == "" {
			continue
		}
		c := ReverseCheck{Address: ip, Hostname: host}
		cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		names, err := r.LookupAddr(cctx, ip)
		cancel()
		for _, n := range names {
			c.PTR = append(c.PTR, strings.ToLower(strings.TrimSuffix(n, ".")))
		}
		if err == nil {
			for _, n := range c.PTR {
				fwd, _ := lookup(ctx, r, n, map[bool]string{true: "AAAA", false: "A"}[strings.Contains(ip, ":")])
				for _, f := range fwd {
					if net.ParseIP(f).Equal(net.ParseIP(ip)) {
						c.Forward = true
					}
				}
			}
		}
		c.OK = c.Forward && len(c.PTR) > 0 && (host == "" || c.PTR[0] == host)
		switch {
		case len(c.PTR) == 0:
			c.Advice = "This address has no reverse DNS. Mail servers often reject mail from it. In your VPS provider's control panel, set the reverse DNS (PTR) of " + ip + " to " + orHost(host) + "."
		case host != "" && c.PTR[0] != host:
			c.Advice = "Reverse DNS says " + c.PTR[0] + " but this server is " + host + ". Change the PTR record at your VPS provider to " + host + "."
		case !c.Forward:
			c.Advice = c.PTR[0] + " does not point back to " + ip + ". Add an A/AAAA record for it."
		default:
			c.Advice = "Reverse DNS is set up correctly."
		}
		out = append(out, c)
	}
	return out
}

func orHost(h string) string {
	if h == "" {
		return "your server's hostname (for example mail.yourdomain.com)"
	}
	return h
}

// ---- ACME DNS-01 ----

// SetACMEChallenge publishes _acme-challenge TXT values for a domain and waits
// until the zone's name servers return them. certbot's hooks call it once per
// value; values for the same name accumulate (example.com and *.example.com
// share a name) until Cleanup.
func (m *Manager) SetACMEChallenge(ctx context.Context, domain, value string) error {
	domain = strings.TrimPrefix(strings.ToLower(domain), "*.")
	if len(value) < 20 || len(value) > 100 || strings.ContainsAny(value, " \"\\;") {
		return fmt.Errorf("challenge value looks wrong")
	}
	name := "_acme-challenge." + domain
	z := m.ZoneFor(name)
	if z == nil {
		return fmt.Errorf("no DNS zone here covers %s; add the zone first or use a single-domain certificate", domain)
	}
	rel := RelativeTo(name, z.Name)
	_, err := m.change(ctx, "", z.Name, func(z *Zone) error {
		for _, r := range z.Records {
			if r.Managed == "acme" && r.Name == rel && r.Content == value {
				return nil
			}
		}
		z.Records = append(z.Records, Record{ID: randID(), Name: rel, Type: "TXT", TTL: 60, Content: value, Managed: "acme"})
		return nil
	})
	if err != nil {
		return err
	}
	return m.waitTXT(ctx, z, name, value)
}

func (m *Manager) waitTXT(ctx context.Context, z *Zone, name, value string) error {
	if m.env.Cfg.DryRun {
		return nil
	}
	// Ask the zone's own name servers (resolved to addresses).
	var servers []string
	if z.Backend == "local" {
		servers = []string{"127.0.0.1"}
	} else {
		for _, ns := range z.Nameservers {
			if ips, err := net.DefaultResolver.LookupHost(ctx, ns); err == nil && len(ips) > 0 {
				servers = append(servers, ips[0])
			}
		}
	}
	if len(servers) == 0 {
		servers = []string{PublicResolvers[0].Addr}
	}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		all := true
		for _, s := range servers {
			got, _ := lookup(ctx, m.resolver(s), name, "TXT")
			if !containsAll(got, []string{value}) {
				all = false
			}
		}
		if all {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return fmt.Errorf("%s did not show up on the name servers within 3 minutes", name)
}

// CleanupACME removes every challenge record for a domain.
func (m *Manager) CleanupACME(ctx context.Context, domain string) error {
	domain = strings.TrimPrefix(strings.ToLower(domain), "*.")
	name := "_acme-challenge." + domain
	z := m.ZoneFor(name)
	if z == nil {
		return nil
	}
	rel := RelativeTo(name, z.Name)
	_, err := m.change(ctx, "", z.Name, func(z *Zone) error {
		out := z.Records[:0]
		for _, r := range z.Records {
			if !(r.Managed == "acme" && r.Name == rel) {
				out = append(out, r)
			}
		}
		z.Records = out
		return nil
	})
	return err
}

// CanIssueWildcard reports whether DNS-01 is possible for a domain here.
func (m *Manager) CanIssueWildcard(domain string) bool {
	return m.ZoneFor("_acme-challenge."+strings.TrimPrefix(domain, "*.")) != nil
}

var _ = strconv.Itoa
