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

	"github.com/YOUR-ORG/ike/agent/internal/api"
	"github.com/YOUR-ORG/ike/agent/internal/cluster"
	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/firewall"
	"github.com/YOUR-ORG/ike/agent/internal/jobs"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/sites"
	"github.com/YOUR-ORG/ike/agent/internal/updates"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "dns-hook" {
		os.Exit(dnsHook(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "join" {
		os.Exit(join(os.Args[2:]))
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
	fw := &firewall.Manager{Run: r, StateFile: filepath.Join(root, cfg.FirewallState), Protected: cfg.ProtectedPorts}
	jm := jobs.NewManager()
	up := &updates.Manager{Run: r, Jobs: jm, Root: root, DryRun: dryRun, Version: api.Version, Repo: cfg.Repo,
		APIBase: "https://api.github.com", DownloadBase: "https://github.com", BinDir: "/usr/local/bin"}
	srv := &api.Server{Sites: mgr, Updates: up, Jobs: jm, Firewall: fw, Runner: r, Config: cfg,
		AuditPath: filepath.Join(root, "var/log/ike/agent-audit.log")}
	_ = os.MkdirAll(filepath.Dir(srv.AuditPath), 0o750)
	if err := srv.Setup(); err != nil {
		return err
	}
	mgr.RewriteAllKeys()

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

	slog.Info("ike-agent listening", "version", api.Version, "socket", socket)
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
