package sites

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/YOUR-ORG/ike/agent/internal/validate"
)

// ------------------------------------------------------------------ domains

// DomainsRequest sets the extra names a website answers to.
type DomainsRequest struct {
	Aliases         []string `json:"aliases"`
	WWW             string   `json:"www,omitempty"` // both | www | root | none
	RedirectAliases bool     `json:"redirect_aliases"`
}

const maxAliases = 50

func wwwMode(s string) string {
	if s == "" {
		return "both"
	}
	return s
}

// names returns every host name a website claims, main domain first.
func (s *Site) names() []string {
	out := []string{s.Domain}
	if wwwMode(s.WWW) != "none" {
		out = append(out, "www."+s.Domain)
	}
	return append(out, s.Aliases...)
}

// hostOwner returns the website that already serves host (directly, as www.
// or as an alias), ignoring the website named skip.
func (m *Manager) hostOwner(host, skip string) string {
	sites, _ := m.List()
	for _, s := range sites {
		if s.Domain == skip {
			continue
		}
		for _, n := range s.names() {
			if n == host {
				return s.Domain
			}
		}
	}
	return ""
}

// SetDomains changes a website's aliases and www handling.
func (m *Manager) SetDomains(ctx context.Context, domain string, req DomainsRequest) (*Site, error) {
	req.WWW = wwwMode(req.WWW)
	switch req.WWW {
	case "both", "www", "root", "none":
	default:
		return nil, fmt.Errorf("www must be both, www, root or none")
	}
	if len(req.Aliases) > maxAliases {
		return nil, fmt.Errorf("at most %d extra domains per website", maxAliases)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{domain: true, "www." + domain: true}
	aliases := []string{}
	for _, a := range req.Aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		if err := validate.Hostname(a, true); err != nil {
			return nil, fmt.Errorf("%s: %w", a, err)
		}
		if seen[a] {
			if a == domain || a == "www."+domain {
				return nil, fmt.Errorf("%s is already served by this website; it doesn't need to be an alias", a)
			}
			return nil, fmt.Errorf("%s is listed twice", a)
		}
		seen[a] = true
		if owner := m.hostOwner(a, domain); owner != "" {
			return nil, fmt.Errorf("%s is already served by %s", a, owner)
		}
		aliases = append(aliases, a)
	}
	old := *site
	site.Aliases, site.WWW, site.RedirectAliases = aliases, req.WWW, req.RedirectAliases
	if site.WWW == "both" {
		site.WWW = ""
	}
	if len(aliases) == 0 {
		site.RedirectAliases = false
	}
	if err := m.applyWeb(ctx, site, &old); err != nil {
		return nil, err
	}
	return site, nil
}

// applyDomainLayout works out the server_name lists and canonical host.
func (m *Manager) applyDomainLayout(s *Site, L *layout) {
	main := []string{}
	redirect := []string{}
	L.CanonicalHost = s.Domain
	switch wwwMode(s.WWW) {
	case "both":
		main = append(main, s.Domain, "www."+s.Domain)
	case "www":
		L.CanonicalHost = "www." + s.Domain
		main = append(main, "www."+s.Domain)
		redirect = append(redirect, s.Domain)
	case "root":
		main = append(main, s.Domain)
		redirect = append(redirect, "www."+s.Domain)
	case "none":
		main = append(main, s.Domain)
	}
	if s.RedirectAliases {
		redirect = append(redirect, s.Aliases...)
	} else {
		main = append(main, s.Aliases...)
	}
	L.ServerList, L.RedirectList = main, redirect
	L.ServerNames = strings.Join(main, " ")
	L.RedirectNames = strings.Join(redirect, " ")
	L.NginxRedirects, L.NginxRewrites, L.ApacheRules = renderRedirects(s.Redirects)
}

// applyWeb rewrites a website's web server configs after a change and saves
// it. On failure the previous configs are put back.
func (m *Manager) applyWeb(ctx context.Context, site, old *Site) error {
	L, oldL := m.layout(site), m.layout(old)
	restore := func() { _ = m.writeWeb(context.Background(), oldL) }
	if err := m.writeWeb(ctx, L); err != nil {
		restore()
		return err
	}
	if site.Runtime == RuntimePHP && L.PoolFile != "" {
		if err := m.writePool(ctx, L); err != nil {
			restore()
			if old.Runtime == RuntimePHP {
				_ = m.writePool(context.Background(), oldL)
			}
			return fmt.Errorf("configure PHP: %w", err)
		}
	}
	return m.writeJSON(L.StateFile, site)
}

// ---------------------------------------------------------------- redirects

