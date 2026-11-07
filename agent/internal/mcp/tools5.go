package mcp

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
)

// kitPassword makes up a 16-character password.
func kitPassword() string {
	const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

// rawText is a tool result that is shown as text, not JSON (zone files).
type rawText string

func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func strList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}
func enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}
func object(desc string) map[string]any { return map[string]any{"type": "object", "description": desc} }

// query builds "?k=v&..." from the named non-empty string arguments.
func query(a map[string]any, keys ...string) string {
	v := url.Values{}
	for _, k := range keys {
		if s := argStr(a, k); s != "" {
			v.Set(k, s)
		}
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// confirmed checks that a destructive tool's confirm argument repeats the name.
func confirmed(a map[string]any, confirmKey, want string) error {
	if want == "" || argStr(a, confirmKey) != want {
		return fmt.Errorf("%s must exactly match %q; nothing was changed", confirmKey, want)
	}
	return nil
}

func (s *Server) tools5() []tool {
	acct := func(a map[string]any) string { return "/v1/accounts/" + esc(argStr(a, "account")) }
	zone := func(a map[string]any) string { return acct(a) + "/dns/zones/" + esc(argStr(a, "zone")) }
	maild := func(a map[string]any) string { return acct(a) + "/mail/domains/" + esc(argStr(a, "domain")) }
	db := func(a map[string]any) string {
		return acct(a) + "/dbs/" + esc(argStr(a, "engine")) + "/" + esc(argStr(a, "database"))
	}
	// plans: the server's plans, or one account's when account is given.
	plans := func(a map[string]any) string {
		if argStr(a, "account") != "" {
			return acct(a) + "/backups/plans"
		}
		return "/v1/backups/plans"
	}
	plan := func(a map[string]any) string { return plans(a) + "/" + esc(argStr(a, "plan")) }
	orAll := func(all, per string) func(a map[string]any) string {
		return func(a map[string]any) string {
			if argStr(a, "account") != "" {
				return acct(a) + per
			}
			return all
		}
	}
	none := obj(map[string]any{})
	argZone := str("DNS zone (domain), e.g. example.com")
	argMailDomain := str("Email domain, e.g. example.com")
	argEngine := enum("Database engine", "mariadb", "postgres", "mongodb")
	argOptAccount := str("Hosting account (optional; leave out for the whole server, admin only)")
	recordProps := func(extra map[string]any) map[string]any {
		p := map[string]any{"account": argAccount, "zone": argZone,
			"name":     str("Record name relative to the zone: @ for the domain itself, www, mail, _dmarc ..."),
			"type":     enum("Record type", "A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA", "PTR", "TLSA", "SSHFP", "DS", "NAPTR"),
			"content":  str("Value: an address, a host name, or the text"),
			"ttl":      integer("Seconds resolvers may cache it (default 3600)"),
			"priority": integer("MX and SRV priority (lower is tried first)"),
			"weight":   integer("SRV weight"), "port": integer("SRV port"),
			"proxied": boolean("Cloudflare zones only: send traffic through Cloudflare"),
			"comment": str("Note (optional)")}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	recordBody := pick("name", "type", "content", "ttl", "priority", "weight", "port", "proxied", "comment")

	// mailboxMerge reads the mailbox, applies the given fields and writes it
	// back, so a change to one field keeps the others.
	mailboxMerge := func(fields ...string) func(context.Context, map[string]any) (any, error) {
		return func(ctx context.Context, a map[string]any) (any, error) {
			var d struct {
				Mailboxes []map[string]any `json:"mailboxes"`
			}
			if err := s.Agent.Get(ctx, maild(a), &d); err != nil {
				return nil, err
			}
			var box map[string]any
			for _, m := range d.Mailboxes {
				if m["local"] == argStr(a, "local") {
					box = m
				}
			}
			if box == nil {
				return nil, fmt.Errorf("%s@%s doesn't exist", argStr(a, "local"), argStr(a, "domain"))
			}
			for _, k := range []string{"local", "created_at", "usage"} {
				delete(box, k)
			}
			if len(fields) == 1 && fields[0] == "auto_reply" {
				ar := map[string]any{}
				for _, k := range []string{"enabled", "subject", "body", "days", "until"} {
					if v, ok := a[k]; ok {
						ar[k] = v
					}
				}
				box["auto_reply"] = ar
			} else {
				for _, k := range fields {
					if v, ok := a[k]; ok {
						box[k] = v
					}
				}
			}
			var out any
			return out, s.Agent.Do(ctx, "PUT", maild(a)+"/mailboxes/"+esc(argStr(a, "local")), box, &out)
		}
	}
	fileOp := func(op string, keys ...string) func(context.Context, map[string]any) (any, error) {
		return s.call("POST", func(a map[string]any) string { return acct(a) + "/files/" + op }, pick(keys...))
	}

	return []tool{
		// ------------------------------------------------- website SFTP users
		{Name: "list_sftp_users", Title: "Website SFTP users", area: "files", Annotations: readOnly(),
			Description: "An account's website SFTP users: each signs in as <account>_<name> and only reaches one website's folder (public_html by default), read-write or download only, with a password and/or keys, optionally until a date.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/sftp-users" })},
		{Name: "create_sftp_user", Title: "Add a website SFTP user", area: "files", write: true, Annotations: write(false, false),
			Description: "Give someone SFTP access to just one website. folder is inside the website folder: public_html (default), \"\" for the whole website (includes settings files like .env), or e.g. public_html/wp-content/themes. With no password and no key, a password is made up and returned once; give it to the human with the server name and port 22.",
			InputSchema: obj(map[string]any{"account": argAccount, "name": str("Short name, 1-12 lowercase letters/digits (they sign in as <account>_<name>)"),
				"domain": argDomain, "folder": str("Folder inside the website (optional)"), "read_only": boolean("Download only"),
				"password": str("10+ characters (optional)"), "public_key": str("Their SSH public key (optional)"), "expires": str("Last day it works, YYYY-MM-DD (optional)")},
				"account", "name", "domain"),
			handler: s.call("POST", func(a map[string]any) string { return acct(a) + "/sftp-users" }, pick("name", "domain", "folder", "read_only", "password", "public_key", "expires"))},
		{Name: "update_sftp_user", Title: "Change a website SFTP user", area: "files", write: true, Annotations: write(false, true),
			Description: "Change a website SFTP user: password (\"generate\" makes a new one, returned once; \"\" removes it when they have a key), add a public_key, read_only, folder or expires.",
			InputSchema: obj(map[string]any{"account": argAccount, "user": str("Full user name, e.g. shop_designer"), "password": str("New password, \"generate\" or \"\""),
				"public_key": str("Add this key"), "read_only": boolean("Download only"), "folder": str("Folder inside the website"), "expires": str("YYYY-MM-DD, or \"\" for never")},
				"account", "user"),
			handler: s.call("PATCH", func(a map[string]any) string { return acct(a) + "/sftp-users/" + esc(argStr(a, "user")) }, pick("password", "public_key", "read_only", "folder", "expires"))},
		{Name: "delete_sftp_user", Title: "Remove a website SFTP user", area: "files", write: true, Annotations: write(true, true),
			Description: "Remove a website SFTP user. The website's files stay. Only when the human asked.",
			InputSchema: obj(map[string]any{"account": argAccount, "user": str("Full user name")}, "account", "user"),
			handler:     s.call("DELETE", func(a map[string]any) string { return acct(a) + "/sftp-users/" + esc(argStr(a, "user")) }, nil)},

		// ------------------------------------------------- password protection
		{Name: "get_site_protection", Title: "Password-protected paths", area: "sites", Annotations: readOnly(),
			Description: "A website's password-protected paths (who can open each, which addresses skip the password) and its sign-in users. Passwords are never shown.",
			InputSchema: obj(map[string]any{"domain": argDomain}, "domain"),
			handler:     s.get(func(a map[string]any) string { return "/v1/sites/" + esc(argStr(a, "domain")) + "/protection" })},
		{Name: "set_protection_user", Title: "Add a sign-in user", area: "sites", write: true, Annotations: write(false, true),
			Description: "Add a user who can open a website's protected paths, or give them a new password. Leave out the password to have one made up; it's returned once, so give it to the human.",
			InputSchema: obj(map[string]any{"domain": argDomain, "user": str("User name: letters, digits, . _ @ -"), "password": str("8+ characters (optional)")}, "domain", "user"),
			handler: s.call("PUT", func(a map[string]any) string {
				return "/v1/sites/" + esc(argStr(a, "domain")) + "/protection/users/" + esc(argStr(a, "user"))
			}, pick("password"))},
		{Name: "set_protected_paths", Title: "Password-protect paths", area: "sites", write: true, Annotations: write(false, true),
			Description: "Replace a website's protected paths. Each is {\"path\": \"/admin/\", \"realm\": \"Staff only\", \"users\": [\"ada\"] (empty: every user), \"allow_ips\": [\"203.0.113.0/24\"]}. \"/\" protects the whole website. Read get_site_protection first and keep the paths you aren't changing. Add users first.",
			InputSchema: obj(map[string]any{"domain": argDomain,
				"paths": map[string]any{"type": "array", "description": "Every protected path of the website", "items": map[string]any{"type": "object",
					"properties": map[string]any{"path": map[string]any{"type": "string"}, "realm": map[string]any{"type": "string"},
						"users": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "allow_ips": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}}},
				"domain", "paths"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				var out any
				return out, s.Agent.Do(ctx, "PUT", "/v1/sites/"+esc(argStr(a, "domain"))+"/protection/paths", a["paths"], &out)
			}},
		{Name: "delete_protection_user", Title: "Remove a sign-in user", area: "sites", write: true, Annotations: write(true, true),
			Description: "Remove a website sign-in user. Refused if they are the only user allowed on a path (that path would open to everyone).",
			InputSchema: obj(map[string]any{"domain": argDomain, "user": str("User name")}, "domain", "user"),
			handler: s.call("DELETE", func(a map[string]any) string {
				return "/v1/sites/" + esc(argStr(a, "domain")) + "/protection/users/" + esc(argStr(a, "user"))
			}, nil)},

		// ------------------------------------------------------------------ DNS
		{Name: "list_dns_zones", Title: "DNS zones", area: "dns", Annotations: readOnly(),
			Description: "DNS zones with where they're served (this server, Cloudflare, DigitalOcean), DNSSEC and domain expiry. One account's, or every zone (admin).",
			InputSchema: obj(map[string]any{"account": argOptAccount}),
			handler:     s.get(orAll("/v1/dns/zones", "/dns/zones"))},
		{Name: "get_dns_zone", Title: "DNS zone and records", area: "dns", Annotations: readOnly(),
			Description: "One zone with every record (each has an id for update_dns_record and delete_dns_record), name servers and DNSSEC DS records.",
			InputSchema: obj(map[string]any{"account": argAccount, "zone": argZone}, "account", "zone"),
			handler:     s.get(zone)},
		{Name: "check_dns", Title: "Check DNS propagation", area: "dns", Annotations: readOnly(),
			Description: "Ask Cloudflare, Google, Quad9 and OpenDNS for each record and compare with the zone; also whether the registrar points at the right name servers. Explains what's wrong in plain words.",
			InputSchema: obj(map[string]any{"account": argAccount, "zone": argZone}, "account", "zone"),
			handler:     s.get(func(a map[string]any) string { return zone(a) + "/check" })},
		{Name: "export_dns_zone", Title: "Export zone file", area: "dns", Annotations: readOnly(),
			Description: "The zone as a BIND zone file.",
			InputSchema: obj(map[string]any{"account": argAccount, "zone": argZone}, "account", "zone"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				b, err := s.Agent.Raw(ctx, "GET", zone(a)+"/export", nil)
				return rawText(b), err
			}},
		{Name: "get_reverse_dns", Title: "Reverse DNS", area: "dns", Annotations: readOnly(), InputSchema: none,
			Description: "Whether this server's IP addresses have reverse DNS (PTR) that matches its host name, which mail delivery needs, with what to ask the VPS provider for.",
			handler:     s.get(fixed("/v1/dns/reverse"))},
		{Name: "check_domain_availability", Title: "Check domain availability", area: "dns", Annotations: readOnly(),
			Description: "Ask the connected registrar whether domains can be registered, and their price.",
			InputSchema: obj(map[string]any{"domains": strList("Domains to check, e.g. [\"example.ng\", \"example.com\"]")}, "domains"),
			handler:     s.call("POST", fixed("/v1/dns/registrar/check"), pick("domains"))},
		{Name: "create_dns_zone", Title: "Add DNS zone", area: "dns", write: true, Annotations: write(false, false),
			Description: "Add a zone for a domain. A template fills in the records: website (A, AAAA, www, CAA), website-email (also MX, SPF, DMARC, mail discovery), google, microsoft, zoho or empty.",
			InputSchema: obj(map[string]any{"account": argAccount, "name": argZone,
				"template": enum("Starting records", "website", "website-email", "google", "microsoft", "zoho", "empty"),
				"backend":  enum("Where the zone is served (default: the server's setting)", "local", "cloudflare", "digitalocean")}, "account", "name"),
			handler: s.call("POST", func(a map[string]any) string { return acct(a) + "/dns/zones" }, pick("name", "template", "backend"))},
		{Name: "add_dns_record", Title: "Add DNS record", area: "dns", write: true, Annotations: write(false, false),
			Description: "Add one record. The zone's serial goes up and secondaries are notified.",
			InputSchema: obj(recordProps(nil), "account", "zone", "name", "type", "content"),
			handler:     s.call("POST", func(a map[string]any) string { return zone(a) + "/records" }, recordBody)},
		{Name: "update_dns_record", Title: "Change DNS record", area: "dns", write: true, Annotations: write(false, true),
			Description: "Replace one record (by id from get_dns_zone) with the given fields.",
			InputSchema: obj(recordProps(map[string]any{"id": str("Record id from get_dns_zone")}), "account", "zone", "id", "name", "type", "content"),
			handler:     s.call("PUT", func(a map[string]any) string { return zone(a) + "/records/" + esc(argStr(a, "id")) }, recordBody)},
		{Name: "delete_dns_record", Title: "Delete DNS record", area: "dns", write: true, Annotations: write(true, true),
			Description: "Delete one record by id. Only when the human asked.",
			InputSchema: obj(map[string]any{"account": argAccount, "zone": argZone, "id": str("Record id from get_dns_zone")}, "account", "zone", "id"),
			handler:     s.call("DELETE", func(a map[string]any) string { return zone(a) + "/records/" + esc(argStr(a, "id")) }, nil)},
		{Name: "apply_dns_template", Title: "Apply DNS template", area: "dns", write: true, Annotations: write(false, true),
			Description: "Add a template's records to a zone. Mail templates replace the old MX, SPF and DMARC records.",
			InputSchema: obj(map[string]any{"account": argAccount, "zone": argZone,
				"template": enum("Template", "website", "website-email", "google", "microsoft", "zoho")}, "account", "zone", "template"),
			handler: s.call("POST", func(a map[string]any) string { return zone(a) + "/template" }, pick("template"))},
		{Name: "import_dns_zone", Title: "Import zone file", area: "dns", write: true, Annotations: write(false, false),
			Description: "Add the records in a BIND zone file (for example exported from the old DNS host). replace=true removes the zone's other records first.",
			InputSchema: obj(map[string]any{"account": argAccount, "zone": argZone, "zone_file": str("BIND zone file text"),
				"replace": boolean("Remove the existing records first")}, "account", "zone", "zone_file"),
			handler: s.call("POST", func(a map[string]any) string { return zone(a) + "/import" }, pick("zone_file", "replace"))},
		{Name: "delete_dns_zone", Title: "Delete DNS zone", area: "dns", write: true, Annotations: write(true, true),
			Description: "Delete a zone and all its records; the domain stops resolving if this server is its DNS. Only when the human explicitly asked.",
			InputSchema: obj(map[string]any{"account": argAccount, "zone": argZone, "confirm_zone": str("Type the zone again to confirm")}, "account", "zone", "confirm_zone"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				if err := confirmed(a, "confirm_zone", argStr(a, "zone")); err != nil {
					return nil, err
				}
				var out any
				return out, s.Agent.Do(ctx, "DELETE", zone(a), nil, &out)
			}},
		{Name: "register_domain", Title: "Register domain", area: "dns", write: true, Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": true},
			Description: "Buy a domain through the connected registrar (this charges the registrar account). Check it with check_domain_availability first, and only when the human explicitly asked to buy it.",
			InputSchema: obj(map[string]any{"domain": str("Domain to register"), "confirm_domain": str("Type the domain again to confirm the purchase"),
				"years": integer("Years (default 1)"), "account": str("Also create a DNS zone for it in this account (optional)")}, "domain", "confirm_domain"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				if err := confirmed(a, "confirm_domain", argStr(a, "domain")); err != nil {
					return nil, err
				}
				var out any
				return out, s.Agent.Do(ctx, "POST", "/v1/dns/registrar/register", pick("domain", "years", "account")(a), &out)
			}},

		// ----------------------------------------------------------------- mail
		{Name: "list_mail_domains", Title: "Email domains", area: "mail", Annotations: readOnly(),
			Description: "Domains with email, their mailboxes (with storage used), forwarders, DKIM and TLS state. One account's, or all (admin).",
			InputSchema: obj(map[string]any{"account": argOptAccount}),
			handler:     s.get(orAll("/v1/mail/domains", "/mail/domains"))},
		{Name: "get_mail_domain", Title: "Email domain", area: "mail", Annotations: readOnly(),
			Description: "One email domain with its mailboxes, forwarders and catch-all.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain}, "account", "domain"),
			handler:     s.get(maild)},
		{Name: "check_mail_deliverability", Title: "Check email deliverability", area: "mail", Annotations: readOnly(),
			Description: "Check MX, SPF, DKIM, DMARC, reverse DNS, outgoing port 25, blocklists and the mail certificate, each with a plain-words fix. Use it when mail goes to spam or doesn't arrive.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain}, "account", "domain"),
			handler:     s.get(func(a map[string]any) string { return maild(a) + "/checks" })},
		{Name: "get_mail_dns_records", Title: "DNS records for email", area: "mail", Annotations: readOnly(),
			Description: "The MX, A, SPF, DKIM and DMARC records an email domain needs, to set at another DNS host.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain}, "account", "domain"),
			handler:     s.get(func(a map[string]any) string { return maild(a) + "/records" })},
		{Name: "get_mail_queue", Title: "Mail queue", area: "mail", Annotations: readOnly(),
			Description: "Messages waiting to be sent, with why each is delayed. One account's, or the whole server's.",
			InputSchema: obj(map[string]any{"account": argOptAccount}),
			handler:     s.get(orAll("/v1/mail/queue", "/mail/queue"))},
		{Name: "search_mail_log", Title: "Mail delivery log", area: "mail", Annotations: readOnly(),
			Description: "Recent deliveries (sent, deferred, bounced, rejected) with the receiving server's reply. Search by address or queue id.",
			InputSchema: obj(map[string]any{"account": argOptAccount, "search": str("Address, domain or queue id (optional)")}),
			handler: s.get(func(a map[string]any) string {
				return orAll("/v1/mail/log", "/mail/log")(a) + query(a, "search")
			})},
		{Name: "get_mail_settings", Title: "Mail server settings", area: "mail", Annotations: readOnly(), InputSchema: none,
			Description: "Outgoing relay, sending limits, spam thresholds and antivirus (admin).",
			handler:     s.get(fixed("/v1/mail"))},
		{Name: "enable_mail_domain", Title: "Turn on email for a domain", area: "mail", write: true, Annotations: write(false, false),
			Description: "Turn on email for a domain in an account (creates DKIM keys). Then add the DNS records with add_mail_dns_records or get_mail_dns_records.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain}, "account", "domain"),
			handler:     s.call("POST", func(a map[string]any) string { return acct(a) + "/mail/domains" }, pick("domain"))},
		{Name: "add_mail_dns_records", Title: "Add email DNS records", area: "mail", write: true, Annotations: write(false, true),
			Description: "Add the records email needs to the domain's DNS zone on this server.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain}, "account", "domain"),
			handler:     s.call("POST", func(a map[string]any) string { return maild(a) + "/dns" }, nil)},
		{Name: "issue_mail_certificate", Title: "Certificate for mail", area: "mail", write: true, Annotations: write(false, true),
			Description: "Get a Let's Encrypt certificate for mail.<domain> so mail apps connect without warnings. Returns a job id.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain}, "account", "domain"),
			handler:     s.call("POST", func(a map[string]any) string { return maild(a) + "/tls" }, nil)},
		{Name: "create_mailbox", Title: "Create mailbox", area: "mail", write: true, Annotations: write(false, false),
			Description: "Create local@domain. Leave out the password to generate one; it's shown once, so give it to the human with the mail settings returned.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain, "local": str("Part before @, e.g. info"),
				"name": str("Display name (optional)"), "password": str("10+ characters (optional)"),
				"quota_mb": integer("Storage in MB (default: the server's setting)"), "send_only": boolean("Only sends (for websites); receives nothing")},
				"account", "domain", "local"),
			handler: s.call("POST", func(a map[string]any) string { return maild(a) + "/mailboxes" }, pick("local", "name", "password", "quota_mb", "send_only"))},
		{Name: "update_mailbox", Title: "Change mailbox", area: "mail", write: true, Annotations: write(false, true),
			Description: "Change a mailbox's name, password, storage, forwarding or whether it's on. Fields left out stay as they are.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain, "local": str("Part before @"),
				"name": str("Display name"), "password": str("New password, 10+ characters"), "quota_mb": integer("Storage in MB"),
				"enabled": boolean("Mailbox is on"), "forward": strList("Also forward mail to these addresses"), "keep_copy": boolean("Keep a copy when forwarding")},
				"account", "domain", "local"),
			handler: mailboxMerge("name", "password", "quota_mb", "enabled", "forward", "keep_copy")},
		{Name: "set_auto_reply", Title: "Auto-reply", area: "mail", write: true, Annotations: write(false, true),
			Description: "Turn a mailbox's out-of-office reply on or off.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain, "local": str("Part before @"),
				"enabled": boolean("Reply automatically"), "subject": str("Reply subject"), "body": str("Reply text"),
				"days": integer("Reply to the same sender at most once every this many days (default 1)"), "until": str("Stop on this date, YYYY-MM-DD (optional)")},
				"account", "domain", "local", "enabled"),
			handler: mailboxMerge("auto_reply")},
		{Name: "set_mail_forwarders", Title: "Set forwarders", area: "mail", write: true, Annotations: write(false, true),
			Description: "Replace a domain's forwarders. Each is {\"from\": \"sales\", \"to\": [\"ada@gmail.com\"]}; from is the part before @.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain,
				"aliases": map[string]any{"type": "array", "description": "Every forwarder for the domain", "items": map[string]any{"type": "object",
					"properties": map[string]any{"from": map[string]any{"type": "string"}, "to": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}}},
				"account", "domain", "aliases"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				var out any
				return out, s.Agent.Do(ctx, "PUT", maild(a)+"/aliases", a["aliases"], &out)
			}},
		{Name: "mail_queue_action", Title: "Retry or remove queued mail", area: "mail", write: true, Annotations: write(true, false),
			Description: "Retry (flush) the queue, or hold, release or delete one message by id from get_mail_queue.",
			InputSchema: obj(map[string]any{"account": argOptAccount, "action": enum("What to do", "flush", "retry", "hold", "release", "delete"),
				"id": str("Queue id (leave out to apply to every message)")}, "action"),
			handler: s.call("POST", func(a map[string]any) string {
				return orAll("/v1/mail/queue", "/mail/queue")(a) + "/" + esc(argStr(a, "action")) + query(a, "id")
			}, nil)},
		{Name: "delete_mailbox", Title: "Delete mailbox", area: "mail", write: true, Annotations: write(true, true),
			Description: "Delete a mailbox. delete_mail=true also deletes its messages; otherwise they're kept on disk. Only when the human explicitly asked.",
			InputSchema: obj(map[string]any{"account": argAccount, "domain": argMailDomain, "local": str("Part before @"),
				"confirm_address": str("Type the full address again to confirm"), "delete_mail": boolean("Also delete the messages")},
				"account", "domain", "local", "confirm_address"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				if err := confirmed(a, "confirm_address", argStr(a, "local")+"@"+argStr(a, "domain")); err != nil {
					return nil, err
				}
				p := maild(a) + "/mailboxes/" + esc(argStr(a, "local"))
				if b, _ := a["delete_mail"].(bool); b {
					p += "?delete_mail=true"
				}
				var out any
				return out, s.Agent.Do(ctx, "DELETE", p, nil, &out)
			}},

		// ------------------------------------------------------------- security
		{Name: "list_malware_scans", Title: "Malware scans", area: "security", Annotations: readOnly(),
			Description: "An account's malware scans with each finding (path, signature, severity, whether quarantined).",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/malware/scans" })},
		{Name: "get_malware_overview", Title: "Malware overview", area: "security", Annotations: readOnly(), InputSchema: none,
			Description: "Scan schedule, every account's latest report and the quarantine (admin).",
			handler:     s.get(fixed("/v1/security/malware"))},
		{Name: "list_quarantine", Title: "Quarantined files", area: "security", Annotations: readOnly(),
			Description: "Files moved out of an account's websites by the malware scanner.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/quarantine" })},
		{Name: "get_waf_events", Title: "Web firewall events", area: "security", Annotations: readOnly(),
			Description: "Requests the web application firewall (ModSecurity, OWASP rules) flagged or blocked for a website, with the rule id.",
			InputSchema: obj(map[string]any{"domain": argDomain}, "domain"),
			handler:     s.get(func(a map[string]any) string { return "/v1/sites/" + esc(argStr(a, "domain")) + "/waf/events" })},
		{Name: "get_secrets_status", Title: "Secret key status", area: "security", Annotations: readOnly(), InputSchema: none,
			Description: "The key that encrypts stored passwords and API keys, and when it was last rotated (admin).",
			handler:     s.get(fixed("/v1/secrets"))},
		{Name: "scan_for_malware", Title: "Scan for malware", area: "security", write: true, Annotations: write(false, false),
			Description: "Scan an account (or one folder in it) with ClamAV and PHP backdoor checks. Returns a job id; then list_malware_scans.",
			InputSchema: obj(map[string]any{"account": argAccount, "path": str("Folder inside the account, e.g. web/example.com (optional)")}, "account"),
			handler:     s.call("POST", func(a map[string]any) string { return acct(a) + "/malware/scans" }, pick("path"))},
		{Name: "quarantine_findings", Title: "Quarantine malware", area: "security", write: true, Annotations: write(false, true),
			Description: "Move a scan's findings out of the website into quarantine, where they can't run. Leave out findings for all of them.",
			InputSchema: obj(map[string]any{"account": argAccount, "scan": str("Scan id"), "findings": strList("Finding ids (optional)")}, "account", "scan"),
			handler: s.call("POST", func(a map[string]any) string {
				return acct(a) + "/malware/scans/" + esc(argStr(a, "scan")) + "/quarantine"
			}, pick("findings"))},
		{Name: "restore_quarantined_file", Title: "Restore quarantined file", area: "security", write: true, Annotations: write(false, true),
			Description: "Put a quarantined file back where it was (for a false positive). Only when the human asked.",
			InputSchema: obj(map[string]any{"account": argAccount, "id": str("Quarantine id")}, "account", "id"),
			handler:     s.call("POST", func(a map[string]any) string { return acct(a) + "/quarantine/" + esc(argStr(a, "id")) + "/restore" }, nil)},
		{Name: "set_waf", Title: "Web firewall settings", area: "security", write: true, Annotations: write(false, true),
			Description: "Set a website's web application firewall: off, detect (log only) or block. profile tunes rules for an app; disabled_rules turns off rule ids that block legitimate requests.",
			InputSchema: obj(map[string]any{"domain": argDomain, "mode": enum("Firewall mode", "off", "detect", "block"),
				"profile":        enum("App profile (optional)", "wordpress", "drupal", "nextcloud", "dokuwiki", "cpanel", "xenforo", "phpbb", "phpmyadmin"),
				"disabled_rules": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Rule ids to turn off, e.g. [942100]"}},
				"domain", "mode"),
			handler: s.call("PUT", func(a map[string]any) string { return "/v1/sites/" + esc(argStr(a, "domain")) + "/waf" }, pick("mode", "profile", "disabled_rules"))},

		// ------------------------------------------------------------ databases
		{Name: "list_all_databases", Title: "Databases (every engine)", area: "databases", Annotations: readOnly(),
			Description: "An account's MariaDB, PostgreSQL and MongoDB databases with users and sizes, its Redis, and which engines the server has.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/dbs" })},
		{Name: "diagnose_database", Title: "Diagnose database connection", area: "databases", Annotations: readOnly(),
			Description: "Work out why an app can't connect: database exists, user exists and has access, password right, host. Each check says how to fix it.",
			InputSchema: obj(map[string]any{"account": argAccount, "engine": argEngine, "database": str("Database name"),
				"user": str("User the app connects as"), "password": str("Password the app uses (optional; checked, never stored)")}, "account", "engine", "database", "user"),
			handler: s.call("POST", func(a map[string]any) string { return db(a) + "/diagnose" }, pick("user", "password"))},
		{Name: "list_db_processes", Title: "Running queries", area: "databases", Annotations: readOnly(),
			Description: "Queries an account's MariaDB users are running now, with how long each has run.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/dbs/processes" })},
		{Name: "list_slow_queries", Title: "Slow queries", area: "databases", Annotations: readOnly(),
			Description: "An account's slowest recent MariaDB queries with rows examined, to suggest indexes.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/dbs/slow-queries" })},
		{Name: "get_redis", Title: "Redis status", area: "databases", Annotations: readOnly(),
			Description: "An account's private Redis (Valkey): memory, keys, hit rate, clients.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/redis" })},
		{Name: "get_replication", Title: "Database replication", area: "databases", Annotations: readOnly(), InputSchema: none,
			Description: "Whether this server's MariaDB replicates from or to another server, and how far behind it is (admin).",
			handler:     s.get(fixed("/v1/databases/replication"))},
		{Name: "create_engine_database", Title: "Create PostgreSQL or MongoDB database", area: "databases", write: true, Annotations: write(false, false),
			Description: "Create a PostgreSQL or MongoDB database and owner named <account>_<name> (for MariaDB use create_database). The connection URL and password are shown once; give them to the human.",
			InputSchema: obj(map[string]any{"account": argAccount, "engine": enum("Engine", "postgres", "mongodb"),
				"name": str("1-16 lowercase letters/digits"), "note": str("Short note (optional)")}, "account", "engine", "name"),
			handler: s.call("POST", func(a map[string]any) string { return acct(a) + "/dbs" }, pick("engine", "name", "note"))},
		{Name: "reset_database_password", Title: "New database password", area: "databases", write: true, Annotations: write(false, false),
			Description: "Give a database user a new password (shown once). The app's config must be updated with it.",
			InputSchema: obj(map[string]any{"account": argAccount, "engine": argEngine, "database": str("Database name"),
				"role": str("User to reset (default: the owner)")}, "account", "engine", "database"),
			handler: s.call("POST", func(a map[string]any) string { return db(a) + "/password" }, pick("role"))},
		{Name: "add_database_user", Title: "Add database user", area: "databases", write: true, Annotations: write(false, false),
			Description: "Add a second user with read or write access to a PostgreSQL or MongoDB database (for reports, or a second app). The password is shown once.",
			InputSchema: obj(map[string]any{"account": argAccount, "engine": enum("Engine", "postgres", "mongodb"), "database": str("Database name"),
				"name": str("User name after <account>_"), "access": enum("Access", "read", "write")}, "account", "engine", "database", "name", "access"),
			handler: s.call("POST", func(a map[string]any) string { return db(a) + "/roles" }, pick("name", "access"))},
		{Name: "set_redis", Title: "Set up a cache", area: "databases", write: true, Annotations: write(false, true),
			Description: "Create or change an account's private cache (Redis, Valkey or Dragonfly, whichever the server has; see list_engine_databases → engines.caches) on a unix socket. The first time, the password is shown once. Changing engine starts an empty instance (same password and socket); Dragonfly needs at least 256 MB.",
			InputSchema: obj(map[string]any{"account": argAccount, "memory_mb": integer("Memory limit in MB, e.g. 128"),
				"policy":  enum("What to do when full", "allkeys-lru", "volatile-lru", "allkeys-lfu", "noeviction"),
				"persist": boolean("Save to disk so data survives a restart"),
				"engine":  enum("Engine (default: the current one, or the server's first)", "redis", "valkey", "dragonfly")}, "account", "memory_mb"),
			handler: s.call("PUT", func(a map[string]any) string { return acct(a) + "/redis" }, pick("memory_mb", "policy", "persist", "engine"))},
		{Name: "kill_db_query", Title: "Stop a query", area: "databases", write: true, Annotations: write(true, true),
			Description: "Stop one running MariaDB query by id from list_db_processes.",
			InputSchema: obj(map[string]any{"account": argAccount, "id": str("Process id")}, "account", "id"),
			handler:     s.call("POST", func(a map[string]any) string { return acct(a) + "/dbs/processes/" + esc(argStr(a, "id")) + "/kill" }, nil)},
		{Name: "delete_engine_database", Title: "Delete database", area: "databases", write: true, Annotations: write(true, true),
			Description: "Delete a database of any engine with all its data. Only when the human explicitly asked; suggest a backup first.",
			InputSchema: obj(map[string]any{"account": argAccount, "engine": argEngine, "database": str("Database name"),
				"confirm_database": str("Type the database name again to confirm")}, "account", "engine", "database", "confirm_database"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				if err := confirmed(a, "confirm_database", argStr(a, "database")); err != nil {
					return nil, err
				}
				var out any
				return out, s.Agent.Do(ctx, "DELETE", db(a), nil, &out)
			}},

		// ---------------------------------------------------------------- files
		{Name: "list_files", Title: "List files", area: "files", Annotations: readOnly(),
			Description: "A folder in an account's home: names, sizes, modes and times. Paths are relative to the home folder, e.g. web/example.com/public_html.",
			InputSchema: obj(map[string]any{"account": argAccount, "path": str("Folder (default: the home folder)")}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/files" + query(a, "path") })},
		{Name: "read_file", Title: "Read file", area: "files", Annotations: readOnly(),
			Description: "A text file's contents (up to 2 MB), such as .htaccess, wp-config.php or .env.",
			InputSchema: obj(map[string]any{"account": argAccount, "path": str("File path")}, "account", "path"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/files/content" + query(a, "path") })},
		{Name: "search_files", Title: "Search files", area: "files", Annotations: readOnly(),
			Description: "Find files by name, or by text inside them.",
			InputSchema: obj(map[string]any{"account": argAccount, "path": str("Folder to search (optional)"),
				"name": str("Part of the file name"), "content": str("Text inside the file")}, "account"),
			handler: s.get(func(a map[string]any) string { return acct(a) + "/files/search" + query(a, "path", "name", "content") })},
		{Name: "folder_sizes", Title: "Folder sizes", area: "files", Annotations: readOnly(),
			Description: "How much space each folder inside a folder uses, to find what fills the disk.",
			InputSchema: obj(map[string]any{"account": argAccount, "path": str("Folder (optional)")}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/files/sizes" + query(a, "path") })},
		{Name: "list_file_snapshots", Title: "File snapshots", area: "files", Annotations: readOnly(),
			Description: "Point-in-time copies of an account's files on this server, newest first.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/snapshots" })},
		{Name: "list_share_links", Title: "Share links", area: "files", Annotations: readOnly(),
			Description: "An account's download links with expiry and download counts.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/shares" })},
		{Name: "write_file", Title: "Write file", area: "files", write: true, Annotations: write(true, true),
			Description: "Create or replace a text file, as the account's own user. Existing files are replaced only with overwrite=true. Read the file first and keep what you don't mean to change.",
			InputSchema: obj(map[string]any{"account": argAccount, "path": str("File path"), "content": str("Whole new contents"),
				"overwrite": boolean("Replace an existing file")}, "account", "path", "content"),
			handler: s.call("PUT", func(a map[string]any) string { return acct(a) + "/files/content" + query(a, "path") }, pick("content", "overwrite"))},
		{Name: "make_folder", Title: "New folder", area: "files", write: true, Annotations: write(false, true),
			InputSchema: obj(map[string]any{"account": argAccount, "path": str("New folder path")}, "account", "path"),
			Description: "Create a folder.", handler: fileOp("mkdir", "path")},
		{Name: "move_files", Title: "Move or rename", area: "files", write: true, Annotations: write(false, false),
			Description: "Move files or folders into a folder, or rename one (rename=true, to is the new path).",
			InputSchema: obj(map[string]any{"account": argAccount, "from": strList("Paths to move"), "to": str("Destination folder, or new path when renaming"),
				"rename": boolean("Rename the single path in from to to")}, "account", "from", "to"),
			handler: fileOp("move", "from", "to", "rename")},
		{Name: "copy_files", Title: "Copy files", area: "files", write: true, Annotations: write(false, false),
			Description: "Copy files or folders into a folder.",
			InputSchema: obj(map[string]any{"account": argAccount, "from": strList("Paths to copy"), "to": str("Destination folder")}, "account", "from", "to"),
			handler:     fileOp("copy", "from", "to")},
		{Name: "set_file_mode", Title: "Change permissions", area: "files", write: true, Annotations: write(false, true),
			Description: "chmod files or folders, e.g. 0644 for files and 0755 for folders.",
			InputSchema: obj(map[string]any{"account": argAccount, "paths": strList("Paths"), "mode": str("Octal mode, e.g. 0644"),
				"recursive": boolean("Also everything inside folders")}, "account", "paths", "mode"),
			handler: fileOp("chmod", "paths", "mode", "recursive")},
		{Name: "compress_files", Title: "Compress", area: "files", write: true, Annotations: write(false, false),
			Description: "Make a .zip or .tar.gz of items in a folder.",
			InputSchema: obj(map[string]any{"account": argAccount, "dir": str("Folder holding the items"), "items": strList("Names inside dir"),
				"name": str("Archive file name"), "format": enum("Format", "zip", "tar.gz")}, "account", "dir", "items", "name"),
			handler: fileOp("archive", "dir", "items", "name", "format")},
		{Name: "extract_archive", Title: "Extract archive", area: "files", write: true, Annotations: write(false, false),
			Description: "Unpack a .zip, .tar.gz or .tar into a folder.",
			InputSchema: obj(map[string]any{"account": argAccount, "path": str("Archive path"), "into": str("Folder to unpack into")}, "account", "path", "into"),
			handler:     fileOp("extract", "path", "into")},
		{Name: "delete_files", Title: "Delete files", area: "files", write: true, Annotations: write(true, true),
			Description: "Delete files or folders. Only what the human asked to delete; suggest create_file_snapshot first.",
			InputSchema: obj(map[string]any{"account": argAccount, "paths": strList("Paths to delete"),
				"confirm_count": integer("How many paths you are deleting, to confirm")}, "account", "paths", "confirm_count"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				paths, _ := a["paths"].([]any)
				if n, _ := a["confirm_count"].(float64); int(n) != len(paths) || len(paths) == 0 {
					return nil, fmt.Errorf("confirm_count must equal the number of paths (%d); nothing was deleted", len(paths))
				}
				return fileOp("delete", "paths")(ctx, a)
			}},
		{Name: "create_file_snapshot", Title: "Snapshot files", area: "files", write: true, Annotations: write(false, false),
			Description: "Take a point-in-time copy of an account's files on this server (fast, uses little space). Returns a job id.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.call("POST", func(a map[string]any) string { return acct(a) + "/snapshots" }, nil)},
		{Name: "restore_file_snapshot", Title: "Restore from snapshot", area: "files", write: true, Annotations: write(true, false),
			Description: "Copy a file or folder back from a snapshot. overwrite=true replaces the current version. Only when the human asked.",
			InputSchema: obj(map[string]any{"account": argAccount, "snapshot": str("Snapshot id"), "path": str("File or folder to restore"),
				"overwrite": boolean("Replace the current files")}, "account", "snapshot", "path"),
			handler: s.call("POST", func(a map[string]any) string {
				return acct(a) + "/snapshots/" + esc(argStr(a, "snapshot")) + "/restore"
			}, pick("path", "overwrite"))},
		{Name: "create_share_link", Title: "Share a file", area: "files", write: true, Annotations: write(false, false),
			Description: "Make a download link for a file or folder, with expiry, optional password and download limit.",
			InputSchema: obj(map[string]any{"account": argAccount, "path": str("File or folder"), "expires_hours": integer("Hours until it stops working (default 72)"),
				"password": str("Password (optional)"), "max_downloads": integer("Most downloads (optional)")}, "account", "path"),
			handler: s.call("POST", func(a map[string]any) string { return acct(a) + "/shares" }, pick("path", "expires_hours", "password", "max_downloads"))},
		{Name: "delete_share_link", Title: "Stop sharing", area: "files", write: true, Annotations: write(false, true),
			InputSchema: obj(map[string]any{"account": argAccount, "token": str("Share token")}, "account", "token"),
			Description: "Turn off a share link.",
			handler:     s.call("DELETE", func(a map[string]any) string { return acct(a) + "/shares/" + esc(argStr(a, "token")) }, nil)},

		// -------------------------------------------------------------- backups
		{Name: "list_backup_plans", Title: "Backup plans", area: "backups", Annotations: readOnly(),
			Description: "Backup plans with schedule, retention, last run and last integrity check. The server's (admin), or one account's.",
			InputSchema: obj(map[string]any{"account": argOptAccount}),
			handler:     s.get(plans)},
		{Name: "list_backup_snapshots", Title: "Backups of a plan", area: "backups", Annotations: readOnly(),
			Description: "Every backup a plan has kept, newest first.",
			InputSchema: obj(map[string]any{"account": argOptAccount, "plan": str("Plan id")}, "plan"),
			handler:     s.get(func(a map[string]any) string { return plan(a) + "/snapshots" })},
		{Name: "browse_backup", Title: "Browse a backup", area: "backups", Annotations: readOnly(),
			Description: "Files and database dumps inside one backup, to pick what to restore.",
			InputSchema: obj(map[string]any{"account": argOptAccount, "plan": str("Plan id"), "snapshot": str("Backup id"), "path": str("Folder inside the backup (default /)")}, "plan", "snapshot"),
			handler: s.get(func(a map[string]any) string {
				return plan(a) + "/snapshots/" + esc(argStr(a, "snapshot")) + "/browse" + query(a, "path")
			})},
		{Name: "list_storage", Title: "Backup storage", area: "backups", Annotations: readOnly(),
			Description: "Where backups go (S3, Backblaze B2, SFTP, another disk), with the last connection check. Keys are never shown.",
			InputSchema: obj(map[string]any{"account": argOptAccount}),
			handler:     s.get(orAll("/v1/storage", "/storage"))},
		{Name: "run_backup", Title: "Back up now", area: "backups", write: true, Annotations: write(false, false),
			Description: "Run a backup plan now. Returns a job id.",
			InputSchema: obj(map[string]any{"account": argOptAccount, "plan": str("Plan id")}, "plan"),
			handler:     s.call("POST", func(a map[string]any) string { return plan(a) + "/run" }, nil)},
		{Name: "check_backup", Title: "Check backups", area: "backups", write: true, Annotations: write(false, true),
			Description: "Read part of a plan's stored data back to prove it can be restored. Returns a job id.",
			InputSchema: obj(map[string]any{"account": argOptAccount, "plan": str("Plan id")}, "plan"),
			handler:     s.call("POST", func(a map[string]any) string { return plan(a) + "/check" }, nil)},
		{Name: "restore_backup", Title: "Restore from backup", area: "backups", write: true, Annotations: write(true, false),
			Description: "Restore files and/or databases from a backup. Without overwrite, files come back beside the current ones. Only when the human explicitly asked; say what will be replaced.",
			InputSchema: obj(map[string]any{"account": argOptAccount, "plan": str("Plan id"), "snapshot": str("Backup id"),
				"paths":     strList("Paths from browse_backup to restore (optional)"),
				"databases": strList("Database dump names to import, or [\"*\"] for all (optional)"),
				"overwrite": boolean("Replace the current files and databases"), "confirm_snapshot": str("Type the backup id again to confirm")},
				"plan", "snapshot", "confirm_snapshot"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				if err := confirmed(a, "confirm_snapshot", argStr(a, "snapshot")); err != nil {
					return nil, err
				}
				var out any
				return out, s.Agent.Do(ctx, "POST", plan(a)+"/restore", pick("snapshot", "paths", "databases", "overwrite")(a), &out)
			}},

		// ------------------------------------------------------- infrastructure
		{Name: "get_server_metrics", Title: "Server CPU, memory, swap, network and disk", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "The whole server right now and over 24 hours (5-minute points): CPU %, memory and swap in use, network bytes/second in and out per interface (with error counts), disk read/write bytes/second, load. Use it to explain slowness: high swap means too little memory, network near the port speed means traffic is the limit.",
			handler:     s.get(fixed("/v1/server/metrics"))},
		{Name: "get_inventory", Title: "Hardware inventory", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "CPU, memory, disks, network interfaces, virtualization and asset details (owner, location, renewal date, cost).",
			handler:     s.get(fixed("/v1/inventory"))},
		{Name: "get_events", Title: "Recent events", area: "server", Annotations: readOnly(),
			Description: "What happened on the server, newest last: sites created, backups, certificates, malware, failovers, domains expiring.",
			InputSchema: obj(map[string]any{"since": str("Only events after this id (optional)"), "limit": str("Most events (default 100)")}),
			handler:     s.get(func(a map[string]any) string { return "/v1/events" + query(a, "since", "limit") })},
		{Name: "list_maintenance", Title: "Maintenance windows", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "Scheduled maintenance windows (maintenance page, updates, reboot).",
			handler:     s.get(fixed("/v1/maintenance"))},
		{Name: "list_webhooks", Title: "Webhooks", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "Webhook URLs that receive server events, with the events each gets.",
			handler:     s.get(fixed("/v1/webhooks"))},
		{Name: "export_spec", Title: "Server as code", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "This server's accounts, sites, databases, DNS, mail and cron as a declarative spec (no secrets), to copy to another server.",
			handler:     s.get(fixed("/v1/spec/export"))},
		{Name: "plan_spec", Title: "Preview a spec", area: "server", Annotations: readOnly(),
			Description: "What applying a spec would create or change, without changing anything. Always run this before apply_spec and show the human the plan.",
			InputSchema: obj(map[string]any{"spec": object("Spec with accounts, sites, databases, dns, mail, cron lists"), "variables": object("Values for ${name} placeholders (optional)")}, "spec"),
			handler:     s.call("POST", fixed("/v1/spec/plan"), pick("spec", "variables"))},
		{Name: "apply_spec", Title: "Apply a spec", area: "server", write: true, Annotations: write(false, true),
			Description: "Create what a spec describes (it never deletes). Run plan_spec first. Returns a job id.",
			InputSchema: obj(map[string]any{"spec": object("Spec"), "variables": object("Values for placeholders (optional)")}, "spec"),
			handler:     s.call("POST", fixed("/v1/spec/apply"), pick("spec", "variables"))},
		{Name: "schedule_maintenance", Title: "Schedule maintenance", area: "server", write: true, Annotations: write(false, false),
			Description: "Schedule a maintenance window: show a maintenance page on some sites, optionally install updates and reboot. Times are RFC 3339.",
			InputSchema: obj(map[string]any{"title": str("What it's for"), "start": str("Start, e.g. 2026-11-08T01:00:00+01:00"), "end": str("End"),
				"sites": strList("Websites that show the maintenance page"), "updates": boolean("Install updates"), "reboot": boolean("Reboot if needed")}, "title", "start", "end"),
			handler: s.call("POST", fixed("/v1/maintenance"), pick("title", "start", "end", "sites", "updates", "reboot"))},
		{Name: "list_cpanel_imports", Title: "cPanel backups", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "cPanel full backups uploaded to /var/lib/ike/imports, ready to restore.",
			handler:     s.get(fixed("/v1/imports"))},
		{Name: "inspect_cpanel_backup", Title: "Read cPanel backup", area: "server", Annotations: readOnly(),
			Description: "What a cPanel backup holds: domains, databases, mailboxes, cron jobs, size, and anything that won't come across.",
			InputSchema: obj(map[string]any{"file": str("Backup file name from list_cpanel_imports")}, "file"),
			handler:     s.get(func(a map[string]any) string { return "/v1/imports/cpanel/" + esc(argStr(a, "file")) })},
		{Name: "restore_cpanel_backup", Title: "Restore cPanel backup", area: "server", write: true, Annotations: write(false, false),
			Description: "Restore a cPanel backup into a new account: websites, files, databases (rewiring WordPress), mailboxes with their passwords, forwarders, cron and DNS. Returns a job id whose log has the new database passwords.",
			InputSchema: obj(map[string]any{"file": str("Backup file name"), "account": str("New account name (default: the cPanel user)"),
				"skip":                 map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"files", "databases", "mail", "cron", "dns"}}, "description": "Parts to leave out"},
				"point_to_this_server": boolean("Change the imported DNS records from the old server's IP to this one")}, "file"),
			handler: s.call("POST", func(a map[string]any) string { return "/v1/imports/cpanel/" + esc(argStr(a, "file")) + "/restore" }, pick("account", "skip", "point_to_this_server"))},
		{Name: "get_disk_quotas", Title: "Disk quotas", area: "billing", Annotations: readOnly(), InputSchema: none,
			Description: "Each account's disk use against its quota.",
			handler:     s.get(fixed("/v1/quotas"))},

		// ------------------------------------------------- real-time media
		{Name: "get_realtime", Title: "TURN and LiveKit", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "The TURN server (coturn) and LiveKit: installed, running, settings, addresses, ports and API keys (never secrets).",
			handler:     s.get(fixed("/v1/realtime"))},
		{Name: "set_turn", Title: "Set up TURN", area: "server", write: true, Annotations: write(false, true),
			Description: "Switch the TURN server on or off and change it. domain must point at this server; tls needs HTTPS for that name here. Opens 3478, 5349 and 40000-49999/udp. Send every field: missing ones are reset.",
			InputSchema: obj(map[string]any{"enabled": boolean("Run it"), "domain": str("e.g. turn.example.com"), "tls": boolean("Also TURN over TLS on 5349"),
				"user_quota": integer("Logins per user, 0 = no limit"), "total_quota": integer("Logins in all, 0 = no limit"),
				"max_kbps": integer("Kbit/s per session, 0 = no limit"), "allow_accounts": boolean("Account owners may make TURN logins")}, "enabled", "domain"),
			handler: s.call("PUT", fixed("/v1/realtime/turn"), func(a map[string]any) any { return a })},
		{Name: "make_turn_login", Title: "TURN login", area: "server", write: true, Annotations: write(false, false),
			Description: "A short-lived TURN username and credential (default 12 hours, at most 168) with the addresses, for testing or an app's RTCPeerConnection.",
			InputSchema: obj(map[string]any{"user": str("Who it's for (letters, digits, _ . @ : -)"), "ttl_hours": integer("Hours it lasts")}),
			handler:     s.call("POST", fixed("/v1/realtime/turn/credentials"), pick("user", "ttl_hours"))},
		{Name: "rotate_turn_secret", Title: "New TURN secret", area: "server", write: true, Annotations: write(true, false),
			Description: "Replace the TURN shared secret; every login signed with the old one stops working. Only when the human asked (e.g. the secret leaked).",
			InputSchema: obj(map[string]any{"confirm": str(`Type "rotate" to confirm`)}, "confirm"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				if err := confirmed(a, "confirm", "rotate"); err != nil {
					return nil, err
				}
				var out any
				return out, s.Agent.Do(ctx, "POST", "/v1/realtime/turn/secret", nil, &out)
			}},
		{Name: "set_livekit", Title: "Set up LiveKit", area: "server", write: true, Annotations: write(false, true),
			Description: "Switch LiveKit on or off and change it. Ike makes domain a website owned by account that forwards to LiveKit and tries to get it a certificate (the name must point here). The first time it returns an API key and secret, once. Send every field: missing ones are reset.",
			InputSchema: obj(map[string]any{"enabled": boolean("Run it"), "domain": str("e.g. rtc.example.com"), "account": argAccount,
				"use_turn": boolean("Give callers this server's TURN server (switch TURN on first)"), "empty_timeout": integer("Seconds before an empty room closes (default 300)"),
				"max_participants": integer("People per room, 0 = no limit"), "webhook_urls": strList("https:// addresses for room events (max 5)"),
				"allow_accounts": boolean("Account owners may make their own API keys")}, "enabled", "domain", "account"),
			handler: s.call("PUT", fixed("/v1/realtime/livekit"), func(a map[string]any) any { return a })},
		{Name: "create_livekit_key", Title: "Add a LiveKit key", area: "server", write: true, Annotations: write(false, false),
			Description: "Add a LiveKit API key for an app; the secret is returned once. Give it to an account with account. LiveKit restarts.",
			InputSchema: obj(map[string]any{"name": str("What it's for"), "account": str("Account it belongs to (optional)")}, "name"),
			handler:     s.call("POST", fixed("/v1/realtime/livekit/keys"), pick("name", "account"))},
		{Name: "delete_livekit_key", Title: "Remove a LiveKit key", area: "server", write: true, Annotations: write(true, true),
			Description: "Remove a LiveKit API key; apps using it stop working and LiveKit restarts. Only when the human asked.",
			InputSchema: obj(map[string]any{"key": str("The key, APIxxxxxxxxxxxx"), "confirm_key": str("Type the key again")}, "key", "confirm_key"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				if err := confirmed(a, "confirm_key", argStr(a, "key")); err != nil {
					return nil, err
				}
				var out any
				return out, s.Agent.Do(ctx, "DELETE", "/v1/realtime/livekit/keys/"+esc(argStr(a, "key")), nil, &out)
			}},
		{Name: "make_livekit_token", Title: "LiveKit join token", area: "server", write: true, Annotations: write(false, false),
			Description: "A join token for a room, to test LiveKit (e.g. in LiveKit Meet) or for a small app. Returns the token and wss:// URL.",
			InputSchema: obj(map[string]any{"room": str("Room name"), "identity": str("Participant id"), "name": str("Display name"),
				"key": str("API key (default: the first)"), "ttl_minutes": integer("Minutes it lasts (default 60)"), "can_publish": boolean("May send audio/video (default true)")}, "room", "identity"),
			handler: s.call("POST", fixed("/v1/realtime/livekit/token"), pick("room", "identity", "name", "key", "ttl_minutes", "can_publish"))},
		{Name: "get_account_realtime", Title: "Account's video and calls", area: "sites", Annotations: readOnly(),
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			Description: "What an account can use: the LiveKit URL, its own API keys (no secrets), the TURN addresses and whether it may make keys and TURN logins.",
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/realtime" })},

		// ------------------------------------------------------- expiry dates
		{Name: "get_server_expiry", Title: "Domain and certificate expiry", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "Every registered domain the server hosts (DNS here or elsewhere) with its expiry date from the registry, and every website certificate with its expiry. Hosting terms are in list_expiry (panel).",
			handler:     s.get(fixed("/v1/expiry/server"))},
		{Name: "check_domain_expiry", Title: "Check a domain's expiry now", area: "sites", write: true, Annotations: write(false, true),
			Description: "Ask the domain's registry (RDAP) for its expiry date now instead of waiting for the daily check.",
			InputSchema: obj(map[string]any{"domain": argDomain}, "domain"),
			handler:     s.call("POST", func(a map[string]any) string { return "/v1/expiry/domains/" + esc(argStr(a, "domain")) + "/refresh" }, nil)},
		{Name: "list_expiry", Title: "Expiry dates", area: "billing", panel: true, Annotations: readOnly(),
			Description: "Domains, SSL certificates and hosting the person can see, soonest first, with state (ok, soon, urgent, expired, unknown), what happens next and a summary. Use it to answer \"what's about to expire?\".",
			InputSchema: obj(map[string]any{"kind": enum("Only one kind", "domain", "ssl", "hosting"), "account": str("Only one account")}),
			handler:     s.get(func(a map[string]any) string { return "/v1/expiry" + query(a, "kind", "account") })},
		{Name: "set_hosting_expiry", Title: "Set a hosting end date", area: "billing", panel: true, write: true, Annotations: write(false, true),
			Description: "Record when an account's hosting ends, for accounts billed outside Ike (subscriptions have their own dates). Empty expires removes it.",
			InputSchema: obj(map[string]any{"account": argAccount, "expires": str("Date, YYYY-MM-DD, or empty")}, "account", "expires"),
			handler: s.call("PUT", func(a map[string]any) string { return "/v1/expiry/hosting/" + esc(argStr(a, "account")) },
				func(a map[string]any) any { return map[string]any{"expires": argStr(a, "expires")} })},

		// ------------------------------------------------- panel: many servers
		{Name: "list_servers", Title: "Servers", area: "server", panel: true, Annotations: readOnly(), InputSchema: none,
			Description: "Other servers this panel manages, with group, version and whether they're online. Every other tool acts on the main server.",
			handler:     s.get(fixed("/v1/servers"))},
		{Name: "list_clusters", Title: "Database clusters", area: "server", panel: true, Annotations: readOnly(), InputSchema: none,
			Description: "MariaDB and Redis clusters across servers: primary, replicas and health.",
			handler:     s.get(fixed("/v1/clusters"))},
		{Name: "promote_replica", Title: "Fail over a cluster", area: "server", panel: true, write: true, Annotations: write(true, false),
			Description: "Make a replica the new primary of a database cluster (when the primary is down or for maintenance). Only when the human explicitly asked.",
			InputSchema: obj(map[string]any{"cluster": str("Cluster id"), "server": str("Server id of the replica to promote"),
				"confirm_server": str("Type the server id again to confirm")}, "cluster", "server", "confirm_server"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				if err := confirmed(a, "confirm_server", argStr(a, "server")); err != nil {
					return nil, err
				}
				var out any
				return out, s.Agent.Do(ctx, "POST", "/v1/clusters/"+esc(argStr(a, "cluster"))+"/promote", pick("server")(a), &out)
			}},

		// ---------------------------------------------------- Ike and Ubuntu updates
		{Name: "get_ike_versions", Title: "Ike versions", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "The running Ike version, the newest release and its notes, the update mode (notify, auto, off), versions kept for rollback and the update history.",
			handler:     s.get(fixed("/v1/ike"))},
		{Name: "update_ike", Title: "Update Ike", area: "server", write: true, Annotations: write(false, true), InputSchema: none,
			Description: "Install the newest Ike release on this server. The previous version is kept and goes back by itself if the new one isn't healthy. Only when the human asked. Returns a job id.",
			handler:     s.call("POST", fixed("/v1/ike/update"), nil)},
		{Name: "rollback_ike", Title: "Go back to an earlier Ike", area: "server", write: true, Annotations: write(true, true),
			Description: "Go back to a kept Ike version (see get_ike_versions). Only when the human asked. Returns a job id.",
			InputSchema: obj(map[string]any{"version": str("Kept version, e.g. 0.6.0 (optional: the newest older one)")}),
			handler:     s.call("POST", fixed("/v1/ike/rollback"), pick("version"))},
		{Name: "get_package_update_history", Title: "Package update history", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "Ubuntu package updates installed through Ike, with each package's version before and after, and which were rolled back.",
			handler:     s.get(fixed("/v1/updates/history"))},
		{Name: "rollback_package_update", Title: "Roll back a package update", area: "server", write: true, Annotations: write(true, true),
			Description: "Reinstall the earlier versions of the packages one update run changed (see get_package_update_history), where apt can still get them. Only when the human asked. Returns a job id.",
			InputSchema: obj(map[string]any{"run": str("Run id")}, "run"),
			handler:     s.call("POST", func(a map[string]any) string { return "/v1/updates/runs/" + esc(argStr(a, "run")) + "/rollback" }, nil)},
		{Name: "check_ubuntu_release", Title: "Newer Ubuntu release", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "Whether a newer Ubuntu LTS is available and whether the server is ready to move. Moving is only started by a person in the panel, after taking a snapshot.",
			handler:     s.get(fixed("/v1/updates/release"))},

		// ---------------------------------------------------- package managers
		{Name: "list_package_tools", Title: "Package managers", area: "server", Annotations: readOnly(), InputSchema: none,
			Description: "Package managers deploys detect (npm, pnpm, Yarn, Bun, Composer, Phive, pip, uv, Poetry, Pipenv, Maven, Gradle, Go modules, pub) and which are installed.",
			handler:     s.get(fixed("/v1/package-tools"))},
		{Name: "install_pecl_extension", Title: "Build a PECL extension", area: "server", write: true, Annotations: write(false, true),
			Description: "Build a PHP extension from PECL for one PHP version (prefer install_php_extension when Ubuntu packages it), turn it on and reload PHP. Returns a job id.",
			InputSchema: obj(map[string]any{"version": str("PHP version, e.g. 8.3"), "name": str("PECL package, e.g. swoole or swoole-5.1.4")}, "version", "name"),
			handler: s.call("POST", func(a map[string]any) string {
				return "/v1/php/" + esc(argStr(a, "version")) + "/pecl/" + esc(argStr(a, "name"))
			}, nil)},
		{Name: "install_pear_package", Title: "Install a PEAR package", area: "server", write: true, Annotations: write(false, true),
			Description: "Install a PEAR package (a PHP library shared by every website), e.g. Mail or Net_SMTP. Returns a job id.",
			InputSchema: obj(map[string]any{"name": str("PEAR package")}, "name"),
			handler:     s.call("POST", func(a map[string]any) string { return "/v1/php/pear?name=" + url.QueryEscape(argStr(a, "name")) }, nil)},

		// ---------------------------------------------------- panel: passkeys
		{Name: "list_my_passkeys", Title: "My passkeys", panel: true, Annotations: readOnly(), InputSchema: none,
			Description: "The passkeys the token's owner can sign in with (name, synced or one device, last used). Passkeys are added and removed in the panel in a browser, never through a token.",
			handler:     s.get(fixed("/v1/me/passkeys"))},

		// ---------------------------------------------------- panel: developers
		{Name: "list_developers", Title: "Developers", area: "accounts", panel: true, Annotations: readOnly(), InputSchema: none,
			Description: "People invited to work inside some hosting accounts, with the areas they may look at or change.",
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				var ps []map[string]any
				if err := s.Agent.Get(ctx, "/v1/people", &ps); err != nil {
					return nil, err
				}
				out := []map[string]any{}
				for _, p := range ps {
					if p["role"] == "developer" {
						out = append(out, p)
					}
				}
				return out, nil
			}},
		{Name: "invite_developer", Title: "Invite a developer", area: "accounts", panel: true, write: true, Annotations: write(false, false),
			Description: "Give a developer their own sign-in to some of your accounts. areas maps each area (sites, files, databases, containers, dns, mail, backups, security) to read or write. Developers never reach billing, accounts, people or the server, and can't add or delete websites. The password is shown once: give it to the human to send privately.",
			InputSchema: obj(map[string]any{"username": str("Their sign-in name, lowercase"), "email": str("Their email"), "name": str("Display name (optional)"),
				"accounts": strList("Hosting accounts they work on"), "areas": object("Area → read or write, e.g. {\"sites\": \"write\", \"files\": \"write\"}")},
				"username", "email", "accounts", "areas"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				pw := kitPassword()
				body := pick("username", "email", "name", "accounts", "areas")(a).(map[string]any)
				body["role"], body["password"] = "developer", pw
				var out map[string]any
				if err := s.Agent.Do(ctx, "POST", "/v1/people", body, &out); err != nil {
					return nil, err
				}
				out["password"] = pw
				return out, nil
			}},
		{Name: "remove_developer", Title: "Remove a developer", area: "accounts", panel: true, write: true, Annotations: write(true, true),
			Description: "Remove a developer: they're signed out at once and their API tokens stop working. Only when the human asked.",
			InputSchema: obj(map[string]any{"username": str("Developer's sign-in name"), "confirm_username": str("Type it again to confirm")}, "username", "confirm_username"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				if err := confirmed(a, "confirm_username", argStr(a, "username")); err != nil {
					return nil, err
				}
				var out any
				return out, s.Agent.Do(ctx, "DELETE", "/v1/people/"+esc(argStr(a, "username")), nil, &out)
			}},

		// ---------------------------------------------------- panel: business
		{Name: "list_notifications", Title: "Notifications", area: "", panel: true, Annotations: readOnly(), InputSchema: none,
			Description: "The signed-in person's notifications, newest first, with which are unread.",
			handler:     s.get(fixed("/v1/notifications"))},
		{Name: "list_packages", Title: "Hosting packages", area: "billing", panel: true, Annotations: readOnly(), InputSchema: none,
			Description: "Hosting packages with price and limits (websites, domains, disk, bandwidth, mailboxes, databases, CPU, memory), and how many accounts use each.",
			handler:     s.get(fixed("/v1/packages"))},
		{Name: "list_account_packages", Title: "Which package each account has", area: "billing", panel: true, Annotations: readOnly(), InputSchema: none,
			Description: "Account → package id.", handler: s.get(fixed("/v1/account-packages"))},
		{Name: "get_bandwidth", Title: "Bandwidth this month", area: "billing", panel: true, Annotations: readOnly(), InputSchema: none,
			Description: "Each account's traffic this month against its package's cap, and whether it was suspended for going over.",
			handler:     s.get(fixed("/v1/bandwidth"))},
		{Name: "list_subscriptions", Title: "Subscriptions", area: "billing", panel: true, Annotations: readOnly(), InputSchema: none,
			Description: "Recurring billing: account, package, price, next due date. Invoices are made 7 days before.",
			handler:     s.get(fixed("/v1/billing/subscriptions"))},
		{Name: "list_invoices", Title: "Invoices", area: "billing", panel: true, Annotations: readOnly(), InputSchema: none,
			Description: "Invoices with amount, status (open, paid, void) and due date.",
			handler:     s.get(fixed("/v1/billing/invoices"))},
		{Name: "create_package", Title: "Create package", area: "billing", panel: true, write: true, Annotations: write(false, false),
			Description: "Create a hosting package. price is in the smallest unit (kobo, cents): 250000 = NGN 2,500. In limits, 0 means unlimited.",
			InputSchema: obj(map[string]any{"name": str("Package name"), "price": integer("Price in the smallest currency unit"),
				"currency": str("ISO currency, e.g. NGN"), "interval": enum("Billing interval", "month", "year", "none"),
				"limits": object("sites, domains, databases, email_domains, mailboxes, disk_mb, bandwidth_gb, cpu_percent, memory_mb, processes, php_workers (numbers); shell, containers, backups (true/false)")},
				"name", "price", "currency", "interval", "limits"),
			handler: s.call("POST", fixed("/v1/packages"), pick("name", "price", "currency", "interval", "limits"))},
		{Name: "assign_package", Title: "Change an account's package", area: "billing", panel: true, write: true, Annotations: write(false, true),
			Description: "Put an account on a package; its CPU, memory, process and disk limits change right away. Refused if the account already uses more than the package allows.",
			InputSchema: obj(map[string]any{"account": argAccount, "package": str("Package id from list_packages")}, "account", "package"),
			handler:     s.call("PUT", func(a map[string]any) string { return "/v1/account-packages/" + esc(argStr(a, "account")) }, pick("package"))},
		{Name: "create_subscription", Title: "Start a subscription", area: "billing", panel: true, write: true, Annotations: write(false, false),
			Description: "Bill an account for its package every month or year, starting on first_due (YYYY-MM-DD, default today).",
			InputSchema: obj(map[string]any{"account": argAccount, "package_id": str("Package id"), "first_due": str("First due date (optional)")}, "account", "package_id"),
			handler:     s.call("POST", fixed("/v1/billing/subscriptions"), pick("account", "package_id", "first_due"))},
		{Name: "cancel_subscription", Title: "Cancel a subscription", area: "billing", panel: true, write: true, Annotations: write(true, true),
			Description: "Stop billing a subscription; invoices already made stay. Only when the human asked.",
			InputSchema: obj(map[string]any{"id": str("Subscription id")}, "id"),
			handler:     s.call("POST", func(a map[string]any) string { return "/v1/billing/subscriptions/" + esc(argStr(a, "id")) + "/cancel" }, nil)},
	}
}

