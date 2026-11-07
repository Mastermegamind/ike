package sites

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// WAFSettings turn ModSecurity with the OWASP Core Rule Set on for a website.
type WAFSettings struct {
	Mode          string `json:"mode"` // off | detect (log only) | block
	DisabledRules []int  `json:"disabled_rules,omitempty"`
	// Profile turns on the Core Rule Set's exclusions for a known app so it
	// isn't blocked by its own normal requests (Nginx only).
	Profile string `json:"profile,omitempty"`
}

// WAFProfiles are the application exclusion packages in the Core Rule Set.
var WAFProfiles = []string{"wordpress", "drupal", "nextcloud", "dokuwiki", "cpanel", "xenforo", "phpbb", "phpmyadmin"}

// WAFDir holds one rules file per website for Nginx's ModSecurity module.
const WAFDir = "/etc/ike/modsec"

// WAFMain is written by the installer: engine defaults plus the Core Rule Set.
const WAFMain = "/etc/ike/modsec/main.conf"

func (m *Manager) wafFile(domain string) string { return filepath.Join(WAFDir, domain+".conf") }

func (m *Manager) applyWAFLayout(s *Site, L *layout) {
	if s.WAF == nil || s.WAF.Mode == "" || s.WAF.Mode == "off" || !m.cfg.ModSecurity || L.usesCaddy() || L.usesOLS() {
		return
	}
	L.WAFBlock = s.WAF.Mode == "block"
	L.WAFDisabled = s.WAF.DisabledRules
	if L.usesNginx() {
		L.WAFNginx, L.WAFRulesFile = true, m.wafFile(s.Domain)
	} else {
		L.WAFApache = true
	}
}

func renderWAFRules(w *WAFSettings) string {
	var b strings.Builder
	b.WriteString("# Written by Ike. Change it under the website's Security tab.\n")
	if w.Profile != "" {
		fmt.Fprintf(&b, "SecAction \"id:900130,phase:1,nolog,pass,t:none,setvar:tx.crs_exclusions_%s=1\"\n", w.Profile)
	}
	b.WriteString("Include " + WAFMain + "\n")
	if w.Mode == "block" {
		b.WriteString("SecRuleEngine On\n")
	} else {
		b.WriteString("SecRuleEngine DetectionOnly\n")
	}
	for _, id := range w.DisabledRules {
		fmt.Fprintf(&b, "SecRuleRemoveById %d\n", id)
	}
	return b.String()
}

// SetWAF changes a website's firewall mode and rule exclusions.
func (m *Manager) SetWAF(ctx context.Context, domain string, w WAFSettings) (*Site, error) {
	switch w.Mode {
	case "off", "detect", "block":
	default:
		return nil, fmt.Errorf("mode must be off, detect or block")
	}
	if w.Mode != "off" && !m.cfg.ModSecurity {
		return nil, fmt.Errorf("the web application firewall isn't installed; run the installer again with --modsecurity")
	}
	if w.Mode != "off" && (m.cfg.WebStack == "caddy" || m.cfg.WebStack == "openlitespeed") {
		return nil, fmt.Errorf("the web application firewall (ModSecurity) needs Nginx or Apache; this server uses %s", webServerName(m.cfg.WebStack))
	}
	if w.Profile != "" {
		okp := false
		for _, p := range WAFProfiles {
			okp = okp || p == w.Profile
		}
		if !okp {
			return nil, fmt.Errorf("profile must be one of %s", strings.Join(WAFProfiles, ", "))
		}
	}
	if len(w.DisabledRules) > 200 {
		return nil, fmt.Errorf("at most 200 rule exclusions")
	}
	seen := map[int]bool{}
	var rules []int
	for _, id := range w.DisabledRules {
		if id < 1 || id > 9999999 {
			return nil, fmt.Errorf("rule IDs are numbers like 942100")
		}
		if !seen[id] {
			seen[id] = true
			rules = append(rules, id)
		}
	}
	sort.Ints(rules)
	w.DisabledRules = rules
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.get(domain)
	if err != nil {
		return nil, err
	}
	old := *site
	path := m.p(m.wafFile(domain))
	prev, prevErr := os.ReadFile(path)
	if w.Mode == "off" {
		site.WAF = nil
	} else {
		site.WAF = &w
		if err := writeFile(path, []byte(renderWAFRules(&w)), 0o644); err != nil {
			return nil, err
		}
	}
	if err := m.applyWeb(ctx, site, &old); err != nil {
		if prevErr == nil {
			_ = writeFile(path, prev, 0o644)
		}
		return nil, err
	}
	if w.Mode == "off" {
		_ = os.Remove(path)
	}
	return site, nil
}

// WAFEvent is one request ModSecurity flagged or blocked.
type WAFEvent struct {
	Time    string `json:"time"`
	Client  string `json:"client"`
	RuleID  string `json:"rule_id"`
	Message string `json:"message"`
	URI     string `json:"uri"`
	Blocked bool   `json:"blocked"`
}

var (
	wafIDRe     = regexp.MustCompile(`\[id "(\d+)"\]`)
	wafMsgRe    = regexp.MustCompile(`\[msg "([^"]*)"\]`)
	wafURIRe    = regexp.MustCompile(`\[uri "([^"]*)"\]`)
	wafClientRe = regexp.MustCompile(`client: ([0-9a-fA-F.:]+)|\[client ([0-9a-fA-F.:]+)`)
	wafTimeRe   = regexp.MustCompile(`^(\d{4}/\d\d/\d\d \d\d:\d\d:\d\d)|^\[([^\]]+)\]`)
)

// WAFEvents reads the newest ModSecurity messages from the website's error
// logs, newest first. Anomaly-score summary rules (949xxx, 980xxx) are left
// out because they only repeat the rules that caused them.
func (m *Manager) WAFEvents(domain string, limit int) ([]WAFEvent, error) {
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	L := m.layout(site)
	var out []WAFEvent
	for _, name := range []string{"error.log", "apache-error.log"} {
		f, err := os.Open(m.p(filepath.Join(L.LogDir, name)))
		if err != nil {
			continue
		}
		st, _ := f.Stat()
		if st.Size() > 8<<20 {
			_, _ = f.Seek(st.Size()-8<<20, 0)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 256<<10), 256<<10)
		for sc.Scan() {
			l := sc.Text()
			if !strings.Contains(l, "ModSecurity") {
				continue
			}
			ev := WAFEvent{Blocked: strings.Contains(l, "Access denied")}
			if mm := wafIDRe.FindStringSubmatch(l); mm != nil {
				ev.RuleID = mm[1]
			}
			if strings.HasPrefix(ev.RuleID, "949") || strings.HasPrefix(ev.RuleID, "980") {
				if !ev.Blocked {
					continue
				}
			}
			if mm := wafMsgRe.FindStringSubmatch(l); mm != nil {
				ev.Message = mm[1]
			}
			if mm := wafURIRe.FindStringSubmatch(l); mm != nil {
				ev.URI = mm[1]
			}
			if mm := wafClientRe.FindStringSubmatch(l); mm != nil {
				ev.Client = mm[1] + mm[2]
			}
			if mm := wafTimeRe.FindStringSubmatch(l); mm != nil {
				ev.Time = mm[1] + mm[2]
			}
			out = append(out, ev)
		}
		f.Close()
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []WAFEvent{}
	}
	return out, nil
}

var _ = strconv.Itoa
