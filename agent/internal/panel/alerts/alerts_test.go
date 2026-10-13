package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/YOUR-ORG/ike/agent/internal/panel/store"
)

func TestChannelsAndMonitor(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, r.URL.Path+" "+r.Header.Get("Authorization")+" "+toJSON(body))
		mu.Unlock()
	}))
	defer api.Close()
	var s store.AlertSettings
	s.Telegram.Enabled, s.Telegram.BotToken, s.Telegram.ChatID = true, "123:abc", "-100200"
	s.WhatsApp.Enabled, s.WhatsApp.Token, s.WhatsApp.PhoneNumberID, s.WhatsApp.To = true, "EAAG", "1098", "+2348012345678"
	n := &Notifier{Settings: func() store.AlertSettings { return s }, TelegramBase: api.URL, GraphBase: api.URL}

	res := n.Send(context.Background(), "shop.ng is down", "HTTP 502")
	if res["telegram"] != "sent" || res["whatsapp"] != "sent" {
		t.Fatalf("%v", res)
	}
	all := strings.Join(calls, "\n")
	for _, want := range []string{"/bot123:abc/sendMessage", "/v21.0/1098/messages Bearer EAAG", `"to":"2348012345678"`, "shop.ng is down"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}

	// Uptime: a site that returns 502 twice is reported down, then up.
	status := 502
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "shop.ng" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(status)
	}))
	defer web.Close()
	calls = nil
	m := &Monitor{Notifier: n, LocalURL: web.URL, Targets: func(context.Context) ([]Target, error) {
		return []Target{{"shop.ng", "active"}, {"paused.ng", "maintenance"}}, nil
	}}
	m.Check(context.Background())
	if len(m.Events()) != 0 {
		t.Error("one failure must not alert")
	}
	m.Check(context.Background())
	status = 200
	m.Check(context.Background())
	ev := m.Events()
	if len(ev) != 2 || ev[0].Kind != "up" || ev[1].Kind != "down" {
		t.Fatalf("%+v", ev)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "shop.ng is back up") {
		t.Error("recovery not announced")
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
