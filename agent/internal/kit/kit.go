// Package kit holds the small pieces every feature package shares: paths under
// the configured root, atomic files, JSON state, running commands as an
// account user, random values and the agent's event bus.
package kit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/validate"
)

var (
	ErrExists   = errors.New("already exists")
	ErrNotFound = errors.New("not found")
	// ErrForbidden means the caller's role may not do this.
	ErrForbidden = errors.New("not allowed")
	// ErrNotInstalled means the component was not chosen at install time.
	ErrNotInstalled = errors.New("is not installed on this server")
)

// Accounts is what feature packages need to know about hosting accounts.
type Accounts interface {
	AccountExists(name string) error
}

// Env is shared by every feature manager.
type Env struct {
	Cfg      config.Config
	Run      runner.Runner
	Accounts Accounts
	Events   *Bus
	// Firewall opens ports for features that need them (may be nil).
	Firewall Opener
}

// Opener lets features open a port in the server firewall.
type Opener interface {
	AllowFrom(ctx context.Context, from, port, proto, comment string) error
}

// P maps an absolute system path into the configured root.
func (e *Env) P(abs string) string { return filepath.Join(e.Cfg.Root, abs) }

// Home is the account's home folder (an absolute system path).
func (e *Env) Home(account string) string { return filepath.Join(e.Cfg.HomeBase, account) }

// CheckAccount validates the name and makes sure the account exists.
func (e *Env) CheckAccount(name string) error {
	if err := validate.Username(name); err != nil {
		return err
	}
	if e.Accounts == nil {
		return nil
	}
	return e.Accounts.AccountExists(name)
}

// Installed reports whether a program exists (always true in dry-run mode).
func (e *Env) Installed(abs string) bool {
	if e.Cfg.DryRun {
		return true
	}
	_, err := os.Stat(e.P(abs))
	return err == nil
}

// Publish sends an event when a bus is attached.
func (e *Env) Publish(kind, subject string, data map[string]any) {
	if e.Events != nil {
		e.Events.Publish(kind, subject, data)
	}
}

// AsUser runs a program as the account's Linux user. Arguments are never
// interpreted by a shell.
func (e *Env) AsUser(ctx context.Context, account, dir string, stdin io.Reader, stdout io.Writer, name string, args ...string) ([]byte, error) {
	return e.Run.Exec(ctx, runner.Command{
		Name: "runuser", Args: append([]string{"-u", account, "--", name}, args...),
		Dir: dir, StdinReader: stdin, Stdout: stdout,
		Env: []string{"HOME=" + e.Home(account), "USER=" + account, "LANG=C.UTF-8",
			"PATH=/usr/local/bin:/usr/bin:/bin"},
	})
}

// IDs returns the numeric user and group of an account (0, 0 in dry-run mode).
func (e *Env) IDs(account string) (int, int, error) {
	if e.Cfg.DryRun {
		return 0, 0, nil
	}
	u, err := user.Lookup(account)
	if err != nil {
		return 0, 0, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return uid, gid, nil
}

// WriteFile writes atomically: a temp file in the same folder, then rename.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ike-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadJSON reads a JSON file; a missing file returns ErrNotFound.
func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// WriteJSON writes indented JSON readable by root only.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(path, b, 0o600)
}

// Store is one JSON file guarded by a mutex.
type Store[T any] struct {
	Path string
	mu   sync.Mutex
}

// Load returns the stored value, or the zero value when the file is missing.
func (s *Store[T]) Load() (T, error) {
	var v T
	err := ReadJSON(s.Path, &v)
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	return v, err
}

// Update loads, changes and saves the value under the lock.
func (s *Store[T]) Update(fn func(v *T) error) (T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.Load()
	if err != nil {
		return v, err
	}
	if err := fn(&v); err != nil {
		return v, err
	}
	return v, WriteJSON(s.Path, v)
}

// RandomHex returns 2n hex characters.
func RandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const passwordChars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// Password returns a random password without look-alike characters.
func Password(n int) string {
	out := make([]byte, n)
	for i := range out {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(passwordChars))))
		out[i] = passwordChars[k.Int64()]
	}
	return string(out)
}

// Now is replaced in tests.
var Now = func() time.Time { return time.Now().UTC() }