// Redirect is one redirect or internal rewrite rule.
type Redirect struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`  // redirect (visitor is sent elsewhere) | rewrite (served from another path)
	Match     string `json:"match"` // exact | prefix | regex
	From      string `json:"from"`
	To        string `json:"to"`
	Code      int    `json:"code,omitempty"`       // 301 | 302 | 307 | 308 (redirects)
	KeepQuery bool   `json:"keep_query,omitempty"` // add the original ?query to the target
}

const maxRedirects = 200

var (
	// Paths: no spaces, quotes, braces, semicolons or hashes, so a rule can't
	// break out of the config line it's written on.
	pathRe   = regexp.MustCompile(`^/[A-Za-z0-9._~%/:@,+=-]*$`)
	regexRe  = regexp.MustCompile(`^\^[A-Za-z0-9._~%/:@,+=()*|?\[\]\\^$-]*$`)
	targetRe = regexp.MustCompile(`^(https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?)?(/[A-Za-z0-9._~%/:@,+=&?$-]*)?$`)
	dollarRe = regexp.MustCompile(`\$([^1-9]|$)`)
)

func (r *Redirect) check() error {
	if r.Kind == "" {
		r.Kind = "redirect"
	}
	if r.Match == "" {
		r.Match = "exact"
	}
	switch r.Kind {
	case "redirect":
		if r.Code == 0 {
			r.Code = 301
		}
		switch r.Code {
		case 301, 302, 307, 308:
		default:
			return fmt.Errorf("redirect code must be 301, 302, 307 or 308")
		}
	case "rewrite":
		r.Code = 0
		if !strings.HasPrefix(r.To, "/") {
			return fmt.Errorf("a rewrite serves another path on this website, so its target must start with /")
		}
	default:
		return fmt.Errorf("kind must be redirect or rewrite")
	}
	switch r.Match {
	case "exact", "prefix":
		if !pathRe.MatchString(r.From) || strings.Contains(r.From, "$") {
			return fmt.Errorf("%q must be a path starting with / without spaces, quotes or braces", r.From)
		}
		if r.Match == "prefix" && r.From == "/" {
			return fmt.Errorf("to redirect a whole website, use its domain settings instead of a / prefix")
		}
	case "regex":
		if !regexRe.MatchString(r.From) {
			return fmt.Errorf("a pattern must start with ^ and can't contain spaces, quotes, braces, ; or #")
		}
		if _, err := regexp.Compile(r.From); err != nil {
			return fmt.Errorf("pattern %q: %v", r.From, err)
		}
	default:
		return fmt.Errorf("match must be exact, prefix or regex")
	}
	if len(r.From) > 300 || len(r.To) > 500 {
		return fmt.Errorf("rule is too long")
	}
	if r.To == "" || !targetRe.MatchString(r.To) {
		return fmt.Errorf("target %q must be a path like /new or a URL like https://example.com/new", r.To)
	}
	if dollarRe.MatchString(r.To) {
		return fmt.Errorf("the target can only use $1 to $9 (from the pattern)")
	}
	if strings.Contains(r.To, "$") && r.Match != "regex" {
		return fmt.Errorf("$1 to $9 only work with a pattern (regex) rule")
	}
	if r.KeepQuery && strings.Contains(r.To, "?") {
		return fmt.Errorf("the target already has a ?query, so keep query can't be used")
	}
	if r.ID == "" {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		r.ID = hex.EncodeToString(b)
	}
	return nil
}

// pattern turns a rule into an anchored regex and the suffix to append.
func (r Redirect) pattern() (re, suffix string) {
	switch r.Match {
	case "exact":
		return "^" + regexp.QuoteMeta(r.From) + "$", ""
	case "prefix":
		return "^" + regexp.QuoteMeta(strings.TrimRight(r.From, "/")) + "(/.*)?$", "$1"
	}
	return r.From, ""
}

func renderRedirects(rules []Redirect) (nginx, rewrites, apache []string) {
	for _, r := range rules {
		re, suffix := r.pattern()
		to := r.To + suffix
		if r.Kind == "rewrite" {
			// Nginx keeps the query string on rewrites unless the target ends with ?.
			nginx := to
			if !r.KeepQuery && !strings.Contains(nginx, "?") {
				nginx += "?"
			}
			rewrites = append(rewrites, fmt.Sprintf("rewrite %s %s last;", re, nginx))
			flags := "PT,L"
			if r.KeepQuery {
				flags += ",QSA"
			}
			apache = append(apache, fmt.Sprintf("RewriteRule %s %s [%s]", re, apacheEscape(to), flags))
			continue
		}
		target := to
		if r.KeepQuery {
			target += "$is_args$args"
		}
		if r.Match == "exact" {
			nginx = append(nginx, fmt.Sprintf("location = %s { return %d %s; }", r.From, r.Code, target))
		} else {
			nginx = append(nginx, fmt.Sprintf("location ~ %s { return %d %s; }", re, r.Code, target))
		}
		flags := fmt.Sprintf("R=%d,L", r.Code)
		if !r.KeepQuery {
			flags += ",QSD"
		}
		apache = append(apache, fmt.Sprintf("RewriteRule %s %s [%s]", re, apacheEscape(to), flags))
	}
	return
}

// apacheEscape keeps a literal % in a substitution (Apache reads %1 as a
// RewriteCond back-reference).
func apacheEscape(s string) string { return strings.ReplaceAll(s, "%", `\%`) }

// SetRedirects replaces a website's redirect and rewrite rules.
func (m *Manager) SetRedirects(ctx context.Context, domain string, rules []Redirect) (*Site, error) {
	if len(rules) > maxRedirects {
		return nil, fmt.Errorf("at most %d rules per website", maxRedirects)
	}
	seen := map[string]bool{}
	for i := range rules {
		if err := rules[i].check(); err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		key := rules[i].Match + " " + rules[i].From
		if seen[key] {
			return nil, fmt.Errorf("rule %d: %s is already handled by an earlier rule", i+1, rules[i].From)
		}
		seen[key] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	old := *site
	site.Redirects = rules
	if err := m.applyWeb(ctx, site, &old); err != nil {
		return nil, err
	}
	return site, nil
}
