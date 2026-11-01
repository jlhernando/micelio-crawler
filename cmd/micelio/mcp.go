package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/micelio/micelio/internal/mcpserver"
)

func mcpCmd() *cobra.Command {
	var (
		serverURL   string
		authToken   string
		allowCrawl  bool
		noAutostart bool
	)
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Let AI agents (Claude, Cursor...) read your crawls over MCP",
		Long: `Runs an MCP (Model Context Protocol) server on stdin/stdout, so an AI
agent can list crawls, read summaries, query pages and explain URLs.

Every call goes to the dashboard (default http://127.0.0.1:3100). If no
dashboard answers, "micelio ui" is started in the background (log in
~/.micelio/ui/mcp-dashboard.log) unless --no-autostart is set.

Agents can only read crawls unless --allow-crawl is set. Crawls started by
an agent are capped: 10000 pages, 5 parallel requests, 500 ms between
requests, robots.txt respected.`,
		Example: `  claude mcp add micelio -- micelio mcp
  claude mcp add micelio -- micelio mcp --allow-crawl`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// stdout carries the MCP protocol: everything else goes to stderr.
			log.SetOutput(os.Stderr)
			if authToken == "" {
				authToken = os.Getenv("MICELIO_AUTH_TOKEN")
			}
			backend := mcpserver.NewHTTPBackend(serverURL, authToken)
			// The dashboard is checked (and started) on the first tool call,
			// so the agent's handshake never waits for it.
			backend.Ready = func(ctx context.Context) error {
				return ensureDashboard(ctx, backend.Base, authToken, !noAutostart)
			}
			srv := mcpserver.New(backend, mcpserver.Options{AllowCrawl: allowCrawl, Version: version})
			fmt.Fprintf(os.Stderr, "micelio mcp: serving on stdio, dashboard %s (crawl tools %s)\n",
				backend.Base, map[bool]string{true: "on", false: "off"}[allowCrawl])
			return srv.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
	cmd.Flags().StringVar(&serverURL, "server", "http://127.0.0.1:3100", "dashboard to read crawls from")
	cmd.Flags().StringVar(&authToken, "auth-token", "", "dashboard auth token (or MICELIO_AUTH_TOKEN env)")
	cmd.Flags().BoolVar(&allowCrawl, "allow-crawl", false, "let the agent start, wait for and stop crawls")
	cmd.Flags().BoolVar(&noAutostart, "no-autostart", false, "fail instead of starting the dashboard when none answers")
	return cmd
}

// ensureDashboard makes sure a dashboard answers at base, starting one if
// autostart is set.
func ensureDashboard(ctx context.Context, base, token string, autostart bool) error {
	if _, ok := reachableDashboard(base); ok {
		return nil
	}
	if !autostart {
		return fmt.Errorf("no Micelio dashboard answers at %s: start it with \"micelio ui\" (or run micelio mcp without --no-autostart)", base)
	}
	host, port, err := dashboardAddr(base)
	if err != nil {
		return fmt.Errorf("no Micelio dashboard answers at %s and it cannot be started here: %w", base, err)
	}
	proc, logPath, err := startDashboard(host, port, token)
	if err != nil {
		return fmt.Errorf("start the dashboard: %w", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := reachableDashboard(base); ok {
			log.Printf("micelio mcp: started the dashboard on %s (log %s)", net.JoinHostPort(host, port), logPath)
			return proc.Release()
		}
		select {
		case <-ctx.Done():
			proc.Kill()
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	// A dashboard that never answered is of no use: do not leave it running.
	proc.Kill()
	return fmt.Errorf("started \"micelio ui --host %s --port %s\" but it did not answer within 20 s; see %s", host, port, logPath)
}

// dashboardAddr returns the host and port "micelio ui" must listen on to
// answer at base. Only a dashboard on this computer can be started.
func dashboardAddr(base string) (host, port string, err error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("invalid dashboard URL %q", base)
	}
	host = u.Hostname()
	if host == "localhost" {
		host = "127.0.0.1"
	} else if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return "", "", fmt.Errorf("%s is not on this computer", host)
	}
	port = u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return host, port, nil
}

// dashboardPort returns the port of a dashboard URL on this computer.
func dashboardPort(base string) (string, error) {
	_, port, err := dashboardAddr(base)
	return port, err
}

// startDashboard runs "micelio ui --host host --port port" in the
// background, detached from this process, logging to
// ~/.micelio/ui/mcp-dashboard.log.
func startDashboard(host, port, token string) (*os.Process, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Join(home, ".micelio", "ui")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	logPath := filepath.Join(dir, "mcp-dashboard.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, "", err
	}
	defer logFile.Close()

	c := exec.Command(exe, "ui", "--host", host, "--port", port)
	// Never inherit stdout: it carries the MCP protocol.
	c.Stdin, c.Stdout, c.Stderr = nil, logFile, logFile
	c.Env = os.Environ()
	if token != "" {
		c.Env = append(c.Env, "MICELIO_AUTH_TOKEN="+token)
	}
	detach(c)
	if err := c.Start(); err != nil {
		return nil, "", err
	}
	return c.Process, logPath, nil
}
