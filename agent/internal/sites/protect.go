package sites

import (
	"bufio"
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/mail"
)

// Password protection: visitors must sign in (HTTP basic authentication)
// before they can open some paths of a website, or the whole website.
//
// Users and their SHA-512 crypt hashes live in root-owned files that only
// the web server group can read:
//
//	/etc/ike/htpasswd/<domain>/users        every user of the website
//	/etc/ike/htpasswd/<domain>/<path id>    users allowed on one path
//
// Nginx (alone, or in front of Apache) checks the password, so it covers PHP,
// apps, containers and static files alike; Apache alone uses <LocationMatch>
// blocks, which a website's .htaccess can't override.

// ProtectedPath is one password-protected path.
type ProtectedPath struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`                // "/" for the whole website, or /admin/, /wp-login.php ...
	Realm    string   `json:"realm"`               // shown in the browser's sign-in box
	Users    []string `json:"users,omitempty"`     // empty: every user of the website
	AllowIPs []string `json:"allow_ips,omitempty"` // these addresses get in without a password
}

// AuthUser is a user who can sign in to protected paths (never with its hash).
type AuthUser struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
}

// Protection is a website's password protection.
type Protection struct {
	Paths []ProtectedPath `json:"paths"`
	Users []AuthUser      `json:"users"`
}

const (
	htpasswdRoot    = "/etc/ike/htpasswd"
	maxProtected    = 50
	maxAuthUsers    = 500
	minAuthPassword = 8
)

var (
	authUserRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)
	realmRe    = regexp.MustCompile(`^[A-Za-z0-9 .,'()&/+-]{1,60}$`)
)

func htpasswdDir(domain string) string  { return filepath.Join(htpasswdRoot, domain) }
func usersFile(domain string) string    { return filepath.Join(htpasswdDir(domain), "users") }
func pathFile(domain, id string) string { return filepath.Join(htpasswdDir(domain), id) }

// GetProtection returns a website's protected paths and users.
func (m *Manager) GetProtection(domain string) (*Protection, error) {
	s, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	out := &Protection{Paths: []ProtectedPath{}, Users: []AuthUser{}}
	if s.Protection != nil {
		out.Paths = append(out.Paths, s.Protection.Paths...)
		out.Users = append(out.Users, s.Protection.Users...)
	}
	return out, nil
}

func (p *ProtectedPath) check(users map[string]bool) error {
	p.Path = strings.TrimSpace(p.Path)
	if p.Path == "" {
		p.Path = "/"
	}
	if !pathRe.MatchString(p.Path) || strings.Contains(p.Path, "..") || strings.Contains(p.Path, "//") {
		return fmt.Errorf("%q is not a path on the website, like / or /admin/", p.Path)
	}
	if strings.HasPrefix(p.Path, "/.well-known/acme-challenge") || strings.HasPrefix(p.Path, "/__ike_") {
		return fmt.Errorf("%s is used by Ike itself and can't be protected", p.Path)
	}
	p.Realm = strings.TrimSpace(p.Realm)
	if p.Realm == "" {
		p.Realm = "Restricted area"
	}
	if !realmRe.MatchString(p.Realm) {
		return fmt.Errorf("the sign-in message can use letters, digits, spaces and . , ' ( ) & / + - (60 at most)")
	}
	for _, u := range p.Users {
		if !users[u] {
			return fmt.Errorf("%s isn't a user of this website; add the user first", u)
		}
	}
	if len(p.AllowIPs) > 50 {
		return fmt.Errorf("at most 50 addresses")
	}
	for i, a := range p.AllowIPs {
		a = strings.TrimSpace(a)
		if pf, err := netip.ParsePrefix(a); err == nil {
			a = pf.Masked().String()
		} else if ad, err := netip.ParseAddr(a); err == nil {
			a = ad.String()
		} else {
			return fmt.Errorf("%q is not an IP address or range", a)
		}
		p.AllowIPs[i] = a
	}
	if p.ID == "" {
		p.ID = kit.RandomHex(4)
	}
	if !regexp.MustCompile(`^[a-f0-9]{8}$`).MatchString(p.ID) {
		return fmt.Errorf("bad path id")
	}
	return nil
}

