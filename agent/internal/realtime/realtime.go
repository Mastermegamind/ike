// Package realtime runs the servers that video and voice apps need: a TURN
// server (coturn) so calls connect through strict firewalls and mobile
// networks, and LiveKit, a WebRTC media server for rooms, calls and live
// streams.
//
// Ike owns their config files. TURN hands out short-lived credentials signed
// with a shared secret (the TURN REST API scheme), refuses to relay to private
// networks, and serves TLS with a certificate Ike already manages. LiveKit
// listens on localhost behind an Ike website (wss://), keeps its API keys
// sealed, and can use Ike's TURN server for clients that can't reach it
// directly.
package realtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
	"github.com/Mastermegamind/ike/agent/internal/validate"
)

// Default ports. Relay and media ranges are 10 000 ports each, the widest
// range the firewall takes in one rule, and they don't overlap.
const (
	TurnPort      = 3478
	TurnTLSPort   = 5349
	TurnRelayMin  = 40000
	TurnRelayMax  = 49999
	LiveKitPort   = 7880 // signalling (HTTP + WebSocket), localhost only
	LiveKitTCP    = 7881 // media over TCP when UDP is blocked
	LiveKitUDPMin = 50000
	LiveKitUDPMax = 60000

	turnService    = "coturn"
	livekitService = "livekit"
	turnBinary     = "/usr/bin/turnserver"
)

