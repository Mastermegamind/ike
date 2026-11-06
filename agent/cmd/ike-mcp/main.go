// ike-mcp is a Model Context Protocol server over stdio. AI clients (Claude, Cursor,
// VS Code, ...) launch it locally, or remotely with:  ssh root@your-server ike-mcp
//
// Two ways to connect:
//
//	ike-mcp                                   the local agent socket (root, every account)
//	IKE_TOKEN=ike_… ike-mcp --panel https://server:2087
//	                                          through the panel with an API token: the panel
//	                                          enforces the token's areas, accounts, addresses
//	                                          and read-only flag, and tools it can't use are hidden
//
// Either way --tools dns,mail limits the tool areas and writes need --allow-write.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
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
		panelURL   = flag.String("panel", os.Getenv("IKE_URL"), "connect through this panel (https://host:2087) instead of the socket; needs an API token")
		token      = flag.String("token", "", "API token (prefer the IKE_TOKEN environment variable: flags are visible to other users in ps)")
		insecure   = flag.Bool("insecure", os.Getenv("IKE_INSECURE") == "1", "accept the panel's self-signed certificate")
		toolsFlag  = flag.String("tools", "", "only these tool areas, comma separated: "+strings.Join(mcp.Areas, ","))
		listTools  = flag.Bool("list-tools", false, "print the tools this connection would offer and exit")
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var agent *client.Client
	tok := *token
	if tok == "" {
		tok = os.Getenv("IKE_TOKEN")
	}
	if *panelURL != "" {
		if tok == "" {
			fatal("--panel needs an API token: set IKE_TOKEN (create one in the panel under Settings → API tokens)")
		}
		agent = client.Panel(*panelURL, tok, *insecure)
	} else {
		agent = client.Unix(*socket)
		agent.Actor = "ai:mcp"
	}
	srv := &mcp.Server{Agent: agent, AllowWrite: *allowWrite, Version: api.Version, Audit: audit}
	if *toolsFlag != "" {
		srv.Groups = map[string]bool{"": true}
		known := map[string]bool{}
		for _, a := range mcp.Areas {
			known[a] = true
		}
		for _, g := range strings.Split(*toolsFlag, ",") {
			g = strings.TrimSpace(g)
			if !known[g] {
				fatal(fmt.Sprintf("unknown tool area %q; use %s", g, strings.Join(mcp.Areas, ", ")))
			}
			srv.Groups[g] = true
		}
	}
	if *panelURL != "" {
		desc, err := srv.ConnectPanel(ctx)
		if err != nil {
			fatal(err.Error())
		}
		slog.Info(desc)
	}
	if *listTools {
		for _, line := range srv.ToolSummary() {
			fmt.Println(line)
		}
		return
	}
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		slog.Error("mcp server stopped", "err", err)
		os.Exit(1)
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "ike-mcp:", msg)
	os.Exit(1)
}
