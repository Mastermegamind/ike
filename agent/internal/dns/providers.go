package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
)

func randID() string { return kit.RandomHex(6) }

// Provider is an external DNS service. Ike keeps the records and makes the
// provider match them.
type Provider interface {
	// EnsureZone finds or creates the zone and returns its ID and the
	// provider's name servers.
	EnsureZone(ctx context.Context, zone string) (id string, nameservers []string, err error)
	List(ctx context.Context, id, zone string) ([]Record, error)
	Create(ctx context.Context, id, zone string, r Record) error
	Delete(ctx context.Context, id, zone, recordID string) error
	DeleteZone(ctx context.Context, id string) error
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func defaultHTTP() httpDoer { return &http.Client{Timeout: 30 * time.Second} }

func (m *Manager) provider(backend string) (Provider, error) {
	set, err := m.settings()
	if err != nil {
		return nil, err
	}
	switch backend {
	case "cloudflare":
		tok, err := m.keys.Unseal(set.CloudflareToken)
		if err != nil || tok == "" {
			return nil, fmt.Errorf("add a Cloudflare API token in DNS settings first")
		}
		return &cloudflare{token: tok, account: set.CloudflareAccount, http: m.http, base: "https://api.cloudflare.com/client/v4"}, nil
	case "digitalocean":
		tok, err := m.keys.Unseal(set.DigitalOceanToken)
		if err != nil || tok == "" {
			return nil, fmt.Errorf("add a DigitalOcean API token in DNS settings first")
		}
		return &digitalocean{token: tok, http: m.http, base: "https://api.digitalocean.com/v2"}, nil
	}
	return nil, fmt.Errorf("unknown DNS provider %q", backend)
}

// publishProvider makes the provider's records match the zone: records only
// at the provider are deleted, records only in Ike are created. SOA and the
// apex NS set belong to the provider and are left alone.
func (m *Manager) publishProvider(ctx context.Context, z *Zone) error {
	p, err := m.providers(z.Backend)
	if err != nil {
		return err
	}
	if z.ProviderID == "" {
		id, ns, err := p.EnsureZone(ctx, z.Name)
		if err != nil {
			return fmt.Errorf("%s: %w", z.Backend, err)
		}
		z.ProviderID, z.Nameservers = id, ns
	}
	remote, err := p.List(ctx, z.ProviderID, z.Name)
	if err != nil {
		return fmt.Errorf("%s: %w", z.Backend, err)
	}
	same := func(a, b Record) bool {
		return SameIn(a, b, z.Name) && (z.Backend != "cloudflare" || a.Proxied == b.Proxied) && a.TTL == b.TTL
	}
	for _, r := range remote {
		if r.Type == "SOA" || (r.Type == "NS" && r.Name == "@") {
			continue
		}
		keep := false
		for _, l := range z.Records {
			if same(l, r) {
				keep = true
			}
		}
		if !keep {
			if err := p.Delete(ctx, z.ProviderID, z.Name, r.ID); err != nil {
				return fmt.Errorf("%s: removing %s %s: %w", z.Backend, r.Type, r.Name, err)
			}
		}
	}
	for _, l := range z.Records {
		found := false
		for _, r := range remote {
			if same(l, r) {
				found = true
			}
		}
		if !found {
			if err := p.Create(ctx, z.ProviderID, z.Name, l); err != nil {
				return fmt.Errorf("%s: adding %s %s: %w", z.Backend, l.Type, l.Name, err)
			}
		}
	}
	return nil
}

// PullFromProvider imports the provider's current records into Ike (for zones
// that already existed at the provider).
func (m *Manager) PullFromProvider(ctx context.Context, account, zone string) (*Zone, error) {
	m.mu.Lock()
	z, err := m.Get(account, zone)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if z.Backend == "local" {
		return nil, fmt.Errorf("%s is served by this server", zone)
	}
	p, err := m.providers(z.Backend)
	if err != nil {
		return nil, err
	}
	remote, err := p.List(ctx, z.ProviderID, z.Name)
	if err != nil {
		return nil, err
	}
	recs := []Record{}
	for _, r := range remote {
		if r.Type == "SOA" || (r.Type == "NS" && r.Name == "@") {
			continue
		}
		r.ID = ""
		if r.Validate() == nil {
			recs = append(recs, r)
		}
	}
	return m.ReplaceRecords(ctx, account, zone, recs)
}

func doJSON(ctx context.Context, h httpDoer, method, u, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &e)
		msg := e.Message
		for _, x := range e.Errors {
			msg += " " + x.Message
		}
		if strings.TrimSpace(msg) == "" {
			msg = resp.Status
		}
		return fmt.Errorf("%s", strings.TrimSpace(msg))
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// ---- Cloudflare ----

type cloudflare struct {
	token, account, base string
	http                 httpDoer
}

type cfRecord struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content,omitempty"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority,omitempty"`
	Proxied  *bool  `json:"proxied,omitempty"`
	Data     any    `json:"data,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

func (c *cloudflare) EnsureZone(ctx context.Context, zone string) (string, []string, error) {
	var res struct {
		Result []struct {
			ID          string   `json:"id"`
			NameServers []string `json:"name_servers"`
		} `json:"result"`
	}
	if err := doJSON(ctx, c.http, "GET", c.base+"/zones?name="+url.QueryEscape(zone), c.token, nil, &res); err != nil {
		return "", nil, err
	}
	if len(res.Result) > 0 {
		return res.Result[0].ID, res.Result[0].NameServers, nil
	}
	body := map[string]any{"name": zone, "type": "full"}
	if c.account != "" {
		body["account"] = map[string]string{"id": c.account}
	}
	var created struct {
		Result struct {
			ID          string   `json:"id"`
			NameServers []string `json:"name_servers"`
		} `json:"result"`
	}
	if err := doJSON(ctx, c.http, "POST", c.base+"/zones", c.token, body, &created); err != nil {
		return "", nil, err
	}
	return created.Result.ID, created.Result.NameServers, nil
}

func (c *cloudflare) List(ctx context.Context, id, zone string) ([]Record, error) {
	out := []Record{}
	for page := 1; page <= 50; page++ {
		var res struct {
			Result []struct {
				cfRecord
				Proxied bool `json:"proxied"`
				Data    struct {
					Priority, Weight, Port int
					Target                 string
					Flags                  int
					Tag, Value             string
				} `json:"data"`
				Priority int `json:"priority"`
			} `json:"result"`
			ResultInfo struct {
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
		}
		u := fmt.Sprintf("%s/zones/%s/dns_records?per_page=500&page=%d", c.base, url.PathEscape(id), page)
		if err := doJSON(ctx, c.http, "GET", u, c.token, nil, &res); err != nil {
			return nil, err
		}
		for _, r := range res.Result {
			rec := Record{ID: r.ID, Type: r.Type, Name: RelativeTo(r.Name, zone), TTL: r.TTL, Content: r.Content, Proxied: r.Proxied, Comment: r.Comment}
			if rec.TTL == 1 {
				rec.TTL = 300 // "automatic"
			}
			switch r.Type {
			case "MX":
				rec.Priority = r.Priority
				rec.Content += "."
			case "CNAME", "NS", "PTR":
				rec.Content += "."
			case "SRV":
				rec.Priority, rec.Weight, rec.Port, rec.Content = r.Data.Priority, r.Data.Weight, r.Data.Port, r.Data.Target+"."
			case "CAA":
				rec.Content = fmt.Sprintf(`%d %s "%s"`, r.Data.Flags, r.Data.Tag, r.Data.Value)
			case "TXT":
				rec.Content = unquoteTXT(r.Content)
			}
			out = append(out, rec)
		}
		if page >= res.ResultInfo.TotalPages {
			break
		}
	}
	return out, nil
}

func (c *cloudflare) Create(ctx context.Context, id, zone string, r Record) error {
	cr := cfRecord{Type: r.Type, Name: strings.TrimSuffix(FQDN(r.Name, zone), "."), TTL: r.TTL, Comment: r.Comment}
	if r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME" {
		p := r.Proxied
		cr.Proxied = &p
		if p {
			cr.TTL = 1
		}
	}
	host := strings.TrimSuffix(absTarget(r.Content, zone), ".")
	switch r.Type {
	case "MX":
		p := r.Priority
		cr.Priority, cr.Content = &p, host
	case "CNAME", "NS", "PTR":
		cr.Content = host
	case "SRV":
		cr.Data = map[string]any{"priority": r.Priority, "weight": r.Weight, "port": r.Port, "target": host}
	case "CAA":
		f := strings.SplitN(r.Content, " ", 3)
		var flags int
		fmt.Sscanf(f[0], "%d", &flags)
		cr.Data = map[string]any{"flags": flags, "tag": f[1], "value": strings.Trim(f[2], `"`)}
	case "TXT":
		cr.Content = r.Content
	default:
		cr.Content = r.Content
	}
	return doJSON(ctx, c.http, "POST", fmt.Sprintf("%s/zones/%s/dns_records", c.base, url.PathEscape(id)), c.token, cr, nil)
}

func (c *cloudflare) Delete(ctx context.Context, id, _ string, rid string) error {
	return doJSON(ctx, c.http, "DELETE", fmt.Sprintf("%s/zones/%s/dns_records/%s", c.base, url.PathEscape(id), url.PathEscape(rid)), c.token, nil, nil)
}

func (c *cloudflare) DeleteZone(ctx context.Context, id string) error {
	return doJSON(ctx, c.http, "DELETE", fmt.Sprintf("%s/zones/%s", c.base, url.PathEscape(id)), c.token, nil, nil)
}

// ---- DigitalOcean ----

type digitalocean struct {
	token, base string
	http        httpDoer
}

type doRecord struct {
	ID       int    `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Data     string `json:"data"`
	Priority *int   `json:"priority"`
	Port     *int   `json:"port"`
	Weight   *int   `json:"weight"`
	TTL      int    `json:"ttl"`
	Flags    *int   `json:"flags"`
	Tag      string `json:"tag,omitempty"`
}

func (d *digitalocean) EnsureZone(ctx context.Context, zone string) (string, []string, error) {
	ns := []string{"ns1.digitalocean.com", "ns2.digitalocean.com", "ns3.digitalocean.com"}
	err := doJSON(ctx, d.http, "GET", d.base+"/domains/"+url.PathEscape(zone), d.token, nil, nil)
	if err == nil {
		return zone, ns, nil
	}
	if err := doJSON(ctx, d.http, "POST", d.base+"/domains", d.token, map[string]string{"name": zone}, nil); err != nil {
		return "", nil, err
	}
	return zone, ns, nil
}

func (d *digitalocean) List(ctx context.Context, id, zone string) ([]Record, error) {
	out := []Record{}
	for page := 1; page <= 50; page++ {
		var res struct {
			Records []doRecord `json:"domain_records"`
			Links   struct {
				Pages struct {
					Next string `json:"next"`
				} `json:"pages"`
			} `json:"links"`
		}
		u := fmt.Sprintf("%s/domains/%s/records?per_page=200&page=%d", d.base, url.PathEscape(id), page)
		if err := doJSON(ctx, d.http, "GET", u, d.token, nil, &res); err != nil {
			return nil, err
		}
		for _, r := range res.Records {
			rec := Record{ID: fmt.Sprint(r.ID), Type: r.Type, Name: r.Name, TTL: r.TTL, Content: r.Data}
			if r.Priority != nil {
				rec.Priority = *r.Priority
			}
			if r.Port != nil {
				rec.Port = *r.Port
			}
			if r.Weight != nil {
				rec.Weight = *r.Weight
			}
			switch r.Type {
			case "CAA":
				f := 0
				if r.Flags != nil {
					f = *r.Flags
				}
				rec.Content = fmt.Sprintf(`%d %s "%s"`, f, r.Tag, strings.Trim(r.Data, `"`))
			case "MX", "CNAME", "NS", "SRV":
				if r.Data == "@" {
					rec.Content = "@"
				} else if !strings.HasSuffix(r.Data, ".") {
					rec.Content = r.Data + "." + zone + "."
				}
			}
			out = append(out, rec)
		}
		if res.Links.Pages.Next == "" {
			break
		}
	}
	return out, nil
}

