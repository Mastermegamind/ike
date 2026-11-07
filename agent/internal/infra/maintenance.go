package infra

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
)

// ---- maintenance windows ----

// Window is planned maintenance: websites show the maintenance page, updates
// can be installed and the server rebooted, then everything comes back.
type Window struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Start     time.Time         `json:"start"`
	End       time.Time         `json:"end"`
	Sites     []string          `json:"sites"` // empty = every website; ["-"] = none
	Updates   bool              `json:"updates"`
	Reboot    bool              `json:"reboot"`
	State     string            `json:"state"` // scheduled | active | done | cancelled
	Previous  map[string]string `json:"previous,omitempty"`
	Log       []string          `json:"log,omitempty"`
	CreatedBy string            `json:"created_by,omitempty"`
}

var titleRe = regexp.MustCompile(`^[\p{L}\p{N} .,:;'()/&+-]{1,100}$`)

func (m *Manager) Windows() ([]Window, error) {
	w, err := m.maint.Load()
	if w == nil {
		w = []Window{}
	}
	return w, err
}

func (m *Manager) AddWindow(w Window, actor string) (Window, error) {
	if !titleRe.MatchString(w.Title) {
		return w, fmt.Errorf("give the window a short title")
	}
	w.Start, w.End = w.Start.UTC().Truncate(time.Minute), w.End.UTC().Truncate(time.Minute)
	if w.Start.Before(time.Now().Add(-time.Minute)) {
		return w, fmt.Errorf("the start is in the past")
	}
	if !w.End.After(w.Start) || w.End.Sub(w.Start) > 24*time.Hour {
		return w, fmt.Errorf("a window lasts up to 24 hours and ends after it starts")
	}
	if len(w.Sites) > 1000 {
		return w, fmt.Errorf("too many websites")
	}
	w.ID, w.State, w.CreatedBy, w.Previous, w.Log = kit.RandomHex(5), "scheduled", actor, nil, nil
	_, err := m.maint.Update(func(ws *[]Window) error {
		for _, o := range *ws {
			if (o.State == "scheduled" || o.State == "active") && w.Start.Before(o.End) && o.Start.Before(w.End) {
				return fmt.Errorf("overlaps %q", o.Title)
			}
		}
		*ws = append(*ws, w)
		return nil
	})
	if err == nil {
		m.env.Publish("maintenance.scheduled", w.Title, map[string]any{"start": w.Start, "end": w.End})
	}
	return w, err
}

func (m *Manager) CancelWindow(ctx context.Context, id string) error {
	var active *Window
	_, err := m.maint.Update(func(ws *[]Window) error {
		for i := range *ws {
			if (*ws)[i].ID == id {
				if (*ws)[i].State == "active" {
					c := (*ws)[i]
					active = &c
				}
				if (*ws)[i].State == "scheduled" || (*ws)[i].State == "active" {
					(*ws)[i].State = "cancelled"
				}
				return nil
			}
		}
		return fmt.Errorf("maintenance window %s %w", id, kit.ErrNotFound)
	})
	if err == nil && active != nil {
		m.endWindow(ctx, active)
	}
	return err
}

// RunMaintenance starts and ends windows on time.
func (m *Manager) RunMaintenance(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			ws, _ := m.maint.Load()
			for i := range ws {
				w := ws[i]
				switch {
				case w.State == "scheduled" && !now.Before(w.Start) && now.Before(w.End):
					m.startWindow(ctx, &w)
				case w.State == "active" && !now.Before(w.End):
					m.endWindow(ctx, &w)
				case w.State == "scheduled" && !now.Before(w.End):
					m.setWindow(w.ID, func(x *Window) { x.State = "done"; x.Log = append(x.Log, "missed: the server was off") })
				}
			}
		}
	}
}

func (m *Manager) setWindow(id string, fn func(*Window)) {
	_, _ = m.maint.Update(func(ws *[]Window) error {
		for i := range *ws {
			if (*ws)[i].ID == id {
				fn(&(*ws)[i])
			}
		}
		return nil
	})
}

