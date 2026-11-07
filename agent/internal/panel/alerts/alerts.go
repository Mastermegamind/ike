// Package alerts sends notifications to Telegram, WhatsApp and email, and runs
// the uptime monitor that decides when to send them.
package alerts

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

type Notifier struct {
	Settings func() store.AlertSettings
	HTTP     *http.Client
	// Base URLs, overridable in tests.
	TelegramBase string // https://api.telegram.org
	GraphBase    string // https://graph.facebook.com
	TermiiBase   string // https://api.ng.termii.com (or the account's base URL)
	ATBase       string // https://api.africastalking.com
}

func (n *Notifier) client() *http.Client {
	if n.HTTP != nil {
		return n.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Send delivers a message on every enabled channel and returns the errors per channel.
func (n *Notifier) Send(ctx context.Context, title, body string) map[string]string {
	s := n.Settings()
	results := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	run := func(name string, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := f()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[name] = err.Error()
				slog.Warn("alert not delivered", "channel", name, "err", err)
			} else {
				results[name] = "sent"
			}
		}()
	}
	if s.Telegram.Enabled {
		run("telegram", func() error { return n.telegram(ctx, s, title, body) })
	}
	if s.WhatsApp.Enabled {
		run("whatsapp", func() error { return n.whatsapp(ctx, s, title, body) })
	}
	if s.SMS.Enabled {
		run("sms", func() error { return n.sms(ctx, s, title, body) })
	}
	if s.Email.Enabled {
		run("email", func() error { return sendEmail(s, title, body) })
	}
	wg.Wait()
	return results
}