func (d *digitalocean) Create(ctx context.Context, id, zone string, r Record) error {
	ip := func(v int) *int { return &v }
	dr := doRecord{Type: r.Type, Name: r.Name, TTL: r.TTL, Data: r.Content}
	if dr.TTL < 30 {
		dr.TTL = 30
	}
	switch r.Type {
	case "MX":
		dr.Priority, dr.Data = ip(r.Priority), absTarget(r.Content, zone)
	case "SRV":
		dr.Priority, dr.Weight, dr.Port, dr.Data = ip(r.Priority), ip(r.Weight), ip(r.Port), absTarget(r.Content, zone)
	case "CNAME", "NS":
		dr.Data = absTarget(r.Content, zone)
	case "CAA":
		f := strings.SplitN(r.Content, " ", 3)
		var flags int
		fmt.Sscanf(f[0], "%d", &flags)
		dr.Flags, dr.Tag, dr.Data = ip(flags), f[1], strings.Trim(f[2], `"`)
	case "PTR", "TLSA", "SSHFP", "DS", "NAPTR":
		return fmt.Errorf("DigitalOcean DNS does not support %s records", r.Type)
	}
	return doJSON(ctx, d.http, "POST", fmt.Sprintf("%s/domains/%s/records", d.base, url.PathEscape(id)), d.token, dr, nil)
}

func (d *digitalocean) Delete(ctx context.Context, id, _ string, rid string) error {
	return doJSON(ctx, d.http, "DELETE", fmt.Sprintf("%s/domains/%s/records/%s", d.base, url.PathEscape(id), url.PathEscape(rid)), d.token, nil, nil)
}

func (d *digitalocean) DeleteZone(ctx context.Context, id string) error {
	return doJSON(ctx, d.http, "DELETE", d.base+"/domains/"+url.PathEscape(id), d.token, nil, nil)
}
