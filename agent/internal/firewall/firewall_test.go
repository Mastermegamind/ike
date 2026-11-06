package firewall

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

func newFW(t *testing.T) (*Manager, *runner.DryRun) {
	r := &runner.DryRun{}
	return &Manager{Run: r, StateFile: filepath.Join(t.TempDir(), "fw.json"), Protected: []string{"22/tcp", "80/tcp", "443/tcp"}}, r
}

func TestPorts(t *testing.T) {
	m, r := newFW(t)
	ctx := context.Background()
	if _, err := m.OpenPort(ctx, "8080", "tcp", "Staging app"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenPort(ctx, "6000:6100", "udp", ""); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][2]string{{"0", "tcp"}, {"70000", "tcp"}, {"80", "icmp"}, {"22;rm", "tcp"}, {"100:50", "tcp"}, {"8080", "tcp"}} {
		if _, err := m.OpenPort(ctx, bad[0], bad[1], ""); err == nil {
			t.Errorf("OpenPort(%v) should fail", bad)
		}
	}
	if err := m.ClosePort(ctx, "22", "tcp", ""); err == nil {
		t.Error("closing SSH must be refused")
	}
	if err := m.ClosePort(ctx, "8080", "tcp", ""); err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(r.History(), "\n")
	for _, want := range []string{"ufw allow 8080/tcp comment Staging app", "ufw allow 6000:6100/udp", "ufw delete allow 8080/tcp"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("missing %q in\n%s", want, cmds)
		}
	}
	st, _ := m.Status()
	if len(st.Ports) != 4 || !st.Ports[0].Protected {
		t.Errorf("status ports = %+v", st.Ports)
	}
}

func TestBlocks(t *testing.T) {
	m, r := newFW(t)
	ctx := context.Background()
	if _, err := m.BlockAddress(ctx, "203.0.113.7", "Brute force"); err != nil {
		t.Fatal(err)
	}
	b, err := m.BlockAddress(ctx, "198.51.100.9/24", "")
	if err != nil || b.Address != "198.51.100.0/24" {
		t.Fatalf("cidr normalize: %v %+v", err, b)
	}
	for _, bad := range []string{"0.0.0.0/0", "10.0.0.0/4", "127.0.0.1", "::1", "nope", "1.2.3.4; reboot", "203.0.113.7"} {
		if _, err := m.BlockAddress(ctx, bad, ""); err == nil {
			t.Errorf("BlockAddress(%q) should fail", bad)
		}
	}
	if err := m.UnblockAddress(ctx, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.History(), "\n"), "ufw insert 1 deny from 203.0.113.7 comment Brute force") {
		t.Error("block rule not inserted at top")
	}
}

func TestParseFail2ban(t *testing.T) {
	jails := parseJails("Status\n|- Number of jail:\t2\n`- Jail list:\tsshd, nginx-http-auth\n")
	if strings.Join(jails, ",") != "sshd,nginx-http-auth" {
		t.Errorf("jails = %v", jails)
	}
	ips := parseBanned("Status for the jail: sshd\n`- Actions\n   |- Currently banned:\t2\n   `- Banned IP list:\t203.0.113.5 2001:db8::1 junk\n")
	if strings.Join(ips, ",") != "203.0.113.5,2001:db8::1" {
		t.Errorf("ips = %v", ips)
	}
}
