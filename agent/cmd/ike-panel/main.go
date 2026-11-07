// ike-panel is Ike's web control panel: sign-in with 2FA, admin / reseller /
// account-owner panels on their own ports, billing and alerts. It talks to
// ike-agent over its unix socket and never runs as a privileged process.
//
//	ike-panel --config /etc/ike/panel.json
//	ike-panel create-admin --username marvellous --email marvellous@example.com   (password on stdin, or generated)
package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/api"
	"github.com/Mastermegamind/ike/agent/internal/client"
	"github.com/Mastermegamind/ike/agent/internal/cluster"
	"github.com/Mastermegamind/ike/agent/internal/panel"
	"github.com/Mastermegamind/ike/agent/internal/panel/alerts"
	"github.com/Mastermegamind/ike/agent/internal/panel/auth"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
	"github.com/Mastermegamind/ike/agent/internal/panel/ui"
)

type Config struct {
	Listeners []panel.Listener `json:"listeners"`
	TLSCert   string           `json:"tls_cert"`
	TLSKey    string           `json:"tls_key"`
	Agent     string           `json:"agent_socket"`
	Store     string           `json:"store"`
	PublicURL string           `json:"public_url"` // e.g. https://panel.example.com:2083
	Hostname  string           `json:"hostname"`
	// Multi-server management
	ClusterDir string `json:"cluster_dir"`
	PublicIP   string `json:"public_ipv4"`
}

