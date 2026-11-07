package panel

// Passkeys (WebAuthn): sign in with a fingerprint, face, PIN or security
// key instead of a password and code. A passkey is bound to the panel's host
// name, so it can't be phished by a look-alike site.
//
//	POST   /api/v1/me/passkeys/options   start adding a passkey (creation options)
//	POST   /api/v1/me/passkeys           finish adding one {"name","password","id","client_data_json","attestation_object"}
//	GET    /api/v1/me/passkeys           your passkeys
//	PATCH  /api/v1/me/passkeys/{id}      rename {"name"}
//	DELETE /api/v1/me/passkeys/{id}      remove one
//	POST   /api/v1/session/passkey/options  start a passkey sign-in (request options)
//	POST   /api/v1/session/passkey       finish it {"id","client_data_json","authenticator_data","signature","user_handle"}

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/YOUR-ORG/ike/agent/internal/panel/auth"
	"github.com/YOUR-ORG/ike/agent/internal/panel/store"
	"github.com/YOUR-ORG/ike/agent/internal/panel/webauthn"
)

const (
	passkeyTimeout = 5 * time.Minute
	maxPending     = 5000
	maxPasskeys    = 20
)

type pendingCeremony struct {
	purpose  string // register | login
	user     string // register only
	listener string
	rpID     string
	origin   string
	expires  time.Time
}

// relyingParty is the panel's identity for this request: the host name the
// browser used (passkeys can't be bound to an IP address) and the origin.
func (s *Server) relyingParty(r *http.Request) (rpID, origin string, err error) {
	host := strings.ToLower(r.Host)
	name := host
	if h, _, e := net.SplitHostPort(host); e == nil {
		name = h
	}
	name = strings.TrimSuffix(name, ".")
	if name == "" || net.ParseIP(strings.Trim(name, "[]")) != nil {
		return "", "", errors.New("passkeys need the panel opened by its name (like panel.example.com), not by an IP address")
	}
	scheme := "https"
	if !s.TLS {
		if name != "localhost" {
			return "", "", errors.New("passkeys need the panel on HTTPS")
		}
		scheme = "http"
	}
	return name, scheme + "://" + host, nil
}

func (s *Server) putCeremony(challenge []byte, c pendingCeremony) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ceremonies == nil {
		s.ceremonies = map[string]pendingCeremony{}
	}
	if len(s.ceremonies) >= maxPending {
		now := time.Now()
		for k, v := range s.ceremonies {
			if now.After(v.expires) {
				delete(s.ceremonies, k)
			}
		}
		if len(s.ceremonies) >= maxPending {
			return errors.New("too many passkey sign-ins are in progress; try again in a minute")
		}
	}
	c.expires = time.Now().Add(passkeyTimeout)
	s.ceremonies[webauthn.Encode(challenge)] = c
	return nil
}

// takeCeremony returns the pending ceremony for the challenge in the
// client data and forgets it: every challenge works once.
func (s *Server) takeCeremony(clientDataJSON []byte, purpose string) ([]byte, pendingCeremony, bool) {
	var cd struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(clientDataJSON, &cd); err != nil || cd.Challenge == "" {
		return nil, pendingCeremony{}, false
	}
	ch, err := webauthn.Decode(cd.Challenge)
	if err != nil {
		return nil, pendingCeremony{}, false
	}
	key := webauthn.Encode(ch)
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.ceremonies[key]
	delete(s.ceremonies, key)
	if !ok || c.purpose != purpose || time.Now().After(c.expires) {
		return nil, pendingCeremony{}, false
	}
	return ch, c, true
}

func browserOnly(w http.ResponseWriter, p *store.Person) bool {
	if p.Token != nil {
		fail(w, 403, "Passkeys are added and removed from the panel in a browser, not with an API token.")
		return false
	}
	return true
}