func (m *Manager) windowSites(w *Window) []string {
	if len(w.Sites) == 1 && w.Sites[0] == "-" {
		return nil
	}
	if len(w.Sites) > 0 {
		return w.Sites
	}
	if m.AllSites != nil {
		return m.AllSites()
	}
	return nil
}

func (m *Manager) startWindow(ctx context.Context, w *Window) {
	logs := []string{}
	prev := map[string]string{}
	if sites := m.windowSites(w); len(sites) > 0 && m.SetMaintenance != nil {
		p, err := m.SetMaintenance(ctx, sites, true)
		prev = p
		if err != nil {
			logs = append(logs, "maintenance pages: "+err.Error())
		} else {
			logs = append(logs, fmt.Sprintf("%d websites show the maintenance page", len(sites)))
		}
	}
	m.setWindow(w.ID, func(x *Window) { x.State, x.Previous, x.Log = "active", prev, append(x.Log, logs...) })
	m.env.Publish("maintenance.started", w.Title, map[string]any{"end": w.End})
	if w.Updates && m.ApplyUpdates != nil {
		go func() {
			err := m.ApplyUpdates(context.WithoutCancel(ctx), w.Reboot)
			msg := "updates installed"
			if err != nil {
				msg = "updates failed: " + err.Error()
			}
			m.setWindow(w.ID, func(x *Window) { x.Log = append(x.Log, msg) })
		}()
	}
}

func (m *Manager) endWindow(ctx context.Context, w *Window) {
	var logs []string
	if m.SetMaintenance != nil && len(w.Previous) > 0 {
		var sites []string
		for s := range w.Previous {
			sites = append(sites, s)
		}
		if _, err := m.SetMaintenance(ctx, sites, false); err != nil {
			logs = append(logs, "restoring websites: "+err.Error())
		} else {
			logs = append(logs, "websites are back")
		}
	}
	m.setWindow(w.ID, func(x *Window) {
		if x.State == "active" {
			x.State = "done"
		}
		x.Log = append(x.Log, logs...)
	})
	m.env.Publish("maintenance.ended", w.Title, nil)
}

// ---- event webhooks ----

