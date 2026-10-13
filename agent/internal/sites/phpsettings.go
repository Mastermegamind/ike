package sites

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

// PHPSettings tune one website's PHP-FPM pool and php.ini values. Zero
// values mean "use the default", so an empty struct changes nothing.
type PHPSettings struct {
	PM                  string `json:"pm,omitempty"`           // ondemand (default) | dynamic | static
	MaxChildren         int    `json:"max_children,omitempty"` // default: the account's PHP workers
	StartServers        int    `json:"start_servers,omitempty"`
	MinSpareServers     int    `json:"min_spare_servers,omitempty"`
	MaxSpareServers     int    `json:"max_spare_servers,omitempty"`
	MaxRequests         int    `json:"max_requests,omitempty"`
	MemoryLimitMB       int    `json:"memory_limit_mb,omitempty"`
	UploadMaxMB         int    `json:"upload_max_mb,omitempty"`
	PostMaxMB           int    `json:"post_max_mb,omitempty"`
	MaxExecutionTime    int    `json:"max_execution_time,omitempty"` // seconds
	MaxInputVars        int    `json:"max_input_vars,omitempty"`
	DisplayErrors       bool   `json:"display_errors,omitempty"`
	AllowShellFunctions bool   `json:"allow_shell_functions,omitempty"` // exec, proc_open ... (run as the account user)
	Timezone            string `json:"timezone,omitempty"`
}

var tzRe = regexp.MustCompile(`^[A-Za-z_]+(/[A-Za-z0-9_+-]+){0,2}$|^UTC$`)

func (p *PHPSettings) check(lim Limits) error {
	if p.PM == "" {
		p.PM = "ondemand"
	}
	switch p.PM {
	case "ondemand", "dynamic", "static":
	default:
		return fmt.Errorf("process manager must be ondemand, dynamic or static")
	}
	maxChildren := p.MaxChildren
	if maxChildren == 0 {
		maxChildren = lim.phpWorkers()
	}
	switch {
	case p.MaxChildren < 0 || p.MaxChildren > 200:
		return fmt.Errorf("PHP workers must be 1-200")
	case lim.PHPWorkers > 0 && p.MaxChildren > lim.PHPWorkers:
		return fmt.Errorf("the account allows at most %d PHP workers per website", lim.PHPWorkers)
	case p.MaxRequests < 0 || p.MaxRequests > 100000:
		return fmt.Errorf("max requests must be 0-100000")
	case p.MemoryLimitMB != 0 && (p.MemoryLimitMB < 32 || p.MemoryLimitMB > 4096):
		return fmt.Errorf("memory limit must be 32-4096 MB")
	case lim.MemoryMB > 0 && p.MemoryLimitMB > lim.MemoryMB:
		return fmt.Errorf("memory limit can't be more than the account's %d MB", lim.MemoryMB)
	case p.UploadMaxMB < 0 || p.UploadMaxMB > 10240 || p.PostMaxMB < 0 || p.PostMaxMB > 10240:
		return fmt.Errorf("upload and post sizes must be 1-10240 MB")
	case p.MaxExecutionTime < 0 || p.MaxExecutionTime > 3600:
		return fmt.Errorf("max execution time must be 0-3600 seconds")
	case p.MaxInputVars != 0 && (p.MaxInputVars < 100 || p.MaxInputVars > 100000):
		return fmt.Errorf("max input vars must be 100-100000")
	}
	if p.PM == "dynamic" {
		start, minS, maxS := p.StartServers, p.MinSpareServers, p.MaxSpareServers
		if minS == 0 {
			minS = 1
		}
		if maxS == 0 {
			maxS = max(minS, maxChildren/2)
		}
		if start == 0 {
			start = minS
		}
		if !(minS <= start && start <= maxS && maxS <= maxChildren) {
			return fmt.Errorf("dynamic needs min spare ≤ start servers ≤ max spare ≤ workers (%d)", maxChildren)
		}
	}
	up, post := p.UploadMaxMB, p.PostMaxMB
	if up == 0 {
		up = 64
	}
	if post == 0 {
		post = max(64, up)
	}
	if post < up {
		return fmt.Errorf("post max size (%d MB) must be at least the upload size (%d MB)", post, up)
	}
	if p.Timezone != "" {
		if !tzRe.MatchString(p.Timezone) {
			return fmt.Errorf("unknown timezone %q", p.Timezone)
		}
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			return fmt.Errorf("unknown timezone %q", p.Timezone)
		}
	}
	return nil
}

// applyPHP fills L.PHP with the effective values for the pool and Nginx.
func (L *layout) applyPHP(p *PHPSettings, lim Limits) {
	var e PHPSettings
	if p != nil {
		e = *p
	}
	if e.PM == "" {
		e.PM = "ondemand"
	}
	if e.MaxChildren == 0 {
		e.MaxChildren = L.PHPWorkers
	}
	if lim.PHPWorkers > 0 && e.MaxChildren > lim.PHPWorkers { // the account limit was lowered later
		e.MaxChildren = lim.PHPWorkers
	}
	if e.PM == "dynamic" && e.MaxSpareServers > e.MaxChildren {
		e.MaxSpareServers, e.MinSpareServers, e.StartServers = e.MaxChildren, min(e.MinSpareServers, e.MaxChildren), min(e.StartServers, e.MaxChildren)
	}
	if e.MaxRequests == 0 {
		e.MaxRequests = 500
	}
	if e.MemoryLimitMB == 0 {
		e.MemoryLimitMB = L.PHPMemoryMB
	}
	if e.UploadMaxMB == 0 {
		e.UploadMaxMB = 64
	}
	if e.PostMaxMB == 0 {
		e.PostMaxMB = max(64, e.UploadMaxMB)
	}
	if e.MaxExecutionTime == 0 {
		e.MaxExecutionTime = 60
	}
	if e.MaxInputVars == 0 {
		e.MaxInputVars = 3000
	}
	if e.PM == "dynamic" {
		if e.MinSpareServers == 0 {
			e.MinSpareServers = 1
		}
		if e.MaxSpareServers == 0 {
			e.MaxSpareServers = max(e.MinSpareServers, e.MaxChildren/2)
		}
		if e.StartServers == 0 {
			e.StartServers = e.MinSpareServers
		}
	}
	L.PHP = e
	L.PHPWorkers, L.PHPMemoryMB = e.MaxChildren, e.MemoryLimitMB
	L.PHPReadTimeout = max(300, e.MaxExecutionTime+30)
	L.BodyLimitMB = 64
	if L.Runtime == RuntimePHP {
		L.BodyLimitMB = e.PostMaxMB + 1
	}
}

// SetPHPSettings changes a PHP website's pool and php.ini values.
func (m *Manager) SetPHPSettings(ctx context.Context, domain string, p PHPSettings) (*Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if site.Runtime != RuntimePHP {
		return nil, fmt.Errorf("%s doesn't use PHP", domain)
	}
	a, err := m.getAccount(site.Account)
	if err != nil {
		return nil, err
	}
	if err := p.check(a.Limits); err != nil {
		return nil, err
	}
	old := *site
	site.PHP = &p
	if err := m.applyWeb(ctx, site, &old); err != nil {
		return nil, err
	}
	return site, nil
}
