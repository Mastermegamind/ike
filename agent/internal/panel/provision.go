package panel

// Provisioning API for billing systems (WHMCS, Blesta, HostBill, your own).
// Sign in with an API token that belongs to an admin or a reseller:
//
//	Authorization: Bearer ike_…
//
//	GET    /api/v1/provision/packages
//	POST   /api/v1/provision/accounts               {"account","domain","package","email","username","password"}
//	POST   /api/v1/provision/accounts/{a}/suspend   {"reason"}
//	POST   /api/v1/provision/accounts/{a}/unsuspend
//	DELETE /api/v1/provision/accounts/{a}
//	PUT    /api/v1/provision/accounts/{a}/package   {"package"}
//	PUT    /api/v1/provision/accounts/{a}/password  {"password"}
//	POST   /api/v1/provision/accounts/{a}/sso       → {"url"} one-time sign-in link, 2 minutes
//	GET    /api/v1/provision/usage                  disk and bandwidth per account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/panel/auth"
	"github.com/YOUR-ORG/ike/agent/internal/panel/store"
)

// formatMoney shows minor units in their currency: 250000 NGN → ₦2,500.00.
func formatMoney(minor int64, cur string) string {
	sym := map[string]string{"NGN": "₦", "USD": "$", "EUR": "€", "GBP": "£", "GHS": "GH₵", "KES": "KSh ", "ZAR": "R"}[cur]
	neg := minor < 0
	if neg {
		minor = -minor
	}
	whole := strconv.FormatInt(minor/100, 10)
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	s := fmt.Sprintf("%s.%02d", b.String(), minor%100)
	if sym == "" {
		s = s + " " + cur
	} else {
		s = sym + s
	}
	if neg {
		s = "-" + s
	}
	return s
}

// FormatMoney is formatMoney for other packages.
func FormatMoney(minor int64, cur string) string { return formatMoney(minor, cur) }

func (s *Server) provisionOnly(next func(w http.ResponseWriter, r *http.Request)) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if me := personFrom(r); me.Role == store.User {
			fail(w, 403, "Use an API token that belongs to a server admin or a reseller.")
			return
		}
		next(w, r)
	}
}

type provisionRequest struct {
	Account  string `json:"account"`  // Linux account name
	Domain   string `json:"domain"`   // main website (optional)
	Package  string `json:"package"`  // package ID or name
	Email    string `json:"email"`    // customer's email
	Username string `json:"username"` // panel login (default: the account name)
	Password string `json:"password"` // default: generated and returned
	Runtime  string `json:"runtime"`  // default php
}

func (s *Server) findPackage(me *store.Person, ref string) *store.Package {
	var out *store.Package
	s.Store.View(func(d *store.Data) {
		for _, p := range d.Packages {
			if (p.ID == ref || strings.EqualFold(p.Name, ref)) && canSeePackage(me, p) {
				c := *p
				out = &c
				return
			}
		}
	})
	return out
}

