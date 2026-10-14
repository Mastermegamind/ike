// Package dns manages DNS zones. Ike is the source of truth: zones are kept as
// JSON and published either to the local PowerDNS server (bind backend, one
// zone file per domain) or to a DNS provider (Cloudflare, DigitalOcean).
package dns

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Record is one DNS record. Name is relative to the zone: "@" for the zone
// itself, "www", "*.dev", "_dmarc".
type Record struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	TTL      int    `json:"ttl"`
	Content  string `json:"content"`            // the value; for MX/SRV the target host
	Priority int    `json:"priority,omitempty"` // MX, SRV
	Weight   int    `json:"weight,omitempty"`   // SRV
	Port     int    `json:"port,omitempty"`     // SRV
	Proxied  bool   `json:"proxied,omitempty"`  // Cloudflare only
	Comment  string `json:"comment,omitempty"`
	// Managed records were added by Ike (mail, DKIM, ACME) and are updated by it.
	Managed string `json:"managed,omitempty"`
}

// Types lists every record type Ike can publish, with a short explanation.
var Types = []TypeInfo{
	{"A", "Points a name at an IPv4 address"},
	{"AAAA", "Points a name at an IPv6 address"},
	{"CNAME", "Makes a name an alias of another name"},
	{"MX", "Mail server for the domain, with a priority (lower is tried first)"},
	{"TXT", "Free text: SPF, DKIM, DMARC and site verification"},
	{"NS", "Delegates a subdomain to other name servers"},
	{"SRV", "Service location: priority, weight, port and target"},
	{"CAA", "Which certificate authorities may issue certificates"},
	{"PTR", "Reverse DNS (in reverse zones only)"},
	{"TLSA", "DANE certificate pin for a service"},
	{"SSHFP", "SSH host key fingerprint"},
	{"DS", "DNSSEC delegation for a child zone"},
	{"NAPTR", "Name authority pointer (telephony, SIP)"},
}

type TypeInfo struct {
	Type string `json:"type"`
	Help string `json:"help"`
}

var (
	labelRe   = regexp.MustCompile(`^(\*|[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?)$`)
	hexRe     = regexp.MustCompile(`^[0-9A-Fa-f]+$`)
	caaTagRe  = regexp.MustCompile(`^(issue|issuewild|iodef)$`)
	naptrFlag = regexp.MustCompile(`^[A-Za-z0-9]*$`)
)

// ValidName checks a relative record name.
func ValidName(n string) error {
	if n == "@" {
		return nil
	}
	if n == "" || len(n) > 200 || strings.HasSuffix(n, ".") {
		return fmt.Errorf("name %q: use @ for the domain itself, or a label like www", n)
	}
	for i, l := range strings.Split(n, ".") {
		if !labelRe.MatchString(l) || (l == "*" && i != 0) {
			return fmt.Errorf("name %q is not a valid DNS name", n)
		}
	}
	return nil
}

// ValidHost checks a host name used as a record target. A trailing dot is
// allowed; "@" means the zone itself.
func ValidHost(h string) error {
	if h == "@" || h == "." {
		return nil
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" || len(h) > 253 {
		return fmt.Errorf("%q is not a host name", h)
	}
	for _, l := range strings.Split(h, ".") {
		if l == "*" || !labelRe.MatchString(l) {
			return fmt.Errorf("%q is not a host name", h)
		}
	}
	return nil
}

func noControl(s string) error {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("value contains control characters")
		}
	}
	return nil
}

