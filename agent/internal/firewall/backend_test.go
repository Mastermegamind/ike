package firewall

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

func newBackend(t *testing.T, backend string) (*Manager, *runner.DryRun) {
	r := &runner.DryRun{}
	dir := t.TempDir()
	return &Manager{Run: r, StateFile: filepath.Join(dir, "fw.json"), Protected: []string{"22/tcp", "80/tcp", "443/tcp"},
		Backend: backend, RulesetFile: filepath.Join(dir, "etc/ike/nftables.nft")}, r
}

// exercise makes the same changes on any backend.
func exercise(t *testing.T, m *Manager) {
	t.Helper()
	ctx := context.Background()
	steps := []func() error{
		func() error { _, err := m.OpenPort(ctx, "8080", "tcp", "Staging app"); return err },
		func() error { _, err := m.OpenPort(ctx, "6000:6100", "udp", ""); return err },
		func() error { _, err := m.AllowFrom(ctx, "192.0.2.10", "2089", "tcp", "Ike panel"); return err },
		func() error { _, err := m.AllowFrom(ctx, "2001:db8::5", "6379", "tcp", "Redis replica"); return err },
		func() error { _, err := m.BlockAddress(ctx, "203.0.113.7", "Brute force"); return err },
		func() error { _, err := m.BlockAddress(ctx, "2001:db8:bad::/48", ""); return err },
		func() error { return m.ClosePort(ctx, "8080", "tcp", "") },
		func() error { return m.UnblockAddress(ctx, "203.0.113.7") },
	}
	for i, f := range steps {
		if err := f(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	// The same port from one source and from anyone are separate rules.
	if _, err := m.AllowFrom(ctx, "192.0.2.10", "2089", "tcp", ""); err == nil {
		t.Error("duplicate source rule accepted")
	}
	if _, err := m.AllowFrom(ctx, "not-an-ip", "2089", "tcp", ""); err == nil {
		t.Error("bad source accepted")
	}
	if err := m.ClosePort(ctx, "2089", "tcp", ""); err == nil {
		t.Error("closed a rule that only exists for one source")
	}
	st, _ := m.Status()
	if st.Backend != m.backend() {
		t.Errorf("status backend = %q", st.Backend)
	}
}

func TestUFWBackend(t *testing.T) {
	m, r := newBackend(t, "")
	exercise(t, m)
	cmds := strings.Join(r.History(), "\n")
	for _, want := range []string{
		"ufw allow 8080/tcp comment Staging app",
		"ufw allow 6000:6100/udp",
		"ufw allow from 192.0.2.10 to any port 2089 proto tcp comment Ike panel",
		"ufw insert 1 deny from 203.0.113.7 comment Brute force",
		"ufw delete allow 8080/tcp",
		"ufw delete deny from 203.0.113.7",
	} {
		if !strings.Contains(cmds, want) {
			t.Errorf("missing %q in\n%s", want, cmds)
		}
	}
}

func TestFirewalldBackend(t *testing.T) {
	m, r := newBackend(t, Firewalld)
	exercise(t, m)
	cmds := strings.Join(r.History(), "\n")
	for _, want := range []string{
		"firewall-cmd --zone=public --add-port=8080/tcp",
		"firewall-cmd --permanent --zone=public --add-port=8080/tcp",
		"firewall-cmd --zone=public --add-port=6000-6100/udp",
		`firewall-cmd --zone=public --add-rich-rule=rule family="ipv4" source address="192.0.2.10" port port="2089" protocol="tcp" accept`,
		`--add-rich-rule=rule family="ipv6" source address="2001:db8::5" port port="6379" protocol="tcp" accept`,
		"firewall-cmd --permanent --zone=drop --add-source=203.0.113.7",
		"firewall-cmd --permanent --zone=public --remove-port=8080/tcp",
		"firewall-cmd --permanent --zone=drop --remove-source=203.0.113.7",
	} {
		if !strings.Contains(cmds, want) {
			t.Errorf("missing %q in\n%s", want, cmds)
		}
	}
	if strings.Contains(cmds, "ufw") {
		t.Error("firewalld backend ran ufw")
	}
}

func TestNFTablesBackend(t *testing.T) {
	m, r := newBackend(t, NFTables)
	exercise(t, m)
	cmds := strings.Join(r.History(), "\n")
	if !strings.Contains(cmds, "nft --check --file "+m.RulesetFile+".new") || !strings.Contains(cmds, "nft --file "+m.RulesetFile) || strings.Contains(cmds, "ufw") {
		t.Errorf("commands:\n%s", cmds)
	}
	b, err := os.ReadFile(m.RulesetFile)
	if err != nil {
		t.Fatal(err)
	}
	rs := string(b)
	for _, want := range []string{
		"table inet ike\ndelete table inet ike\n",
		"policy drop;",
		"ct state established,related accept",
		`tcp dport 22 accept comment "Required"`,
		"udp dport 6000-6100 accept",
		`ip saddr 192.0.2.10 tcp dport 2089 accept comment "Ike panel"`,
		`ip6 saddr 2001:db8::5 tcp dport 6379 accept comment "Redis replica"`,
		"elements = { 2001:db8:bad::/48 }",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("ruleset is missing %q:\n%s", want, rs)
		}
	}
	for _, gone := range []string{"8080", "203.0.113.7"} {
		if strings.Contains(rs, gone) {
			t.Errorf("ruleset still has %s:\n%s", gone, rs)
		}
	}
	// Sync renders the same table again (at agent start).
	_ = os.Remove(m.RulesetFile)
	if err := m.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(m.RulesetFile); string(b2) != rs {
		t.Error("Sync rendered a different table")
	}
}

// A ruleset nft rejects is never loaded or kept.
type rejectCheck struct{ runner.DryRun }

func (r *rejectCheck) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "nft" && len(args) > 0 && args[0] == "--check" {
		return []byte("Error: syntax error\nmore"), os.ErrInvalid
	}
	return r.DryRun.Run(ctx, name, args...)
}