var (
	nameRe    = regexp.MustCompile(`^[A-Za-z0-9 ._-]{1,40}$`)
	identRe   = regexp.MustCompile(`^[A-Za-z0-9_.@:-]{1,64}$`)
	roomRe    = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
	keyIDRe   = regexp.MustCompile(`^API[A-Za-z0-9]{12}$`)
	webhookRe = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/%?&=+-]*)?$`)
)

// TurnSettings configure coturn.
type TurnSettings struct {
	Enabled bool `json:"enabled"`
	// Domain is the name clients use (turn.example.com). With TLS, Ike uses
	// the certificate of the website for that name, or one listing it.
	Domain string `json:"domain"`
	TLS    bool   `json:"tls"`
	// Limits: concurrent allocations per user and in all (0 = coturn's
	// default), and bandwidth per session in kilobits per second (0 = none).
	UserQuota  int `json:"user_quota"`
	TotalQuota int `json:"total_quota"`
	MaxKbps    int `json:"max_kbps"`
	// AllowAccounts lets account owners make TURN credentials for their apps.
	AllowAccounts bool      `json:"allow_accounts"`
	Secret        string    `json:"secret,omitempty"` // sealed; never sent out
	UpdatedAt     time.Time `json:"updated_at,omitzero"`
}

// LiveKitSettings configure LiveKit.
type LiveKitSettings struct {
	Enabled bool `json:"enabled"`
	// Domain is served as an Ike website (a proxy to LiveKit) owned by Account.
	Domain  string `json:"domain"`
	Account string `json:"account"`
	// UseTurn advertises Ike's TURN server to LiveKit's clients.
	UseTurn         bool     `json:"use_turn"`
	EmptyTimeout    int      `json:"empty_timeout"`    // seconds an empty room lives (default 300)
	MaxParticipants int      `json:"max_participants"` // per room, 0 = no limit
	WebhookURLs     []string `json:"webhook_urls,omitempty"`
	// AllowAccounts lets account owners make API keys for their apps.
	AllowAccounts bool      `json:"allow_accounts"`
	Keys          []APIKey  `json:"keys"`
	UpdatedAt     time.Time `json:"updated_at,omitzero"`
}

// APIKey is a LiveKit API key. The secret is sealed and shown only once.
type APIKey struct {
	Key     string    `json:"key"`
	Secret  string    `json:"secret,omitempty"`
	Name    string    `json:"name"`
	Account string    `json:"account,omitempty"` // "" = the admin's
	Created time.Time `json:"created"`
}

type state struct {
	Turn    TurnSettings    `json:"turn"`
	LiveKit LiveKitSettings `json:"livekit"`
	Jitsi   JitsiSettings   `json:"jitsi"`
	// TurnCertSum is the fingerprint of the certificate last copied for coturn.
	TurnCertSum string `json:"turn_cert_sum,omitempty"`
	// LiveKitTurnUntil is when the TURN credential in LiveKit's config runs out.
	LiveKitTurnUntil time.Time `json:"livekit_turn_until,omitzero"`
}

// Manager keeps both servers' settings and config files.
type Manager struct {
	env  *kit.Env
	keys *secrets.Keyring
	mu   sync.Mutex

	// CertFiles finds the certificate for a host name (sites.Manager.CertFiles).
	CertFiles func(name string) (cert, key string, ok bool)
	// ProxySite makes sure a website forwards domain to a local port, with
	// HTTPS when possible. It returns a note when HTTPS isn't ready yet.
	ProxySite func(ctx context.Context, account, domain string, port int) (string, error)
}

// New makes the manager and registers its sealed values for key rotation.
func New(env *kit.Env, keys *secrets.Keyring) *Manager {
	m := &Manager{env: env, keys: keys}
	if keys != nil {
		keys.Register("realtime", m.reseal)
	}
	return m
}

func (m *Manager) dir() string       { return m.env.P(m.env.Cfg.RealtimeDir) }
func (m *Manager) stateFile() string { return filepath.Join(m.dir(), "realtime.json") }

func (m *Manager) load() (*state, error) {
	st := &state{}
	err := kit.ReadJSON(m.stateFile(), st)
	if errors.Is(err, kit.ErrNotFound) {
		err = nil
	}
	return st, err
}

func (m *Manager) save(st *state) error { return kit.WriteJSON(m.stateFile(), st) }

func (m *Manager) reseal(open, seal func(string) (string, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.load()
	if err != nil {
		return err
	}
	re := func(v *string) error {
		if *v == "" {
			return nil
		}
		p, err := open(*v)
		if err != nil {
			return err
		}
		*v, err = seal(p)
		return err
	}
	if err := re(&st.Turn.Secret); err != nil {
		return err
	}
	for i := range st.LiveKit.Keys {
		if err := re(&st.LiveKit.Keys[i].Secret); err != nil {
			return err
		}
	}
	return m.save(st)
}

func (m *Manager) seal(v string) (string, error) {
	if m.keys == nil {
		return v, nil
	}
	return m.keys.Seal(v)
}

func (m *Manager) unseal(v string) (string, error) {
	if m.keys == nil || v == "" {
		return v, nil
	}
	return m.keys.Unseal(v)
}

// ---------------------------------------------------------------- status

// TurnInfo is what the panel shows about TURN.
type TurnInfo struct {
	TurnSettings
	Installed bool     `json:"installed"`
	Running   string   `json:"running"` // systemd state
	URIs      []string `json:"uris"`
	Ports     []string `json:"ports"`
	CertReady bool     `json:"cert_ready"`
}

// LiveKitInfo is what the panel shows about LiveKit.
type LiveKitInfo struct {
	LiveKitSettings
	Installed bool   `json:"installed"`
	Running   string `json:"running"`
	URL       string `json:"url"` // wss://domain
	Version   string `json:"version,omitempty"`
}

// Status is both servers, without secrets.
type Status struct {
	Turn     TurnInfo    `json:"turn"`
	LiveKit  LiveKitInfo `json:"livekit"`
	Jitsi    *JitsiInfo  `json:"jitsi,omitempty"`
	PublicIP string      `json:"public_ip"`
	Notes    []string    `json:"notes,omitempty"`
}

func (m *Manager) turnInstalled() bool    { return m.env.Installed(turnBinary) }
func (m *Manager) livekitInstalled() bool { return m.env.Installed(m.env.Cfg.LiveKitBinary) }

func (m *Manager) active(ctx context.Context, unit string) string {
	out, _ := m.env.Run.Run(ctx, "systemctl", "is-active", unit)
	s := strings.TrimSpace(string(out))
	if s == "" {
		s = "unknown"
	}
	return s
}

func turnURIs(t TurnSettings) []string {
	if t.Domain == "" {
		return nil
	}
	out := []string{
		fmt.Sprintf("stun:%s:%d", t.Domain, TurnPort),
		fmt.Sprintf("turn:%s:%d?transport=udp", t.Domain, TurnPort),
		fmt.Sprintf("turn:%s:%d?transport=tcp", t.Domain, TurnPort),
	}
	if t.TLS {
		out = append(out, fmt.Sprintf("turns:%s:%d?transport=tcp", t.Domain, TurnTLSPort))
	}
	return out
}

func turnPorts(t TurnSettings) []string {
	p := []string{fmt.Sprintf("%d/udp", TurnPort), fmt.Sprintf("%d/tcp", TurnPort)}
	if t.TLS {
		p = append(p, fmt.Sprintf("%d/tcp", TurnTLSPort), fmt.Sprintf("%d/udp", TurnTLSPort))
	}
	return append(p, fmt.Sprintf("%d:%d/udp", TurnRelayMin, TurnRelayMax))
}

func livekitPorts() []string {
	return []string{fmt.Sprintf("%d/tcp", LiveKitTCP), fmt.Sprintf("%d:%d/udp", LiveKitUDPMin, LiveKitUDPMax)}
}

func publicKeys(keys []APIKey) []APIKey {
	out := make([]APIKey, 0, len(keys))
	for _, k := range keys {
		k.Secret = ""
		out = append(out, k)
	}
	return out
}

// Status reports both servers.
func (m *Manager) Status(ctx context.Context) (*Status, error) {
	m.mu.Lock()
	st, err := m.load()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := &Status{PublicIP: m.env.Cfg.PublicIPv4}
	t := st.Turn
	t.Secret = ""
	out.Turn = TurnInfo{TurnSettings: t, Installed: m.turnInstalled(), URIs: turnURIs(t), Ports: turnPorts(t)}
	if out.Turn.Installed {
		out.Turn.Running = m.active(ctx, turnService)
	}
	if t.TLS && m.CertFiles != nil {
		_, _, out.Turn.CertReady = m.CertFiles(t.Domain)
	}
	lk := st.LiveKit
	lk.Keys = publicKeys(lk.Keys)
	out.LiveKit = LiveKitInfo{LiveKitSettings: lk, Installed: m.livekitInstalled()}
	if lk.Domain != "" {
		out.LiveKit.URL = "wss://" + lk.Domain
	}
	if out.LiveKit.Installed {
		out.LiveKit.Running = m.active(ctx, livekitService)
		if v, err := m.env.Run.Run(ctx, m.env.Cfg.LiveKitBinary, "--version"); err == nil {
			out.LiveKit.Version = lastField(string(v))
		}
	}
	out.Jitsi, _ = m.JitsiStatus(ctx)
	if m.env.Cfg.PublicIPv4 == "" {
		out.Notes = append(out.Notes, "This server's public IPv4 address isn't known, so media servers will try to discover it themselves.")
	}
	return out, nil
}

func lastField(s string) string {
	f := strings.Fields(strings.TrimSpace(s))
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// ---------------------------------------------------------------- TURN

// TurnRequest changes TURN settings.
type TurnRequest struct {
	Enabled       bool   `json:"enabled"`
	Domain        string `json:"domain"`
	TLS           bool   `json:"tls"`
	UserQuota     int    `json:"user_quota"`
	TotalQuota    int    `json:"total_quota"`
	MaxKbps       int    `json:"max_kbps"`
	AllowAccounts bool   `json:"allow_accounts"`
}

// SetTurn saves TURN settings, writes coturn's config and (re)starts or
// stops it. The firewall opens the ports TURN uses when it is on.
func (m *Manager) SetTurn(ctx context.Context, req TurnRequest) (*Status, error) {
	if !m.turnInstalled() {
		return nil, fmt.Errorf("the TURN server (coturn) %w; add it with install.sh --turn", kit.ErrNotInstalled)
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if req.Enabled || req.Domain != "" {
		if err := validate.Domain(req.Domain); err != nil {
			return nil, fmt.Errorf("TURN domain: %w", err)
		}
	}
	if req.UserQuota < 0 || req.TotalQuota < 0 || req.MaxKbps < 0 || req.UserQuota > 1000 || req.TotalQuota > 100000 || req.MaxKbps > 10_000_000 {
		return nil, fmt.Errorf("limits must be 0 (none) or a sensible positive number")
	}
	m.mu.Lock()
	st, err := m.load()
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	t := &st.Turn
	wasOn := t.Enabled
	t.Enabled, t.Domain, t.TLS = req.Enabled, req.Domain, req.TLS
	t.UserQuota, t.TotalQuota, t.MaxKbps, t.AllowAccounts = req.UserQuota, req.TotalQuota, req.MaxKbps, req.AllowAccounts
	if t.Secret == "" {
		if t.Secret, err = m.seal(kit.RandomHex(32)); err != nil {
			m.mu.Unlock()
			return nil, err
		}
	}
	t.UpdatedAt = kit.Now()
	notes, err := m.applyTurn(ctx, st)
	if err == nil {
		err = m.save(st)
	}
	if err == nil && wasOn && !t.Enabled {
		m.closePorts(ctx, turnPortsAll())
	}
	lkOn := st.LiveKit.Enabled && st.LiveKit.UseTurn
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if lkOn { // LiveKit advertises the TURN server; keep it in step
		if _, lerr := m.applyLiveKitNow(ctx); lerr != nil {
			notes = append(notes, "LiveKit didn't take the new TURN settings: "+lerr.Error())
		}
	}
	m.env.Publish("realtime.turn.changed", t.Domain, map[string]any{"enabled": t.Enabled})
	out, err := m.Status(ctx)
	if out != nil {
		out.Notes = append(out.Notes, notes...)
	}
	return out, err
}

// turnCertDir holds copies of the certificate coturn can read (it doesn't
// run as root, and Let's Encrypt's files are root-only).
func (m *Manager) turnCertDir() string { return filepath.Join(m.env.Cfg.RealtimeDir, "turn") }

// applyTurn writes the config and starts, restarts or stops coturn. Called
// with m.mu held.
func (m *Manager) applyTurn(ctx context.Context, st *state) ([]string, error) {
	t := &st.Turn
	var notes []string
	if !t.Enabled {
		_, _ = m.env.Run.Run(ctx, "systemctl", "disable", "--now", turnService)
		return nil, nil
	}
	secret, err := m.unseal(t.Secret)
	if err != nil {
		return nil, err
	}
	certOK := false
	if t.TLS {
		sum, cerr := m.copyTurnCert(ctx, t.Domain)
		switch {
		case cerr != nil:
			notes = append(notes, "TURN over TLS is waiting for a certificate: "+cerr.Error())
		default:
			certOK = true
			st.TurnCertSum = sum
		}
	}
	conf := renderTurnConf(*t, secret, m.env.Cfg.PublicIPv4, m.turnCertDir(), certOK)
	if err := kit.WriteFile(m.env.P(m.env.Cfg.TurnConfig), []byte(conf), 0o640); err != nil {
		return nil, err
	}
	m.chgrp(m.env.Cfg.TurnConfig, "turnserver")
	for _, p := range turnPorts(*t) {
		port, proto, _ := strings.Cut(p, "/")
		if err := m.openPort(ctx, port, proto, "TURN"); err != nil {
			notes = append(notes, "Could not open "+p+" in the firewall: "+err.Error())
		}
	}
	if _, err := m.env.Run.Run(ctx, "systemctl", "enable", turnService); err != nil {
		return notes, err
	}
	if _, err := m.env.Run.Run(ctx, "systemctl", "restart", turnService); err != nil {
		return notes, fmt.Errorf("coturn did not start: %w", err)
	}
	return notes, nil
}

func turnPortsAll() []string { return turnPorts(TurnSettings{TLS: true}) }

// copyTurnCert copies the domain's certificate where coturn can read it and
// returns its fingerprint.
func (m *Manager) copyTurnCert(ctx context.Context, domain string) (string, error) {
	if m.CertFiles == nil {
		return "", fmt.Errorf("certificates aren't available")
	}
	cert, key, ok := m.CertFiles(domain)
	if !ok {
		return "", fmt.Errorf("no website on this server has HTTPS for %s; add one (a static website is enough) and get it a certificate", domain)
	}
	cb, err := os.ReadFile(m.env.P(cert))
	if err != nil {
		if m.env.Cfg.DryRun {
			cb = []byte("dry-run certificate for " + domain)
		} else {
			return "", fmt.Errorf("certificate file unreadable: %w", err)
		}
	}
	kb, err := os.ReadFile(m.env.P(key))
	if err != nil {
		if m.env.Cfg.DryRun {
			kb = []byte("dry-run key")
		} else {
			return "", fmt.Errorf("key file unreadable: %w", err)
		}
	}
	dir := m.turnCertDir()
	if err := kit.WriteFile(m.env.P(filepath.Join(dir, "fullchain.pem")), cb, 0o644); err != nil {
		return "", err
	}
	if err := kit.WriteFile(m.env.P(filepath.Join(dir, "privkey.pem")), kb, 0o640); err != nil {
		return "", err
	}
	m.chgrp(filepath.Join(dir, "privkey.pem"), "turnserver")
	_ = os.Chmod(m.env.P(dir), 0o750)
	m.chgrp(dir, "turnserver")
	sum := sha256.Sum256(append(cb, kb...))
	return base64.RawURLEncoding.EncodeToString(sum[:12]), nil
}

// chgrp gives a group read access to a root-owned file.
func (m *Manager) chgrp(abs, group string) {
	if m.env.Cfg.DryRun {
		return
	}
	_, _ = m.env.Run.Run(context.Background(), "chgrp", group, abs)
}

// privateRanges are never relayed to: a TURN server that relays into the
// server's own network is an open door to everything behind it.
var privateRanges = []string{
	"0.0.0.0-0.255.255.255", "10.0.0.0-10.255.255.255", "100.64.0.0-100.127.255.255",
	"127.0.0.0-127.255.255.255", "169.254.0.0-169.254.255.255", "172.16.0.0-172.31.255.255",
	"192.0.0.0-192.0.0.255", "192.168.0.0-192.168.255.255", "198.18.0.0-198.19.255.255",
	"224.0.0.0-255.255.255.255",
	"::1", "64:ff9b::-64:ff9b::ffff:ffff", "fc00::-fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
	"fe80::-febf:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
}

func renderTurnConf(t TurnSettings, secret, publicIP, certDir string, tls bool) string {
	var b strings.Builder
	b.WriteString("# Written by Ike. Change TURN in the panel under Server → Real-time media.\n")
	fmt.Fprintf(&b, "listening-port=%d\n", TurnPort)
	if tls {
		fmt.Fprintf(&b, "tls-listening-port=%d\n", TurnTLSPort)
		fmt.Fprintf(&b, "cert=%s\npkey=%s\n", filepath.Join(certDir, "fullchain.pem"), filepath.Join(certDir, "privkey.pem"))
		b.WriteString("no-tlsv1\nno-tlsv1_1\n")
	} else {
		b.WriteString("no-tls\nno-dtls\n")
	}
	if publicIP != "" {
		fmt.Fprintf(&b, "external-ip=%s\n", publicIP)
	}
	fmt.Fprintf(&b, "min-port=%d\nmax-port=%d\n", TurnRelayMin, TurnRelayMax)
	fmt.Fprintf(&b, "realm=%s\nserver-name=%s\n", t.Domain, t.Domain)
	b.WriteString("fingerprint\nuse-auth-secret\n")
	fmt.Fprintf(&b, "static-auth-secret=%s\n", secret)
	if t.UserQuota > 0 {
		fmt.Fprintf(&b, "user-quota=%d\n", t.UserQuota)
	}
	if t.TotalQuota > 0 {
		fmt.Fprintf(&b, "total-quota=%d\n", t.TotalQuota)
	}
	if t.MaxKbps > 0 {
		fmt.Fprintf(&b, "max-bps=%d\n", t.MaxKbps*1000/8)
	}
	b.WriteString("stale-nonce=600\nno-cli\nno-multicast-peers\nno-software-attribute\n")
	for _, r := range privateRanges {
		fmt.Fprintf(&b, "denied-peer-ip=%s\n", r)
	}
	b.WriteString("syslog\nsimple-log\n")
	return b.String()
}

// TurnCredential is a short-lived login for a TURN client (RTCIceServer).
type TurnCredential struct {
	URIs       []string  `json:"uris"`
	Username   string    `json:"username"`
	Credential string    `json:"credential"`
	TTL        int       `json:"ttl"` // seconds
	Expires    time.Time `json:"expires"`
}

// turnSign makes a credential under the TURN REST API scheme coturn checks
// with use-auth-secret: username "<expiry>:<user>", password
// base64(HMAC-SHA1(secret, username)).
func turnSign(secret, user string, expires time.Time) (string, string) {
	username := strconv.FormatInt(expires.Unix(), 10)
	if user != "" {
		username += ":" + user
	}
	h := hmac.New(sha1.New, []byte(secret))
	h.Write([]byte(username))
	return username, base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// TurnCredentials signs a login valid for ttl. account limits it to account
// owners when TURN allows them; "" is the admin.
func (m *Manager) TurnCredentials(account, user string, ttl time.Duration) (*TurnCredential, error) {
	m.mu.Lock()
	st, err := m.load()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	t := st.Turn
	if !t.Enabled {
		return nil, fmt.Errorf("the TURN server is switched off")
	}
	if account != "" && !t.AllowAccounts {
		return nil, fmt.Errorf("TURN credentials %w for account owners on this server", kit.ErrForbidden)
	}
	if user != "" && !identRe.MatchString(user) {
		return nil, fmt.Errorf("user may only contain letters, digits and _ . @ : - (max 64)")
	}
	if account != "" {
		user = strings.TrimSuffix(account+":"+user, ":")
	}
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	if ttl > 7*24*time.Hour {
		return nil, fmt.Errorf("credentials last at most 7 days; ask for new ones instead")
	}
	secret, err := m.unseal(t.Secret)
	if err != nil {
		return nil, err
	}
	exp := kit.Now().Add(ttl).Truncate(time.Second)
	u, c := turnSign(secret, user, exp)
	return &TurnCredential{URIs: turnURIs(t), Username: u, Credential: c, TTL: int(ttl.Seconds()), Expires: exp}, nil
}

// RotateTurnSecret makes a new shared secret; credentials signed with the
// old one stop working.
func (m *Manager) RotateTurnSecret(ctx context.Context) (*Status, error) {
	m.mu.Lock()
	st, err := m.load()
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if st.Turn.Secret, err = m.seal(kit.RandomHex(32)); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	var notes []string
	if st.Turn.Enabled {
		if notes, err = m.applyTurn(ctx, st); err != nil {
			m.mu.Unlock()
			return nil, err
		}
	}
	err = m.save(st)
	lk := st.LiveKit.Enabled && st.LiveKit.UseTurn
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if lk {
		_, _ = m.applyLiveKitNow(ctx)
	}
	m.env.Publish("realtime.turn.secret_rotated", st.Turn.Domain, nil)
	out, err := m.Status(ctx)
	if out != nil {
		out.Notes = append(out.Notes, notes...)
	}
	return out, err
}

// ---------------------------------------------------------------- LiveKit

// LiveKitRequest changes LiveKit settings.
type LiveKitRequest struct {
	Enabled         bool     `json:"enabled"`
	Domain          string   `json:"domain"`
	Account         string   `json:"account"`
	UseTurn         bool     `json:"use_turn"`
	EmptyTimeout    int      `json:"empty_timeout"`
	MaxParticipants int      `json:"max_participants"`
	WebhookURLs     []string `json:"webhook_urls"`
	AllowAccounts   bool     `json:"allow_accounts"`
}

// SetLiveKit saves LiveKit settings, makes sure its website exists, writes
// its config and (re)starts or stops it. The first time it is switched on
// it gets an API key, returned with its secret.
func (m *Manager) SetLiveKit(ctx context.Context, req LiveKitRequest) (*Status, *APIKey, error) {
	if !m.livekitInstalled() {
		return nil, nil, fmt.Errorf("LiveKit %w; add it with install.sh --livekit", kit.ErrNotInstalled)
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if req.Enabled || req.Domain != "" {
		if err := validate.Domain(req.Domain); err != nil {
			return nil, nil, fmt.Errorf("LiveKit domain: %w", err)
		}
		if err := m.env.CheckAccount(req.Account); err != nil {
			return nil, nil, fmt.Errorf("the account that owns LiveKit's website: %w", err)
		}
	}
	if req.EmptyTimeout == 0 {
		req.EmptyTimeout = 300
	}
	if req.EmptyTimeout < 10 || req.EmptyTimeout > 86400 || req.MaxParticipants < 0 || req.MaxParticipants > 10000 {
		return nil, nil, fmt.Errorf("empty rooms close after 10-86400 seconds; participants are 0 (no limit) to 10000")
	}
	if len(req.WebhookURLs) > 5 {
		return nil, nil, fmt.Errorf("at most 5 webhook addresses")
	}
	for _, u := range req.WebhookURLs {
		if !webhookRe.MatchString(u) {
			return nil, nil, fmt.Errorf("webhook %q must be an https:// address", u)
		}
	}
	m.mu.Lock()
	st, err := m.load()
	if err != nil {
		m.mu.Unlock()
		return nil, nil, err
	}
	if req.UseTurn && !st.Turn.Enabled {
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("switch the TURN server on first, or untick “use TURN”")
	}
	lk := &st.LiveKit
	wasOn := lk.Enabled
	lk.Enabled, lk.Domain, lk.Account, lk.UseTurn = req.Enabled, req.Domain, req.Account, req.UseTurn
	lk.EmptyTimeout, lk.MaxParticipants, lk.WebhookURLs, lk.AllowAccounts = req.EmptyTimeout, req.MaxParticipants, req.WebhookURLs, req.AllowAccounts
	lk.UpdatedAt = kit.Now()
	var first *APIKey
	if lk.Enabled && len(lk.Keys) == 0 {
		k, plain, kerr := m.newKey("Default", "")
		if kerr != nil {
			m.mu.Unlock()
			return nil, nil, kerr
		}
		lk.Keys = append(lk.Keys, k)
		first = &APIKey{Key: k.Key, Secret: plain, Name: k.Name, Created: k.Created}
	}
	notes, err := m.applyLiveKit(ctx, st)
	if err == nil {
		err = m.save(st)
	}
	if err == nil && wasOn && !lk.Enabled {
		m.closePorts(ctx, livekitPorts())
	}
	m.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	if lk.Enabled && m.ProxySite != nil {
		if note, perr := m.ProxySite(ctx, lk.Account, lk.Domain, LiveKitPort); perr != nil {
			notes = append(notes, "LiveKit's website couldn't be set up: "+perr.Error())
		} else if note != "" {
			notes = append(notes, note)
		}
	}
	m.env.Publish("realtime.livekit.changed", lk.Domain, map[string]any{"enabled": lk.Enabled})
	out, err := m.Status(ctx)
	if out != nil {
		out.Notes = append(out.Notes, notes...)
	}
	return out, first, err
}

// applyLiveKitNow rewrites LiveKit's config from saved settings.
func (m *Manager) applyLiveKitNow(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.load()
	if err != nil {
		return nil, err
	}
	if !st.LiveKit.Enabled {
		return nil, nil
	}
	notes, err := m.applyLiveKit(ctx, st)
	if err != nil {
		return notes, err
	}
	return notes, m.save(st)
}

// liveKitTurnLife is how long the TURN login written into LiveKit's config
// lasts; the maintenance loop renews it a year before it runs out.
const liveKitTurnLife = 5 * 365 * 24 * time.Hour

// applyLiveKit writes the config and starts, restarts or stops LiveKit.
// Called with m.mu held.
func (m *Manager) applyLiveKit(ctx context.Context, st *state) ([]string, error) {
	lk := st.LiveKit
	if !lk.Enabled {
		_, _ = m.env.Run.Run(ctx, "systemctl", "disable", "--now", livekitService)
		return nil, nil
	}
	keys := map[string]string{}
	for _, k := range lk.Keys {
		s, err := m.unseal(k.Secret)
		if err != nil {
			return nil, err
		}
		keys[k.Key] = s
	}
	var turn []turnServer
	if lk.UseTurn && st.Turn.Enabled && st.Turn.Domain != "" {
		secret, err := m.unseal(st.Turn.Secret)
		if err != nil {
			return nil, err
		}
		exp := kit.Now().Add(liveKitTurnLife).Truncate(time.Second)
		u, c := turnSign(secret, "livekit", exp)
		turn = append(turn, turnServer{st.Turn.Domain, TurnPort, "udp", u, c})
		if st.Turn.TLS {
			turn = append(turn, turnServer{st.Turn.Domain, TurnTLSPort, "tls", u, c})
		}
		st.LiveKitTurnUntil = exp
	}
	conf := renderLiveKitConf(lk, keys, turn, m.env.Cfg.PublicIPv4)
	if err := kit.WriteFile(m.env.P(m.env.Cfg.LiveKitConfig), []byte(conf), 0o640); err != nil {
		return nil, err
	}
	m.chgrp(m.env.Cfg.LiveKitConfig, "livekit")
	var notes []string
	for _, p := range livekitPorts() {
		port, proto, _ := strings.Cut(p, "/")
		if err := m.openPort(ctx, port, proto, "LiveKit media"); err != nil {
			notes = append(notes, "Could not open "+p+" in the firewall: "+err.Error())
		}
	}
	if _, err := m.env.Run.Run(ctx, "systemctl", "enable", livekitService); err != nil {
		return notes, err
	}
	if _, err := m.env.Run.Run(ctx, "systemctl", "restart", livekitService); err != nil {
		return notes, fmt.Errorf("LiveKit did not start: %w", err)
	}
	return notes, nil
}

type turnServer struct {
	Host     string
	Port     int
	Protocol string // udp | tls
	Username string
	Cred     string
}

// yamlStr quotes a value for YAML (JSON strings are valid YAML).
func yamlStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func renderLiveKitConf(lk LiveKitSettings, keys map[string]string, turn []turnServer, publicIP string) string {
	var b strings.Builder
	b.WriteString("# Written by Ike. Change LiveKit in the panel under Server → Real-time media.\n")
	fmt.Fprintf(&b, "port: %d\n", LiveKitPort)
	b.WriteString("bind_addresses:\n  - 127.0.0.1\n") // signalling reaches it through the website
	b.WriteString("rtc:\n")
	fmt.Fprintf(&b, "  tcp_port: %d\n  port_range_start: %d\n  port_range_end: %d\n", LiveKitTCP, LiveKitUDPMin, LiveKitUDPMax)
	if publicIP != "" {
		fmt.Fprintf(&b, "  use_external_ip: false\n  node_ip: %s\n", yamlStr(publicIP))
	} else {
		b.WriteString("  use_external_ip: true\n")
	}
	if len(turn) > 0 {
		b.WriteString("  turn_servers:\n")
		for _, t := range turn {
			fmt.Fprintf(&b, "    - host: %s\n      port: %d\n      protocol: %s\n      username: %s\n      credential: %s\n",
				yamlStr(t.Host), t.Port, t.Protocol, yamlStr(t.Username), yamlStr(t.Cred))
		}
	}
	b.WriteString("keys:\n")
	ids := make([]string, 0, len(keys))
	for k := range keys {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, k := range ids {
		fmt.Fprintf(&b, "  %s: %s\n", k, yamlStr(keys[k]))
	}
	b.WriteString("room:\n")
	fmt.Fprintf(&b, "  empty_timeout: %d\n  max_participants: %d\n", lk.EmptyTimeout, lk.MaxParticipants)
	if len(lk.WebhookURLs) > 0 && len(ids) > 0 {
		fmt.Fprintf(&b, "webhook:\n  api_key: %s\n  urls:\n", ids[0])
		for _, u := range lk.WebhookURLs {
			fmt.Fprintf(&b, "    - %s\n", yamlStr(u))
		}
	}
	b.WriteString("logging:\n  level: info\n")
	return b.String()
}

func (m *Manager) newKey(name, account string) (APIKey, string, error) {
	plain := kit.Password(40)
	sealed, err := m.seal(plain)
	if err != nil {
		return APIKey{}, "", err
	}
	return APIKey{Key: "API" + kit.Password(12), Secret: sealed, Name: name, Account: account, Created: kit.Now()}, plain, nil
}

// CreateKey adds a LiveKit API key and returns it with its secret, once.
// account "" is the admin's; an account may only add its own keys when
// LiveKit allows it.
func (m *Manager) CreateKey(ctx context.Context, name, account string, asAdmin bool) (*APIKey, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("name: letters, digits, spaces and . _ - (max 40)")
	}
	if account != "" {
		if err := m.env.CheckAccount(account); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	st, err := m.load()
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if !st.LiveKit.Enabled {
		m.mu.Unlock()
		return nil, fmt.Errorf("switch LiveKit on first")
	}
	if !asAdmin && !st.LiveKit.AllowAccounts {
		m.mu.Unlock()
		return nil, fmt.Errorf("LiveKit API keys %w for account owners on this server", kit.ErrForbidden)
	}
	if len(st.LiveKit.Keys) >= 100 {
		m.mu.Unlock()
		return nil, fmt.Errorf("at most 100 API keys")
	}
	k, plain, err := m.newKey(name, account)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	st.LiveKit.Keys = append(st.LiveKit.Keys, k)
	_, err = m.applyLiveKit(ctx, st)
	if err == nil {
		err = m.save(st)
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	m.env.Publish("realtime.livekit.key.created", k.Key, map[string]any{"account": account, "name": name})
	k.Secret = plain
	return &k, nil
}

// DeleteKey removes a key (only the account's own when account isn't "").
// LiveKit restarts so the key stops working at once.
func (m *Manager) DeleteKey(ctx context.Context, key, account string, asAdmin bool) error {
	if !keyIDRe.MatchString(key) {
		return fmt.Errorf("API key %w", kit.ErrNotFound)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.load()
	if err != nil {
		return err
	}
	keep := st.LiveKit.Keys[:0]
	found := false
	for _, k := range st.LiveKit.Keys {
		if k.Key == key && (asAdmin || k.Account == account) {
			found = true
			continue
		}
		keep = append(keep, k)
	}
	if !found {
		return fmt.Errorf("API key %w", kit.ErrNotFound)
	}
	if len(keep) == 0 && st.LiveKit.Enabled {
		return fmt.Errorf("LiveKit needs at least one API key; add another first")
	}
	st.LiveKit.Keys = keep
	if _, err := m.applyLiveKit(ctx, st); err != nil {
		return err
	}
	if err := m.save(st); err != nil {
		return err
	}
	m.env.Publish("realtime.livekit.key.deleted", key, map[string]any{"account": account})
	return nil
}

// TokenRequest asks for a LiveKit join token.
type TokenRequest struct {
	Key        string `json:"key"`
	Room       string `json:"room"`
	Identity   string `json:"identity"`
	Name       string `json:"name"`
	TTLMinutes int    `json:"ttl_minutes"`
	CanPublish *bool  `json:"can_publish"` // default true
	RoomAdmin  bool   `json:"room_admin"`
}

// Token is a signed join token and where to use it.
type Token struct {
	Token   string    `json:"token"`
	URL     string    `json:"url"`
	Expires time.Time `json:"expires"`
}

// JoinToken signs a LiveKit access token (a JWT) with one of the keys, for
// testing a room or for small apps without a server-side SDK.
func (m *Manager) JoinToken(req TokenRequest, account string, asAdmin bool) (*Token, error) {
	if !roomRe.MatchString(req.Room) {
		return nil, fmt.Errorf("room: letters, digits and _ . : - (max 64)")
	}
	if !identRe.MatchString(req.Identity) {
		return nil, fmt.Errorf("identity: letters, digits and _ . @ : - (max 64)")
	}
	if len(req.Name) > 64 {
		return nil, fmt.Errorf("name is too long")
	}
	if req.TTLMinutes == 0 {
		req.TTLMinutes = 60
	}
	if req.TTLMinutes < 1 || req.TTLMinutes > 24*60 {
		return nil, fmt.Errorf("tokens last 1 minute to 24 hours")
	}
	m.mu.Lock()
	st, err := m.load()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !st.LiveKit.Enabled {
		return nil, fmt.Errorf("LiveKit is switched off")
	}
	// The named key, or the caller's first one.
	var key *APIKey
	for i, k := range st.LiveKit.Keys {
		mine := asAdmin || k.Account == account
		if mine && (req.Key == "" || k.Key == req.Key) {
			key = &st.LiveKit.Keys[i]
			break
		}
	}
	if key == nil {
		return nil, fmt.Errorf("API key %w", kit.ErrNotFound)
	}
	secret, err := m.unseal(key.Secret)
	if err != nil {
		return nil, err
	}
	pub := true
	if req.CanPublish != nil {
		pub = *req.CanPublish
	}
	now := kit.Now()
	exp := now.Add(time.Duration(req.TTLMinutes) * time.Minute)
	tok, err := signJWT(secret, map[string]any{
		"iss": key.Key, "sub": req.Identity, "name": req.Name, "nbf": now.Unix() - 10, "exp": exp.Unix(),
		"video": map[string]any{"room": req.Room, "roomJoin": true, "canPublish": pub, "canSubscribe": true,
			"canPublishData": pub, "roomAdmin": req.RoomAdmin},
	})
	if err != nil {
		return nil, err
	}
	return &Token{Token: tok, URL: "wss://" + st.LiveKit.Domain, Expires: exp}, nil
}

// signJWT makes an HS256 JSON Web Token.
func signJWT(secret string, claims map[string]any) (string, error) {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil)), nil
}

// ---------------------------------------------------------------- accounts

// AccountView is what an account owner sees: where to connect, their own
// keys (no secrets) and what they may do.
type AccountView struct {
	LiveKitURL      string   `json:"livekit_url,omitempty"`
	LiveKitKeys     []APIKey `json:"livekit_keys"`
	CanMakeKeys     bool     `json:"can_make_keys"`
	TurnURIs        []string `json:"turn_uris,omitempty"`
	CanMakeTurnCred bool     `json:"can_make_turn_credentials"`
}

func (m *Manager) ForAccount(account string) (*AccountView, error) {
	if err := m.env.CheckAccount(account); err != nil {
		return nil, err
	}
	m.mu.Lock()
	st, err := m.load()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	v := &AccountView{LiveKitKeys: []APIKey{}}
	if st.LiveKit.Enabled {
		v.LiveKitURL = "wss://" + st.LiveKit.Domain
		v.CanMakeKeys = st.LiveKit.AllowAccounts
		for _, k := range publicKeys(st.LiveKit.Keys) {
			if k.Account == account {
				v.LiveKitKeys = append(v.LiveKitKeys, k)
			}
		}
	}
	if st.Turn.Enabled {
		v.TurnURIs = turnURIs(st.Turn)
		v.CanMakeTurnCred = st.Turn.AllowAccounts
	}
	return v, nil
}

// DeleteAccount removes an account's LiveKit keys when the account goes.
func (m *Manager) DeleteAccount(ctx context.Context, account string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.load()
	if err != nil {
		return
	}
	keep := st.LiveKit.Keys[:0]
	for _, k := range st.LiveKit.Keys {
		if k.Account != account {
			keep = append(keep, k)
		}
	}
	if len(keep) == len(st.LiveKit.Keys) {
		return
	}
	st.LiveKit.Keys = keep
	if st.LiveKit.Enabled {
		_, _ = m.applyLiveKit(ctx, st)
	}
	_ = m.save(st)
}

// ---------------------------------------------------------------- upkeep

// RunMaintenance renews what runs out on its own: the certificate copy coturn
// reads (after Let's Encrypt renews it) and LiveKit's TURN login.
func (m *Manager) RunMaintenance(ctx context.Context) {
	t := time.NewTimer(5 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.maintain(ctx)
		t.Reset(12 * time.Hour)
	}
}

func (m *Manager) maintain(ctx context.Context) {
	m.mu.Lock()
	st, err := m.load()
	if err != nil {
		m.mu.Unlock()
		return
	}
	changed := false
	if st.Turn.Enabled && st.Turn.TLS {
		sum, err := m.copyTurnCert(ctx, st.Turn.Domain)
		if err == nil && sum != st.TurnCertSum {
			if _, err := m.applyTurn(ctx, st); err != nil {
				slog.Warn("TURN didn't restart with the renewed certificate", "err", err)
			} else {
				m.env.Publish("realtime.turn.certificate_renewed", st.Turn.Domain, nil)
			}
			changed = true
		}
	}
	if st.LiveKit.Enabled && st.LiveKit.UseTurn && !st.LiveKitTurnUntil.IsZero() &&
		time.Until(st.LiveKitTurnUntil) < 365*24*time.Hour {
		if _, err := m.applyLiveKit(ctx, st); err != nil {
			slog.Warn("LiveKit's TURN login wasn't renewed", "err", err)
		}
		changed = true
	}
	if changed {
		_ = m.save(st)
	}
	m.mu.Unlock()
}

// ---------------------------------------------------------------- firewall

func (m *Manager) openPort(ctx context.Context, port, proto, comment string) error {
	if m.env.Firewall == nil {
		return nil
	}
	return m.env.Firewall.AllowFrom(ctx, "", port, proto, comment)
}

// closePorts closes ports Ike opened for a server that was switched off.
func (m *Manager) closePorts(ctx context.Context, ports []string) {
	c, ok := m.env.Firewall.(kit.Closer)
	if !ok {
		return
	}
	for _, p := range ports {
		port, proto, _ := strings.Cut(p, "/")
		_ = c.Close(ctx, port, proto)
	}
}
