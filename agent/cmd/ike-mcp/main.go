// ike-mcp is a Model Context Protocol server over stdio. AI clients (Claude, Cursor,
// VS Code, ...) launch it locally, or remotely with:  ssh root@your-server ike-mcp
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/YOUR-ORG/ike/agent/internal/api"
	"github.com/YOUR-ORG/ike/agent/internal/client"
	"github.com/YOUR-ORG/ike/agent/internal/mcp"
)

func main() {
	var (
		socket     = flag.String("socket", "/run/ike/agent.sock", "ike-agent unix socket")
		allowWrite = flag.Bool("allow-write", false, "enable tools that change the server (create/delete sites, SSH access)")
		auditPath  = flag.String("audit-log", "/var/log/ike/mcp-audit.log", "append every tool call here (empty to disable)")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	// stdout carries the protocol, so all logging goes to stderr.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if *showVer {
		fmt.Println("ike-mcp", api.Version)
		return
	}

	var audit io.Writer
	if *auditPath != "" {
		f, err := os.OpenFile(*auditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			slog.Warn("audit log unavailable", "path", *auditPath, "err", err)
		} else {
			defer f.Close()
			audit = f
		}
	}

	srv := &mcp.Server{Agent: client.Unix(*socket), AllowWrite: *allowWrite, Version: api.Version, Audit: audit}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		slog.Error("mcp server stopped", "err", err)
		os.Exit(1)
	}
}