// provisionAccount creates the hosting account, its first website, the
// customer's panel login, assigns the package and starts the subscription.
// If a later step fails, earlier steps are undone.
func (s *Server) provisionAccount(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	var req provisionRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Account = strings.ToLower(strings.TrimSpace(req.Account))
	if req.Username == "" {
		req.Username = req.Account
	}
	req.Username = strings.ToLower(req.Username)
	if err := store.ValidateUsername(req.Username); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := store.ValidateEmail(req.Email); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var pkg *store.Package
	if req.Package != "" {
		if pkg = s.findPackage(me, req.Package); pkg == nil {
			fail(w, 404, "No package called "+req.Package+". Create it under Packages first.")
			return
		}
	}
	generated := req.Password == ""
	if generated {
		req.Password = auth.RandomToken(12)
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	// The reseller's account quota, checked the same way as in the panel.
	if me.Role == store.Reseller {
		var quotaErr error
		s.Store.View(func(d *store.Data) {
			if d.ResellerAccounts(me.Username) >= me.MaxAccounts {
				quotaErr = denied("your plan allows %d accounts", me.MaxAccounts)
			}
			if _, err := d.Person(req.Username); err == nil {
				quotaErr = fmt.Errorf("panel login %s %w", req.Username, store.ErrExists)
			}
		})
		if quotaErr != nil {
			failErr(w, quotaErr)
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	var undo []func()
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
	if err := s.agentCall(ctx, s.Agent, me, "POST", "/v1/accounts", map[string]string{"name": req.Account}, nil); err != nil {
		failErr(w, err)
		return
	}
	undo = append(undo, func() {
		_ = s.agentCall(context.Background(), s.Agent, me, "DELETE", "/v1/accounts/"+req.Account+"?with_sites=true", nil, nil)
	})
	_ = s.Store.Update(func(d *store.Data) error {
		if me.Role == store.Reseller {
			d.AccountOf[req.Account] = me.Username
		}
		return nil
	})
	undo = append(undo, func() {
		_ = s.Store.Update(func(d *store.Data) error { delete(d.AccountOf, req.Account); return nil })
	})
	if req.Domain != "" {
		if err := s.checkPackageLimits(ctx, me, "POST", "/v1/sites", []byte(`{"account":"`+req.Account+`"}`)); err != nil {
			rollback()
			failErr(w, err)
			return
		}
		body := map[string]string{"domain": req.Domain, "account": req.Account}
		if req.Runtime != "" {
			body["runtime"] = req.Runtime
		}
		if err := s.agentCall(ctx, s.Agent, me, "POST", "/v1/sites", body, nil); err != nil {
			rollback()
			failErr(w, err)
			return
		}
	}
	if pkg != nil {
		if _, err := s.assignPackage(ctx, me, req.Account, pkg.ID); err != nil {
			rollback()
			failErr(w, err)
			return
		}
		undo = append(undo, func() {
			_ = s.Store.Update(func(d *store.Data) error { delete(d.AccountPackage, req.Account); return nil })
		})
	}
	err = s.Store.Update(func(d *store.Data) error {
		p := &store.Person{Username: req.Username, Email: req.Email, Role: store.User, PasswordHash: hash, Accounts: []string{req.Account}}
		if me.Role == store.Reseller {
			p.Reseller = me.Username
		}
		return d.AddPerson(p)
	})
	if err != nil {
		rollback()
		failErr(w, err)
		return
	}
	out := map[string]any{"account": req.Account, "username": req.Username, "domain": req.Domain, "panel_url": s.PublicURL}
	if pkg != nil {
		out["package"] = pkg.Name
	}
	if generated {
		out["password"] = req.Password
	}
	writeJSON(w, 201, out)
}

func (s *Server) provisionAccess(w http.ResponseWriter, r *http.Request) (string, bool) {
	me := personFrom(r)
	a := r.PathValue("a")
	if !s.accessFor(me).has(a) {
		failErr(w, errForbidden)
		return "", false
	}
	return a, true
}

func (s *Server) provisionSuspend(w http.ResponseWriter, r *http.Request) {
	a, ok := s.provisionAccess(w, r)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = decode(r, &req)
	if req.Reason == "" {
		req.Reason = "Suspended by billing"
	}
	if len(req.Reason) > 200 {
		req.Reason = req.Reason[:200]
	}
	if err := s.agentCall(r.Context(), s.Agent, personFrom(r), "POST", "/v1/accounts/"+a+"/suspend", map[string]string{"reason": req.Reason}, nil); err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"suspended": a})
}

func (s *Server) provisionUnsuspend(w http.ResponseWriter, r *http.Request) {
	a, ok := s.provisionAccess(w, r)
	if !ok {
		return
	}
	if err := s.agentCall(r.Context(), s.Agent, personFrom(r), "POST", "/v1/accounts/"+a+"/unsuspend", map[string]string{}, nil); err != nil {
		failErr(w, err)
		return
	}
	_ = s.Store.Update(func(d *store.Data) error { delete(d.BWSuspended, a); return nil })
	writeJSON(w, 200, map[string]string{"unsuspended": a})
}

// provisionTerminate removes the account, its websites, its panel logins
// (people who manage only this account) and cancels its subscription.
func (s *Server) provisionTerminate(w http.ResponseWriter, r *http.Request) {
	a, ok := s.provisionAccess(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	if err := s.agentCall(ctx, s.Agent, personFrom(r), "DELETE", "/v1/accounts/"+a+"?with_sites=true", nil, nil); err != nil {
		failErr(w, err)
		return
	}
	var removed []string
	_ = s.Store.Update(func(d *store.Data) error {
		delete(d.AccountOf, a)
		delete(d.AccountPackage, a)
		delete(d.BWSuspended, a)
		for _, sub := range d.Subscriptions {
			if sub.Account == a && sub.Status == "active" {
				sub.Status = "cancelled"
			}
		}
		for name, p := range d.People {
			if p.Role != store.User {
				continue
			}
			keep := p.Accounts[:0]
			for _, x := range p.Accounts {
				if x != a {
					keep = append(keep, x)
				}
			}
			p.Accounts = keep
			if len(p.Accounts) == 0 {
				delete(d.People, name)
				removed = append(removed, name)
			}
		}
		return nil
	})
	writeJSON(w, 200, map[string]any{"terminated": a, "logins_removed": removed})
}

func (s *Server) provisionPackage(w http.ResponseWriter, r *http.Request) {
	a, ok := s.provisionAccess(w, r)
	if !ok {
		return
	}
	var req struct {
		Package string `json:"package"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	pkg := s.findPackage(personFrom(r), req.Package)
	if pkg == nil {
		fail(w, 404, "No package called "+req.Package)
		return
	}
	if _, err := s.assignPackage(r.Context(), personFrom(r), a, pkg.ID); err != nil {
		failErr(w, err)
		return
	}
	// a bigger package lifts a bandwidth suspension
	var bw string
	s.Store.View(func(d *store.Data) { bw = d.BWSuspended[a] })
	if bw != "" {
		_ = s.agentCall(r.Context(), s.Agent, personFrom(r), "POST", "/v1/accounts/"+a+"/unsuspend", map[string]string{}, nil)
		_ = s.Store.Update(func(d *store.Data) error { delete(d.BWSuspended, a); return nil })
	}
	writeJSON(w, 200, map[string]string{"account": a, "package": pkg.Name})
}

// ownerOf returns the customer login that manages an account.
func ownerOf(d *store.Data, account string) *store.Person {
	for _, p := range d.People {
		if p.Role != store.User {
			continue
		}
		for _, x := range p.Accounts {
			if x == account {
				return p
			}
		}
	}
	return nil
}

func (s *Server) provisionPassword(w http.ResponseWriter, r *http.Request) {
	a, ok := s.provisionAccess(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	h, err := auth.HashPassword(req.Password)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	user := ""
	err = s.Store.Update(func(d *store.Data) error {
		p := ownerOf(d, a)
		if p == nil {
			return errors.New("no panel login manages this account")
		}
		p.PasswordHash, user = h, p.Username
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	s.endSessions(user)
	writeJSON(w, 200, map[string]string{"username": user})
}

// endSessions signs a person out everywhere.
func (s *Server) endSessions(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, sess := range s.sessions {
		if sess.user == user {
			delete(s.sessions, k)
		}
	}
}

func (s *Server) provisionSSO(w http.ResponseWriter, r *http.Request) {
	a, ok := s.provisionAccess(w, r)
	if !ok {
		return
	}
	tok := auth.RandomToken(24)
	user := ""
	err := s.Store.Update(func(d *store.Data) error {
		p := ownerOf(d, a)
		if p == nil {
			return errors.New("no panel login manages this account")
		}
		if p.Disabled {
			return errors.New("the panel login for this account is disabled")
		}
		user = p.Username
		d.SSOTokens = append(d.SSOTokens, store.SSOToken{Hash: auth.HashToken(tok), Username: user, Expires: time.Now().Add(2 * time.Minute)})
		if len(d.SSOTokens) > 200 {
			d.SSOTokens = d.SSOTokens[len(d.SSOTokens)-200:]
		}
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"url": s.PublicURL + "/sso/" + tok, "username": user, "expires_in": "120s"})
}

func (s *Server) provisionUsage(w http.ResponseWriter, r *http.Request) {
	acc := s.accessFor(personFrom(r))
	var usage []struct {
		Account string  `json:"account"`
		DiskMB  float64 `json:"disk_mb"`
	}
	_ = s.Agent.Get(r.Context(), "/v1/usage", &usage)
	month := time.Now().UTC().Format("2006-01")
	type row struct {
		Account     string  `json:"account"`
		DiskMB      float64 `json:"disk_mb"`
		DiskLimitMB int     `json:"disk_limit_mb"`
		BandwidthMB float64 `json:"bandwidth_mb"`
		BWLimitMB   int     `json:"bandwidth_limit_mb"`
	}
	out := []row{}
	s.Store.View(func(d *store.Data) {
		for _, u := range usage {
			if !acc.has(u.Account) {
				continue
			}
			x := row{Account: u.Account, DiskMB: u.DiskMB, BandwidthMB: d.Bandwidth[month][u.Account]}
			for _, p := range d.Packages {
				if p.ID == d.AccountPackage[u.Account] {
					x.DiskLimitMB, x.BWLimitMB = p.Limits.DiskMB, p.Limits.BandwidthGB*1024
				}
			}
			out = append(out, x)
		}
	})
	writeJSON(w, 200, out)
}