func (s *Server) passkeyOptions(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	if !browserOnly(w, me) {
		return
	}
	rpID, origin, err := s.relyingParty(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if len(me.Passkeys) >= maxPasskeys {
		fail(w, 400, fmt.Sprintf("You have %d passkeys, the most allowed. Remove one you don't use first.", maxPasskeys))
		return
	}
	handle := me.WebAuthnID
	if handle == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		handle = webauthn.Encode(b)
		if err := s.Store.Update(func(d *store.Data) error {
			p, err := d.Person(me.Username)
			if err != nil {
				return err
			}
			if p.WebAuthnID == "" {
				p.WebAuthnID = handle
			}
			handle = p.WebAuthnID
			return nil
		}); err != nil {
			failErr(w, err)
			return
		}
	}
	l, _ := r.Context().Value(keyListener).(Listener)
	ch := webauthn.NewChallenge()
	if err := s.putCeremony(ch, pendingCeremony{purpose: "register", user: me.Username, listener: l.Name, rpID: rpID, origin: origin}); err != nil {
		fail(w, 429, err.Error())
		return
	}
	params := []map[string]any{}
	for _, a := range webauthn.Algorithms {
		params = append(params, map[string]any{"type": "public-key", "alg": a})
	}
	exclude := []map[string]any{}
	for _, k := range me.Passkeys {
		exclude = append(exclude, map[string]any{"type": "public-key", "id": k.ID})
	}
	display := me.Name
	if display == "" {
		display = me.Username
	}
	writeJSON(w, 200, map[string]any{"publicKey": map[string]any{
		"challenge":          webauthn.Encode(ch),
		"rp":                 map[string]string{"id": rpID, "name": s.Issuer},
		"user":               map[string]string{"id": handle, "name": me.Username, "displayName": display},
		"pubKeyCredParams":   params,
		"excludeCredentials": exclude,
		"authenticatorSelection": map[string]any{
			"residentKey": "required", "requireResidentKey": true, "userVerification": "required"},
		"attestation": "none",
		"timeout":     int(passkeyTimeout / time.Millisecond),
	}})
}

func cleanPasskeyName(n string) (string, error) {
	n = strings.Join(strings.Fields(n), " ")
	if n == "" {
		return "Passkey", nil
	}
	if utf8.RuneCountInString(n) > 40 {
		return "", errors.New("keep the passkey's name under 40 characters")
	}
	for _, c := range n {
		if c < 0x20 || c == 0x7f {
			return "", errors.New("the passkey's name can't contain control characters")
		}
	}
	return n, nil
}