func TestNFTablesRejected(t *testing.T) {
	m, _ := newBackend(t, NFTables)
	rc := &rejectCheck{}
	m.Run = rc
	_, err := m.OpenPort(context.Background(), "8080", "tcp", "")
	if err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(m.RulesetFile); err == nil {
		t.Error("rejected ruleset was written")
	}
	if st, _ := m.Status(); len(st.Ports) != 3 {
		t.Errorf("rejected change was recorded: %+v", st.Ports)
	}
	for _, c := range rc.History() {
		if strings.HasPrefix(c, "nft --file") {
			t.Error("rejected ruleset was loaded")
		}
	}
}

// TestRealNFTCheck has the real nft parse the rendered table (it needs nft
// installed; --check doesn't change the running firewall).
func TestRealNFTCheck(t *testing.T) {
	nft, err := exec.LookPath("nft")
	if err != nil {
		t.Skip("nft isn't installed")
	}
	s := &State{
		Ports: []Port{{Port: "8080", Proto: "tcp", Comment: "Staging app"}, {Port: "6000:6100", Proto: "udp"},
			{Port: "2089", Proto: "tcp", From: "192.0.2.10", Comment: "Ike panel"}, {Port: "6379", Proto: "tcp", From: "2001:db8::/64"}},
		Blocks: []Block{{Address: "203.0.113.7"}, {Address: "198.51.100.0/24"}, {Address: "2001:db8:bad::/48"}},
	}
	for name, st := range map[string]*State{"full": s, "empty": {}} {
		f := filepath.Join(t.TempDir(), "ike.nft")
		if err := os.WriteFile(f, []byte(RenderNFT(st, []string{"22/tcp", "80/tcp", "443/tcp", "443/udp", "2083/tcp"})), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(nft, "--check", "--file", f).CombinedOutput()
		if err != nil && strings.Contains(string(out), "Operation not permitted") {
			t.Skip("nft needs CAP_NET_ADMIN here")
		}
		if err != nil {
			b, _ := os.ReadFile(f)
			t.Fatalf("%s: nft rejected the ruleset: %v\n%s\n%s", name, err, out, b)
		}
	}
}
