package panel

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/YOUR-ORG/ike/agent/internal/panel/store"
)

// areaOf says which part of the API a request touches, for limited API
// tokens. "" means always allowed (who am I, job progress, branding);
// "tokens" is never allowed with a limited token.
func areaOf(apiPath string) string {
	p := strings.TrimPrefix(strings.TrimPrefix(apiPath, "/api"), "/v1")
	seg := strings.Split(strings.Trim(p, "/"), "/")
	if len(seg) == 0 || seg[0] == "" {
		return "server"
	}
	switch seg[0] {
	case "session", "jobs", "branding", "notifications", "events", "health", "runtimes", "apps":
		return ""
	case "me", "tokens":
		return "tokens"
	case "ssl":
		return "sites"
	case "sites":
		if len(seg) >= 3 && seg[2] == "waf" {
			return "security"
		}
		return "sites"
	case "dns":
		return "dns"
	case "mail":
		return "mail"
	case "databases":
		return "databases"
	case "backups", "storage":
		return "backups"
	case "security", "secrets", "firewall":
		return "security"
	case "docker", "kubernetes":
		return "containers"
	case "packages", "billing", "account-packages", "bandwidth":
		return "billing"
	case "provision", "people":
		return "accounts"
	case "shares":
		return "files"
	case "accounts":
		if len(seg) < 3 {
			return "accounts"
		}
		switch seg[2] {
		case "dns":
			return "dns"
		case "mail":
			return "mail"
		case "dbs", "databases", "redis":
			return "databases"
		case "files", "snapshots", "shares", "terminal", "sftp-users":
			return "files"
		case "cron": // cron jobs run website tasks
			return "sites"
		case "backups", "storage":
			return "backups"
		case "containers", "stacks", "volumes", "networks", "registries":
			return "containers"
		case "malware", "quarantine":
			return "security"
		}
		return "accounts"
	}
	return "server"
}

// tokenAllows checks a limited token against one request.
func tokenAllows(t *store.APIToken, method, apiPath, ip string) error {
	if len(t.AllowIPs) > 0 {
		ok := false
		addr, err := netip.ParseAddr(ip)
		for _, a := range t.AllowIPs {
			if pfx, perr := netip.ParsePrefix(a); perr == nil && err == nil && pfx.Contains(addr) {
				ok = true
			} else if a == ip {
				ok = true
			}
		}
		if !ok {
			return denied("this API token can't be used from %s", ip)
		}
	}
	if !t.Limited {
		return nil
	}
	area := areaOf(apiPath)
	if area == "" {
		return nil
	}
	if area == "tokens" {
		if method == "GET" && strings.HasSuffix(apiPath, "/session") {
			return nil
		}
		return denied("limited API tokens can't manage tokens or sign-in settings")
	}
	level := t.Scopes[area]
	switch {
	case level == "write":
		return nil
	case level == "read" && method == "GET":
		return nil
	case level == "read":
		return denied("this API token can only read %s", area)
	}
	return denied("this API token has no access to %s", area)
}

// validScopes checks a token request.
func validScopes(sc map[string]string, ips []string) error {
	known := map[string]bool{}
	for _, a := range store.TokenAreas {
		known[a] = true
	}
	for a, l := range sc {
		if !known[a] {
			return fmt.Errorf("unknown area %q", a)
		}
		if l != "read" && l != "write" && l != "none" {
			return fmt.Errorf("%s access is read, write or none", a)
		}
	}
	for a, l := range sc {
		if l == "none" {
			delete(sc, a)
		}
	}
	if len(ips) > 20 {
		return fmt.Errorf("at most 20 addresses")
	}
	for _, ip := range ips {
		if _, err := netip.ParseAddr(ip); err != nil {
			if _, err := netip.ParsePrefix(ip); err != nil {
				return fmt.Errorf("%q is not an IP address or range", ip)
			}
		}
	}
	return nil
}
