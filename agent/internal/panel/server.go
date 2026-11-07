// Package panel is Ike's control plane: sign-in with 2FA, roles (server admin,
// reseller, account owner), billing, alerts, and an authorizing proxy in front
// of the root agent. The agent never sees a request this package hasn't checked.
package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/client"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/panel/alerts"
	"github.com/Mastermegamind/ike/agent/internal/panel/auth"
	"github.com/Mastermegamind/ike/agent/internal/panel/billing"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

const sessionCookie = "ike_session"

// Listener describes one panel port and which roles may sign in on it.
type Listener struct {
	Name  string       `json:"name"` // admin | reseller | panel
	Addr  string       `json:"addr"` // ":2087"
	Roles []store.Role `json:"roles"`
}

type session struct {
	user    string
	expires time.Time
	ip      string
}

type Server struct {
	Store     *store.Store
	Agent     *client.Client
	UI        fs.FS
	PublicURL string // https://panel.example.com:2083, for payment return links
	// MailHook replaces sending customer emails (tests).
	MailHook func(fromName, to, subject, body string) error
	TLS      bool
	Issuer   string // shown in authenticator apps
	Notifier *alerts.Notifier
	Monitor  *alerts.Monitor
	// Providers overrides billing providers (tests); nil = from settings.
	Providers func() map[string]billing.Provider

	throttle       *auth.Throttle
	mu             sync.Mutex
	sessions       map[string]*session        // key: hash of token
	ceremonies     map[string]pendingCeremony // passkey challenges, single use
	siteAcct       sync.Map                   // domain -> account (cache)
	webhookLimiter rateLimiter

	// Multi-server: the cluster authority lives in ClusterDir; joining
	// servers check the panel certificate's CertFingerprint.
	ClusterDir      string
	CertFingerprint string
	AdminPort       string // "2087"
	LocalIP         string // this server's public address (cluster primaries)
	remote          remotes
}

type ctxKey int

const (
	keyListener ctxKey = iota
	keyPerson
)

func (s *Server) init() {
	if s.throttle == nil {
		s.throttle = auth.NewThrottle(10, 15*time.Minute)
	}
	if s.sessions == nil {
		s.sessions = map[string]*session{}
	}
	if s.Issuer == "" {
		s.Issuer = "Ike"
	}
}

// Handler serves one listener.
func (s *Server) Handler(l Listener) http.Handler {
	s.init()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/session", s.login)
	mux.HandleFunc("GET /api/v1/session", s.authed(s.me))
	mux.HandleFunc("DELETE /api/v1/session", s.authed(s.logout))
	mux.HandleFunc("POST /api/v1/me/password", s.authed(s.changePassword))
	mux.HandleFunc("POST /api/v1/me/2fa/setup", s.authed(s.totpSetup))
	mux.HandleFunc("POST /api/v1/me/2fa/enable", s.authed(s.totpEnable))
	mux.HandleFunc("POST /api/v1/me/2fa/disable", s.authed(s.totpDisable))
	mux.HandleFunc("POST /api/v1/me/passkeys/options", s.authed(s.passkeyOptions))
	mux.HandleFunc("POST /api/v1/me/passkeys", s.authed(s.addPasskey))
	mux.HandleFunc("GET /api/v1/me/passkeys", s.authed(s.listPasskeys))
	mux.HandleFunc("PATCH /api/v1/me/passkeys/{id}", s.authed(s.updatePasskey))
	mux.HandleFunc("DELETE /api/v1/me/passkeys/{id}", s.authed(s.deletePasskey))
	mux.HandleFunc("POST /api/v1/session/passkey/options", s.passkeyLoginOptions)
	mux.HandleFunc("POST /api/v1/session/passkey", s.passkeyLogin)

	mux.HandleFunc("GET /api/v1/people", s.authed(s.listPeople))
	mux.HandleFunc("POST /api/v1/people", s.authed(s.createPerson))
	mux.HandleFunc("PATCH /api/v1/people/{username}", s.authed(s.updatePerson))
	mux.HandleFunc("DELETE /api/v1/people/{username}", s.authed(s.deletePerson))

	mux.HandleFunc("GET /api/v1/billing/settings", s.authed(s.adminOnly(s.getBillingSettings)))
	mux.HandleFunc("PUT /api/v1/billing/settings", s.authed(s.adminOnly(s.putBillingSettings)))
	mux.HandleFunc("GET /api/v1/billing/invoices", s.authed(s.listInvoices))
	mux.HandleFunc("POST /api/v1/billing/invoices", s.authed(s.createInvoice))
	mux.HandleFunc("POST /api/v1/billing/invoices/{id}/checkout", s.authed(s.checkout))
	mux.HandleFunc("POST /api/v1/billing/invoices/{id}/transfer", s.authed(s.transfer))
	mux.HandleFunc("POST /api/v1/billing/invoices/{id}/void", s.authed(s.voidInvoice))
	mux.HandleFunc("POST /api/v1/billing/invoices/{id}/mark-paid", s.authed(s.adminOnly(s.markPaid)))
	mux.HandleFunc("POST /webhooks/{provider}", s.webhook)
	mux.HandleFunc("POST /webhooks/git/{domain}", s.gitWebhook)

	mux.HandleFunc("GET /api/v1/alerts/settings", s.authed(s.adminOnly(s.getAlertSettings)))
	mux.HandleFunc("PUT /api/v1/alerts/settings", s.authed(s.adminOnly(s.putAlertSettings)))
	mux.HandleFunc("POST /api/v1/alerts/test", s.authed(s.adminOnly(s.testAlert)))
	mux.HandleFunc("GET /api/v1/alerts/events", s.authed(s.adminOnly(s.alertEvents)))

	mux.HandleFunc("POST /cluster/join", s.join)
	mux.HandleFunc("GET /api/v1/servers", s.authed(s.adminOnly(s.listServers)))
	mux.HandleFunc("POST /api/v1/servers/join-token", s.authed(s.adminOnly(s.createJoinToken)))
	mux.HandleFunc("PATCH /api/v1/servers/{id}", s.authed(s.adminOnly(s.updateServer)))
	mux.HandleFunc("DELETE /api/v1/servers/{id}", s.authed(s.adminOnly(s.deleteServer)))
	mux.HandleFunc("POST /api/v1/fanout", s.authed(s.adminOnly(s.fanout)))
	mux.HandleFunc("GET /api/v1/clusters", s.authed(s.adminOnly(s.listClusters)))
	mux.HandleFunc("POST /api/v1/clusters", s.authed(s.adminOnly(s.createCluster)))
	mux.HandleFunc("POST /api/v1/clusters/{id}/promote", s.authed(s.adminOnly(s.promoteCluster)))
	mux.HandleFunc("DELETE /api/v1/clusters/{id}", s.authed(s.adminOnly(s.deleteCluster)))
	mux.HandleFunc("GET /s/{token}", s.shareLink)
	mux.HandleFunc("POST /s/{token}", s.shareLink)
	mux.HandleFunc("GET /api/v1/tokens", s.authed(s.listTokens))
	mux.HandleFunc("POST /api/v1/tokens", s.authed(s.createToken))
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.authed(s.deleteToken))
	mux.HandleFunc("GET /api/v1/packages", s.authed(s.listPackages))
	mux.HandleFunc("POST /api/v1/packages", s.authed(s.resellerOrAdmin(s.savePackage)))
	mux.HandleFunc("PUT /api/v1/packages/{id}", s.authed(s.resellerOrAdmin(s.savePackage)))
	mux.HandleFunc("DELETE /api/v1/packages/{id}", s.authed(s.resellerOrAdmin(s.deletePackage)))
	mux.HandleFunc("PUT /api/v1/account-packages/{name}", s.authed(s.setAccountPackage))
	mux.HandleFunc("GET /api/v1/account-packages", s.authed(s.listAccountPackages))
	mux.HandleFunc("GET /api/v1/bandwidth", s.authed(s.bandwidth))
	mux.HandleFunc("GET /api/v1/billing/subscriptions", s.authed(s.listSubscriptions))
	mux.HandleFunc("POST /api/v1/billing/subscriptions", s.authed(s.createSubscription))
	mux.HandleFunc("POST /api/v1/billing/subscriptions/{id}/cancel", s.authed(s.cancelSubscription))
	mux.HandleFunc("GET /api/v1/branding", s.getBranding) // public: the sign-in page needs it
	mux.HandleFunc("GET /api/v1/me/branding", s.authed(s.getMyBranding))
	mux.HandleFunc("PUT /api/v1/me/branding", s.authed(s.putBranding))
	mux.HandleFunc("GET /api/v1/notifications", s.authed(s.notifications))
	mux.HandleFunc("POST /api/v1/notifications/read", s.authed(s.markNotificationsRead))
	mux.HandleFunc("GET /sso/{token}", s.ssoLogin)
	mux.HandleFunc("GET /api/v1/provision/packages", s.authed(s.provisionOnly(s.listPackages)))
	mux.HandleFunc("GET /api/v1/provision/usage", s.authed(s.provisionOnly(s.provisionUsage)))
	mux.HandleFunc("POST /api/v1/provision/accounts", s.authed(s.provisionOnly(s.provisionAccount)))
	mux.HandleFunc("POST /api/v1/provision/accounts/{a}/suspend", s.authed(s.provisionOnly(s.provisionSuspend)))
	mux.HandleFunc("POST /api/v1/provision/accounts/{a}/unsuspend", s.authed(s.provisionOnly(s.provisionUnsuspend)))
	mux.HandleFunc("DELETE /api/v1/provision/accounts/{a}", s.authed(s.provisionOnly(s.provisionTerminate)))
	mux.HandleFunc("PUT /api/v1/provision/accounts/{a}/package", s.authed(s.provisionOnly(s.provisionPackage)))
	mux.HandleFunc("PUT /api/v1/provision/accounts/{a}/password", s.authed(s.provisionOnly(s.provisionPassword)))
	mux.HandleFunc("POST /api/v1/provision/accounts/{a}/sso", s.authed(s.provisionOnly(s.provisionSSO)))
	mux.HandleFunc("GET /api/v1/expiry", s.authed(s.listExpiry))
	mux.HandleFunc("PUT /api/v1/expiry/hosting/{account}", s.authed(s.setHostingExpiry))
	mux.HandleFunc("POST /api/v1/expiry/domains/{domain}/refresh", s.authed(s.refreshDomainExpiry))
	mux.HandleFunc("PUT /api/v1/expiry/domains/{domain}", s.authed(s.setDomainRenewal))
	mux.HandleFunc("POST /api/v1/expiry/domains/{domain}/renew", s.authed(s.renewDomain))
	mux.HandleFunc("/api/v1/", s.authed(s.proxy))
	mux.HandleFunc("/", s.static)

	return s.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), keyListener, l)
		mux.ServeHTTP(w, r.WithContext(ctx))
	}))
}

func (s *Server) securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
			"font-src 'self' https://fonts.gstatic.com; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if s.TLS {
			hd.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- helpers --

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	// An empty list is [], not null, so clients can always iterate it.
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		_, _ = w.Write([]byte("[]\n"))
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func failErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, kit.ErrNotFound):
		fail(w, 404, err.Error())
	case errors.Is(err, store.ErrExists):
		fail(w, 409, err.Error())
	case errors.Is(err, errForbidden), errors.Is(err, kit.ErrForbidden):
		fail(w, 403, err.Error())
	default:
		fail(w, 400, err.Error())
	}
}

var errForbidden = errors.New("you don't have access to that")

// decodeBody decodes JSON or answers 400.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := decode(r, v); err != nil {
		fail(w, 400, err.Error())
		return false
	}
	return true
}

func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 256<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func personFrom(r *http.Request) *store.Person {
	p, _ := r.Context().Value(keyPerson).(*store.Person)
	return p
}

// ------------------------------------------------------------- sessions --

func (s *Server) newSession(w http.ResponseWriter, r *http.Request, user string) {
	tok := auth.RandomToken(32)
	s.mu.Lock()
	s.sessions[auth.HashToken(tok)] = &session{user: user, expires: time.Now().Add(12 * time.Hour), ip: clientIP(r)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, Secure: s.TLS,
		SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600})
}

func (s *Server) sessionUser(r *http.Request) (string, string) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", ""
	}
	key := auth.HashToken(c.Value)
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[key]
	if !ok || time.Now().After(sess.expires) {
		delete(s.sessions, key)
		return "", ""
	}
	sess.expires = time.Now().Add(12 * time.Hour) // sliding
	return sess.user, key
}

// authed requires a valid session, an allowed role for this port, and — for
// anything that changes state — the X-Requested-With header that browsers
// won't send cross-site without permission (CSRF protection on top of SameSite).
func (s *Server) authed(next func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := s.sessionUser(r)
		viaToken := false
		var tok *store.APIToken
		if user == "" {
			// Scripts, the ike CLI, AI agents and billing systems use API
			// tokens. A browser never sends this header on its own, so no CSRF check.
			if user, tok = s.bearerUser(r); user != "" {
				viaToken = true
				if tok.ReadOnly && r.Method != http.MethodGet {
					fail(w, 403, "This API token is read-only.")
					return
				}
				if err := tokenAllows(tok, r.Method, r.URL.Path, clientIP(r)); err != nil {
					fail(w, 403, err.Error())
					return
				}
			}
		}
		if user == "" {
			fail(w, 401, "Sign in to continue.")
			return
		}
		if !viaToken && r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "ike" {
			fail(w, 403, "Missing X-Requested-With header.")
			return
		}
		var p store.Person
		var err error
		s.Store.View(func(d *store.Data) {
			var pp *store.Person
			if pp, err = d.Person(user); err == nil {
				p = *pp
			}
		})
		if err != nil || p.Disabled {
			fail(w, 401, "Sign in to continue.")
			return
		}
		if err := developerAllows(&p, r.Method, r.URL.Path); err != nil {
			fail(w, 403, err.Error())
			return
		}
		p.Token = tok
		ctx := context.WithValue(r.Context(), keyPerson, &p)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) adminOnly(next func(w http.ResponseWriter, r *http.Request)) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if personFrom(r).Role != store.Admin {
			fail(w, 403, "Only server admins can do that.")
			return
		}
		next(w, r)
	}
}

func (s *Server) resellerOrAdmin(next func(w http.ResponseWriter, r *http.Request)) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if personFrom(r).Role.Customer() {
			fail(w, 403, "Only resellers and server admins can do that.")
			return
		}
		next(w, r)
	}
}

func roleAllowed(l Listener, role store.Role) bool {
	if len(l.Roles) == 0 {
		return true
	}
	for _, r := range l.Roles {
		// Developers sign in wherever account owners do.
		if r == role || (role == store.Developer && r == store.User) {
			return true
		}
	}
	return false
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	user := strings.ToLower(strings.TrimSpace(body.Username))
	ip := clientIP(r)
	now := time.Now()
	if blocked, wait := s.throttle.Blocked(now, "u:"+user, "ip:"+ip); blocked {
		fail(w, 429, fmt.Sprintf("Too many failed sign-ins. Try again in %d minutes.", int(wait.Minutes())+1))
		return
	}
	l, _ := r.Context().Value(keyListener).(Listener)
	var result struct {
		ok, needCode bool
		msg          string
	}
	err := s.Store.Update(func(d *store.Data) error {
		p, err := d.Person(user)
		// Same message whether the user exists or not.
		if err != nil || p.Disabled || !auth.VerifyPassword(p.PasswordHash, body.Password) {
			result.msg = "That username and password don't match."
			return errors.New("no change")
		}
		if !roleAllowed(l, p.Role) {
			result.msg = fmt.Sprintf("This sign-in page is for %s. Use your own panel address.", strings.Join(roleNames(l.Roles), " and "))
			return errors.New("no change")
		}
		if p.TOTPEnabled {
			if body.Code == "" {
				result.needCode = true
				return errors.New("no change")
			}
			if ctr, ok := auth.VerifyTOTP(p.TOTPSecret, body.Code, now, p.TOTPLast); ok {
				p.TOTPLast = ctr
			} else if i := indexOf(p.RecoveryCodes, auth.HashToken(strings.ToLower(strings.TrimSpace(body.Code)))); i >= 0 {
				p.RecoveryCodes = append(p.RecoveryCodes[:i], p.RecoveryCodes[i+1:]...)
			} else {
				result.msg = "That code isn't right. Use the current code from your authenticator app, or a recovery code."
				return errors.New("no change")
			}
		}
		p.LastLoginAt = now.UTC()
		result.ok = true
		return nil
	})
	switch {
	case result.ok && err == nil:
		s.throttle.Reset("u:"+user, "ip:"+ip)
		s.newSession(w, r, user)
		var pub store.Public
		s.Store.View(func(d *store.Data) { p, _ := d.Person(user); pub = p.Public() })
		slog.Info("signed in", "user", user, "ip", ip, "listener", l.Name)
		writeJSON(w, 200, pub)
	case result.needCode:
		writeJSON(w, 401, map[string]any{"two_factor_required": true, "error": "Enter the code from your authenticator app."})
	default:
		s.throttle.Fail(now, "u:"+user, "ip:"+ip)
		slog.Warn("sign-in failed", "user", user, "ip", ip)
		fail(w, 401, result.msg)
	}
}

func roleNames(rs []store.Role) []string {
	m := map[store.Role]string{store.Admin: "server admins", store.Reseller: "resellers", store.User: "account owners and developers", store.Developer: "developers"}
	var out []string
	for _, r := range rs {
		out = append(out, m[r])
	}
	return out
}

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return -1
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := personFrom(r)
	if p.Token == nil {
		writeJSON(w, 200, p.Public())
		return
	}
	// Signed in with an API token: say what the token may do, so the CLI
	// and MCP server can show (and hide) what's out of reach.
	writeJSON(w, 200, struct {
		store.Public
		Token tokenView `json:"token"`
	}{p.Public(), viewOf(p.Token)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if _, key := s.sessionUser(r); key != "" {
		s.mu.Lock()
		delete(s.sessions, key)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.TLS, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"signed_out": true})
}

// dropSessions signs a person out everywhere (password change, disable, delete).
func (s *Server) dropSessions(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.sessions {
		if v.user == user {
			delete(s.sessions, k)
		}
	}
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct{ Current, New string }
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	me := personFrom(r)
	err := s.Store.Update(func(d *store.Data) error {
		p, err := d.Person(me.Username)
		if err != nil {
			return err
		}
		if !auth.VerifyPassword(p.PasswordHash, body.Current) {
			return errors.New("your current password isn't right")
		}
		h, err := auth.HashPassword(body.New)
		if err != nil {
			return err
		}
		p.PasswordHash = h
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	s.dropSessions(me.Username)
	s.newSession(w, r, me.Username)
	writeJSON(w, 200, map[string]bool{"changed": true})
}

func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	if me.TOTPEnabled {
		fail(w, 400, "Two-factor sign-in is already on. Turn it off first to set up a new device.")
		return
	}
	secret := auth.NewTOTPSecret()
	_ = s.Store.Update(func(d *store.Data) error {
		p, err := d.Person(me.Username)
		if err != nil {
			return err
		}
		p.TOTPSecret = secret
		return nil
	})
	writeJSON(w, 200, map[string]string{"secret": secret, "otpauth_url": auth.TOTPURL(s.Issuer, me.Username, secret)})
}

func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request) {
	var body struct{ Code string }
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	me := personFrom(r)
	codes := auth.NewRecoveryCodes(8)
	err := s.Store.Update(func(d *store.Data) error {
		p, err := d.Person(me.Username)
		if err != nil {
			return err
		}
		if p.TOTPSecret == "" {
			return errors.New("start two-factor setup first")
		}
		ctr, ok := auth.VerifyTOTP(p.TOTPSecret, body.Code, time.Now(), 0)
		if !ok {
			return errors.New("that code isn't right; check the time on your phone and try the newest code")
		}
		p.TOTPEnabled, p.TOTPLast = true, ctr
		p.RecoveryCodes = nil
		for _, c := range codes {
			p.RecoveryCodes = append(p.RecoveryCodes, auth.HashToken(c))
		}
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "recovery_codes": codes})
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	var body struct{ Password string }
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	me := personFrom(r)
	err := s.Store.Update(func(d *store.Data) error {
		p, err := d.Person(me.Username)
		if err != nil {
			return err
		}
		if !auth.VerifyPassword(p.PasswordHash, body.Password) {
			return errors.New("your password isn't right")
		}
		p.TOTPEnabled, p.TOTPSecret, p.RecoveryCodes = false, "", nil
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"enabled": false})
}

// ---------------------------------------------------------------- people --

func (s *Server) listPeople(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	var out []store.Public
	s.Store.View(func(d *store.Data) {
		for _, p := range d.PeopleList() {
			if me.Role == store.Admin || (me.Role == store.Reseller && p.Reseller == me.Username) || p.Username == me.Username ||
				(p.Role == store.Developer && p.InvitedBy == me.Username) {
				out = append(out, p)
			}
		}
	})
	if out == nil {
		out = []store.Public{}
	}
	writeJSON(w, 200, out)
}

type personRequest struct {
	Username    string            `json:"username"`
	Email       string            `json:"email"`
	Name        string            `json:"name"`
	Role        store.Role        `json:"role"`
	Password    string            `json:"password"`
	Accounts    []string          `json:"accounts"`
	MaxAccounts int               `json:"max_accounts"`
	Areas       map[string]string `json:"areas"` // developers
}

// canManage says whether me may change or remove p: admins anyone,
// resellers their own people, account owners the developers they invited.
func canManage(me *store.Person, p *store.Person) bool {
	return me.Role == store.Admin || (me.Role == store.Reseller && p.Reseller == me.Username) ||
		(me.Role == store.User && p.Role == store.Developer && p.InvitedBy == me.Username)
}

// ownsAccounts checks that me may give these accounts to someone.
func ownsAccounts(me *store.Person, d *store.Data, accounts []string) error {
	for _, a := range accounts {
		ok := me.Role == store.Admin
		switch me.Role {
		case store.Reseller:
			ok = d.AccountOf[a] == me.Username
		case store.User:
			ok = indexOf(me.Accounts, a) >= 0
		}
		if !ok {
			return fmt.Errorf("%w: account %s isn't one of yours", errForbidden, a)
		}
	}
	return nil
}