func defaults() Config {
	return Config{
		Listeners: []panel.Listener{
			{Name: "panel", Addr: ":2083", Roles: []store.Role{store.User, store.Reseller, store.Admin}},
			{Name: "reseller", Addr: ":2086", Roles: []store.Role{store.Reseller, store.Admin}},
			{Name: "admin", Addr: ":2087", Roles: []store.Role{store.Admin}},
		},
		TLSCert:    "/etc/ike/tls/panel.crt",
		TLSKey:     "/etc/ike/tls/panel.key",
		Agent:      "/run/ike/agent.sock",
		Store:      "/var/lib/ike/panel/panel.json",
		ClusterDir: "/var/lib/ike/panel/cluster",
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		if err := resetPassword(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "create-admin" {
		if err := createAdmin(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	cfgPath := flag.String("config", "/etc/ike/panel.json", "config file")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVer {
		fmt.Println("ike-panel", api.Version)
		return
	}
	if err := run(*cfgPath); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func loadConfig(path string) (Config, error) {
	c := defaults()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func run(cfgPath string) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.Store)
	if err != nil {
		return err
	}
	cert, err := loadOrCreateCert(cfg.TLSCert, cfg.TLSKey, cfg.Hostname)
	if err != nil {
		return err
	}
	if cfg.PublicURL == "" {
		host := cfg.Hostname
		if host == "" {
			host, _ = os.Hostname()
		}
		cfg.PublicURL = "https://" + host + ":2083"
	}
	agent := client.Unix(cfg.Agent)
	srv := &panel.Server{Store: st, Agent: agent, UI: ui.FS(), PublicURL: strings.TrimRight(cfg.PublicURL, "/"), TLS: true,
		ClusterDir: cfg.ClusterDir, LocalIP: cfg.PublicIP}
	if len(cert.Certificate) > 0 {
		srv.CertFingerprint = cluster.Fingerprint(cert.Certificate[0])
	}
	for _, l := range cfg.Listeners {
		if l.Name == "admin" {
			_, srv.AdminPort, _ = net.SplitHostPort(l.Addr)
		}
	}
	srv.Notifier = &alerts.Notifier{Settings: func() (a store.AlertSettings) { st.View(func(d *store.Data) { a = d.Alerts }); return }}
	srv.Monitor = &alerts.Monitor{Notifier: srv.Notifier, Targets: func(ctx context.Context) ([]alerts.Target, error) {
		var sites []struct {
			Domain string `json:"domain"`
			Status string `json:"status"`
		}
		err := agent.Get(ctx, "/v1/sites", &sites)
		out := make([]alerts.Target, len(sites))
		for i, s := range sites {
			out[i] = alerts.Target{Domain: s.Domain, Status: s.Status}
		}
		return out, err
	}}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go background(ctx, srv, st, agent)

	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	errs := make(chan error, len(cfg.Listeners))
	var servers []*http.Server
	for _, l := range cfg.Listeners {
		hs := &http.Server{Addr: l.Addr, Handler: srv.Handler(l), TLSConfig: tlsCfg,
			ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 10 * time.Minute, IdleTimeout: 2 * time.Minute}
		servers = append(servers, hs)
		go func(l panel.Listener) {
			ln, err := net.Listen("tcp", l.Addr)
			if err != nil {
				errs <- fmt.Errorf("listen %s (%s): %w", l.Addr, l.Name, err)
				return
			}
			slog.Info("panel listening", "name", l.Name, "addr", l.Addr)
			errs <- hs.ServeTLS(ln, "", "")
		}(l)
	}
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shut, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, hs := range servers {
		_ = hs.Shutdown(shut)
	}
	return nil
}

// background runs the overdue-invoice enforcer, the uptime monitor, and
// forwards the agent's app-health events to alerts.
func background(ctx context.Context, srv *panel.Server, st *store.Store, agent *client.Client) {
	overdue := time.NewTicker(time.Hour)
	tick := time.NewTicker(time.Minute)
	defer overdue.Stop()
	defer tick.Stop()
	var lastUptime, lastDaily, lastBandwidth time.Time
	// start from the newest event so a restart doesn't re-send old alerts
	lastEventID := srv.LatestEventID(ctx)
	lastEvent := time.Now()
	told := map[string]bool{} // certificate problems and image updates already announced
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-overdue.C:
			srv.RunSubscriptions(ctx, now)
			srv.EnforceOverdue(ctx, now)
		case now := <-tick.C:
			var every int
			st.View(func(d *store.Data) { every = d.Alerts.UptimeIntervalMin })
			if every > 0 && now.Sub(lastUptime) >= time.Duration(every)*time.Minute {
				lastUptime = now
				srv.Monitor.Check(ctx)
			}
			if now.Sub(lastBandwidth) >= 15*time.Minute {
				lastBandwidth = now
				srv.TrackBandwidth(ctx, now)
			}
			lastEventID = srv.ForwardEvents(ctx, lastEventID)
			if now.Sub(lastDaily) >= 6*time.Hour {
				lastDaily = now
				dailyChecks(ctx, srv, agent, told)
			}
			var events []struct {
				Time   time.Time `json:"time"`
				Domain string    `json:"domain"`
				Kind   string    `json:"kind"`
				Detail string    `json:"detail"`
			}
			if agent.Get(ctx, "/v1/health/events", &events) == nil {
				for i := len(events) - 1; i >= 0; i-- {
					e := events[i]
					if e.Time.After(lastEvent) && e.Kind != "recovered" {
						srv.Notifier.Send(ctx, e.Domain+": app "+e.Kind, e.Detail)
					}
				}
				if len(events) > 0 && events[0].Time.After(lastEvent) {
					lastEvent = events[0].Time
				}
			}
		}
	}
}

// loadOrCreateCert uses the configured certificate, or makes a self-signed one
// on first start so the panel is never served over plain HTTP. The installer
// replaces it with Let's Encrypt once a hostname points at the server.
func loadOrCreateCert(certPath, keyPath, host string) (tls.Certificate, error) {
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	if host == "" {
		host, _ = os.Hostname()
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"Ike self-signed"}},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	if err := os.MkdirAll(filepath.Dir(certPath), 0o750); err == nil {
		_ = os.WriteFile(keyPath, keyPEM, 0o600)
		_ = os.WriteFile(certPath, certPEM, 0o644)
	}
	slog.Warn("using a self-signed certificate; browsers will warn until a real certificate is installed", "host", host)
	return tls.X509KeyPair(certPEM, keyPEM)
}

func createAdmin(args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ExitOnError)
	username := fs.String("username", "", "admin username")
	email := fs.String("email", "", "admin email")
	storePath := fs.String("store", defaults().Store, "panel data file")
	_ = fs.Parse(args)
	if *username == "" || *email == "" {
		return errors.New("--username and --email are required")
	}
	pw := ""
	if fi, _ := os.Stdin.Stat(); fi.Mode()&os.ModeCharDevice == 0 {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pw = strings.TrimRight(line, "\r\n")
	}
	generated := false
	if pw == "" {
		pw, generated = auth.RandomToken(12), true
	}
	h, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	st, err := store.Open(*storePath)
	if err != nil {
		return err
	}
	if err := st.Update(func(d *store.Data) error {
		return d.AddPerson(&store.Person{Username: *username, Email: *email, Role: store.Admin, PasswordHash: h})
	}); err != nil {
		return err
	}
	if generated {
		fmt.Printf("Created admin %s. Password: %s\n", *username, pw)
	} else {
		fmt.Printf("Created admin %s.\n", *username)
	}
	return nil
}