func (n *Notifier) postJSON(ctx context.Context, url, bearer string, payload any) error {
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := n.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// smsText fits an alert into two SMS parts (306 characters).
func smsText(title, body string) string {
	t := title
	if body != "" {
		t += ": " + body
	}
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > 306 {
		t = string(r[:305]) + "…"
	}
	return t
}

// smsNumbers turns "+234 803 123 4567, 08031234567" into international
// numbers without the plus (234803...), which both providers accept.
func smsNumbers(list string) ([]string, error) {
	var out []string
	for _, raw := range strings.Split(list, ",") {
		n := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, raw)
		if n == "" {
			continue
		}
		if strings.HasPrefix(n, "0") && len(n) == 11 { // Nigerian local format
			n = "234" + n[1:]
		}
		if len(n) < 10 || len(n) > 15 {
			return nil, fmt.Errorf("%q isn't a phone number", strings.TrimSpace(raw))
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("add at least one phone number")
	}
	return out, nil
}

func (n *Notifier) sms(ctx context.Context, s store.AlertSettings, title, body string) error {
	c := s.SMS
	if c.APIKey == "" {
		return errors.New("the SMS API key is required")
	}
	to, err := smsNumbers(c.To)
	if err != nil {
		return err
	}
	text := smsText(title, body)
	switch c.Provider {
	case "termii":
		base := n.TermiiBase
		if base == "" {
			base = c.BaseURL
		}
		if base == "" {
			base = "https://api.ng.termii.com"
		}
		ch := c.Channel
		if ch == "" {
			ch = "dnd"
		}
		if c.SenderID == "" {
			return errors.New("Termii needs a sender ID")
		}
		var payload map[string]any
		if len(to) == 1 {
			payload = map[string]any{"to": to[0], "from": c.SenderID, "sms": text, "type": "plain", "channel": ch, "api_key": c.APIKey}
		} else {
			payload = map[string]any{"to": to, "from": c.SenderID, "sms": text, "type": "plain", "channel": ch, "api_key": c.APIKey}
		}
		return n.postJSON(ctx, strings.TrimRight(base, "/")+"/api/sms/send", "", payload)
	case "africastalking":
		base := n.ATBase
		if base == "" {
			base = "https://api.africastalking.com"
			if c.Username == "sandbox" {
				base = "https://api.sandbox.africastalking.com"
			}
		}
		if c.Username == "" {
			return errors.New("Africa's Talking needs the app username")
		}
		plus := make([]string, len(to))
		for i, x := range to {
			plus[i] = "+" + x
		}
		form := url.Values{"username": {c.Username}, "to": {strings.Join(plus, ",")}, "message": {text}}
		if c.SenderID != "" {
			form.Set("from", c.SenderID)
		}
		req, _ := http.NewRequestWithContext(ctx, "POST", base+"/version1/messaging", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("apiKey", c.APIKey)
		resp, err := n.client().Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode >= 300 {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		// A 201 can still carry per-number failures.
		var res struct {
			SMSMessageData struct {
				Recipients []struct {
					Number string `json:"number"`
					Status string `json:"status"`
				} `json:"Recipients"`
				Message string `json:"Message"`
			} `json:"SMSMessageData"`
		}
		if json.Unmarshal(raw, &res) == nil {
			if len(res.SMSMessageData.Recipients) == 0 && res.SMSMessageData.Message != "" {
				return errors.New(res.SMSMessageData.Message)
			}
			for _, r := range res.SMSMessageData.Recipients {
				if r.Status != "Success" {
					return fmt.Errorf("%s: %s", r.Number, r.Status)
				}
			}
		}
		return nil
	}
	return errors.New("SMS provider must be termii or africastalking")
}

func (n *Notifier) telegram(ctx context.Context, s store.AlertSettings, title, body string) error {
	if s.Telegram.BotToken == "" || s.Telegram.ChatID == "" {
		return errors.New("bot token and chat id are required")
	}
	base := n.TelegramBase
	if base == "" {
		base = "https://api.telegram.org"
	}
	return n.postJSON(ctx, base+"/bot"+s.Telegram.BotToken+"/sendMessage", "", map[string]any{
		"chat_id": s.Telegram.ChatID, "text": "⚠️ " + title + "\n" + body, "disable_web_page_preview": true,
	})
}

// WhatsApp Cloud API. Free-form text only reaches people who messaged your
// business number in the last 24 hours; set an approved template for alerts
// that must always arrive (its first body parameter receives the message).
func (n *Notifier) whatsapp(ctx context.Context, s store.AlertSettings, title, body string) error {
	w := s.WhatsApp
	if w.Token == "" || w.PhoneNumberID == "" || w.To == "" {
		return errors.New("token, phone number id and recipient are required")
	}
	base := n.GraphBase
	if base == "" {
		base = "https://graph.facebook.com"
	}
	ver := w.APIVersion
	if ver == "" {
		ver = "v21.0"
	}
	msg := map[string]any{"messaging_product": "whatsapp", "to": strings.TrimPrefix(w.To, "+")}
	if w.Template != "" {
		msg["type"] = "template"
		msg["template"] = map[string]any{"name": w.Template, "language": map[string]string{"code": "en"},
			"components": []any{map[string]any{"type": "body", "parameters": []any{map[string]string{"type": "text", "text": title + ": " + body}}}}}
	} else {
		msg["type"] = "text"
		msg["text"] = map[string]string{"body": "⚠️ " + title + "\n" + body}
	}
	return n.postJSON(ctx, base+"/"+ver+"/"+w.PhoneNumberID+"/messages", w.Token, msg)
}

// sendEmail uses implicit TLS on 465 and STARTTLS otherwise; it never sends a
// password over an unencrypted connection.
func sendEmail(s store.AlertSettings, title, body string) error {
	if s.Email.To == "" {
		return errors.New("SMTP host, from and to are required")
	}
	return SendMail(s, "", s.Email.To, "[Ike] "+title, body)
}

// SendMail sends one plain-text email through the alert SMTP server, to
// customers as well as admins. fromName is the display name ("" for none);
// to may list several addresses separated by commas.
func SendMail(s store.AlertSettings, fromName, to, subject, body string) error {
	e := s.Email
	if e.Host == "" || e.From == "" || to == "" {
		return errors.New("SMTP host, from and to are required")
	}
	for _, v := range []string{fromName, to, subject} {
		if strings.ContainsAny(v, "\r\n") {
			return errors.New("email headers can't contain line breaks")
		}
	}
	from := e.From
	if fromName != "" {
		from = (&mail.Address{Name: fromName, Address: e.From}).String()
	}
	port := e.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(e.Host, strconv.Itoa(port))
	msg := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + mime.QEncoding.Encode("utf-8", subject) +
		"\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n") + "\r\n"
	tlsCfg := &tls.Config{ServerName: e.Host, MinVersion: tls.VersionTLS12}
	var c *smtp.Client
	var err error
	if port == 465 {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, tlsCfg)
		if err != nil {
			return err
		}
		c, err = smtp.NewClient(conn, e.Host)
		if err != nil {
			return err
		}
	} else {
		c, err = smtp.Dial(addr)
		if err != nil {
			return err
		}
		if ok, _ := c.Extension("STARTTLS"); !ok {
			c.Close()
			return errors.New("the mail server doesn't offer STARTTLS; refusing to send credentials in plain text")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			c.Close()
			return err
		}
	}
	defer c.Close()
	if e.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", e.Username, e.Password, e.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(e.From); err != nil {
		return err
	}
	for _, rcpt := range strings.Split(to, ",") {
		if err := c.Rcpt(strings.TrimSpace(rcpt)); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// ---------------------------------------------------------------- uptime --

// Target is a website to watch.
type Target struct {
	Domain string
	Status string // only "active" sites are checked
}

// Monitor checks each website through the local web server (Host header) and
// alerts on changes: down after two failed checks in a row, then up again.
type Monitor struct {
	Notifier *Notifier
	Targets  func(ctx context.Context) ([]Target, error)
	LocalURL string // http://127.0.0.1
	HTTP     *http.Client

	mu    sync.Mutex
	fails map[string]int
	down  map[string]time.Time
	Log   []Event
	// days counts checks per website per UTC day: domain → "2006-01-02" →
	// [checks, ok]. Kept 90 days; Snapshot and Restore persist it.
	days    map[string]map[string][2]int
	checked map[string]time.Time
}

// historyDays is how much uptime history status pages show.
const historyDays = 90

// Uptime is one website's state and history.
type Uptime struct {
	Domain    string    `json:"domain"`
	Up        bool      `json:"up"`
	DownSince time.Time `json:"down_since,omitzero"`
	Checked   time.Time `json:"checked,omitzero"`
	// Days are the last 90 days, oldest first; -1 means not checked that day.
	Days    []float64 `json:"days"`
	Percent float64   `json:"percent"` // over the days that were checked; -1 when none
}

func (m *Monitor) record(domain string, ok bool) {
	day := time.Now().UTC().Format("2006-01-02")
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.days == nil {
		m.days = map[string]map[string][2]int{}
	}
	if m.checked == nil {
		m.checked = map[string]time.Time{}
	}
	d := m.days[domain]
	if d == nil {
		d = map[string][2]int{}
		m.days[domain] = d
	}
	c := d[day]
	c[0]++
	if ok {
		c[1]++
	}
	d[day] = c
	m.checked[domain] = time.Now().UTC()
	if len(d) > historyDays+5 {
		cut := time.Now().UTC().AddDate(0, 0, -historyDays).Format("2006-01-02")
		for k := range d {
			if k < cut {
				delete(d, k)
			}
		}
	}
}

// Snapshot copies the history for saving.
func (m *Monitor) Snapshot() map[string]map[string][2]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]map[string][2]int, len(m.days))
	for dom, d := range m.days {
		c := make(map[string][2]int, len(d))
		for k, v := range d {
			c[k] = v
		}
		out[dom] = c
	}
	return out
}

