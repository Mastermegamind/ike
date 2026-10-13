// Package runner executes system commands with separate arguments.
// There is deliberately no way to run a shell string.
package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Runner runs a program with arguments and returns combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	// RunInput is Run with data on stdin. Use it for secrets (SQL with passwords,
	// prompted values) so they never appear in the process list or error messages.
	RunInput(ctx context.Context, stdin string, name string, args ...string) ([]byte, error)
}

// Exec runs real commands.
type Exec struct{}

func (e Exec) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return e.RunInput(ctx, "", name, args...)
}

func (Exec) RunInput(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	// Callers with long work (package upgrades) pass their own deadline.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// DryRun logs commands instead of running them. Used for development and tests.
type DryRun struct {
	mu       sync.Mutex
	Commands []string
	Stdin    []string // what each RunInput call received, for tests
	// FailOn makes any command whose joined text starts with this prefix fail.
	FailOn string
}

func (d *DryRun) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return d.RunInput(ctx, "", name, args...)
}

func (d *DryRun) RunInput(_ context.Context, stdin string, name string, args ...string) ([]byte, error) {
	cmd := strings.TrimSpace(name + " " + strings.Join(args, " "))
	if stdin != "" {
		d.mu.Lock()
		d.Stdin = append(d.Stdin, stdin)
		d.mu.Unlock()
		cmd += " <stdin>"
	}
	d.mu.Lock()
	d.Commands = append(d.Commands, cmd)
	d.mu.Unlock()
	slog.Info("dry-run", "cmd", cmd)
	if d.FailOn != "" && strings.HasPrefix(cmd, d.FailOn) {
		return []byte("simulated failure"), fmt.Errorf("%s: simulated failure", cmd)
	}
	return nil, nil
}

// History returns a copy of the recorded commands.
func (d *DryRun) History() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.Commands...)
}
