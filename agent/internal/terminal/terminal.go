package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// PTY is the controlling side of a pseudo terminal.
type PTY struct {
	f *os.File
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// Open makes a pseudo terminal and returns both ends.
func Open() (*PTY, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if err := ioctl(m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		m.Close()
		return nil, nil, err
	}
	var n uint32
	if err := ioctl(m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		m.Close()
		return nil, nil, err
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return &PTY{f: m}, s, nil
}

// Resize sets the terminal size.
func (p *PTY) Resize(cols, rows int) error {
	if cols < 2 || rows < 2 || cols > 1000 || rows > 500 {
		return errors.New("bad size")
	}
	ws := struct{ Row, Col, X, Y uint16 }{uint16(rows), uint16(cols), 0, 0}
	return ioctl(p.f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
}

func (p *PTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *PTY) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *PTY) Close() error                { return p.f.Close() }

// Start runs cmd attached to a new pseudo terminal as its controlling terminal.
func Start(cmd *exec.Cmd, cols, rows int) (*PTY, error) {
	p, tty, err := Open()
	if err != nil {
		return nil, err
	}
	defer tty.Close()
	if cols > 0 && rows > 0 {
		_ = p.Resize(cols, rows)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// Session limits.
const (
	MaxSessions = 20
	IdleTimeout = 30 * time.Minute
	MaxDuration = 8 * time.Hour
)

var (
	sessMu   sync.Mutex
	sessions int
)

// control is a text message from the browser.
type control struct {
	Type string `json:"type"` // resize
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// Serve runs cmd on a pseudo terminal and connects it to the WebSocket until
// either side ends. Binary messages are keystrokes; text messages are JSON
// controls ({"type":"resize","cols":120,"rows":32}).
func Serve(ctx context.Context, ws *Conn, cmd *exec.Cmd, cols, rows int) error {
	sessMu.Lock()
	if sessions >= MaxSessions {
		sessMu.Unlock()
		_ = ws.Close(1013, "too many terminals open; close one and try again")
		return errors.New("too many sessions")
	}
	sessions++
	sessMu.Unlock()
	defer func() { sessMu.Lock(); sessions--; sessMu.Unlock() }()

	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	p, err := Start(cmd, cols, rows)
	if err != nil {
		_ = ws.Close(1011, "could not start the shell: "+err.Error())
		return err
	}
	defer p.Close()
	done := make(chan struct{})
	go func() { // shell → browser
		defer close(done)
		buf := make([]byte, 32<<10)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				if werr := ws.WriteBinary(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { // browser → shell
		for {
			_ = ws.SetReadDeadline(time.Now().Add(IdleTimeout))
			op, data, err := ws.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			if op == OpText {
				var c control
				if json.Unmarshal(data, &c) == nil && c.Type == "resize" {
					_ = p.Resize(c.Cols, c.Rows)
				}
				continue
			}
			if _, err := p.Write(data); err != nil {
				cancel()
				return
			}
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
		time.AfterFunc(3*time.Second, func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	}
	_ = cmd.Wait()
	reason := "session ended"
	if ctx.Err() == context.DeadlineExceeded {
		reason = fmt.Sprintf("closed after %s", MaxDuration)
	}
	_ = ws.Close(1000, reason)
	slog.Info("terminal closed", "cmd", cmd.Path)
	return nil
}

var _ io.Reader = (*PTY)(nil)
