// Package firewall manages open ports and blocked addresses through ufw,
// nftables or firewalld, and reads/clears fail2ban bans. Ike keeps its own
// record of the rules it added so it never touches rules an admin created by
// hand (with nftables, Ike's rules live in their own "inet ike" table).
package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")

	commentRe = regexp.MustCompile(`^[A-Za-z0-9 ._,:()/-]{0,60}$`)
	jailRe    = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
)

type Port struct {
	Port      string    `json:"port"`  // "8080" or a range "6000:6100"
	Proto     string    `json:"proto"` // tcp | udp
	Comment   string    `json:"comment,omitempty"`
	From      string    `json:"from,omitempty"` // only this IP or range may connect; empty means anyone
	Protected bool      `json:"protected"`      // can't be closed from the panel (SSH, panel ports)
	AddedAt   time.Time `json:"added_at"`
}

type Block struct {
	Address string    `json:"address"` // IP or CIDR
	Comment string    `json:"comment,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

type Ban struct {
	Jail string `json:"jail"`
	IP   string `json:"ip"`
}

type State struct {
	Backend string  `json:"backend,omitempty"` // filled in by Status
	Ports   []Port  `json:"ports"`
	Blocks  []Block `json:"blocks"`
}

type Manager struct {
	Run       runner.Runner
	StateFile string   // absolute, already rooted
	Protected []string // ports that may never be closed, e.g. "22/tcp"
	// Backend is "ufw" (the default), "nftables" or "firewalld".
	Backend string
	// RulesetFile is where the nftables backend keeps Ike's table, loaded at
	// boot by ike-firewall.service. Absolute, already rooted.
	RulesetFile string
	mu          sync.Mutex
}

func (m *Manager) load() (*State, error) {
	b, err := os.ReadFile(m.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return &State{Ports: []Port{}, Blocks: []Block{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (m *Manager) save(s *State) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.MkdirAll(filepath.Dir(m.StateFile), 0o700); err != nil {
		return err
	}
	tmp := m.StateFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.StateFile)
}

func (m *Manager) isProtected(port, proto string) bool {
	for _, p := range m.Protected {
		if p == port+"/"+proto {
			return true
		}
	}
	return false
}

// Status returns open ports (including protected ones) and blocked addresses.
func (m *Manager) Status() (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.load()
	if err != nil {
		return nil, err
	}
	out := &State{Backend: m.backend(), Blocks: s.Blocks, Ports: []Port{}}
	for _, p := range m.Protected {
		port, proto, _ := strings.Cut(p, "/")
		out.Ports = append(out.Ports, Port{Port: port, Proto: proto, Protected: true, Comment: "Required"})
	}
	out.Ports = append(out.Ports, s.Ports...)
	return out, nil
}

func checkPort(port, proto string) error {
	if proto != "tcp" && proto != "udp" {
		return fmt.Errorf("protocol must be tcp or udp")
	}
	lo, hi, isRange := strings.Cut(port, ":")
	a, err := strconv.Atoi(lo)
	if err != nil || a < 1 || a > 65535 || strconv.Itoa(a) != lo {
		return fmt.Errorf("port must be 1-65535 or a range like 6000:6100")
	}
	if isRange {
		b, err := strconv.Atoi(hi)
		if err != nil || b <= a || b > 65535 || strconv.Itoa(b) != hi {
			return fmt.Errorf("port range must look like 6000:6100")
		}
		if b-a > 10000 {
			return fmt.Errorf("port range is too wide (max 10000 ports)")
		}
	}
	return nil
}

// OpenPort allows incoming traffic on a port from anyone.
func (m *Manager) OpenPort(ctx context.Context, port, proto, comment string) (*Port, error) {
	return m.AllowFrom(ctx, "", port, proto, comment)
}

// AllowFrom allows incoming traffic on a port, only from one IP address or
// range when from isn't empty (e.g. the panel reaching a joined server).
func (m *Manager) AllowFrom(ctx context.Context, from, port, proto, comment string) (*Port, error) {
	if err := checkPort(port, proto); err != nil {
		return nil, err
	}
	if !commentRe.MatchString(comment) {
		return nil, fmt.Errorf("note may only contain letters, numbers and basic punctuation (max 60)")
	}
	from, err := normalizeSource(from)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.load()
	if err != nil {
		return nil, err
	}
	if from == "" && m.isProtected(port, proto) {
		return nil, fmt.Errorf("port %s/%s is already open", port, proto)
	}
	for _, p := range s.Ports {
		if p.Port == port && p.Proto == proto && p.From == from {
			return nil, fmt.Errorf("port %s/%s %w", port, proto, ErrExists)
		}
	}
	p := Port{Port: port, Proto: proto, Comment: comment, From: from, AddedAt: time.Now().UTC()}
	next := &State{Ports: append(append([]Port{}, s.Ports...), p), Blocks: s.Blocks}
	if err := m.change(ctx, opAllow, p, Block{}, next); err != nil {
		return nil, err
	}
	return &p, m.save(next)
}

// ClosePort removes a port Ike opened (from one source when from isn't
// empty). Protected ports can't be closed.
func (m *Manager) ClosePort(ctx context.Context, port, proto, from string) error {
	if err := checkPort(port, proto); err != nil {
		return err
	}
	from, err := normalizeSource(from)
	if err != nil {
		return err
	}
	if from == "" && m.isProtected(port, proto) {
		return fmt.Errorf("port %s/%s is needed to reach this server and can't be closed from the panel", port, proto)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.load()
	if err != nil {
		return err
	}
	out := []Port{}
	var gone *Port
	for _, p := range s.Ports {
		if p.Port == port && p.Proto == proto && p.From == from {
			gone = &p
			continue
		}
		out = append(out, p)
	}
	if gone == nil {
		return fmt.Errorf("port %s/%s %w", port, proto, ErrNotFound)
	}
	next := &State{Ports: out, Blocks: s.Blocks}
	if err := m.change(ctx, opDisallow, *gone, Block{}, next); err != nil {
		return err
	}
	return m.save(next)
}

// normalizeAddress accepts an IP or CIDR and refuses blocks that would cut off
// the whole internet or the server itself.
func normalizeAddress(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if ip := net.ParseIP(addr); ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return "", fmt.Errorf("%s can't be blocked", addr)
		}
		return ip.String(), nil
	}
	ip, n, err := net.ParseCIDR(addr)
	if err != nil {
		return "", fmt.Errorf("%q is not an IP address or range (e.g. 203.0.113.7 or 203.0.113.0/24)", addr)
	}
	ones, bits := n.Mask.Size()
	if (bits == 32 && ones < 8) || (bits == 128 && ones < 16) {
		return "", fmt.Errorf("range %s is too broad to block", addr)
	}
	if ip.IsLoopback() {
		return "", fmt.Errorf("%s can't be blocked", addr)
	}
	return n.String(), nil
}

// BlockAddress denies all traffic from an IP or range. The rule is inserted at
// the top so it wins over any allow rule.
func (m *Manager) BlockAddress(ctx context.Context, addr, comment string) (*Block, error) {
	a, err := normalizeAddress(addr)
	if err != nil {
		return nil, err
	}
	if !commentRe.MatchString(comment) {
		return nil, fmt.Errorf("note may only contain letters, numbers and basic punctuation (max 60)")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.load()
	if err != nil {
		return nil, err
	}
	for _, b := range s.Blocks {
		if b.Address == a {
			return nil, fmt.Errorf("%s is already blocked", a)
		}
	}
	b := Block{Address: a, Comment: comment, AddedAt: time.Now().UTC()}
	next := &State{Ports: s.Ports, Blocks: append(append([]Block{}, s.Blocks...), b)}
	if err := m.change(ctx, opBlock, Port{}, b, next); err != nil {
		return nil, err
	}
	return &b, m.save(next)
}

// UnblockAddress removes a block Ike added.
func (m *Manager) UnblockAddress(ctx context.Context, addr string) error {
	a, err := normalizeAddress(addr)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.load()
	if err != nil {
		return err
	}
	out := []Block{}
	var gone *Block
	for _, b := range s.Blocks {
		if b.Address == a {
			gone = &b
			continue
		}
		out = append(out, b)
	}
	if gone == nil {
		return fmt.Errorf("%s %w in the block list", a, ErrNotFound)
	}
	next := &State{Ports: s.Ports, Blocks: out}
	if err := m.change(ctx, opUnblock, Port{}, *gone, next); err != nil {
		return err
	}
	return m.save(next)
}

// Bans lists addresses fail2ban has banned automatically.
func (m *Manager) Bans(ctx context.Context) ([]Ban, error) {
	out, err := m.Run.Run(ctx, "fail2ban-client", "status")
	if err != nil {
		return nil, err
	}
	bans := []Ban{}
	for _, jail := range parseJails(string(out)) {
		st, err := m.Run.Run(ctx, "fail2ban-client", "status", jail)
		if err != nil {
			continue
		}
		for _, ip := range parseBanned(string(st)) {
			bans = append(bans, Ban{Jail: jail, IP: ip})
		}
	}
	return bans, nil
}

// Unban releases an address from a fail2ban jail.
func (m *Manager) Unban(ctx context.Context, jail, ip string) error {
	if !jailRe.MatchString(jail) {
		return fmt.Errorf("invalid jail name")
	}
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("%q is not an IP address", ip)
	}
	_, err := m.Run.Run(ctx, "fail2ban-client", "set", jail, "unbanip", ip)
	return err
}

// parseJails reads "`- Jail list:	sshd, nginx-http-auth".
func parseJails(s string) []string {
	for _, line := range strings.Split(s, "\n") {
		if _, after, ok := strings.Cut(line, "Jail list:"); ok {
			var out []string
			for _, j := range strings.Split(after, ",") {
				if j = strings.TrimSpace(j); jailRe.MatchString(j) {
					out = append(out, j)
				}
			}
			return out
		}
	}
	return nil
}

// parseBanned reads "   `- Banned IP list:	1.2.3.4 5.6.7.8".
func parseBanned(s string) []string {
	for _, line := range strings.Split(s, "\n") {
		if _, after, ok := strings.Cut(line, "Banned IP list:"); ok {
			var out []string
			for _, ip := range strings.Fields(after) {
				if net.ParseIP(ip) != nil {
					out = append(out, ip)
				}
			}
			return out
		}
	}
	return nil
}
