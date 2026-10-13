package sites

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// HealthCheck is a recovery policy for a Node.js/Python app: Ike requests Path
// on the app's local port every IntervalSec, and after FailThreshold failures
// in a row it takes Action. systemd already restarts apps that crash; this
// catches apps that are running but stuck.
type HealthCheck struct {
	Path          string `json:"path"`           // e.g. /health
	IntervalSec   int    `json:"interval_sec"`   // 10-3600
	FailThreshold int    `json:"fail_threshold"` // 1-20
	Action        string `json:"action"`         // restart | notify
}

// HealthEvent is one thing the monitor noticed or did.
type HealthEvent struct {
	Time   time.Time `json:"time"`
	Domain string    `json:"domain"`
	Kind   string    `json:"kind"` // failed | recovered | restarted
	Detail string    `json:"detail"`
}

var healthPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,200}$`)

func (h *HealthCheck) check() error {
	if h.Path == "" {
		h.Path = "/"
	}
	if !healthPathRe.MatchString(h.Path) {
		return fmt.Errorf("health check path must start with / and contain only letters, numbers and ._~/-")
	}
	if h.IntervalSec == 0 {
		h.IntervalSec = 30
	}
	if h.IntervalSec < 10 || h.IntervalSec > 3600 {
		return fmt.Errorf("interval must be 10-3600 seconds")
	}
	if h.FailThreshold == 0 {
		h.FailThreshold = 3
	}
	if h.FailThreshold < 1 || h.FailThreshold > 20 {
		return fmt.Errorf("failure threshold must be 1-20")
	}
	if h.Action == "" {
		h.Action = "restart"
	}
	if h.Action != "restart" && h.Action != "notify" {
		return fmt.Errorf("action must be restart or notify")
	}
	return nil
}

// SetHealthCheck sets or (with nil) removes an app's health check.
func (m *Manager) SetHealthCheck(domain string, hc *HealthCheck) (*Site, error) {
	if hc != nil {
		if err := hc.check(); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if !IsApp(s.Runtime) {
		return nil, fmt.Errorf("health checks are for Node.js and Python apps; PHP websites are checked by the health assistant")
	}
	s.Health = hc
	return s, m.writeJSON(m.layout(s).StateFile, s)
}

// monitor state lives outside Manager's mutex so checks never block changes.
type monitor struct {
	mu      sync.Mutex
	fails   map[string]int
	lastRun map[string]time.Time
	down    map[string]bool
	events  []HealthEvent
	client  *http.Client
	OnEvent func(HealthEvent)
}

var mon = &monitor{fails: map[string]int{}, lastRun: map[string]time.Time{}, down: map[string]bool{},
	client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}

func (mo *monitor) record(e HealthEvent) {
	mo.mu.Lock()
	mo.events = append(mo.events, e)
	if len(mo.events) > 200 {
		mo.events = mo.events[len(mo.events)-200:]
	}
	cb := mo.OnEvent
	mo.mu.Unlock()
	slog.Info("app health", "domain", e.Domain, "kind", e.Kind, "detail", e.Detail)
	if cb != nil {
		cb(e)
	}
}

// HealthEvents returns recent monitor events, newest first.
func (m *Manager) HealthEvents() []HealthEvent {
	mon.mu.Lock()
	defer mon.mu.Unlock()
	out := make([]HealthEvent, len(mon.events))
	for i, e := range mon.events {
		out[len(out)-1-i] = e
	}
	return out
}

// RunHealthMonitor checks apps until ctx ends. Call once from main.
func (m *Manager) RunHealthMonitor(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.healthTick(ctx, time.Now())
		}
	}
}

func (m *Manager) healthTick(ctx context.Context, now time.Time) {
	sites, err := m.List()
	if err != nil {
		return
	}
	for _, s := range sites {
		if s.Health == nil || !IsApp(s.Runtime) || statusOf(s.Status) != StatusActive || s.Port == 0 {
			continue
		}
		mon.mu.Lock()
		due := now.Sub(mon.lastRun[s.Domain]) >= time.Duration(s.Health.IntervalSec)*time.Second
		if due {
			mon.lastRun[s.Domain] = now
		}
		mon.mu.Unlock()
		if due {
			m.probe(ctx, s)
		}
	}
}

func (m *Manager) probe(ctx context.Context, s Site) {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", s.Port, s.Health.Path)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "ike-health/1")
	req.Host = s.Domain
	resp, err := mon.client.Do(req)
	ok := err == nil && resp.StatusCode < 500
	detail := ""
	if err != nil {
		detail = "no answer: " + err.Error()
	} else {
		resp.Body.Close()
		detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	mon.mu.Lock()
	if ok {
		wasDown := mon.down[s.Domain]
		mon.fails[s.Domain], mon.down[s.Domain] = 0, false
		mon.mu.Unlock()
		if wasDown {
			mon.record(HealthEvent{Time: time.Now().UTC(), Domain: s.Domain, Kind: "recovered", Detail: detail})
		}
		return
	}
	mon.fails[s.Domain]++
	n := mon.fails[s.Domain]
	trigger := n == s.Health.FailThreshold
	if trigger {
		mon.down[s.Domain] = true
	}
	mon.mu.Unlock()
	if !trigger {
		return
	}
	mon.record(HealthEvent{Time: time.Now().UTC(), Domain: s.Domain, Kind: "failed",
		Detail: fmt.Sprintf("%s failed %d checks in a row (%s)", s.Health.Path, n, detail)})
	if s.Health.Action == "restart" {
		if _, err := m.run.Run(ctx, "systemctl", "restart", m.layout(&s).UnitName); err == nil {
			mon.record(HealthEvent{Time: time.Now().UTC(), Domain: s.Domain, Kind: "restarted", Detail: "Restarted by the health check"})
		}
		mon.mu.Lock()
		mon.fails[s.Domain] = 0
		mon.mu.Unlock()
	}
}

// SetHealthEventHook lets main forward events (e.g. to alerting).
func SetHealthEventHook(f func(HealthEvent)) {
	mon.mu.Lock()
	mon.OnEvent = f
	mon.mu.Unlock()
}
