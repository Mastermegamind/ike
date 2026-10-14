package kit

import (
	"sync"
	"time"
)

// Event is something that happened on the server: a website created, a
// certificate renewed, a backup failed. The panel shows them in the
// notification centre and event webhooks deliver them.
type Event struct {
	ID      int64          `json:"id"`
	Time    time.Time      `json:"time"`
	Type    string         `json:"type"`    // dotted: site.created, dns.zone.changed, backup.failed
	Subject string         `json:"subject"` // the thing it is about: a domain, account or database
	Level   string         `json:"level"`   // info | warn | bad
	Data    map[string]any `json:"data,omitempty"`
}

// Bus keeps recent events in memory and fans them out to subscribers.
type Bus struct {
	mu     sync.Mutex
	next   int64
	recent []Event
	subs   []func(Event)
}

const keepEvents = 1000

func NewBus() *Bus { return &Bus{next: 1} }

// Subscribe registers fn for every future event. fn must not block.
func (b *Bus) Subscribe(fn func(Event)) {
	b.mu.Lock()
	b.subs = append(b.subs, fn)
	b.mu.Unlock()
}

// Publish records an event. The level is taken from the type suffix:
// *.failed and *.down are bad, *.warning and *.expiring are warn.
func (b *Bus) Publish(kind, subject string, data map[string]any) Event {
	b.mu.Lock()
	ev := Event{ID: b.next, Time: Now(), Type: kind, Subject: subject, Level: levelOf(kind), Data: data}
	b.next++
	b.recent = append(b.recent, ev)
	if len(b.recent) > keepEvents {
		b.recent = b.recent[len(b.recent)-keepEvents:]
	}
	subs := append([](func(Event)){}, b.subs...)
	b.mu.Unlock()
	for _, fn := range subs {
		fn(ev)
	}
	return ev
}

// Since returns events newer than id, oldest first.
func (b *Bus) Since(id int64, limit int) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if limit <= 0 || limit > keepEvents {
		limit = 200
	}
	out := []Event{}
	for _, e := range b.recent {
		if e.ID > id {
			out = append(out, e)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func levelOf(kind string) string {
	for _, s := range []string{".failed", ".down", ".error", ".exceeded", ".infected"} {
		if hasSuffix(kind, s) {
			return "bad"
		}
	}
	for _, s := range []string{".warning", ".expiring", ".suspended", ".slow"} {
		if hasSuffix(kind, s) {
			return "warn"
		}
	}
	return "info"
}

func hasSuffix(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }
