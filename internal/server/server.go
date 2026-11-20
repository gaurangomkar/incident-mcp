// Package server wires the read-only tools into an MCP server.
// Adding a backend means implementing its Register function and calling it from New.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaurangomkar/incident-mcp/internal/alertmanager"
	"github.com/gaurangomkar/incident-mcp/internal/config"
	"github.com/gaurangomkar/incident-mcp/internal/kube"
	"github.com/gaurangomkar/incident-mcp/internal/mcptool"
	"github.com/gaurangomkar/incident-mcp/internal/prometheus"
	"github.com/gaurangomkar/incident-mcp/internal/redact"
	"github.com/gaurangomkar/incident-mcp/internal/triage"
)

const instructions = "incident-mcp is read-only. It can query Prometheus, list Alertmanager alerts and silences, and inspect Kubernetes pods, events, deployments, and pod logs. It cannot change a cluster, create silences, or delete anything. Prefer triage_service for a first look at a deployment, then use the specific tools to drill in. If a namespace allowlist is configured, other namespaces are rejected. Output is truncated and secret-looking values are redacted on a best-effort basis; redaction is not a guarantee. There is no tool for Secret objects."

// Options builds a server. The client overrides exist so tests can inject fakes;
// leave them nil to use Config.
type Options struct {
	Config     config.Config
	Logger     *slog.Logger
	Version    string
	Now        func() time.Time
	Prometheus prometheus.Querier
	Alerts     alertmanager.Client
	Cluster    *kube.Client
}

// Server is an MCP server with the incident tools registered.
type Server struct {
	mcp     *mcp.Server
	cfg     config.Config
	log     *slog.Logger
	version string
}

// New validates configuration, connects to the configured backends, and registers tools.
func New(opt Options) (*Server, error) {
	if err := opt.Config.Validate(); err != nil {
		return nil, err
	}
	logger := opt.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	version := opt.Version
	if version == "" {
		version = "dev"
	}
	userAgent := "incident-mcp/" + version

	promClient := opt.Prometheus
	if promClient == nil && opt.Config.Prometheus.URL != "" {
		c, err := prometheus.New(opt.Config.Prometheus, userAgent)
		if err != nil {
			return nil, err
		}
		promClient = c
	}
	alertClient := opt.Alerts
	if alertClient == nil && opt.Config.Alertmanager.URL != "" {
		c, err := alertmanager.New(opt.Config.Alertmanager, userAgent)
		if err != nil {
			return nil, err
		}
		alertClient = c
	}
	cluster := opt.Cluster
	if cluster == nil {
		c, err := kube.Connect(opt.Config.Kubernetes, userAgent)
		if err != nil {
			return nil, err
		}
		cluster = c
	}
	if cluster.Disabled() {
		logger.Warn("kubernetes is not configured; kubernetes tools will fail until credentials are available")
	} else if cluster.Unrestricted() {
		logger.Warn("kubernetes namespace allowlist is empty; every namespace is readable")
	}
	logger.Info("backends",
		"prometheus", config.HostOf(opt.Config.Prometheus.URL),
		"alertmanager", config.HostOf(opt.Config.Alertmanager.URL),
		"kubernetes", !cluster.Disabled(),
	)

	queries, err := opt.Config.Triage.Compile()
	if err != nil {
		return nil, err
	}
	rt := mcptool.Runtime{
		Logger:   logger,
		Redactor: redact.New(opt.Config.RedactionEnabled()),
		Timeout:  opt.Config.Timeout.Std(),
		MaxBytes: opt.Config.Limits.MaxOutputBytes,
		Now:      opt.Now,
	}
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:        "incident-mcp",
		Title:       "Incident MCP",
		Version:     version,
		Description: "Read-only Prometheus, Alertmanager, and Kubernetes tools for incident triage.",
	}, &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       logger,
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{},
		},
	})
	prometheus.Register(mcpServer, prometheus.Options{
		Querier:       promClient,
		Runtime:       rt,
		MaxSeries:     opt.Config.Limits.MaxPromSeries,
		MaxSamples:    opt.Config.Limits.MaxPromSamples,
		MaxRange:      opt.Config.Limits.MaxRange.Std(),
		MaxQueryBytes: opt.Config.Limits.MaxQueryBytes,
	})
	alertmanager.Register(mcpServer, alertmanager.Options{
		Client:    alertClient,
		Runtime:   rt,
		MaxAlerts: opt.Config.Limits.MaxAlerts,
	})
	kube.Register(mcpServer, kube.Options{
		Client:      cluster,
		Runtime:     rt,
		MaxPods:     opt.Config.Limits.MaxPods,
		MaxEvents:   opt.Config.Limits.MaxEvents,
		MaxLogLines: opt.Config.Limits.MaxLogLines,
	})
	triage.Register(mcpServer, triage.Options{
		Cluster:       cluster,
		Prom:          promClient,
		Alerts:        alertClient,
		Queries:       queries,
		Runtime:       rt,
		LogLines:      opt.Config.Triage.LogLines,
		EventLimit:    opt.Config.Triage.EventLimit,
		MaxLogLines:   opt.Config.Limits.MaxLogLines,
		MaxQueryBytes: opt.Config.Limits.MaxQueryBytes,
	})
	return &Server{mcp: mcpServer, cfg: opt.Config, log: logger, version: version}, nil
}

// Run serves until the client disconnects or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	switch s.cfg.Server.Transport {
	case "stdio":
		err := s.mcp.Run(ctx, &mcp.StdioTransport{})
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case "http":
		return s.runHTTP(ctx)
	default:
		return fmt.Errorf("server.transport %q is not supported", s.cfg.Server.Transport)
	}
}

func (s *Server) runHTTP(ctx context.Context) error {
	httpSrv := &http.Server{
		Addr:              s.cfg.Server.HTTPAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http listening", "addr", s.cfg.Server.HTTPAddr)
		errCh <- httpSrv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
