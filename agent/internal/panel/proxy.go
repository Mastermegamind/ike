package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/YOUR-ORG/ike/agent/internal/panel/store"
)

// access describes which hosting accounts a person may manage.
type access struct {
	all      bool
	accounts map[string]bool
}

func (a access) has(acct string) bool { return a.all || a.accounts[acct] }

func (s *Server) accessFor(p *store.Person) access {
	if p.Role == store.Admin {
		return access{all: true}
	}
	acc := access{accounts: map[string]bool{}}
	s.Store.View(func(d *store.Data) {
		switch p.Role {
		case store.Reseller:
			for a, owner := range d.AccountOf {
				if owner == p.Username {
					acc.accounts[a] = true
				}
			}
		case store.User:
			for _, a := range p.Accounts {
				acc.accounts[a] = true
			}
		}
	})
	return acc
}

// siteAccount finds which account a website belongs to (cached).
func (s *Server) siteAccount(ctx context.Context, domain string) (string, error) {
	if v, ok := s.siteAcct.Load(domain); ok {
		return v.(string), nil
	}
	var site struct {
		Account string `json:"account"`
	}
	if err := s.Agent.Get(ctx, "/v1/sites/"+domain, &site); err != nil {
		return "", err
	}
	s.siteAcct.Store(domain, site.Account)
	return site.Account, nil
}

// Endpoints any signed-in person may read.
var publicReads = map[string]bool{"/v1/health": true, "/v1/system": true, "/v1/services": true, "/v1/runtimes": true, "/v1/apps": true}

// authorize decides whether a proxied agent request is allowed and returns a
// filter for list responses. It's deliberately an allow-list: anything not
// matched below is server-admin only.
func (s *Server) authorize(ctx context.Context, p *store.Person, method, apiPath string, body []byte) (filter func([]byte) []byte, after func(status int, resp []byte), err error) {
	acc := s.accessFor(p)
	if acc.all {
		return nil, nil, nil
	}
	parts := strings.Split(strings.Trim(apiPath, "/"), "/") // v1, accounts, name, ...
	if len(parts) < 2 {
		return nil, nil, errForbidden
	}
	if method == "GET" && publicReads[apiPath] {
		return nil, nil, nil
	}
	isReseller := p.Role == store.Reseller
	switch parts[1] {
	case "accounts":
		if len(parts) == 2 {
			switch method {
			case "GET":
				return filterBy("name", acc), nil, nil
			case "POST":
				if !isReseller {
					return nil, nil, fmt.Errorf("%w: only resellers and admins create accounts", errForbidden)
				}
				var req struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(body, &req)
				var quotaErr error
				s.Store.View(func(d *store.Data) {
					if d.ResellerAccounts(p.Username) >= p.MaxAccounts {
						quotaErr = fmt.Errorf("%w: your plan allows %d accounts", errForbidden, p.MaxAccounts)
					}
				})
				if quotaErr != nil {
					return nil, nil, quotaErr
				}
				return nil, func(status int, _ []byte) {
					if status == 201 {
						_ = s.Store.Update(func(d *store.Data) error { d.AccountOf[req.Name] = p.Username; return nil })
					}
				}, nil
			}
			return nil, nil, errForbidden
		}
		if !acc.has(parts[2]) {
			return nil, nil, errForbidden
		}
		if !isReseller {
			// Account owners manage what's inside their account, not the account itself.
			if method == "DELETE" && len(parts) == 3 {
				return nil, nil, fmt.Errorf("%w: ask your hosting provider to delete the account", errForbidden)
			}
			if len(parts) >= 4 && (parts[3] == "suspend" || parts[3] == "unsuspend" || parts[3] == "limits") {
				return nil, nil, errForbidden
			}
		}
		if method == "DELETE" && len(parts) == 3 {
			name := parts[2]
			return nil, func(status int, _ []byte) {
				if status < 300 {
					_ = s.Store.Update(func(d *store.Data) error { delete(d.AccountOf, name); return nil })
				}
			}, nil
		}
		return nil, nil, nil
	case "sites":
		if len(parts) == 2 {
			switch method {
			case "GET":
				return filterBy("account", acc), nil, nil
			case "POST":
				var req struct {
					Account string `json:"account"`
				}
				_ = json.Unmarshal(body, &req)
				if !acc.has(req.Account) {
					return nil, nil, fmt.Errorf("%w: account %s isn't one of yours", errForbidden, req.Account)
				}
				return nil, nil, nil
			}
			return nil, nil, errForbidden
		}
		a, err := s.siteAccount(ctx, parts[2])
		if err != nil {
			return nil, nil, err
		}
		if !acc.has(a) {
			return nil, nil, errForbidden
		}
		if !isReseller && len(parts) == 4 && parts[3] == "status" && bytes.Contains(body, []byte(`"suspended"`)) {
			return nil, nil, fmt.Errorf("%w: only your hosting provider can suspend a website", errForbidden)
		}
		var after func(int, []byte)
		if method == "DELETE" && len(parts) == 3 {
			domain := parts[2]
			after = func(int, []byte) { s.siteAcct.Delete(domain) }
		}
		if method == "POST" && len(parts) == 4 && (parts[3] == "staging" || parts[3] == "push") {
			after = s.recordJobOwner(p.Username)
		}
		return nil, after, nil
	case "usage":
		if method == "GET" {
			return filterBy("account", acc), nil, nil
		}
	case "jobs":
		if method == "GET" && len(parts) == 3 {
			var owner string
			s.Store.View(func(d *store.Data) { owner = d.JobOwner[parts[2]] })
			if owner == p.Username {
				return nil, nil, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("%w: only server admins can use %s", errForbidden, "/"+strings.Join(parts[1:2], "/"))
}

func (s *Server) recordJobOwner(user string) func(int, []byte) {
	return func(status int, resp []byte) {
		if status != http.StatusAccepted {
			return
		}
		var j struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(resp, &j) == nil && j.ID != "" {
			_ = s.Store.Update(func(d *store.Data) error { d.JobOwner[j.ID] = user; return nil })
		}
	}
}

// filterBy keeps list items whose key names an account the person can manage.
func filterBy(key string, acc access) func([]byte) []byte {
	return func(b []byte) []byte {
		var items []map[string]any
		if err := json.Unmarshal(b, &items); err != nil {
			return []byte("[]")
		}
		out := []map[string]any{}
		for _, it := range items {
			if v, _ := it[key].(string); acc.has(v) {
				out = append(out, it)
			}
		}
		nb, _ := json.Marshal(out)
		return nb
	}
}

// proxy forwards /api/v1/* to the agent's /v1/* after authorization.
func (s *Server) proxy(w http.ResponseWriter, r *http.Request) {
	p := personFrom(r)
	apiPath := "/v1/" + strings.TrimPrefix(r.URL.Path, "/api/v1/")
	if strings.Contains(apiPath, "..") {
		fail(w, 400, "Bad path.")
		return
	}
	body, err := readAll(r, 1<<20)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	filter, after, err := s.authorize(r.Context(), p, r.Method, apiPath, body)
	if err != nil {
		failErr(w, err)
		return
	}
	target := s.Agent.BaseURL + apiPath
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, _ := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	actor := p.Username
	if p.Role == store.Reseller {
		actor = "reseller:" + p.Username
	}
	req.Header.Set("X-Ike-Actor", actor)
	resp, err := s.Agent.HTTP.Do(req)
	if err != nil {
		fail(w, 502, "The server agent isn't answering. Check that ike-agent is running.")
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if after != nil {
		after(resp.StatusCode, data)
	}
	if filter != nil && resp.StatusCode == 200 {
		data = filter(data)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(data)
}
