package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/cluster"
	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/firewall"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// join connects this server to a panel on another server.
//
//	ike-agent join https://panel.example.com:2087 <token> <fingerprint>
func join(args []string) int {
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: ike-agent join <panel URL> <join token> <panel certificate fingerprint>")
		return 2
	}
	if err := doJoin(args[0], args[1], args[2], "/etc/ike/agent.json"); err != nil {
		fmt.Fprintln(os.Stderr, "Joining failed:", err)
		return 1
	}
	return 0
}

func doJoin(panelURL, token, fingerprint, cfgPath string) error {
	u, err := url.Parse(panelURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("the panel address looks like https://panel.example.com:2087")
	}
	want := cluster.NormalizeFingerprint(fingerprint)
	if len(want) != 95 {
		return errors.New("the fingerprint is the 64 hex characters shown in the panel")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	keyPEM, csrPEM, err := cluster.NewCSR("join-request")
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"token": token, "csr": string(csrPEM), "hostname": host,
		"addresses": nonEmpty(cfg.PublicIPv4, cfg.PublicIPv6)})
	// The panel may use a self-signed certificate: trust exactly the one whose
	// fingerprint the admin copied from the panel.
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 || cluster.Fingerprint(raw[0]) != want {
			return errors.New("the panel's certificate doesn't match the fingerprint; check the address")
		}
		return nil
	}}}
	c := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	resp, err := c.Post(strings.TrimRight(panelURL, "/")+"/cluster/join", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return fmt.Errorf("the panel said: %s", firstNonEmpty(e.Error, resp.Status))
	}
	var res struct {
		ServerID string `json:"server_id"`
		Cert     string `json:"cert"`
		CA       string `json:"ca"`
		PanelIP  string `json:"panel_ip"`
	}
	if err := json.Unmarshal(b, &res); err != nil || res.Cert == "" || res.CA == "" {
		return errors.New("the panel's answer was incomplete")
	}
	dir := cfg.ClusterDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"agent.key": keyPEM, "agent.crt": []byte(res.Cert), "ca.crt": []byte(res.CA)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
	}
	// turn the cluster listener on in agent.json, keeping everything else
	raw := map[string]any{}
	if b, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(b, &raw)
	}
	raw["cluster_listen"] = ":2089"
	nb, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(cfgPath, nb, 0o640); err != nil {
		return err
	}
	// only the panel may reach port 2089
	if ip := net.ParseIP(res.PanelIP); ip != nil {
		fw := &firewall.Manager{Run: runner.Exec{}, StateFile: cfg.FirewallState, Protected: cfg.ProtectedPorts,
			Backend: cfg.Firewall, RulesetFile: cfg.FirewallRuleset}
		if _, err := fw.AllowFrom(context.Background(), ip.String(), "2089", "tcp", "Ike panel"); err != nil && !errors.Is(err, firewall.ErrExists) {
			fmt.Fprintf(os.Stderr, "warning: couldn't open port 2089 for the panel: %v\n", err)
		}
	}
	_ = exec.Command("systemctl", "restart", "ike-agent").Run()
	fmt.Printf("Joined. This server is %s in the panel; it now accepts the panel on port 2089.\n", res.ServerID)
	return nil
}

func nonEmpty(v ...string) []string {
	var out []string
	for _, s := range v {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
