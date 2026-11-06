// Package client talks to ike-agent's JSON API. Used by ike-mcp and, later, the control plane.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Actor   string // sent as X-Ike-Actor for the audit log
	// Token is an ike_ API token, sent as a bearer token. Set for panel
	// connections, where the panel enforces the token's scopes.
	Token string
	// Stream has no overall timeout, for downloads and uploads.
	Stream *http.Client
	// Dial opens a raw connection to the agent (WebSocket tunnels).
	Dial func(ctx context.Context) (net.Conn, error)
}

// Unix returns a client for the agent's unix socket.
func Unix(socket string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &Client{BaseURL: "http://ike-agent", HTTP: &http.Client{Transport: tr, Timeout: 6 * time.Minute},
		Stream: &http.Client{Transport: tr},
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		}}
}

// Panel returns a client that goes through a Ike panel's API with an API
// token, instead of the local agent socket. Paths stay "/v1/...": the panel
// serves them under /api/v1 and checks the token's areas, accounts and
// allowed addresses on every request.
func Panel(panelURL, token string, insecure bool) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for self-signed panels
	}
	base := strings.TrimRight(panelURL, "/") + "/api"
	return &Client{BaseURL: base, Token: token, HTTP: &http.Client{Transport: tr, Timeout: 6 * time.Minute}, Stream: &http.Client{Transport: tr}}
}

// Do sends a request and decodes the JSON response into out (if non-nil).
// Agent errors come back as Go errors carrying the agent's message.
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	data, err := c.Raw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Raw sends a request and returns the response body as it is (for text
// responses such as zone files).
func (c *Client) Raw(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Actor != "" && c.Token == "" {
		req.Header.Set("X-Ike-Actor", c.Actor)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if c.Token != "" {
			return nil, fmt.Errorf("cannot reach the panel: %w", err)
		}
		return nil, fmt.Errorf("cannot reach ike-agent: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		if c.Token != "" {
			return nil, fmt.Errorf("panel returned HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("agent returned HTTP %d", resp.StatusCode)
	}
	return data, nil
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

// PathEscape is exposed so callers build safe URLs.
func PathEscape(s string) string { return url.PathEscape(s) }

// QueryEscape escapes a query-string value.
func QueryEscape(s string) string { return url.QueryEscape(s) }