func (s *Server) addPasskey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name              string   `json:"name"`
		Password          string   `json:"password"`
		ID                string   `json:"id"`
		ClientDataJSON    string   `json:"client_data_json"`
		AttestationObject string   `json:"attestation_object"`
		Transports        []string `json:"transports"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	me := personFrom(r)
	if !browserOnly(w, me) {
		return
	}
	name, err := cleanPasskeyName(body.Name)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	cd, err1 := webauthn.Decode(body.ClientDataJSON)
	att, err2 := webauthn.Decode(body.AttestationObject)
	if err1 != nil || err2 != nil {
		fail(w, 400, "The browser's answer isn't readable. Try again.")
		return
	}
	ip := clientIP(r)
	if blocked, wait := s.throttle.Blocked(time.Now(), "u:"+me.Username, "ip:"+ip); blocked {
		fail(w, 429, fmt.Sprintf("Too many failed attempts. Try again in %d minutes.", int(wait.Minutes())+1))
		return
	}
	ch, cer, ok := s.takeCeremony(cd, "register")
	l, _ := r.Context().Value(keyListener).(Listener)
	if !ok || cer.user != me.Username || cer.listener != l.Name {
		fail(w, 400, "That passkey request has expired. Start again.")
		return
	}
	cred, err := webauthn.VerifyRegistration(webauthn.Expect{Challenge: ch, RPID: cer.rpID, Origins: []string{cer.origin}, RequireUV: true}, cd, att)
	if err != nil {
		fail(w, 400, "Ike couldn't add that passkey: "+err.Error()+".")
		return
	}
	id := webauthn.Encode(cred.ID)
	if body.ID != "" && body.ID != id {
		fail(w, 400, "The passkey's id doesn't match its data.")
		return
	}
	var info store.PasskeyInfo
	wrongPassword := false
	err = s.Store.Update(func(d *store.Data) error {
		p, err := d.Person(me.Username)
		if err != nil {
			return err
		}
		// Adding a way to sign in needs the password, so a borrowed
		// session can't leave a passkey behind.
		if !auth.VerifyPassword(p.PasswordHash, body.Password) {
			wrongPassword = true
			return errors.New("your password isn't right")
		}
		if other, _ := d.PersonByPasskey(id); other != nil {
			return fmt.Errorf("that passkey is already registered %w", store.ErrExists)
		}
		if len(p.Passkeys) >= maxPasskeys {
			return fmt.Errorf("you already have %d passkeys", maxPasskeys)
		}
		k := store.Passkey{ID: id, Name: name, PublicKey: cred.PublicKey, Alg: cred.Alg, SignCount: cred.SignCount,
			Synced: cred.BackupEligible, CreatedAt: time.Now().UTC()}
		p.Passkeys = append(p.Passkeys, k)
		info = p.PasskeyInfos()[len(p.Passkeys)-1]
		return nil
	})
	if err != nil {
		if wrongPassword {
			s.throttle.Fail(time.Now(), "u:"+me.Username, "ip:"+ip)
		}
		failErr(w, err)
		return
	}
	slog.Info("passkey added", "user", me.Username, "name", name, "synced", cred.BackupEligible)
	writeJSON(w, 201, info)
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	writeJSON(w, 200, me.PasskeyInfos())
}

func (s *Server) updatePasskey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	me := personFrom(r)
	if !browserOnly(w, me) {
		return
	}
	name, err := cleanPasskeyName(body.Name)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	id := r.PathValue("id")
	var info store.PasskeyInfo
	err = s.Store.Update(func(d *store.Data) error {
		p, err := d.Person(me.Username)
		if err != nil {
			return err
		}
		for i := range p.Passkeys {
			if p.Passkeys[i].ID == id {
				p.Passkeys[i].Name = name
				info = p.PasskeyInfos()[i]
				return nil
			}
		}
		return fmt.Errorf("passkey %w", store.ErrNotFound)
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, info)
}

func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	if !browserOnly(w, me) {
		return
	}
	id := r.PathValue("id")
	err := s.Store.Update(func(d *store.Data) error {
		p, err := d.Person(me.Username)
		if err != nil {
			return err
		}
		for i := range p.Passkeys {
			if p.Passkeys[i].ID == id {
				p.Passkeys = append(p.Passkeys[:i], p.Passkeys[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("passkey %w", store.ErrNotFound)
	})
	if err != nil {
		failErr(w, err)
		return
	}
	slog.Info("passkey removed", "user", me.Username)
	writeJSON(w, 200, map[string]bool{"removed": true})
}

// ---- sign-in

func (s *Server) passkeyLoginOptions(w http.ResponseWriter, r *http.Request) {
	rpID, origin, err := s.relyingParty(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	ip := clientIP(r)
	if blocked, wait := s.throttle.Blocked(time.Now(), "ip:"+ip); blocked {
		fail(w, 429, fmt.Sprintf("Too many failed sign-ins. Try again in %d minutes.", int(wait.Minutes())+1))
		return
	}
	l, _ := r.Context().Value(keyListener).(Listener)
	ch := webauthn.NewChallenge()
	if err := s.putCeremony(ch, pendingCeremony{purpose: "login", listener: l.Name, rpID: rpID, origin: origin}); err != nil {
		fail(w, 429, err.Error())
		return
	}
	// No allowCredentials: the browser offers the passkeys it has for this
	// site, so the panel never reveals which usernames exist.
	writeJSON(w, 200, map[string]any{"publicKey": map[string]any{
		"challenge":        webauthn.Encode(ch),
		"rpId":             rpID,
		"userVerification": "required",
		"timeout":          int(passkeyTimeout / time.Millisecond),
	}})
}

func (s *Server) passkeyLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID                string `json:"id"`
		ClientDataJSON    string `json:"client_data_json"`
		AuthenticatorData string `json:"authenticator_data"`
		Signature         string `json:"signature"`
		UserHandle        string `json:"user_handle"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	ip := clientIP(r)
	now := time.Now()
	if blocked, wait := s.throttle.Blocked(now, "ip:"+ip); blocked {
		fail(w, 429, fmt.Sprintf("Too many failed sign-ins. Try again in %d minutes.", int(wait.Minutes())+1))
		return
	}
	failed := func(user, msg string) {
		s.throttle.Fail(now, "ip:"+ip)
		slog.Warn("passkey sign-in failed", "user", user, "ip", ip, "why", msg)
		fail(w, 401, msg)
	}
	cd, e1 := webauthn.Decode(body.ClientDataJSON)
	ad, e2 := webauthn.Decode(body.AuthenticatorData)
	sig, e3 := webauthn.Decode(body.Signature)
	if e1 != nil || e2 != nil || e3 != nil {
		failed("", "The browser's answer isn't readable. Try again.")
		return
	}
	ch, cer, ok := s.takeCeremony(cd, "login")
	l, _ := r.Context().Value(keyListener).(Listener)
	if !ok || cer.listener != l.Name {
		failed("", "That sign-in request has expired. Try again.")
		return
	}
	e := webauthn.Expect{Challenge: ch, RPID: cer.rpID, Origins: []string{cer.origin}, RequireUV: true}
	var user, msg string
	cloned := false
	err := s.Store.Update(func(d *store.Data) error {
		p, i := d.PersonByPasskey(body.ID)
		if p == nil {
			msg = "This panel doesn't know that passkey. Sign in with your password, then add it under Profile."
			return errors.New("no change")
		}
		user = p.Username
		if body.UserHandle != "" && body.UserHandle != p.WebAuthnID {
			msg = "That passkey belongs to someone else."
			return errors.New("no change")
		}
		k := &p.Passkeys[i]
		if p.Disabled || k.Disabled {
			msg = "That passkey or account is turned off. Sign in with your password."
			return errors.New("no change")
		}
		if !roleAllowed(l, p.Role) {
			msg = fmt.Sprintf("This sign-in page is for %s. Use your own panel address.", strings.Join(roleNames(l.Roles), " and "))
			return errors.New("no change")
		}
		cred := &webauthn.Credential{PublicKey: k.PublicKey, Alg: k.Alg, SignCount: k.SignCount}
		n, err := webauthn.VerifyAssertion(e, cred, cd, ad, sig)
		if errors.Is(err, webauthn.ErrCloned) {
			k.Disabled, cloned = true, true
			msg = err.Error() + ". Sign in with your password."
			return nil // keep the Disabled mark
		}
		if err != nil {
			msg = "That passkey didn't work: " + err.Error() + "."
			return errors.New("no change")
		}
		k.SignCount, k.LastUsedAt = n, now.UTC()
		p.LastLoginAt = now.UTC()
		return nil
	})
	if cloned || err != nil {
		failed(user, msg)
		return
	}
	s.throttle.Reset("u:"+user, "ip:"+ip)
	s.newSession(w, r, user)
	var pub store.Public
	s.Store.View(func(d *store.Data) { p, _ := d.Person(user); pub = p.Public() })
	slog.Info("signed in with a passkey", "user", user, "ip", ip, "listener", l.Name)
	writeJSON(w, 200, pub)
}
