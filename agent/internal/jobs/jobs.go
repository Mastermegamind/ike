// Package jobs runs long operations (package upgrades, staging copies, self
// updates) in the background so API calls return at once. Callers poll a job by id.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	Running Status = "running"
	Done    Status = "done"
	Failed  Status = "failed"
)

// Job is a snapshot of one background operation.
type Job struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"` // "os-upgrade", "ike-update", "staging", ...
	Title      string     `json:"title"`
	Status     Status     `json:"status"`
	Error      string     `json:"error,omitempty"`
	Log        []string   `json:"log"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

const maxLog = 400
const keep = 50

type Manager struct {
	mu      sync.Mutex
	jobs    map[string]*Job
	running map[string]string // kind -> job id, so two upgrades never overlap
}

func NewManager() *Manager {
	return &Manager{jobs: map[string]*Job{}, running: map[string]string{}}
}

// Logger appends a line to a running job's log.
type Logger func(format string, args ...any)

// Start runs fn in the background. Only one job of each kind runs at a time.
func (m *Manager) Start(kind, title string, timeout time.Duration, fn func(ctx context.Context, log Logger) error) (*Job, error) {
	m.mu.Lock()
	if id, busy := m.running[kind]; busy {
		m.mu.Unlock()
		return nil, fmt.Errorf("%s is already running (job %s)", title, id)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	j := &Job{ID: hex.EncodeToString(b), Kind: kind, Title: title, Status: Running, Log: []string{}, StartedAt: time.Now().UTC()}
	m.jobs[j.ID] = j
	m.running[kind] = j.ID
	m.prune()
	snap := *j
	m.mu.Unlock()

	log := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, l := range strings.Split(strings.TrimRight(line, "\n"), "\n") {
			j.Log = append(j.Log, l)
		}
		if len(j.Log) > maxLog {
			j.Log = append([]string{"…"}, j.Log[len(j.Log)-maxLog:]...)
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := fn(ctx, log)
		m.mu.Lock()
		defer m.mu.Unlock()
		now := time.Now().UTC()
		j.FinishedAt = &now
		j.Status = Done
		if err != nil {
			j.Status, j.Error = Failed, err.Error()
			slog.Error("job failed", "kind", kind, "id", j.ID, "err", err)
		} else {
			slog.Info("job done", "kind", kind, "id", j.ID)
		}
		delete(m.running, kind)
	}()
	return &snap, nil
}

// Get returns a copy of a job.
func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, false
	}
	c := *j
	c.Log = append([]string(nil), j.Log...)
	return &c, true
}

// List returns jobs, newest first.
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		c := *j
		c.Log = nil
		out = append(out, c)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].StartedAt.After(out[b].StartedAt) })
	return out
}

// Wait blocks until a job finishes (tests and CLI use).
func (m *Manager) Wait(id string, max time.Duration) *Job {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if j, ok := m.Get(id); ok && j.Status != Running {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, _ := m.Get(id)
	return j
}

func (m *Manager) prune() {
	if len(m.jobs) <= keep {
		return
	}
	var done []*Job
	for _, j := range m.jobs {
		if j.Status != Running {
			done = append(done, j)
		}
	}
	sort.Slice(done, func(a, b int) bool { return done[a].StartedAt.Before(done[b].StartedAt) })
	for i := 0; i < len(done) && len(m.jobs) > keep; i++ {
		delete(m.jobs, done[i].ID)
	}
}
