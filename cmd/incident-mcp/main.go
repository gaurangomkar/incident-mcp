package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gaurangomkar/incident-mcp/internal/config"
	"github.com/gaurangomkar/incident-mcp/internal/server"
)

// version is set with -X main.version at build time.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("incident-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file path (also INCIDENT_MCP_CONFIG). Searches well-known paths when empty")
	transport := fs.String("transport", "", "override transport: stdio or http")
	httpAddr := fs.String("http-addr", "", "override listen address for the http transport")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		say(stderr, "incident-mcp %s\n\nRead-only MCP server for Prometheus, Kubernetes, and Alertmanager.\n\n", version)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		say(stderr, "incident-mcp %s\n", version)
		return 0
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		say(stderr, "config: %v\n", err)
		return 1
	}
	if *transport != "" {
		cfg.Server.Transport = *transport
	}
	if *httpAddr != "" {
		cfg.Server.HTTPAddr = *httpAddr
	}
	if err := cfg.Validate(); err != nil {
		say(stderr, "config: %v\n", err)
		return 1
	}

	logger := newLogger(cfg.LogLevel, stderr)
	slog.SetDefault(logger)
	if cfg.Path != "" {
		logger.Info("loaded config", "path", cfg.Path)
	} else {
		logger.Info("no config file found, using defaults and environment")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv, err := server.New(server.Options{Config: cfg, Logger: logger, Version: version})
	if err != nil {
		logger.Error("startup", "err", err)
		return 1
	}
	logger.Info("starting", "version", version, "transport", cfg.Server.Transport)
	if err := srv.Run(ctx); err != nil {
		logger.Error("server stopped", "err", err)
		return 1
	}
	return 0
}

func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func newLogger(level string, w io.Writer) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lv}))
}