// Validate checks a record and normalises it (upper-case type, default TTL).
func (r *Record) Validate() error {
	r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
	r.Name = strings.ToLower(strings.TrimSpace(r.Name))
	r.Content = strings.TrimSpace(r.Content)
	if r.Name == "" {
		r.Name = "@"
	}
	if err := ValidName(r.Name); err != nil {
		return err
	}
	if r.TTL == 0 {
		r.TTL = 3600
	}
	if r.TTL < 60 || r.TTL > 604800 {
		return fmt.Errorf("TTL must be between 60 seconds and 7 days")
	}
	if len(r.Comment) > 200 || noControl(r.Comment) != nil {
		return fmt.Errorf("comment is too long or has control characters")
	}
	if err := noControl(r.Content); err != nil {
		return err
	}
	if r.Proxied && r.Type != "A" && r.Type != "AAAA" && r.Type != "CNAME" {
		return fmt.Errorf("only A, AAAA and CNAME records can be proxied")
	}
	c := r.Content
	switch r.Type {
	case "A":
		a, err := netip.ParseAddr(c)
		if err != nil || !a.Is4() {
			return fmt.Errorf("%q is not an IPv4 address", c)
		}
	case "AAAA":
		a, err := netip.ParseAddr(c)
		if err != nil || !a.Is6() || a.Is4In6() {
			return fmt.Errorf("%q is not an IPv6 address", c)
		}
		r.Content = a.String()
	case "CNAME":
		if r.Name == "@" {
			return fmt.Errorf("the domain itself can't be a CNAME; use an A record (or a provider that flattens CNAMEs)")
		}
		if err := ValidHost(c); err != nil {
			return err
		}
	case "NS", "PTR":
		if err := ValidHost(c); err != nil {
			return err
		}
	case "MX":
		if err := ValidHost(c); err != nil {
			return err
		}
		if r.Priority < 0 || r.Priority > 65535 {
			return fmt.Errorf("priority must be 0-65535")
		}
	case "SRV":
		if !strings.HasPrefix(r.Name, "_") {
			return fmt.Errorf("SRV names look like _service._tcp or _service._tcp.sub")
		}
		if err := ValidHost(c); err != nil {
			return err
		}
		for _, v := range []int{r.Priority, r.Weight, r.Port} {
			if v < 0 || v > 65535 {
				return fmt.Errorf("priority, weight and port must be 0-65535")
			}
		}
	case "TXT":
		if c == "" || len(c) > 4000 {
			return fmt.Errorf("TXT value must be 1-4000 characters")
		}
		// Stored unquoted; long values are split into 255-byte strings when published.
		if strings.HasPrefix(c, `"`) && strings.HasSuffix(c, `"`) && len(c) > 1 {
			r.Content = unquoteTXT(c)
		}
	case "CAA":
		// 0 issue "letsencrypt.org"
		f := strings.SplitN(c, " ", 3)
		if len(f) != 3 {
			return fmt.Errorf(`CAA looks like: 0 issue "letsencrypt.org"`)
		}
		if n, err := strconv.Atoi(f[0]); err != nil || n < 0 || n > 255 {
			return fmt.Errorf("CAA flag must be 0-255")
		}
		if !caaTagRe.MatchString(f[1]) {
			return fmt.Errorf("CAA tag must be issue, issuewild or iodef")
		}
		v := strings.Trim(f[2], `"`)
		if strings.ContainsAny(v, `"\`) {
			return fmt.Errorf("CAA value can't contain quotes")
		}
		r.Content = f[0] + " " + f[1] + ` "` + v + `"`
	case "TLSA":
		// 3 1 1 <hex>
		f := strings.Fields(c)
		if len(f) != 4 || !smallInts(f[:3], 255) || !hexRe.MatchString(f[3]) {
			return fmt.Errorf("TLSA looks like: 3 1 1 <hex digest>")
		}
		if !strings.HasPrefix(r.Name, "_") {
			return fmt.Errorf("TLSA names look like _443._tcp or _25._tcp.mail")
		}
	case "SSHFP":
		f := strings.Fields(c)
		if len(f) != 3 || !smallInts(f[:2], 255) || !hexRe.MatchString(f[2]) {
			return fmt.Errorf("SSHFP looks like: 4 2 <hex fingerprint>")
		}
	case "DS":
		f := strings.Fields(c)
		if len(f) != 4 || !smallInts(f[:1], 65535) || !smallInts(f[1:3], 255) || !hexRe.MatchString(f[3]) {
			return fmt.Errorf("DS looks like: <key tag> <algorithm> <digest type> <hex digest>")
		}
		if _, err := hex.DecodeString(f[3]); err != nil {
			return fmt.Errorf("DS digest must be hex")
		}
	case "NAPTR":
		// order pref "flags" "service" "regexp" replacement
		f := splitQuoted(c)
		if len(f) != 6 || !smallInts(f[:2], 65535) || !naptrFlag.MatchString(f[2]) {
			return fmt.Errorf(`NAPTR looks like: 100 10 "S" "SIP+D2U" "" _sip._udp.example.com.`)
		}
		if err := ValidHost(f[5]); err != nil {
			return err
		}
	default:
		return fmt.Errorf("record type %q is not supported", r.Type)
	}
	return nil
}

func smallInts(f []string, max int) bool {
	for _, s := range f {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > max {
			return false
		}
	}
	return true
}

// splitQuoted splits on spaces, keeping "quoted strings" (without quotes) together.
func splitQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	in, had := false, false
	for _, r := range s {
		switch {
		case r == '"':
			in = !in
			had = true
		case r == ' ' && !in:
			if cur.Len() > 0 || had {
				out = append(out, cur.String())
				cur.Reset()
				had = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 || had {
		out = append(out, cur.String())
	}
	return out
}

func unquoteTXT(s string) string {
	// "part one" "part two" → part onepart two
	parts := splitQuoted(s)
	return strings.Join(parts, "")
}

// quoteTXT splits a TXT value into quoted 255-byte strings.
func quoteTXT(v string) string {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	var parts []string
	for len(v) > 255 {
		parts = append(parts, `"`+esc.Replace(v[:255])+`"`)
		v = v[255:]
	}
	parts = append(parts, `"`+esc.Replace(v)+`"`)
	return strings.Join(parts, " ")
}

// FQDN turns a relative name into an absolute one with a trailing dot.
func FQDN(name, zone string) string {
	if name == "@" || name == "" {
		return zone + "."
	}
	return name + "." + zone + "."
}

// absTarget makes a target host absolute: names without a dot are relative to
// the zone; names with a dot are taken as full names.
func absTarget(h, zone string) string {
	switch {
	case h == "@":
		return zone + "."
	case h == ".":
		return "."
	case strings.HasSuffix(h, "."):
		return h
	case !strings.Contains(h, "."):
		return h + "." + zone + "."
	default:
		return h + "."
	}
}

// Same reports whether two records are the same data (ignoring ID and TTL).
func Same(a, b Record) bool {
	return a.Name == b.Name && a.Type == b.Type && strings.EqualFold(strings.TrimSuffix(a.Content, "."), strings.TrimSuffix(b.Content, ".")) &&
		a.Priority == b.Priority && a.Weight == b.Weight && a.Port == b.Port
}

// SameIn is Same with host targets made absolute in zone, so "mail" and
// "mail.example.com." compare equal.
func SameIn(a, b Record, zone string) bool {
	switch a.Type {
	case "CNAME", "NS", "PTR", "MX", "SRV":
		a.Content, b.Content = absTarget(a.Content, zone), absTarget(b.Content, zone)
	}
	return Same(a, b)
}
