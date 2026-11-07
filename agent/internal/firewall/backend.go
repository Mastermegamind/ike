package firewall

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// The firewall programs Ike can drive. The server admin picks one at install
// time (installer --firewall); ufw is the default.
const (
	UFW       = "ufw"
	NFTables  = "nftables"
	Firewalld = "firewalld"
)

// Backends lists the supported firewall programs.
func Backends() []string { return []string{UFW, NFTables, Firewalld} }

// CheckBackend reports whether name is a supported firewall program.
func CheckBackend(name string) error {
	for _, b := range Backends() {
		if b == name {
			return nil
		}
	}
	return fmt.Errorf("firewall must be one of %s", strings.Join(Backends(), ", "))
}

type op int

const (
	opAllow op = iota
	opDisallow
	opBlock
	opUnblock
)

func (m *Manager) backend() string {
	if m.Backend == "" {
		return UFW
	}
	return m.Backend
}

// change makes one rule change in the firewall program. next is the whole
// state after the change: the nftables backend renders it in one go, the
// others apply the single change.
func (m *Manager) change(ctx context.Context, o op, p Port, b Block, next *State) error {
	switch m.backend() {
	case NFTables:
		return m.applyNFT(ctx, next)
	case Firewalld:
		for _, args := range firewalldArgs(o, p, b) {
			if _, err := m.Run.Run(ctx, "firewall-cmd", args...); err != nil {
				return err
			}
		}
		return nil
	default:
		_, err := m.Run.Run(ctx, "ufw", ufwArgs(o, p, b)...)
		return err
	}
}

