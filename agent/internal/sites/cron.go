package sites

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/YOUR-ORG/ike/agent/internal/validate"
)

// CronJob runs a command on a schedule as the account's own user.
type CronJob struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule"` // "*/5 * * * *" or @hourly, @daily, @weekly, @monthly, @yearly, @reboot
	Command  string `json:"command"`
	Comment  string `json:"comment,omitempty"`
	Enabled  bool   `json:"enabled"`
}

const maxCronJobs = 50

var (
	cronField    = regexp.MustCompile(`^(\*|[0-9]{1,2}(-[0-9]{1,2})?)(/[0-9]{1,2})?(,(\*|[0-9]{1,2}(-[0-9]{1,2})?)(/[0-9]{1,2})?)*$`)
	cronNames    = regexp.MustCompile(`^(?i)(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec|sun|mon|tue|wed|thu|fri|sat)(-(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec|sun|mon|tue|wed|thu|fri|sat))?$`)
	cronMacros   = map[string]bool{"@hourly": true, "@daily": true, "@weekly": true, "@monthly": true, "@yearly": true, "@annually": true, "@reboot": true}
	cronComment  = regexp.MustCompile(`^[A-Za-z0-9 ._,:()/-]{0,80}$`)
	cronIDRe     = regexp.MustCompile(`^[a-f0-9]{8}$`)
	cronFieldMax = []int{59, 23, 31, 12, 7}
)

// ValidateSchedule accepts standard 5-field cron expressions and @macros.
func ValidateSchedule(s string) error {
	s = strings.TrimSpace(s)
	if cronMacros[strings.ToLower(s)] {
		return nil
	}
	f := strings.Fields(s)
	if len(f) != 5 {
		return fmt.Errorf("schedule needs 5 fields (minute hour day month weekday), e.g. \"30 2 * * *\"")
	}
	for i, field := range f {
		if (i == 3 || i == 4) && cronNames.MatchString(field) {
			continue
		}
		if !cronField.MatchString(field) {
			return fmt.Errorf("schedule field %d (%q) is not valid", i+1, field)
		}
		for _, n := range regexp.MustCompile(`[0-9]+`).FindAllString(field, -1) {
			var v int
			fmt.Sscan(n, &v)
			if v > cronFieldMax[i] && !strings.Contains(field, "/"+n) {
				return fmt.Errorf("schedule field %d: %d is out of range", i+1, v)
			}
		}
	}
	return nil
}

func validateCronCommand(c string) error {
	if strings.TrimSpace(c) == "" {
		return fmt.Errorf("command is required")
	}
	if len(c) > 500 {
		return fmt.Errorf("command must be under 500 characters")
	}
	for _, r := range c {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("command must be a single line")
		}
	}
	return nil
}

// CronRequest creates or edits a job.
type CronRequest struct {
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Comment  string `json:"comment,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"` // default true
}

func (r CronRequest) check() error {
	if err := ValidateSchedule(r.Schedule); err != nil {
		return err
	}
	if err := validateCronCommand(r.Command); err != nil {
		return err
	}
	if !cronComment.MatchString(r.Comment) {
		return fmt.Errorf("note may only contain letters, numbers and basic punctuation (max 80)")
	}
	return nil
}

// CronJobs lists an account's jobs.
func (m *Manager) CronJobs(account string) ([]CronJob, error) {
	a, err := m.GetAccount(account)
	if err != nil {
		return nil, err
	}
	if a.Cron == nil {
		return []CronJob{}, nil
	}
	return a.Cron, nil
}

// AddCronJob adds a job and reinstalls the account's cron file.
func (m *Manager) AddCronJob(account string, req CronRequest) (*CronJob, error) {
	if err := validate.Username(account); err != nil {
		return nil, err
	}
	if err := req.check(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.getAccount(account)
	if err != nil {
		return nil, err
	}
	if len(a.Cron) >= maxCronJobs {
		return nil, fmt.Errorf("an account can have at most %d cron jobs", maxCronJobs)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	job := CronJob{ID: hex.EncodeToString(b), Schedule: strings.TrimSpace(req.Schedule), Command: req.Command,
		Comment: req.Comment, Enabled: req.Enabled == nil || *req.Enabled}
	a.Cron = append(a.Cron, job)
	if err := m.saveAccount(a); err != nil {
		return nil, err
	}
	return &job, m.writeCron(account)
}

// UpdateCronJob edits a job.
func (m *Manager) UpdateCronJob(account, id string, req CronRequest) (*CronJob, error) {
	if err := validate.Username(account); err != nil {
		return nil, err
	}
	if !cronIDRe.MatchString(id) {
		return nil, fmt.Errorf("cron job %s %w", id, ErrNotFound)
	}
	if err := req.check(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.getAccount(account)
	if err != nil {
		return nil, err
	}
	for i := range a.Cron {
		if a.Cron[i].ID == id {
			j := &a.Cron[i]
			j.Schedule, j.Command, j.Comment = strings.TrimSpace(req.Schedule), req.Command, req.Comment
			if req.Enabled != nil {
				j.Enabled = *req.Enabled
			}
			out := *j
			if err := m.saveAccount(a); err != nil {
				return nil, err
			}
			return &out, m.writeCron(account)
		}
	}
	return nil, fmt.Errorf("cron job %s %w", id, ErrNotFound)
}

// DeleteCronJob removes a job.
func (m *Manager) DeleteCronJob(account, id string) error {
	if err := validate.Username(account); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.getAccount(account)
	if err != nil {
		return err
	}
	out := a.Cron[:0]
	found := false
	for _, j := range a.Cron {
		if j.ID == id {
			found = true
			continue
		}
		out = append(out, j)
	}
	if !found {
		return fmt.Errorf("cron job %s %w", id, ErrNotFound)
	}
	a.Cron = out
	if err := m.saveAccount(a); err != nil {
		return err
	}
	return m.writeCron(account)
}

func (m *Manager) cronFile(account string) string {
	return m.p(filepath.Join(m.cfg.CronDir, "ike-"+account))
}

// cronBody renders an account's cron file, or nil when it should not exist.
// The file is root-owned; every line names the account user, so jobs run with
// that user's permissions only.
func (m *Manager) cronBody(a *Account) []byte {
	if a.Suspended || len(a.Cron) == 0 {
		return nil
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Managed by Ike for account %s. Edit jobs in the panel, not here.\n", a.Name)
	b.WriteString("SHELL=/bin/bash\nPATH=/usr/local/bin:/usr/bin:/bin\nMAILTO=\"\"\n\n")
	for _, j := range a.Cron {
		if j.Comment != "" {
			fmt.Fprintf(&b, "# %s\n", j.Comment)
		}
		prefix := ""
		if !j.Enabled {
			prefix = "# disabled: "
		}
		// In crontab syntax an unescaped % means "newline"; escape it so the command runs as written.
		cmd := strings.ReplaceAll(j.Command, "%", `\%`)
		fmt.Fprintf(&b, "%s%s %s cd ~ && %s\n", prefix, j.Schedule, a.Name, cmd)
	}
	return b.Bytes()
}

// writeCron installs /etc/cron.d/ike-<account>.
func (m *Manager) writeCron(account string) error {
	a, err := m.getAccount(account)
	if err != nil {
		return err
	}
	body := m.cronBody(a)
	if body == nil {
		return m.disableCron(account)
	}
	return writeFile(m.cronFile(account), body, 0o644)
}

func (m *Manager) disableCron(account string) error {
	return removeIfExists(m.cronFile(account))
}
