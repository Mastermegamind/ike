package dns

import (
	"bufio"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Render writes a zone in BIND master file format, which PowerDNS's bind
// backend reads directly.
func Render(z *Zone) string {
	var b strings.Builder
	fmt.Fprintf(&b, "; %s - written by Ike. Edit it in the panel; changes here are overwritten.\n", z.Name)
	fmt.Fprintf(&b, "$ORIGIN %s.\n$TTL %d\n", z.Name, z.TTL)
	ns := z.Nameservers
	primary := "ns1." + z.Name + "."
	if len(ns) > 0 {
		primary = strings.TrimSuffix(ns[0], ".") + "."
	}
	email := strings.Replace(strings.TrimSuffix(z.SOAEmail, "."), "@", ".", 1) + "."
	fmt.Fprintf(&b, "@ %d IN SOA %s %s (\n  %d ; serial\n  %d ; refresh\n  %d ; retry\n  %d ; expire\n  %d ; negative cache\n)\n",
		z.TTL, primary, email, z.Serial, z.Refresh, z.Retry, z.Expire, z.Minimum)
	for _, n := range ns {
		fmt.Fprintf(&b, "@ %d IN NS %s.\n", z.TTL, strings.TrimSuffix(n, "."))
	}
	recs := append([]Record(nil), z.Records...)
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].Name != recs[j].Name {
			if recs[i].Name == "@" {
				return true
			}
			if recs[j].Name == "@" {
				return false
			}
			return recs[i].Name < recs[j].Name
		}
		return recs[i].Type < recs[j].Type
	})
	for _, r := range recs {
		if r.Type == "NS" && r.Name == "@" {
			continue // the zone's own NS set comes from Nameservers
		}
		fmt.Fprintf(&b, "%s %d IN %s %s\n", r.Name, r.TTL, r.Type, rdata(r, z.Name))
	}
	return b.String()
}

func rdata(r Record, zone string) string {
	switch r.Type {
	case "CNAME", "NS", "PTR":
		return absTarget(r.Content, zone)
	case "MX":
		return strconv.Itoa(r.Priority) + " " + absTarget(r.Content, zone)
	case "SRV":
		return fmt.Sprintf("%d %d %d %s", r.Priority, r.Weight, r.Port, absTarget(r.Content, zone))
	case "TXT":
		return quoteTXT(r.Content)
	case "NAPTR":
		f := splitQuoted(r.Content)
		return fmt.Sprintf(`%s %s "%s" "%s" "%s" %s`, f[0], f[1], f[2], f[3], f[4], absTarget(f[5], zone))
	default:
		return r.Content
	}
}

