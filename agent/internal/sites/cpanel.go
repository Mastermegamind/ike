package sites

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ImportPlan is what Ike found in a cPanel backup (cpmove-<user>.tar.gz) and
// how it would map onto an Ike account. Reading the backup never extracts
// anything; it only walks the archive's headers and a few small text files.
type ImportPlan struct {
	File       string       `json:"file"`
	CpanelUser string       `json:"cpanel_user"`
	Account    string       `json:"account"` // suggested Ike account name
	MainDomain string       `json:"main_domain"`
	Websites   []ImportSite `json:"websites"`
	Databases  []string     `json:"databases"`
	Mailboxes  []string     `json:"mailboxes"`
	CronJobs   int          `json:"cron_jobs"`
	HomeSizeMB float64      `json:"home_size_mb"`
	Warnings   []string     `json:"warnings"`
}

type ImportSite struct {
	Domain    string `json:"domain"`
	Kind      string `json:"kind"` // main | addon | subdomain | alias
	PHP       string `json:"php,omitempty"`
	PHPOnHere bool   `json:"php_installed"`
	DocRoot   string `json:"doc_root,omitempty"`
}

var (
	eaPHPRe      = regexp.MustCompile(`ea-php(\d)(\d)`)
	importNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+\.tar(\.gz)?$`)
)

// InspectCpanelBackup reads a backup placed in the imports folder.
func (m *Manager) InspectCpanelBackup(name string) (*ImportPlan, error) {
	if !importNameRe.MatchString(name) || strings.Contains(name, "..") {
		return nil, fmt.Errorf("give the backup's file name, e.g. cpmove-adaeze.tar.gz")
	}
	path := m.p(filepath.Join(m.cfg.ImportsDir, name))
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%s isn't in %s; upload it there first", name, m.cfg.ImportsDir)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s isn't a regular file", name)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(name, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("not a gzip file: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	plan := &ImportPlan{File: name, Databases: []string{}, Mailboxes: []string{}, Warnings: []string{}}
	userdata := map[string]string{}
	var main string
	dbs := map[string]bool{}
	var homeBytes int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the archive is damaged: %w", err)
		}
		// Strip the top folder (cpmove-user/ or user/).
		parts := strings.SplitN(strings.TrimPrefix(h.Name, "./"), "/", 2)
		if len(parts) < 2 {
			continue
		}
		top, rel := parts[0], parts[1]
		if plan.CpanelUser == "" {
			plan.CpanelUser = strings.TrimPrefix(top, "cpmove-")
		}
		small := h.Typeflag == tar.TypeReg && h.Size < 1<<20
		read := func() string {
			if !small {
				return ""
			}
			b, _ := io.ReadAll(io.LimitReader(tr, 1<<20))
			return string(b)
		}
		switch {
		case rel == "userdata/main":
			main = read()
		case strings.HasPrefix(rel, "userdata/") && !strings.Contains(rel[9:], "/") && !strings.HasSuffix(rel, ".cache") &&
			!strings.HasSuffix(rel, "_SSL") && rel != "userdata/main" && rel != "userdata/cache":
			userdata[rel[9:]] = read()
		case strings.HasPrefix(rel, "mysql/") && (strings.HasSuffix(rel, ".sql") || strings.HasSuffix(rel, ".create")):
			db := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(rel), ".sql"), ".create")
			dbs[db] = true
		case strings.HasPrefix(rel, "homedir/etc/") && strings.HasSuffix(rel, "/passwd"):
			domain := filepath.Base(filepath.Dir(rel))
			for _, l := range strings.Split(read(), "\n") {
				if u, _, ok := strings.Cut(l, ":"); ok && u != "" {
					plan.Mailboxes = append(plan.Mailboxes, u+"@"+domain)
				}
			}
		case strings.HasPrefix(rel, "cron/"):
			for _, l := range strings.Split(read(), "\n") {
				l = strings.TrimSpace(l)
				if l != "" && !strings.HasPrefix(l, "#") && !strings.Contains(l, "=") {
					plan.CronJobs++
				}
			}
		}
		if strings.HasPrefix(rel, "homedir/") && h.Typeflag == tar.TypeReg {
			homeBytes += h.Size
		}
	}
	plan.HomeSizeMB = float64(homeBytes) / (1 << 20)
	if plan.CpanelUser == "" {
		return nil, fmt.Errorf("this doesn't look like a cPanel backup")
	}
	for db := range dbs {
		plan.Databases = append(plan.Databases, db)
	}
	sort.Strings(plan.Databases)
	sort.Strings(plan.Mailboxes)

	// userdata/main lists the domains.
	installed := map[string]bool{}
	for _, v := range phpInstalled(m) {
		installed[v] = true
	}
	addSite := func(domain, kind string) {
		s := ImportSite{Domain: domain, Kind: kind}
		if ud, ok := userdata[domain]; ok {
			for _, l := range strings.Split(ud, "\n") {
				k, v, _ := strings.Cut(strings.TrimSpace(l), ":")
				v = strings.Trim(strings.TrimSpace(v), `'"`)
				switch k {
				case "phpversion":
					if mm := eaPHPRe.FindStringSubmatch(v); mm != nil {
						s.PHP = mm[1] + "." + mm[2]
					}
				case "documentroot":
					s.DocRoot = v
				}
			}
		}
		s.PHPOnHere = s.PHP == "" || installed[s.PHP]
		if s.PHP != "" && !s.PHPOnHere {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s uses PHP %s, which isn't installed here", domain, s.PHP))
		}
		plan.Websites = append(plan.Websites, s)
	}
	// userdata/main is YAML: top-level keys, with indented entries under
	// addon_domains (addon: backing-subdomain), parked_domains and sub_domains.
	section := ""
	var addons, parked, subs []string
	backing := map[string]bool{}
	for _, l := range strings.Split(main, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || t == "---" {
			continue
		}
		if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "-") {
			k, v, _ := strings.Cut(t, ":")
			v = strings.Trim(strings.TrimSpace(v), `'"`)
			section = ""
			if v == "" {
				section = k
			}
			if k == "main_domain" {
				plan.MainDomain = v
			}
			continue
		}
		item := strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "- ")), `'"`)
		switch section {
		case "addon_domains":
			if d, sub, ok := strings.Cut(t, ":"); ok {
				addons = append(addons, strings.Trim(d, `'" `))
				backing[strings.Trim(strings.TrimSpace(sub), `'"`)] = true
			}
		case "parked_domains":
			parked = append(parked, item)
		case "sub_domains":
			subs = append(subs, item)
		}
	}
	if plan.MainDomain != "" {
		addSite(plan.MainDomain, "main")
	}
	for _, d := range addons {
		addSite(d, "addon")
	}
	for _, d := range subs {
		if !backing[d] { // cPanel creates a hidden subdomain behind every addon domain
			addSite(d, "subdomain")
		}
	}
	for _, d := range parked {
		addSite(d, "alias")
	}

	acct := strings.ToLower(regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(strings.ToLower(plan.CpanelUser), ""))
	if acct == "" || acct[0] < 'a' || acct[0] > 'z' {
		acct = "c" + acct
	}
	if len(acct) > 16 {
		acct = acct[:16]
	}
	plan.Account = acct
	if _, err := m.getAccount(acct); err == nil {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("An account named %s already exists; the import would use a new name", acct))
	}
	if len(plan.Mailboxes) > 0 {
		plan.Warnings = append(plan.Warnings, "Mailboxes are listed; importing mail arrives with the email phase")
	}
	return plan, nil
}

func phpInstalled(m *Manager) []string {
	matches, _ := filepath.Glob(m.p("/etc/php/*/fpm"))
	var out []string
	for _, x := range matches {
		out = append(out, filepath.Base(filepath.Dir(x)))
	}
	return out
}