// SetProtectedPaths replaces the website's protected paths.
func (m *Manager) SetProtectedPaths(ctx context.Context, domain string, paths []ProtectedPath) (*Protection, error) {
	if len(paths) > maxProtected {
		return nil, fmt.Errorf("at most %d protected paths per website", maxProtected)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	pr := Protection{}
	if site.Protection != nil {
		pr.Users = site.Protection.Users
	}
	known := map[string]bool{}
	for _, u := range pr.Users {
		known[u.Name] = true
	}
	seen := map[string]bool{}
	for i := range paths {
		if err := paths[i].check(known); err != nil {
			return nil, fmt.Errorf("path %d: %w", i+1, err)
		}
		if seen[paths[i].Path] {
			return nil, fmt.Errorf("%s is listed twice", paths[i].Path)
		}
		seen[paths[i].Path] = true
	}
	if len(paths) > 0 && len(pr.Users) == 0 {
		return nil, fmt.Errorf("add a user first, so someone can sign in")
	}
	// Longest path first, so /admin/settings/ wins over /admin/.
	sort.SliceStable(paths, func(i, j int) bool { return len(paths[i].Path) > len(paths[j].Path) })
	pr.Paths = paths
	return m.saveProtection(ctx, site, pr)
}

// SetAuthUser adds a user or changes their password. An empty password makes
// one up, returned once.
func (m *Manager) SetAuthUser(ctx context.Context, domain, name, password string) (string, error) {
	if !authUserRe.MatchString(name) {
		return "", fmt.Errorf("user names can use letters, digits and . _ @ - (64 at most)")
	}
	made := ""
	if password == "" {
		password = kit.Password(16)
		made = password
	}
	if len(password) < minAuthPassword || len(password) > 200 {
		return "", fmt.Errorf("the password must be %d-200 characters", minAuthPassword)
	}
	if strings.ContainsAny(password, "\n\r") {
		return "", fmt.Errorf("the password can't contain line breaks")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return "", err
	}
	pr := Protection{}
	if site.Protection != nil {
		pr = *site.Protection
		pr.Users = append([]AuthUser(nil), pr.Users...)
	}
	hashes, err := m.readHashes(domain)
	if err != nil {
		return "", err
	}
	exists := false
	for _, u := range pr.Users {
		exists = exists || u.Name == name
	}
	if !exists {
		if len(pr.Users) >= maxAuthUsers {
			return "", fmt.Errorf("at most %d users per website", maxAuthUsers)
		}
		pr.Users = append(pr.Users, AuthUser{Name: name, Created: time.Now().UTC()})
		sort.Slice(pr.Users, func(i, j int) bool { return pr.Users[i].Name < pr.Users[j].Name })
	}
	hashes[name] = cryptHash(password)
	if err := m.writeHashes(domain, hashes); err != nil {
		return "", err
	}
	if _, err := m.saveProtection(ctx, site, pr); err != nil {
		return "", err
	}
	return made, nil
}

// DeleteAuthUser removes a user. A path that only that user could open is refused:
// change the path first, so nothing becomes open to everyone by accident.
func (m *Manager) DeleteAuthUser(ctx context.Context, domain, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return err
	}
	if site.Protection == nil {
		return fmt.Errorf("user %s %w", name, ErrNotFound)
	}
	pr := *site.Protection
	var users []AuthUser
	found := false
	for _, u := range pr.Users {
		if u.Name == name {
			found = true
			continue
		}
		users = append(users, u)
	}
	if !found {
		return fmt.Errorf("user %s %w", name, ErrNotFound)
	}
	if len(users) == 0 && len(pr.Paths) > 0 {
		return fmt.Errorf("%s is the last user; remove the protected paths first, or add another user", name)
	}
	var paths []ProtectedPath
	for _, p := range pr.Paths {
		if len(p.Users) > 0 {
			p.Users = without(p.Users, name)
			if len(p.Users) == 0 {
				// Removing the last named user would open the path to everyone.
				return fmt.Errorf("%s is the only user allowed on %s; change that path first", name, p.Path)
			}
		}
		paths = append(paths, p)
	}
	pr.Users, pr.Paths = users, paths
	hashes, err := m.readHashes(domain)
	if err != nil {
		return err
	}
	delete(hashes, name)
	if err := m.writeHashes(domain, hashes); err != nil {
		return err
	}
	_, err = m.saveProtection(ctx, site, pr)
	return err
}

func without(list []string, v string) []string {
	var out []string
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// saveProtection writes the per-path user files and the web server config.
func (m *Manager) saveProtection(ctx context.Context, site *Site, pr Protection) (*Protection, error) {
	old := *site
	if len(pr.Paths) == 0 && len(pr.Users) == 0 {
		site.Protection = nil
	} else {
		cp := pr
		site.Protection = &cp
	}
	if err := m.writePathFiles(site); err != nil {
		*site = old
		return nil, err
	}
	if err := m.applyWeb(ctx, site, &old); err != nil {
		*site = old
		_ = m.writePathFiles(site)
		return nil, err
	}
	out := &Protection{Paths: []ProtectedPath{}, Users: []AuthUser{}}
	if site.Protection != nil {
		out.Paths = append(out.Paths, site.Protection.Paths...)
		out.Users = append(out.Users, site.Protection.Users...)
	}
	return out, nil
}

func (m *Manager) readHashes(domain string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(m.p(usersFile(domain)))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if n, h, ok := strings.Cut(sc.Text(), ":"); ok {
			out[n] = h
		}
	}
	return out, sc.Err()
}

