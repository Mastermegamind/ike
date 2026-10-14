package dns

import (
	"fmt"
	"strings"
)

// Template describes a ready-made set of records.
type Template struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Help string `json:"help"`
	Mail bool   `json:"mail"` // replaces MX and SPF
}

var Templates = []Template{
	{"website", "Website", "Points the domain and www at this server, and lets Let's Encrypt issue certificates.", false},
	{"website-email", "Website and email on this server", "Website records plus MX, SPF, DMARC and mail client discovery for this server's mail service.", true},
	{"google", "Google Workspace", "Mail goes to Gmail. Add the verification TXT record Google gives you as well.", true},
	{"microsoft", "Microsoft 365", "Mail goes to Exchange Online, plus Teams and Outlook discovery records.", true},
	{"zoho", "Zoho Mail", "Mail goes to Zoho. Add the verification and DKIM records from the Zoho console as well.", true},
}

// MailHost is the name mail clients use; Hook so the mail package can
// supply DKIM records.
var DKIMRecords func(domain string) []Record

func (m *Manager) templateRecords(id, zone, ipv4, ipv6 string) ([]Record, error) {
	if ipv4 == "" {
		ipv4 = m.env.Cfg.PublicIPv4
	}
	if ipv6 == "" {
		ipv6 = m.env.Cfg.PublicIPv6
	}
	if ipv4 != "" && !isIP(ipv4, 4) {
		return nil, fmt.Errorf("%q is not an IPv4 address", ipv4)
	}
	if ipv6 != "" && !isIP(ipv6, 6) {
		return nil, fmt.Errorf("%q is not an IPv6 address", ipv6)
	}
	web := func() []Record {
		var r []Record
		if ipv4 != "" {
			r = append(r, Record{Name: "@", Type: "A", Content: ipv4})
		}
		if ipv6 != "" {
			r = append(r, Record{Name: "@", Type: "AAAA", Content: ipv6})
		}
		r = append(r, Record{Name: "www", Type: "CNAME", Content: "@"},
			Record{Name: "@", Type: "CAA", Content: `0 issue "letsencrypt.org"`},
			Record{Name: "@", Type: "CAA", Content: `0 issuewild "letsencrypt.org"`})
		return r
	}
	dmarc := Record{Name: "_dmarc", Type: "TXT", Content: "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + zone + "; adkim=r; aspf=r"}
	var recs []Record
	switch id {
	case "website":
		if ipv4 == "" && ipv6 == "" {
			return nil, fmt.Errorf("this server's public IP address isn't known; enter it")
		}
		recs = web()
	case "website-email":
		if ipv4 == "" && ipv6 == "" {
			return nil, fmt.Errorf("this server's public IP address isn't known; enter it")
		}
		recs = web()
		spf := "v=spf1 a mx"
		if ipv4 != "" {
			recs = append(recs, Record{Name: "mail", Type: "A", Content: ipv4})
			spf += " ip4:" + ipv4
		}
		if ipv6 != "" {
			recs = append(recs, Record{Name: "mail", Type: "AAAA", Content: ipv6})
			spf += " ip6:" + ipv6
		}
		recs = append(recs,
			Record{Name: "@", Type: "MX", Content: "mail", Priority: 10},
			Record{Name: "@", Type: "TXT", Content: spf + " ~all"},
			dmarc,
			Record{Name: "autoconfig", Type: "CNAME", Content: "mail"},
			Record{Name: "autodiscover", Type: "CNAME", Content: "mail"},
			Record{Name: "_submissions._tcp", Type: "SRV", Priority: 0, Weight: 1, Port: 465, Content: "mail"},
			Record{Name: "_submission._tcp", Type: "SRV", Priority: 0, Weight: 1, Port: 587, Content: "mail"},
			Record{Name: "_imaps._tcp", Type: "SRV", Priority: 0, Weight: 1, Port: 993, Content: "mail"},
			Record{Name: "_pop3s._tcp", Type: "SRV", Priority: 10, Weight: 1, Port: 995, Content: "mail"},
		)
		if DKIMRecords != nil {
			recs = append(recs, DKIMRecords(zone)...)
		}
	case "google":
		recs = []Record{
			{Name: "@", Type: "MX", Content: "smtp.google.com.", Priority: 1},
			{Name: "@", Type: "TXT", Content: "v=spf1 include:_spf.google.com ~all"},
			dmarc,
		}
	case "microsoft":
		host := strings.ReplaceAll(zone, ".", "-") + ".mail.protection.outlook.com."
		recs = []Record{
			{Name: "@", Type: "MX", Content: host, Priority: 0},
			{Name: "@", Type: "TXT", Content: "v=spf1 include:spf.protection.outlook.com -all"},
			{Name: "autodiscover", Type: "CNAME", Content: "autodiscover.outlook.com."},
			{Name: "_sip._tls", Type: "SRV", Priority: 100, Weight: 1, Port: 443, Content: "sipdir.online.lync.com."},
			{Name: "_sipfederationtls._tcp", Type: "SRV", Priority: 100, Weight: 1, Port: 5061, Content: "sipfed.online.lync.com."},
			{Name: "sip", Type: "CNAME", Content: "sipdir.online.lync.com."},
			{Name: "lyncdiscover", Type: "CNAME", Content: "webdir.online.lync.com."},
			dmarc,
		}
	case "zoho":
		recs = []Record{
			{Name: "@", Type: "MX", Content: "mx.zoho.com.", Priority: 10},
			{Name: "@", Type: "MX", Content: "mx2.zoho.com.", Priority: 20},
			{Name: "@", Type: "MX", Content: "mx3.zoho.com.", Priority: 50},
			{Name: "@", Type: "TXT", Content: "v=spf1 include:zoho.com ~all"},
			dmarc,
		}
	default:
		return nil, fmt.Errorf("unknown template %q", id)
	}
	for i := range recs {
		if err := recs[i].Validate(); err != nil {
			return nil, err
		}
	}
	return recs, nil
}

// mergeTemplate adds template records to existing ones. Mail templates first
// remove existing MX, SPF, DMARC and mail discovery records so mail isn't split
// between two providers; website templates replace the apex A/AAAA and www.
func mergeTemplate(existing, add []Record, id string) []Record {
	mail := false
	for _, t := range Templates {
		if t.ID == id {
			mail = t.Mail
		}
	}
	out := []Record{}
	for _, r := range existing {
		drop := false
		if mail {
			switch {
			case r.Type == "MX" && r.Name == "@":
				drop = true
			case r.Type == "TXT" && r.Name == "@" && strings.HasPrefix(r.Content, "v=spf1"):
				drop = true
			case r.Name == "_dmarc" || r.Name == "autodiscover" || r.Name == "autoconfig":
				drop = true
			}
		}
		for _, a := range add {
			if (a.Type == "A" || a.Type == "AAAA" || a.Type == "CNAME") && a.Name == r.Name &&
				(r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME") {
				drop = true
			}
		}
		if !drop {
			out = append(out, r)
		}
	}
	for _, a := range add {
		dup := false
		for _, o := range out {
			if Same(o, a) {
				dup = true
			}
		}
		if !dup {
			if a.ID == "" {
				a.ID = randID()
			}
			out = append(out, a)
		}
	}
	return out
}