// Restore loads saved history (at start, before the first check).
func (m *Monitor) Restore(h map[string]map[string][2]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.days == nil {
		m.days = map[string]map[string][2]int{}
	}
	for dom, d := range h {
		if m.days[dom] == nil {
			m.days[dom] = map[string][2]int{}
		}
		for k, v := range d {
			m.days[dom][k] = v
		}
	}
}

// Uptimes reports each named website, in the order given.
func (m *Monitor) Uptimes(domains []string) []Uptime {
	m.mu.Lock()
	defer m.mu.Unlock()
	today := time.Now().UTC()
	out := make([]Uptime, 0, len(domains))
	for _, dom := range domains {
		u := Uptime{Domain: dom, Up: true, Checked: m.checked[dom], Percent: -1, Days: make([]float64, historyDays)}
		if since, down := m.down[dom]; down {
			u.Up, u.DownSince = false, since.UTC()
		}
		var checks, oks int
		for i := 0; i < historyDays; i++ {
			day := today.AddDate(0, 0, i-historyDays+1).Format("2006-01-02")
			c, ok := m.days[dom][day]
			if !ok || c[0] == 0 {
				u.Days[i] = -1
				continue
			}
			u.Days[i] = float64(int(float64(c[1])/float64(c[0])*10000)) / 100
			checks += c[0]
			oks += c[1]
		}
		if checks > 0 {
			u.Percent = float64(int(float64(oks)/float64(checks)*10000)) / 100
		}
		out = append(out, u)
	}
	return out
}

