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
}

// Exec runs real commands.
type Exec struct{}

func (Exec) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// DryRun logs commands instead of running them. Used for development and tests.
type DryRun struct {
	mu       sync.Mutex
	Commands []string
	// FailOn makes any command whose joined text starts with this prefix fail.
	FailOn string
}

func (d *DryRun) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	cmd := strings.TrimSpace(name + " " + strings.Join(args, " "))
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