// Sync makes the firewall match Ike's record. Only nftables needs it (its
// table is always rendered whole); ufw and firewalld keep their own rules.
func (m *Manager) Sync(ctx context.Context) error {
	if m.backend() != NFTables {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.load()
	if err != nil {
		return err
	}
	return m.applyNFT(ctx, s)
}

// Active reports whether the firewall is on.
func (m *Manager) Active(ctx context.Context) bool { return Active(ctx, m.Run, m.backend()) }

// Active reports whether the named firewall program is on.
func Active(ctx context.Context, r runner.Runner, backend string) bool {
	switch backend {
	case NFTables:
		_, err := r.Run(ctx, "nft", "list", "table", "inet", "ike")
		return err == nil
	case Firewalld:
		out, err := r.Run(ctx, "firewall-cmd", "--state")
		return err == nil && strings.TrimSpace(string(out)) == "running"
	default:
		out, _ := r.Run(ctx, "ufw", "status")
		return strings.Contains(string(out), "Status: active")
	}
}

// EnableHint is what the security scan suggests when the firewall is off.
func EnableHint(backend string) string {
	switch backend {
	case NFTables:
		return "Run: systemctl enable --now ike-firewall (SSH stays open)."
	case Firewalld:
		return "Run: systemctl enable --now firewalld (SSH stays open)."
	default:
		return "Run: ufw --force enable (SSH stays open)."
	}
}

// ---- ufw

func ufwArgs(o op, p Port, b Block) []string {
	switch o {
	case opAllow, opDisallow:
		var args []string
		if o == opDisallow {
			args = []string{"delete"}
		}
		if p.From != "" {
			args = append(args, "allow", "from", p.From, "to", "any", "port", p.Port, "proto", p.Proto)
		} else {
			args = append(args, "allow", p.Port+"/"+p.Proto)
		}
		if o == opAllow && p.Comment != "" {
			args = append(args, "comment", p.Comment)
		}
		return args
	case opBlock:
		args := []string{"insert", "1", "deny", "from", b.Address}
		if b.Comment != "" {
			args = append(args, "comment", b.Comment)
		}
		return args
	default:
		return []string{"delete", "deny", "from", b.Address}
	}
}

// ---- firewalld: ports in the public zone, blocked addresses as sources of
// the drop zone. Every change is made to the running firewall and to the
// saved (--permanent) one, so nothing is lost on reload or reboot.

func firewalldArgs(o op, p Port, b Block) [][]string {
	var one []string
	switch o {
	case opAllow, opDisallow:
		verb := "--add-"
		if o == opDisallow {
			verb = "--remove-"
		}
		port := strings.Replace(p.Port, ":", "-", 1)
		if p.From != "" {
			one = []string{"--zone=public", verb + "rich-rule=" + richRule(p.From, port, p.Proto)}
		} else {
			one = []string{"--zone=public", verb + "port=" + port + "/" + p.Proto}
		}
	case opBlock:
		one = []string{"--zone=drop", "--add-source=" + b.Address}
	default:
		one = []string{"--zone=drop", "--remove-source=" + b.Address}
	}
	return [][]string{one, append([]string{"--permanent"}, one...)}
}

func richRule(from, port, proto string) string {
	return fmt.Sprintf(`rule family="%s" source address="%s" port port="%s" protocol="%s" accept`, family(from), from, port, proto)
}

func family(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil {
		ip, _, _ = net.ParseCIDR(addr)
	}
	if ip != nil && ip.To4() == nil {
		return "ipv6"
	}
	return "ipv4"
}

// ---- nftables: Ike owns one table, "inet ike", rendered whole from its
// record and loaded atomically. Other tables (Docker's, an admin's) are left
// alone.

// RenderNFT is the nft script for the whole "inet ike" table. Loading it
// replaces the previous table in one transaction.
func RenderNFT(s *State, protected []string) string {
	var b strings.Builder
	b.WriteString("#!/usr/sbin/nft -f\n# Written by Ike. Edit open ports and blocked addresses in the panel; changes here are overwritten.\n\n")
	b.WriteString("table inet ike\ndelete table inet ike\n\ntable inet ike {\n")
	var v4, v6 []string
	for _, bl := range s.Blocks {
		if family(bl.Address) == "ipv6" {
			v6 = append(v6, bl.Address)
		} else {
			v4 = append(v4, bl.Address)
		}
	}
	writeSet := func(name, typ string, elems []string) {
		fmt.Fprintf(&b, "\tset %s {\n\t\ttype %s\n\t\tflags interval\n\t\tauto-merge\n", name, typ)
		if len(elems) > 0 {
			sort.Strings(elems)
			fmt.Fprintf(&b, "\t\telements = { %s }\n", strings.Join(elems, ", "))
		}
		b.WriteString("\t}\n\n")
	}
	writeSet("blocked4", "ipv4_addr", v4)
	writeSet("blocked6", "ipv6_addr", v6)

	b.WriteString("\tchain input {\n\t\ttype filter hook input priority filter - 10; policy drop;\n")
	b.WriteString("\t\tip saddr @blocked4 drop\n\t\tip6 saddr @blocked6 drop\n")
	b.WriteString("\t\tct state established,related accept\n\t\tct state invalid drop\n")
	b.WriteString("\t\tiif \"lo\" accept\n")
	b.WriteString("\t\tmeta l4proto icmp accept\n\t\tmeta l4proto ipv6-icmp accept\n")
	// DHCPv6 replies and the like are covered by "established"; IPv6 needs
	// neighbour discovery, which is ICMPv6 above.
	seen := map[string]bool{}
	for _, p := range protected {
		port, proto, ok := strings.Cut(p, "/")
		if !ok || checkPort(port, proto) != nil || seen[p] {
			continue
		}
		seen[p] = true
		fmt.Fprintf(&b, "\t\t%s dport %s accept comment \"Required\"\n", proto, nftPort(port))
	}
	for _, p := range s.Ports {
		if checkPort(p.Port, p.Proto) != nil {
			continue
		}
		rule := fmt.Sprintf("%s dport %s accept", p.Proto, nftPort(p.Port))
		if p.From != "" {
			saddr := "ip saddr"
			if family(p.From) == "ipv6" {
				saddr = "ip6 saddr"
			}
			rule = saddr + " " + p.From + " " + rule
		}
		if p.Comment != "" && commentRe.MatchString(p.Comment) {
			rule += fmt.Sprintf(" comment %q", p.Comment)
		}
		b.WriteString("\t\t" + rule + "\n")
	}
	b.WriteString("\t}\n}\n")
	return b.String()
}

func nftPort(p string) string { return strings.Replace(p, ":", "-", 1) }

// applyNFT writes the table, has nft check it, then loads it. A rejected
// ruleset never replaces the one that's running.
func (m *Manager) applyNFT(ctx context.Context, s *State) error {
	if m.RulesetFile == "" {
		return fmt.Errorf("the nftables ruleset file isn't configured")
	}
	text := RenderNFT(s, m.Protected)
	if err := os.MkdirAll(filepath.Dir(m.RulesetFile), 0o755); err != nil {
		return err
	}
	tmp := m.RulesetFile + ".new"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	if out, err := m.Run.Run(ctx, "nft", "--check", "--file", tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("nftables rejected the new rules: %s", firstLine(string(out), err))
	}
	old, oldErr := os.ReadFile(m.RulesetFile)
	if err := os.Rename(tmp, m.RulesetFile); err != nil {
		return err
	}
	if out, err := m.Run.Run(ctx, "nft", "--file", m.RulesetFile); err != nil {
		if oldErr == nil {
			_ = os.WriteFile(m.RulesetFile, old, 0o600)
		}
		return fmt.Errorf("nftables didn't load the new rules: %s", firstLine(string(out), err))
	}
	return nil
}

func firstLine(out string, err error) string {
	if l, _, _ := strings.Cut(strings.TrimSpace(out), "\n"); l != "" {
		return l
	}
	return err.Error()
}

// normalizeSource accepts "" (anyone), an IP or a CIDR range.
func normalizeSource(from string) (string, error) {
	from = strings.TrimSpace(from)
	if from == "" {
		return "", nil
	}
	if ip := net.ParseIP(from); ip != nil {
		if ip.IsUnspecified() {
			return "", nil
		}
		return ip.String(), nil
	}
	_, n, err := net.ParseCIDR(from)
	if err != nil {
		return "", fmt.Errorf("%q is not an IP address or range (e.g. 203.0.113.7 or 203.0.113.0/24)", from)
	}
	if ones, _ := n.Mask.Size(); ones == 0 {
		return "", nil
	}
	return n.String(), nil
}
