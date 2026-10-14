// Package client talks to ike-agent's JSON API. Used by ike-mcp and, later, the control plane.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Actor   string // sent as X-Ike-Actor for the audit log
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

// Do sends a request and decodes the JSON response into out (if non-nil).
// Agent errors come back as Go errors carrying the agent's message.
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Actor != "" {
		req.Header.Set("X-Ike-Actor", c.Actor)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach ike-agent: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("agent returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

// PathEscape is exposed so callers build safe URLs.
func PathEscape(s string) string { return url.PathEscape(s) }

// QueryEscape escapes a query-string value.
func QueryEscape(s string) string { return url.QueryEscape(s) }