type Event struct {
	Time   time.Time `json:"time"`
	Domain string    `json:"domain"`
	Kind   string    `json:"kind"` // down | up
	Detail string    `json:"detail"`
}

func (m *Monitor) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (m *Monitor) Check(ctx context.Context) {
	m.mu.Lock()
	if m.fails == nil {
		m.fails, m.down = map[string]int{}, map[string]time.Time{}
	}
	m.mu.Unlock()
	targets, err := m.Targets(ctx)
	if err != nil {
		return
	}
	base := m.LocalURL
	if base == "" {
		base = "http://127.0.0.1"
	}
	for _, t := range targets {
		if t.Status != "active" {
			continue
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", base+"/", nil)
		req.Host = t.Domain
		req.Header.Set("User-Agent", "ike-uptime/1")
		resp, err := m.client().Do(req)
		ok, detail := err == nil && resp.StatusCode < 500, ""
		if err != nil {
			detail = err.Error()
		} else {
			resp.Body.Close()
			detail = "HTTP " + strconv.Itoa(resp.StatusCode)
		}
		m.record(t.Domain, ok)
		m.mu.Lock()
		if ok {
			since, wasDown := m.down[t.Domain]
			delete(m.down, t.Domain)
			m.fails[t.Domain] = 0
			m.mu.Unlock()
			if wasDown {
				m.event(ctx, Event{time.Now().UTC(), t.Domain, "up", fmt.Sprintf("Back up after %s", time.Since(since).Round(time.Minute))})
			}
			continue
		}
		m.fails[t.Domain]++
		trigger := m.fails[t.Domain] == 2
		if trigger {
			m.down[t.Domain] = time.Now()
		}
		m.mu.Unlock()
		if trigger {
			m.event(ctx, Event{time.Now().UTC(), t.Domain, "down", detail})
		}
	}
}

func (m *Monitor) event(ctx context.Context, e Event) {
	m.mu.Lock()
	m.Log = append(m.Log, e)
	if len(m.Log) > 200 {
		m.Log = m.Log[len(m.Log)-200:]
	}
	m.mu.Unlock()
	title := e.Domain + " is down"
	if e.Kind == "up" {
		title = e.Domain + " is back up"
	}
	if m.Notifier != nil {
		m.Notifier.Send(ctx, title, e.Detail)
	}
}

func (m *Monitor) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.Log))
	for i, e := range m.Log {
		out[len(out)-1-i] = e
	}
	return out
}