// resetPassword sets a new password for a panel login (run as root or the
// ike user on the server). The new password is read from stdin, or generated
// and printed once. --disable-2fa also turns off two-factor sign-in.
//
//	systemctl stop ike-panel
//	sudo -u ike ike-panel reset-password --username marvellous [--disable-2fa]
//	systemctl start ike-panel
//
// Stop the panel first: it keeps its data in memory and would overwrite the change.
func resetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	username := fs.String("username", "", "login to reset")
	no2fa := fs.Bool("disable-2fa", false, "also turn off two-factor sign-in")
	storePath := fs.String("store", defaults().Store, "panel data file")
	_ = fs.Parse(args)
	if *username == "" {
		return errors.New("--username is required")
	}
	pw := ""
	if fi, _ := os.Stdin.Stat(); fi.Mode()&os.ModeCharDevice == 0 {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pw = strings.TrimRight(line, "\r\n")
	}
	generated := false
	if pw == "" {
		pw, generated = auth.RandomToken(12), true
	}
	if len(pw) < 10 {
		return errors.New("use at least 10 characters")
	}
	h, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	st, err := store.Open(*storePath)
	if err != nil {
		return err
	}
	if err := st.Update(func(d *store.Data) error {
		p, err := d.Person(*username)
		if err != nil {
			return err
		}
		p.PasswordHash, p.Disabled = h, false
		if *no2fa {
			p.TOTPEnabled, p.TOTPSecret, p.RecoveryCodes = false, "", nil
		}
		return nil
	}); err != nil {
		return err
	}
	if generated {
		fmt.Printf("New password for %s: %s\n", *username, pw)
	} else {
		fmt.Printf("Password changed for %s.\n", *username)
	}
	fmt.Println("Start the panel again if you stopped it: systemctl start ike-panel")
	return nil
}

// dailyChecks alerts once about certificates that are expiring or failing to
// renew, and about newer container images.
func dailyChecks(ctx context.Context, srv *panel.Server, agent *client.Client, told map[string]bool) {
	var certs []struct {
		Domain   string `json:"domain"`
		DaysLeft int    `json:"days_left"`
		Problem  string `json:"problem"`
	}
	if agent.Get(ctx, "/v1/ssl", &certs) == nil {
		for _, c := range certs {
			key := "cert:" + c.Domain + ":" + c.Problem
			if c.Problem != "" && !told[key] {
				told[key] = true
				srv.Notifier.Send(ctx, c.Domain+": HTTPS certificate", fmt.Sprintf("%s (%d days left)", c.Problem, c.DaysLeft))
			}
		}
	}
	var images []struct {
		Image           string   `json:"image"`
		RemoteDigest    string   `json:"remote_digest"`
		UpdateAvailable bool     `json:"update_available"`
		Containers      []string `json:"containers"`
	}
	if agent.Get(ctx, "/v1/docker/updates", &images) == nil {
		for _, im := range images {
			key := "img:" + im.Image + ":" + im.RemoteDigest
			if im.UpdateAvailable && !told[key] {
				told[key] = true
				srv.Notifier.Send(ctx, "Container image update: "+im.Image,
					"A newer "+im.Image+" is available. Used by: "+strings.Join(im.Containers, ", "))
			}
		}
	}
}