func htpasswd(hashes map[string]string, names []string) []byte {
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		if h, ok := hashes[n]; ok {
			b.WriteString(n + ":" + h + "\n")
		}
	}
	return []byte(b.String())
}

func (m *Manager) writeHashes(domain string, hashes map[string]string) error {
	names := make([]string, 0, len(hashes))
	for n := range hashes {
		names = append(names, n)
	}
	return m.writeSecretFile(usersFile(domain), htpasswd(hashes, names))
}

// writePathFiles writes one user file per protected path and removes the rest.
func (m *Manager) writePathFiles(site *Site) error {
	dir := htpasswdDir(site.Domain)
	if site.Protection == nil {
		if _, err := os.Stat(m.p(dir)); err == nil {
			return os.RemoveAll(m.p(dir))
		}
		return nil
	}
	hashes, err := m.readHashes(site.Domain)
	if err != nil {
		return err
	}
	keep := map[string]bool{"users": true}
	for _, p := range site.Protection.Paths {
		names := p.Users
		if len(names) == 0 {
			for _, u := range site.Protection.Users {
				names = append(names, u.Name)
			}
		}
		if err := m.writeSecretFile(pathFile(site.Domain, p.ID), htpasswd(hashes, append([]string(nil), names...))); err != nil {
			return err
		}
		keep[p.ID] = true
	}
	entries, _ := os.ReadDir(m.p(dir))
	for _, e := range entries {
		if !keep[e.Name()] {
			_ = os.Remove(filepath.Join(m.p(dir), e.Name()))
		}
	}
	return nil
}

// writeSecretFile writes a file root:www-data 0640 in a root:www-data 0750 folder.
func (m *Manager) writeSecretFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	for _, d := range []string{htpasswdRoot, dir} {
		if err := os.MkdirAll(m.p(d), 0o750); err != nil {
			return err
		}
		_ = os.Chmod(m.p(d), 0o750)
		if err := m.chownWebGroup(d); err != nil {
			return err
		}
	}
	if err := writeFile(m.p(path), data, 0o640); err != nil {
		return err
	}
	return m.chownWebGroup(path)
}

func (m *Manager) chownWebGroup(path string) error {
	if m.cfg.DryRun {
		return nil
	}
	g, err := user.LookupGroup(m.cfg.WebGroup)
	if err != nil {
		return fmt.Errorf("web server group %q: %w", m.cfg.WebGroup, err)
	}
	gid, _ := strconv.Atoi(g.Gid)
	return os.Lchown(m.p(path), 0, gid)
}

// cryptHash is SHA-512 crypt ("$6$..."), which Nginx and Apache both check
// with the system's crypt(3). The mail package adds Dovecot's {SHA512-CRYPT}
// scheme prefix, which web servers don't understand.
func cryptHash(password string) string {
	h := mail.HashPassword(password)
	if i := strings.Index(h, "}"); strings.HasPrefix(h, "{") && i > 0 {
		h = h[i+1:]
	}
	return h
}

// ---------------------------------------------------------------- rendering

// authRule is one protected path, ready for the templates.
type authRule struct {
	ID, Regex, Realm, File, Geo string
	IPs                         []string
}

// applyProtectionLayout fills in the auth rules for the vhost templates.
func (m *Manager) applyProtectionLayout(s *Site, L *layout) {
	if s.Protection == nil || len(s.Protection.Paths) == 0 {
		return
	}
	// Paths are stored longest first; both servers let the last match win,
	// so render them shortest first.
	for i := len(s.Protection.Paths) - 1; i >= 0; i-- {
		p := s.Protection.Paths[i]
		re := "^/"
		if p.Path != "/" {
			re = "^" + regexp.QuoteMeta(strings.TrimSuffix(p.Path, "/")) + "(/|$)"
		}
		L.AuthRules = append(L.AuthRules, authRule{ID: p.ID, Regex: re, Realm: p.Realm, File: pathFile(s.Domain, p.ID),
			Geo: "$ike_auth_ok_" + siteID(s.Domain) + "_" + p.ID, IPs: p.AllowIPs})
		L.AuthAnyIPs = L.AuthAnyIPs || len(p.AllowIPs) > 0
	}
}
