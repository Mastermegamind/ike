// ike-agent is the root daemon that applies changes on a Ike server.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/api"
	"github.com/Mastermegamind/ike/agent/internal/cluster"
	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/firewall"
	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/runner"
	"github.com/Mastermegamind/ike/agent/internal/sites"
	"github.com/Mastermegamind/ike/agent/internal/sysinfo"
	"github.com/Mastermegamind/ike/agent/internal/updates"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "dns-hook" {
		os.Exit(dnsHook(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "join" {
		os.Exit(join(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "upgrade-verify" {
		os.Exit(upgradeVerify(os.Args[2:]))
	}
	var (
		socket  = flag.String("socket", "/run/ike/agent.sock", "unix socket to listen on")
		cfgPath = flag.String("config", "/etc/ike/agent.json", "config file")
		root    = flag.String("root", "/", "filesystem root (for testing)")
		dryRun  = flag.Bool("dry-run", false, "log commands instead of running them")
		group   = flag.String("socket-group", "ike", "group allowed to use the socket")
		showVer = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if *showVer {
		fmt.Println("ike-agent", api.Version)
		return
	}
	if err := run(*socket, *cfgPath, *root, *group, *dryRun); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(socket, cfgPath, root, group string, dryRun bool) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	cfg.Root, cfg.DryRun = root, dryRun

	if !dryRun {
		if os.Geteuid() != 0 {
			return errors.New("ike-agent must run as root (use --dry-run for development)")
		}
		if root != "/" {
			return errors.New("--root can only be changed together with --dry-run")
		}
	}

	var r runner.Runner = runner.Exec{}
	if dryRun {
		r = &runner.DryRun{}
		slog.Warn("DRY-RUN mode: no system commands will be executed", "root", root)
	}

	mgr := sites.NewManager(cfg, r)
	if err := mgr.EnsurePages(); err != nil {
		slog.Warn("could not write default status/error pages", "err", err)
	}
	fw := &firewall.Manager{Run: r, StateFile: filepath.Join(root, cfg.FirewallState), Protected: cfg.ProtectedPorts,
		Backend: cfg.Firewall, RulesetFile: filepath.Join(root, cfg.FirewallRuleset)}
	if err := fw.Sync(context.Background()); err != nil {
		slog.Warn("could not load the firewall rules", "backend", cfg.Firewall, "err", err)
	}
	jm := jobs.NewManager()
	up := &updates.Manager{Run: r, Jobs: jm, Root: root, DryRun: dryRun, Version: api.Version, Repo: cfg.Repo,
		APIBase: "https://api.github.com", DownloadBase: "https://github.com", BinDir: "/usr/local/bin",
		StateDir: "/var/lib/ike", Socket: socket}
	srv := &api.Server{Sites: mgr, Updates: up, Jobs: jm, Firewall: fw, Runner: r, Config: cfg,
		AuditPath: filepath.Join(root, "var/log/ike/agent-audit.log"),
		Metrics:   sysinfo.NewSampler("/proc", filepath.Join(root, "var/lib/ike/metrics.json"))}
	_ = os.MkdirAll(filepath.Dir(srv.AuditPath), 0o750)
	if err := srv.Setup(); err != nil {
		return err
	}
	mgr.RewriteAllKeys()
	up.Publish = srv.F.Env.Publish

	if err := os.MkdirAll(filepath.Dir(socket), 0o750); err != nil {
		return err
	}
	_ = os.Remove(socket) // stale socket from a previous run
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	// Only root and members of the panel group may talk to the agent.
	if err := os.Chmod(socket, 0o660); err != nil {
		return err
	}
	if g, err := user.LookupGroup(group); err == nil {
		gid, _ := strconv.Atoi(g.Gid)
		_ = os.Chown(socket, 0, gid)
		_ = os.Chown(filepath.Dir(socket), 0, gid)
	} else if !dryRun {
		slog.Warn("socket group not found; only root can use the agent", "group", group)
	}

	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdown)
	}()

	go mgr.RunHealthMonitor(ctx)
	go mgr.RunUsageSampler(ctx)
	go srv.Metrics.Run(ctx)
	if !cfg.DryRun {
		go up.Watch(ctx, 6*time.Hour)
		go up.WatchPackages(ctx, 12*time.Hour)
	}
	if !cfg.DryRun {
		f := mgr.DetectWebFeatures(ctx)
		slog.Info("web server features", "nginx", f.NginxVersion, "http2_directive", f.H2Directive, "http3", f.HTTP3)
	}
	go mgr.RunImageUpdateChecks(ctx)
	srv.RunBackground(ctx)

	if cfg.ClusterListen != "" && !dryRun {
		tc, err := cluster.ServerTLS(cfg.ClusterDir)
		if err != nil {
			slog.Error("cluster listener disabled: certificates missing", "err", err)
		} else {
			tl, err := tls.Listen("tcp", cfg.ClusterListen, tc)
			if err != nil {
				return fmt.Errorf("cluster listener: %w", err)
			}
			ths := &http.Server{Handler: hs.Handler, ReadHeaderTimeout: 10 * time.Second}
			go func() { <-ctx.Done(); _ = ths.Close() }()
			go func() {
				slog.Info("accepting the panel over mutual TLS", "addr", cfg.ClusterListen)
				if err := ths.Serve(tl); err != nil && !errors.Is(err, http.ErrServerClosed) {
					slog.Error("cluster listener stopped", "err", err)
				}
			}()
		}
	}

	if cfg.WebStack == "caddy" && !dryRun {
		// Caddy asks here whether a visitor's password opens a protected folder.
		if err := serveAuthSocket(ctx, cfg.AuthSocket, cfg.WebGroup, mgr.AuthHandler()); err != nil {
			slog.Error("password checks for Caddy are off", "err", err)
		}
	}

	slog.Info("ike-agent listening", "version", api.Version, "socket", socket)
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// serveAuthSocket answers Caddy's forward_auth on a unix socket only the web
// server's group can reach.
func serveAuthSocket(ctx context.Context, path, group string, h http.Handler) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		ln.Close()
		return fmt.Errorf("web server group %q: %w", group, err)
	}
	gid, _ := strconv.Atoi(g.Gid)
	for _, p := range []string{path, filepath.Dir(path)} {
		if err := os.Chown(p, 0, gid); err != nil {
			ln.Close()
			return err
		}
	}
	if err := os.Chmod(path, 0o660); err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("password check socket stopped", "err", err)
		}
	}()
	return nil
}

// upgradeVerify is the safety check after an update, run by systemd from the
// copy of the version that was replaced: if the new Ike isn't healthy, it
// puts this version back.
func upgradeVerify(args []string) int {
	fs := flag.NewFlagSet("upgrade-verify", flag.ExitOnError)
	state := fs.String("state", "/var/lib/ike", "state folder")
	socket := fs.String("socket", "/run/ike/agent.sock", "agent socket")
	bin := fs.String("bin", "/usr/local/bin", "where Ike's programs are installed")
	_ = fs.Parse(args)
	m := &updates.Manager{Run: runner.Exec{}, Root: "/", Version: api.Version, StateDir: *state, Socket: *socket, BinDir: *bin}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := m.VerifyUpgrade(ctx); err != nil {
		slog.Error("upgrade check", "err", err)
		return 1
	}
	return 0
}
