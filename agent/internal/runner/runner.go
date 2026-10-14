// Package runner executes system commands with separate arguments.
// There is deliberately no way to run a shell string.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sort"
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
	// Exec runs a Command with full control over environment, directory and output.
	Exec(ctx context.Context, c Command) ([]byte, error)
}

// Command is one program run with extra control. Secrets belong in Env or
// Stdin, never in Args, because arguments are visible in the process list.
type Command struct {
	Name string
	Args []string
	// Env, when not nil, is the complete environment (nothing is inherited).
	Env []string
	Dir string
	// Stdin feeds the program.
	Stdin string
	// StdinReader streams large input (database imports, uploads). Used
	// instead of Stdin when set.
	StdinReader io.Reader
	// Stdout, when set, receives only standard output (database dumps,
	// downloads); standard error is kept for the error message.
	Stdout io.Writer
	// Output, when set, receives combined output as it is produced (build logs);
	// Exec then returns only the last 64 KB.
	Output  io.Writer
	Timeout time.Duration
}

// Exec runs real commands.
type Exec struct{}

func (e Exec) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return e.RunInput(ctx, "", name, args...)
}

func (e Exec) RunInput(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	return e.Exec(ctx, Command{Name: name, Args: args, Stdin: stdin})
}

// tail keeps the last n bytes written to it.
type tail struct {
	n   int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.n {
		t.buf = t.buf[len(t.buf)-t.n:]
	}
	return len(p), nil
}

func (Exec) Exec(ctx context.Context, c Command) ([]byte, error) {
	timeout := c.Timeout
	if timeout == 0 {
		if _, ok := ctx.Deadline(); !ok {
			timeout = 5 * time.Minute // callers with long work pass their own deadline
		}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Env, cmd.Dir = c.Env, c.Dir
	cmd.WaitDelay = 5 * time.Second
	if c.StdinReader != nil {
		cmd.Stdin = c.StdinReader
	} else if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var out []byte
	var err error
	if c.Stdout != nil {
		t := &tail{n: 16 << 10}
		cmd.Stdout, cmd.Stderr = c.Stdout, t
		err = cmd.Run()
		out = t.buf
	} else if c.Output != nil {
		t := &tail{n: 64 << 10}
		w := io.MultiWriter(c.Output, t)
		cmd.Stdout, cmd.Stderr = w, w
		err = cmd.Run()
		out = t.buf
	} else {
		out, err = cmd.CombinedOutput()
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 2000 {
			msg = "…" + msg[len(msg)-2000:]
		}
		return out, fmt.Errorf("%s %s: %w: %s", c.Name, strings.Join(c.Args, " "), err, msg)
	}
	return out, nil
}

// DryRun logs commands instead of running them. Used for development and tests.
type DryRun struct {
	mu       sync.Mutex
	Commands []string
	Stdin    []string // what each RunInput call received, for tests
	Envs     [][]string
	// FailOn makes any command whose joined text starts with this prefix fail.
	FailOn string
	// Responses gives canned output for commands starting with a prefix
	// (longest matching prefix wins).
	Responses map[string]string
}

func (d *DryRun) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return d.RunInput(ctx, "", name, args...)
}

func (d *DryRun) RunInput(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	return d.Exec(ctx, Command{Name: name, Args: args, Stdin: stdin})
}

func (d *DryRun) Exec(_ context.Context, c Command) ([]byte, error) {
	cmd := strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
	if c.StdinReader != nil {
		b, _ := io.ReadAll(io.LimitReader(c.StdinReader, 1<<20))
		c.Stdin = string(b)
		if c.Stdin == "" {
			c.Stdin = "<stream>"
		}
	}
	d.mu.Lock()
	if c.Stdin != "" {
		d.Stdin = append(d.Stdin, c.Stdin)
		cmd += " <stdin>"
	}
	d.Commands = append(d.Commands, cmd)
	if c.Env != nil {
		d.Envs = append(d.Envs, append([]string(nil), c.Env...))
	}
	var resp string
	if d.Responses != nil {
		keys := make([]string, 0, len(d.Responses))
		for k := range d.Responses {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
		for _, k := range keys {
			if strings.HasPrefix(cmd, k) {
				resp = d.Responses[k]
				break
			}
		}
	}
	fail := d.FailOn != "" && strings.HasPrefix(cmd, d.FailOn)
	d.mu.Unlock()
	slog.Debug("dry-run", "cmd", cmd)
	if c.Output != nil && resp != "" {
		_, _ = io.Copy(c.Output, bytes.NewBufferString(resp))
	}
	if c.Stdout != nil && resp != "" {
		_, _ = io.Copy(c.Stdout, bytes.NewBufferString(resp))
	}
	if fail {
		return []byte("simulated failure"), fmt.Errorf("%s: simulated failure", cmd)
	}
	return []byte(resp), nil
}

// History returns a copy of the recorded commands.
func (d *DryRun) History() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.Commands...)
}