// Webhook sends server events to a URL, signed with HMAC-SHA256.
type Webhook struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Secret    string    `json:"secret,omitempty"`
	Events    []string  `json:"events"` // patterns: "*", "backup.*", "site.created"
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// Delivery is one attempt to send an event.
type Delivery struct {
	Webhook string    `json:"webhook"`
	Event   int64     `json:"event"`
	Type    string    `json:"type"`
	Status  int       `json:"status"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
	Attempt int       `json:"attempt"`
}

type delivery struct {
	hook Webhook
	ev   kit.Event
}

var eventPatternRe = regexp.MustCompile(`^(\*|[a-z0-9_]+(\.[a-z0-9_*]+)*)$`)

func (m *Manager) Webhooks() ([]Webhook, error) {
	hs, err := m.hooks.Load()
	out := []Webhook{}
	for _, h := range hs {
		h.Secret = ""
		out = append(out, h)
	}
	return out, err
}

// SaveWebhook adds or changes a webhook. A new webhook's secret is returned
// once so the receiver can verify signatures.
func (m *Manager) SaveWebhook(h Webhook) (Webhook, error) {
	u, err := url.Parse(h.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return h, fmt.Errorf("the URL must start with https://")
	}
	if host := u.Hostname(); host == "localhost" || isPrivate(host) {
		return h, fmt.Errorf("webhooks go to public addresses only")
	}
	if len(h.Events) == 0 {
		h.Events = []string{"*"}
	}
	for _, e := range h.Events {
		if !eventPatternRe.MatchString(e) {
			return h, fmt.Errorf("event %q: use names like backup.failed, backup.* or *", e)
		}
	}
	var out Webhook
	_, err = m.hooks.Update(func(hs *[]Webhook) error {
		for i := range *hs {
			if h.ID != "" && (*hs)[i].ID == h.ID {
				h.Secret, h.CreatedAt = (*hs)[i].Secret, (*hs)[i].CreatedAt
				(*hs)[i] = h
				out = h
				out.Secret = ""
				return nil
			}
		}
		if len(*hs) >= 50 {
			return fmt.Errorf("at most 50 webhooks")
		}
		h.ID, h.Secret, h.CreatedAt = kit.RandomHex(5), "whsec_"+kit.RandomHex(24), kit.Now()
		*hs = append(*hs, h)
		out = h
		return nil
	})
	return out, err
}

func (m *Manager) DeleteWebhook(id string) error {
	_, err := m.hooks.Update(func(hs *[]Webhook) error {
		for i := range *hs {
			if (*hs)[i].ID == id {
				*hs = append((*hs)[:i], (*hs)[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("webhook %s %w", id, kit.ErrNotFound)
	})
	return err
}

func isPrivate(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

func matches(patterns []string, typ string) bool {
	for _, p := range patterns {
		if p == "*" || p == typ {
			return true
		}
		if ok, _ := path.Match(strings.ReplaceAll(p, ".", "/"), strings.ReplaceAll(typ, ".", "/")); ok {
			return true
		}
		if strings.HasSuffix(p, ".*") && strings.HasPrefix(typ, strings.TrimSuffix(p, "*")) {
			return true
		}
	}
	return false
}

// OnEvent queues deliveries for matching webhooks (subscribed to the bus).
func (m *Manager) OnEvent(ev kit.Event) {
	if strings.HasPrefix(ev.Type, "webhook.") {
		return
	}
	hs, _ := m.hooks.Load()
	for _, h := range hs {
		if h.Enabled && matches(h.Events, ev.Type) {
			select {
			case m.deliveries <- delivery{h, ev}:
			default:
				slog.Warn("webhook queue full; event dropped", "type", ev.Type)
			}
		}
	}
}

// Sign returns the X-Ike-Signature header value for a body.
func Sign(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

var webhookHTTP = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// RunWebhooks sends queued events, retrying three times with backoff.
func (m *Manager) RunWebhooks(ctx context.Context) {
	var mu sync.Mutex
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-m.deliveries:
			body, _ := json.Marshal(map[string]any{"id": d.ev.ID, "type": d.ev.Type, "subject": d.ev.Subject, "level": d.ev.Level,
				"time": d.ev.Time, "data": d.ev.Data, "server": m.env.Cfg.Hostname})
			for attempt := 1; attempt <= 3; attempt++ {
				ts := time.Now().Unix()
				req, _ := http.NewRequestWithContext(ctx, "POST", d.hook.URL, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("User-Agent", "Ike-Webhooks/1")
				req.Header.Set("X-Ike-Event", d.ev.Type)
				req.Header.Set("X-Ike-Signature", Sign(d.hook.Secret, ts, body))
				rec := Delivery{Webhook: d.hook.ID, Event: d.ev.ID, Type: d.ev.Type, At: kit.Now(), Attempt: attempt}
				resp, err := webhookHTTP.Do(req)
				if err != nil {
					rec.Error = err.Error()
				} else {
					rec.Status = resp.StatusCode
					resp.Body.Close()
				}
				mu.Lock()
				m.deliveryLog = append(m.deliveryLog, rec)
				if len(m.deliveryLog) > 500 {
					m.deliveryLog = m.deliveryLog[len(m.deliveryLog)-500:]
				}
				mu.Unlock()
				if err == nil && rec.Status < 300 {
					break
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(attempt*attempt) * 10 * time.Second):
				}
			}
		}
	}
}

// Deliveries returns the newest delivery attempts.
func (m *Manager) Deliveries() []Delivery {
	out := append([]Delivery{}, m.deliveryLog...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// TestWebhook sends a test event right away.
func (m *Manager) TestWebhook(id string) error {
	hs, _ := m.hooks.Load()
	for _, h := range hs {
		if h.ID == id {
			select {
			case m.deliveries <- delivery{h, kit.Event{ID: 0, Type: "webhook.test", Subject: m.env.Cfg.Hostname, Level: "info", Time: kit.Now()}}:
				return nil
			default:
				return fmt.Errorf("the delivery queue is full")
			}
		}
	}
	return fmt.Errorf("webhook %s %w", id, kit.ErrNotFound)
}
