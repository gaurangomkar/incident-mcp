package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultValidates(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.RedactionEnabled() {
		t.Fatal("redaction should default on")
	}
	queries, err := cfg.Triage.Compile()
	if err != nil {
		t.Fatal(err)
	}
	got, err := queries[0].Render("payments", "checkout.v2")
	if err != nil {
		t.Fatal(err)
	}
	if want := `up{namespace="payments",pod=~"checkout\.v2-.+"}`; got != want {
		t.Fatalf("render = %s, want %s", got, want)
	}
}

func TestLoadYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "incident.yaml")
	body := []byte(`
server:
  transport: http
  http_addr: 127.0.0.1:9099
log_level: debug
timeout: 30s
redact: false
prometheus:
  url: http://prometheus.example:9090
  timeout: 15s
kubernetes:
  allowed_namespaces: ["payments"]
limits:
  max_output_bytes: 4096
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROMETHEUS_URL", "https://prom.internal")
	t.Setenv("INCIDENT_MCP_ALLOWED_NAMESPACES", "payments, checkout")
	t.Setenv("INCIDENT_MCP_REDACT", "true")
	t.Setenv("INCIDENT_MCP_TIMEOUT", "45s")
	t.Setenv("INCIDENT_MCP_MAX_OUTPUT_BYTES", "8192")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Prometheus.URL != "https://prom.internal" {
		t.Fatalf("url = %s", cfg.Prometheus.URL)
	}
	if cfg.Prometheus.Timeout.Std() != 15*time.Second {
		t.Fatalf("prom timeout = %s", cfg.Prometheus.Timeout)
	}
	if cfg.Timeout.Std() != 45*time.Second {
		t.Fatalf("timeout = %s", cfg.Timeout)
	}
	if !cfg.RedactionEnabled() {
		t.Fatal("env should turn redaction back on")
	}
	if got := stringsJoin(cfg.Kubernetes.AllowedNamespaces); got != "payments,checkout" {
		t.Fatalf("namespaces = %s", got)
	}
	if cfg.Limits.MaxOutputBytes != 8192 {
		t.Fatalf("max bytes = %d", cfg.Limits.MaxOutputBytes)
	}
	if cfg.Limits.MaxPromSeries != 50 {
		t.Fatalf("unset limit changed: %d", cfg.Limits.MaxPromSeries)
	}
	if cfg.Server.Transport != "http" || cfg.LogLevel != "debug" {
		t.Fatalf("server = %+v level %s", cfg.Server, cfg.LogLevel)
	}
}

func TestRejectsPublicHTTPWithoutToken(t *testing.T) {
	cfg := Default()
	cfg.Server.Transport = "http"
	cfg.Server.HTTPAddr = "0.0.0.0:8080"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error")
	}
	cfg.Server.BearerToken = "local-token"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsCredentialInURL(t *testing.T) {
	cfg := Default()
	cfg.Prometheus.URL = "https://user:pass@prometheus.example"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("not_a_field: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestSearchWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("INCIDENT_MCP_CONFIG", "")
	if err := os.WriteFile("incident-mcp.yaml", []byte("log_level: warn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("level = %s", cfg.LogLevel)
	}
	if cfg.Path != "incident-mcp.yaml" {
		t.Fatalf("path = %s", cfg.Path)
	}
}

func TestMissingExplicitFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected error")
	}
}

func TestBadEnv(t *testing.T) {
	t.Setenv("INCIDENT_MCP_CONFIG", "")
	t.Setenv("INCIDENT_MCP_TIMEOUT", "nope")
	if _, err := Load(""); err == nil {
		t.Fatal("expected error")
	}
}

func TestExampleConfig(t *testing.T) {
	cfg, err := Load("../../examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Prometheus.URL == "" || cfg.Alertmanager.URL == "" {
		t.Fatalf("example should point at local upstreams: %+v %+v", cfg.Prometheus, cfg.Alertmanager)
	}
}

func stringsJoin(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