// Parse reads a BIND zone file (from a cPanel backup or an export) into
// records. SOA and the apex NS set are returned separately. Unsupported types
// are reported, not fatal.
func Parse(text, zone string) (recs []Record, apexNS []string, skipped []string, err error) {
	origin := zone + "."
	ttl := 3600
	last := "@"
	sc := bufio.NewScanner(strings.NewReader(joinParens(text)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		raw := sc.Text()
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := splitZoneFields(line)
		if len(f) == 0 {
			continue
		}
		switch strings.ToUpper(f[0]) {
		case "$ORIGIN":
			if len(f) > 1 {
				origin = strings.ToLower(f[1])
			}
			continue
		case "$TTL":
			if len(f) > 1 {
				ttl, _ = parseTTL(f[1])
			}
			continue
		case "$INCLUDE", "$GENERATE":
			skipped = append(skipped, strings.TrimSpace(raw))
			continue
		}
		name := last
		i := 0
		if line[0] != ' ' && line[0] != '\t' {
			name = relName(strings.ToLower(f[0]), origin, zone)
			i = 1
		}
		last = name
		rttl := ttl
		// optional TTL and class in either order
		for k := 0; k < 2 && i < len(f); k++ {
			if t, ok := parseTTL(f[i]); ok {
				rttl = t
				i++
			} else if strings.EqualFold(f[i], "IN") {
				i++
			}
		}
		if i >= len(f) {
			continue
		}
		typ := strings.ToUpper(f[i])
		data := f[i+1:]
		r := Record{Name: name, Type: typ, TTL: rttl}
		switch typ {
		case "SOA":
			continue
		case "NS":
			if len(data) < 1 {
				continue
			}
			if name == "@" {
				apexNS = append(apexNS, strings.TrimSuffix(data[0], "."))
				continue
			}
			r.Content = target(data[0], origin, zone)
		case "A", "AAAA":
			if len(data) < 1 {
				continue
			}
			r.Content = data[0]
		case "CNAME", "PTR":
			if len(data) < 1 {
				continue
			}
			r.Content = target(data[0], origin, zone)
		case "MX":
			if len(data) < 2 {
				continue
			}
			r.Priority, _ = strconv.Atoi(data[0])
			r.Content = target(data[1], origin, zone)
		case "SRV":
			if len(data) < 4 {
				continue
			}
			r.Priority, _ = strconv.Atoi(data[0])
			r.Weight, _ = strconv.Atoi(data[1])
			r.Port, _ = strconv.Atoi(data[2])
			r.Content = target(data[3], origin, zone)
		case "TXT", "SPF":
			r.Type = "TXT"
			var s strings.Builder
			for _, d := range data {
				s.WriteString(strings.ReplaceAll(strings.ReplaceAll(strings.Trim(d, `"`), `\"`, `"`), `\\`, `\`))
			}
			r.Content = s.String()
		case "CAA":
			if len(data) < 3 {
				continue
			}
			r.Content = data[0] + " " + data[1] + " " + strings.Join(data[2:], " ")
		case "TLSA", "SSHFP", "DS", "NAPTR":
			r.Content = strings.Join(data, " ")
		default:
			skipped = append(skipped, strings.TrimSpace(raw))
			continue
		}
		if verr := r.Validate(); verr != nil {
			skipped = append(skipped, strings.TrimSpace(raw)+" ("+verr.Error()+")")
			continue
		}
		recs = append(recs, r)
	}
	return recs, apexNS, skipped, sc.Err()
}

func joinParens(s string) string {
	var b strings.Builder
	depth := 0
	inQ := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQ = !inQ
			b.WriteByte(c)
		case c == ';' && !inQ:
			// keep comment out of joined lines
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if depth == 0 {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		case c == '(' && !inQ:
			depth++
			b.WriteByte(' ')
		case c == ')' && !inQ:
			if depth > 0 {
				depth--
			}
			b.WriteByte(' ')
		case c == '\n' && depth > 0:
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func stripComment(s string) string {
	inQ := false
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			inQ = !inQ
		}
		if s[i] == ';' && !inQ {
			return s[:i]
		}
	}
	return s
}

func splitZoneFields(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && inQ && i+1 < len(s):
			cur.WriteByte(c)
			cur.WriteByte(s[i+1])
			i++
		case c == '"':
			inQ = !inQ
			cur.WriteByte(c)
		case (c == ' ' || c == '\t') && !inQ:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func parseTTL(s string) (int, bool) {
	if n, err := strconv.Atoi(s); err == nil {
		return n, true
	}
	// 1h, 30m, 1d, 1w
	total, num := 0, 0
	seen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= '0' && r <= '9':
			num = num*10 + int(r-'0')
			seen = true
		case r == 's':
			total += num
			num = 0
		case r == 'm':
			total += num * 60
			num = 0
		case r == 'h':
			total += num * 3600
			num = 0
		case r == 'd':
			total += num * 86400
			num = 0
		case r == 'w':
			total += num * 604800
			num = 0
		default:
			return 0, false
		}
	}
	return total + num, seen
}

func relName(n, origin, zone string) string {
	if n == "@" {
		n = origin
	} else if !strings.HasSuffix(n, ".") {
		n = n + "." + origin
	}
	n = strings.TrimSuffix(n, ".")
	if n == zone {
		return "@"
	}
	return strings.TrimSuffix(n, "."+zone)
}

func target(t, origin, zone string) string {
	t = strings.ToLower(t)
	if t == "@" {
		t = origin
	} else if !strings.HasSuffix(t, ".") {
		t = t + "." + origin
	}
	t = strings.TrimSuffix(t, ".")
	if t == zone {
		return "@"
	}
	// one label inside the zone stays relative ("mail"), matching how Ike stores it
	if rel := strings.TrimSuffix(t, "."+zone); rel != t && !strings.Contains(rel, ".") {
		return rel
	}
	return t + "."
}
