// Package validate holds strict allow-list validators for every value that
// reaches the filesystem, a config file or a command argument.
package validate

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	labelRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	tldRe      = regexp.MustCompile(`^([a-z]{2,63}|xn--[a-z0-9-]{1,59})$`)
	usernameRe = regexp.MustCompile(`^[a-z][a-z0-9]{2,15}$`)
	phpRe      = regexp.MustCompile(`^[5-9]\.[0-9]$`)
)

// reservedUsers can never be used for hosting accounts.
var reservedUsers = map[string]bool{
	"root": true, "admin": true, "administrator": true, "daemon": true, "bin": true,
	"sys": true, "sync": true, "games": true, "man": true, "mail": true, "news": true,
	"uucp": true, "proxy": true, "backup": true, "list": true, "irc": true, "nobody": true,
	"systemd": true, "syslog": true, "messagebus": true, "sshd": true, "ubuntu": true,
	"nginx": true, "apache": true, "mysql": true, "mariadb": true, "postgres": true,
	"postfix": true, "dovecot": true, "www": true, "wwwdata": true, "ftp": true,
	"ike": true, "docker": true, "redis": true, "named": true, "bind": true,
	"pdns": true, "caddy": true, "lsadm": true, "lsws": true, "lxd": true, "polkitd": true, "tss": true, "uuidd": true, "tcpdump": true,
}

// Domain accepts lowercase hostnames like "example.com" or "shop.example.com.ng".
// Wildcards, IPs, ports, trailing dots and uppercase are rejected.
func Domain(d string) error {
	if len(d) < 4 || len(d) > 253 {
		return fmt.Errorf("domain must be 4-253 characters")
	}
	if d != strings.ToLower(d) {
		return fmt.Errorf("domain must be lowercase")
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain must contain a dot")
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return fmt.Errorf("invalid domain label %q", l)
		}
	}
	if !tldRe.MatchString(labels[len(labels)-1]) {
		return fmt.Errorf("invalid top-level domain")
	}
	return nil
}

// Username accepts 3-16 chars, lowercase letters and digits, starting with a letter.
func Username(u string) error {
	if !usernameRe.MatchString(u) {
		return fmt.Errorf("username must be 3-16 lowercase letters/digits and start with a letter")
	}
	if reservedUsers[u] {
		return fmt.Errorf("username %q is reserved", u)
	}
	return nil
}

// PHPVersion accepts versions like "8.3".
func PHPVersion(v string) error {
	if !phpRe.MatchString(v) {
		return fmt.Errorf("invalid PHP version %q", v)
	}
	return nil
}

// Hostname accepts a domain, or a wildcard like "*.example.com" when
// allowWildcard is set. The wildcard may only be the whole leftmost label.
func Hostname(h string, allowWildcard bool) error {
	if rest, ok := strings.CutPrefix(h, "*."); ok {
		if !allowWildcard {
			return fmt.Errorf("wildcards aren't allowed here")
		}
		if strings.Count(rest, ".") < 1 {
			return fmt.Errorf("a wildcard needs a domain under it, like *.example.com")
		}
		return Domain(rest)
	}
	return Domain(h)
}

var relPathRe = regexp.MustCompile(`^[A-Za-z0-9._@+-]+(/[A-Za-z0-9._@+-]+)*$`)

// RelPath accepts a relative path inside a project, like "public" or
// "storage/app". No "..", no leading slash, no hidden tricks.
func RelPath(p string) error {
	if p == "" || len(p) > 200 || !relPathRe.MatchString(p) {
		return fmt.Errorf("%q must be a relative path like public or storage/app", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." {
			return fmt.Errorf("%q can't contain . or .. segments", p)
		}
	}
	return nil
}

var safeNameRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// SafeName is a database or user name made of lowercase letters, digits and underscores.
func SafeName(s string) bool { return safeNameRe.MatchString(s) }
