package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

// dnsHook is run by certbot (--manual-auth-hook / --manual-cleanup-hook) to
// publish and remove DNS-01 challenge records through the agent.
//
//	ike-agent dns-hook auth|cleanup   (reads CERTBOT_DOMAIN and CERTBOT_VALIDATION)
func dnsHook(args []string) int {
	if len(args) != 1 || (args[0] != "auth" && args[0] != "cleanup") {
		fmt.Fprintln(os.Stderr, "usage: ike-agent dns-hook auth|cleanup")
		return 2
	}
	domain, value := os.Getenv("CERTBOT_DOMAIN"), os.Getenv("CERTBOT_VALIDATION")
	if domain == "" {
		fmt.Fprintln(os.Stderr, "CERTBOT_DOMAIN is not set; this command is run by certbot")
		return 2
	}
	sock := os.Getenv("IKE_AGENT_SOCKET")
	if sock == "" {
		sock = "/run/ike/agent.sock"
	}
	c := &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}
	var req *http.Request
	if args[0] == "auth" {
		b, _ := json.Marshal(map[string]string{"domain": domain, "value": value})
		req, _ = http.NewRequest("POST", "http://agent/v1/dns/acme", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest("DELETE", "http://agent/v1/dns/acme?domain="+url.QueryEscape(domain), nil)
	}
	req.Header.Set("X-Ike-Actor", "certbot")
	resp, err := c.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ike-agent:", err)
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "ike-agent: %s: %s\n", resp.Status, body)
		return 1
	}
	return 0
}
