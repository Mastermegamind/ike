package sites

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var hostLabelRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,252}[a-zA-Z0-9])?$`)

// RuntimeBalancer spreads a website's traffic over several backend servers
// (other Ike servers, app servers, containers). Nginx keeps a pool of
// backends, takes failing ones out for a while and brings them back.
const RuntimeBalancer = "balancer"

// BalancerSettings describe the pool.
type BalancerSettings struct {
	Method   string    `json:"method"` // round_robin | least_conn | ip_hash (sticky by visitor) | random
	Backends []Backend `json:"backends"`
	HTTPS    bool      `json:"https"`     // talk to backends over HTTPS
	HostName string    `json:"host_name"` // Host header sent to backends ("" = the visitor's)
	// MaxFails failures within FailSeconds take a backend out for FailSeconds.
	MaxFails    int `json:"max_fails"`
	FailSeconds int `json:"fail_seconds"`
}

// Backend is one server in the pool.
type Backend struct {
	Address string `json:"address"` // host:port
	Weight  int    `json:"weight"`  // 1-100
	Backup  bool   `json:"backup"`  // only used when every other backend is down
	Down    bool   `json:"down"`    // taken out by hand (draining)
}

func (b *BalancerSettings) check() error {
	switch b.Method {
	case "", "round_robin":
		b.Method = "round_robin"
	case "least_conn", "ip_hash", "random":
	default:
		return fmt.Errorf("method is round_robin, least_conn, ip_hash or random")
	}
	if len(b.Backends) == 0 || len(b.Backends) > 64 {
		return fmt.Errorf("add 1-64 backends")
	}
	live := 0
	for i := range b.Backends {
		be := &b.Backends[i]
		host, port, err := net.SplitHostPort(strings.TrimSpace(be.Address))
		if err != nil {
			return fmt.Errorf("backend %q: use host:port, like 10.0.0.5:80", be.Address)
		}
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("backend %q: port must be 1-65535", be.Address)
		}
		if net.ParseIP(host) == nil {
			if !hostLabelRe.MatchString(host) {
				return fmt.Errorf("backend %q: host must be an IP address or a host name", be.Address)
			}
		}
		be.Address = net.JoinHostPort(host, port)
		if be.Weight == 0 {
			be.Weight = 1
		}
		if be.Weight < 1 || be.Weight > 100 {
			return fmt.Errorf("weights are 1-100")
		}
		if be.Backup && b.Method == "ip_hash" {
			return fmt.Errorf("backup backends can't be used with ip_hash")
		}
		if !be.Down && !be.Backup {
			live++
		}
	}
	if live == 0 {
		return fmt.Errorf("at least one backend must be in service and not a backup")
	}
	if b.HostName != "" && !hostLabelRe.MatchString(b.HostName) {
		return fmt.Errorf("host name has unusual characters")
	}
	if b.MaxFails == 0 {
		b.MaxFails = 3
	}
	if b.FailSeconds == 0 {
		b.FailSeconds = 30
	}
	if b.MaxFails < 1 || b.MaxFails > 100 || b.FailSeconds < 1 || b.FailSeconds > 3600 {
		return fmt.Errorf("max fails 1-100, fail time 1-3600 seconds")
	}
	return nil
}

// resolvable makes sure Nginx can resolve every backend host name; an
// unknown name would stop Nginx from loading any website.
func (b *BalancerSettings) resolvable(ctx context.Context) error {
	for _, be := range b.Backends {
		host, _, _ := net.SplitHostPort(be.Address)
		if net.ParseIP(host) != nil {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := net.DefaultResolver.LookupHost(c, host)
		cancel()
		if err != nil {
			return fmt.Errorf("backend %s: this server can't resolve %s; use its IP address or add a DNS record first", be.Address, host)
		}
	}
	return nil
}

// upstreamName is unique per website and safe in Nginx and Apache configs.
func upstreamName(domain string) string { return "ike_lb_" + siteID(domain) }

// renderNginxUpstream is placed before the server blocks.
func renderNginxUpstream(domain string, b *BalancerSettings) string {
	var s strings.Builder
	fmt.Fprintf(&s, "upstream %s {\n", upstreamName(domain))
	switch b.Method {
	case "least_conn":
		s.WriteString("    least_conn;\n")
	case "ip_hash":
		s.WriteString("    ip_hash;\n")
	case "random":
		s.WriteString("    random two least_conn;\n")
	}
	for _, be := range b.Backends {
		fmt.Fprintf(&s, "    server %s weight=%d max_fails=%d fail_timeout=%ds", be.Address, be.Weight, b.MaxFails, b.FailSeconds)
		if be.Backup {
			s.WriteString(" backup")
		}
		if be.Down {
			s.WriteString(" down")
		}
		s.WriteString(";\n")
	}
	s.WriteString("    keepalive 32;\n}\n")
	return s.String()
}

// renderApacheBalancer is placed inside the VirtualHost.
func renderApacheBalancer(domain string, b *BalancerSettings) string {
	var s strings.Builder
	scheme := "http"
	if b.HTTPS {
		scheme = "https"
	}
	name := upstreamName(domain)
	fmt.Fprintf(&s, "<Proxy \"balancer://%s\">\n", name)
	for _, be := range b.Backends {
		status := ""
		if be.Backup {
			status = " status=+H"
		}
		if be.Down {
			status = " status=+D"
		}
		fmt.Fprintf(&s, "        BalancerMember \"%s://%s\" loadfactor=%d retry=%d%s\n", scheme, be.Address, be.Weight, b.FailSeconds, status)
	}
	method := map[string]string{"round_robin": "byrequests", "least_conn": "bybusyness", "ip_hash": "byrequests", "random": "byrequests"}[b.Method]
	fmt.Fprintf(&s, "        ProxySet lbmethod=%s", method)
	if b.Method == "ip_hash" {
		s.WriteString(" stickysession=IKE_LB")
	}
	s.WriteString("\n    </Proxy>\n")
	if b.HTTPS {
		s.WriteString("    SSLProxyEngine on\n")
	}
	if b.Method == "ip_hash" {
		s.WriteString("    Header add Set-Cookie \"IKE_LB=.%{BALANCER_WORKER_ROUTE}e; path=/\" env=BALANCER_ROUTE_CHANGED\n")
	}
	fmt.Fprintf(&s, "    ProxyPass /.well-known/acme-challenge/ !\n    ProxyPass / \"balancer://%s/\"\n    ProxyPassReverse / \"balancer://%s/\"\n", name, name)
	return s.String()
}

// SetBalancer changes a balancer website's pool.
func (m *Manager) SetBalancer(ctx context.Context, domain string, b BalancerSettings) (*Site, error) {
	if err := b.check(); err != nil {
		return nil, err
	}
	if !m.cfg.DryRun {
		if err := b.resolvable(ctx); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.get(domain)
	if err != nil {
		return nil, err
	}
	if site.Runtime != RuntimeBalancer {
		return nil, fmt.Errorf("%s isn't a load balancer website", domain)
	}
	old := *site
	site.Balancer = &b
	if err := m.applyWeb(ctx, site, &old); err != nil {
		return nil, err
	}
	return site, nil
}

func (m *Manager) applyBalancerLayout(s *Site, L *layout) {
	if s.Runtime != RuntimeBalancer || s.Balancer == nil {
		return
	}
	L.Balanced = true
	L.Proxied = false
	L.NginxUpstream = renderNginxUpstream(s.Domain, s.Balancer)
	L.ApacheBalancer = renderApacheBalancer(s.Domain, s.Balancer)
	L.UpstreamName = upstreamName(s.Domain)
	L.UpstreamScheme = "http"
	if s.Balancer.HTTPS {
		L.UpstreamScheme = "https"
	}
	L.UpstreamHost = "$host"
	if s.Balancer.HostName != "" {
		L.UpstreamHost = s.Balancer.HostName
	}
}