func (s *Server) createPerson(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	var req personRequest
	if err := decode(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	switch {
	case me.Role == store.Developer:
		fail(w, 403, "Developers can't add people.")
		return
	case me.Role == store.User && req.Role != store.Developer:
		fail(w, 403, "Account owners can only invite developers.")
		return
	case me.Role == store.Reseller && req.Role != store.User && req.Role != store.Developer:
		fail(w, 403, "Resellers can only add account owners and developers.")
		return
	}
	if req.Role != store.Developer {
		req.Areas = nil
	}
	h, err := auth.HashPassword(req.Password)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	var pub store.Public
	err = s.Store.Update(func(d *store.Data) error {
		p := &store.Person{Username: req.Username, Email: req.Email, Name: req.Name, Role: req.Role, PasswordHash: h,
			Accounts: req.Accounts, MaxAccounts: req.MaxAccounts, Areas: req.Areas}
		if err := ownsAccounts(me, d, req.Accounts); err != nil {
			return err
		}
		switch me.Role {
		case store.Reseller:
			p.Reseller, p.MaxAccounts = me.Username, 0
		case store.User:
			p.InvitedBy, p.MaxAccounts = me.Username, 0
		}
		if err := d.AddPerson(p); err != nil {
			return err
		}
		pub = p.Public()
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 201, pub)
}

func (s *Server) updatePerson(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	var req struct {
		Email       *string            `json:"email"`
		Name        *string            `json:"name"`
		Role        *store.Role        `json:"role"`
		Accounts    *[]string          `json:"accounts"`
		MaxAccounts *int               `json:"max_accounts"`
		Disabled    *bool              `json:"disabled"`
		Password    *string            `json:"password"`
		Areas       *map[string]string `json:"areas"` // developers
		// reseller totals
		MaxSites       *int `json:"max_sites"`
		MaxDomains     *int `json:"max_domains"`
		MaxDiskMB      *int `json:"max_disk_mb"`
		MaxBandwidthGB *int `json:"max_bandwidth_gb"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	target := strings.ToLower(r.PathValue("username"))
	var pub store.Public
	signOut := false
	err := s.Store.Update(func(d *store.Data) error {
		p, err := d.Person(target)
		if err != nil {
			return err
		}
		if !canManage(me, p) {
			return errForbidden
		}
		if req.Areas != nil {
			if p.Role != store.Developer {
				return errors.New("only developers have areas")
			}
			if err := store.CheckDeveloperAreas(*req.Areas); err != nil {
				return err
			}
			p.Areas = *req.Areas
		}
		if req.Role != nil && me.Role != store.Admin {
			return fmt.Errorf("%w: only server admins can change roles", errForbidden)
		}
		if (req.MaxAccounts != nil || req.MaxSites != nil || req.MaxDomains != nil || req.MaxDiskMB != nil || req.MaxBandwidthGB != nil) && me.Role != store.Admin {
			return fmt.Errorf("%w: only server admins can change reseller quotas", errForbidden)
		}
		for _, q := range []*int{req.MaxSites, req.MaxDomains, req.MaxDiskMB, req.MaxBandwidthGB} {
			if q != nil && (*q < 0 || *q > 100000000) {
				return errors.New("quotas must be 0 (unlimited) or a positive number")
			}
		}
		if req.MaxSites != nil {
			p.MaxSites = *req.MaxSites
		}
		if req.MaxDomains != nil {
			p.MaxDomains = *req.MaxDomains
		}
		if req.MaxDiskMB != nil {
			p.MaxDiskMB = *req.MaxDiskMB
		}
		if req.MaxBandwidthGB != nil {
			p.MaxBandwidthGB = *req.MaxBandwidthGB
		}
		if req.Email != nil {
			if err := store.ValidateEmail(*req.Email); err != nil {
				return err
			}
			p.Email = *req.Email
		}
		if req.Name != nil {
			p.Name = *req.Name
		}
		if req.Role != nil {
			if p.Role == store.Admin && *req.Role != store.Admin && d.AdminCount() <= 1 {
				return errors.New("this is the last server admin; add another admin first")
			}
			p.Role = *req.Role
		}
		if req.Accounts != nil {
			if err := ownsAccounts(me, d, *req.Accounts); err != nil {
				return err
			}
			if p.Role == store.Developer && len(*req.Accounts) == 0 {
				return errors.New("a developer needs at least one account; remove them instead")
			}
			p.Accounts = *req.Accounts
		}
		if req.MaxAccounts != nil {
			p.MaxAccounts = *req.MaxAccounts
		}
		if req.Disabled != nil {
			if *req.Disabled && p.Role == store.Admin && d.AdminCount() <= 1 {
				return errors.New("this is the last server admin and can't be disabled")
			}
			p.Disabled = *req.Disabled
			signOut = p.Disabled
		}
		if req.Password != nil {
			h, err := auth.HashPassword(*req.Password)
			if err != nil {
				return err
			}
			p.PasswordHash = h
			signOut = true
		}
		pub = p.Public()
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	if signOut && target != me.Username {
		s.dropSessions(target)
	}
	writeJSON(w, 200, pub)
}

func (s *Server) deletePerson(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	target := strings.ToLower(r.PathValue("username"))
	if target == me.Username {
		fail(w, 400, "You can't delete yourself.")
		return
	}
	err := s.Store.Update(func(d *store.Data) error {
		p, err := d.Person(target)
		if err != nil {
			return err
		}
		if !canManage(me, p) {
			return errForbidden
		}
		if p.Role == store.Admin && d.AdminCount() <= 1 {
			return errors.New("this is the last server admin")
		}
		delete(d.People, target)
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	s.dropSessions(target)
	writeJSON(w, 200, map[string]string{"deleted": target})
}

// ------------------------------------------------------------ static UI --

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if s.UI == nil {
		http.NotFound(w, r)
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if _, err := fs.Stat(s.UI, p); err != nil {
		p = "index.html" // single-page app: hash routes all load index.html
	}
	if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, s.UI, p)
}

// readAll is used by the proxy for request bodies it must inspect.
func readAll(r *http.Request, max int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("request body is too large")
	}
	return b, nil
}

var _ = bytes.NewReader