// panelSession is what GET /api/v1/session says about an API token.
type panelSession struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Token    *struct {
		Name     string            `json:"name"`
		ReadOnly bool              `json:"read_only"`
		Limited  bool              `json:"limited"`
		Scopes   map[string]string `json:"scopes"`
		Accounts []string          `json:"accounts"`
	} `json:"token"`
}

// ConnectPanel checks the token with the panel and limits the tools to what
// it may do. It returns a one-line description for the log.
func (s *Server) ConnectPanel(ctx context.Context) (string, error) {
	var me panelSession
	if err := s.Agent.Get(ctx, "/v1/session", &me); err != nil {
		return "", fmt.Errorf("the panel didn't accept the token: %w", err)
	}
	s.Panel = true
	desc := fmt.Sprintf("signed in to the panel as %s (%s)", me.Username, me.Role)
	if t := me.Token; t != nil {
		s.ReadOnlyToken = t.ReadOnly
		if t.Limited {
			s.Scopes = t.Scopes
			if s.Scopes == nil {
				s.Scopes = map[string]string{}
			}
		}
		desc += fmt.Sprintf(" with token %q", t.Name)
		if t.ReadOnly {
			desc += ", read only"
		}
		if t.Limited {
			desc += fmt.Sprintf(", areas %v", t.Scopes)
		}
		if len(t.Accounts) > 0 {
			desc += fmt.Sprintf(", accounts %v", t.Accounts)
		}
	}
	return desc, nil
}

// ToolSummary lists the offered tools as "area  name  (write)" lines.
func (s *Server) ToolSummary() []string {
	var out []string
	for _, t := range s.toolList() {
		w := ""
		if t.write {
			w = "  (changes things)"
		}
		area := t.area
		if area == "" {
			area = "-"
		}
		out = append(out, fmt.Sprintf("%-11s %s%s", area, t.Name, w))
	}
	return out
}
