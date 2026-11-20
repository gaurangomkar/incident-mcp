// Package config loads incident-mcp settings from a YAML file and the environment.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	minOutputBytes = 256
	maxOutputBytes = 2 << 20
	maxRangeLimit  = 7 * 24 * time.Hour
	minTimeout     = time.Second
	maxTimeout     = 2 * time.Minute
)

// Config is the fully resolved server configuration.
type Config struct {
	Path         string     `yaml:"-"`
	Server       Server     `yaml:"server"`
	LogLevel     string     `yaml:"log_level"`
	Timeout      Duration   `yaml:"timeout"`
	Limits       Limits     `yaml:"limits"`
	Redact       *bool      `yaml:"redact"`
	Prometheus   Endpoint   `yaml:"prometheus"`
	Alertmanager Endpoint   `yaml:"alertmanager"`
	Kubernetes   Kubernetes `yaml:"kubernetes"`
	Triage       Triage     `yaml:"triage"`
}

// Server selects how clients connect.
type Server struct {
	Transport       string `yaml:"transport"`
	HTTPAddr        string `yaml:"http_addr"`
	BearerToken     string `yaml:"bearer_token"`
	BearerTokenFile string `yaml:"bearer_token_file"`
}

// Endpoint is a Prometheus or Alertmanager base URL plus optional credentials.
type Endpoint struct {
	URL                string   `yaml:"url"`
	Timeout            Duration `yaml:"timeout"`
	BearerToken        string   `yaml:"bearer_token"`
	BearerTokenFile    string   `yaml:"bearer_token_file"`
	Username           string   `yaml:"username"`
	Password           string   `yaml:"password"`
	InsecureSkipVerify bool     `yaml:"insecure_skip_verify"`
}

// Kubernetes selects the cluster and the namespaces this process may read.
type Kubernetes struct {
	Kubeconfig        string   `yaml:"kubeconfig"`
	Context           string   `yaml:"context"`
	Timeout           Duration `yaml:"timeout"`
	AllowedNamespaces []string `yaml:"allowed_namespaces"`
}

// Limits cap how much data a tool call can return or ask an upstream for.
type Limits struct {
	MaxOutputBytes int      `yaml:"max_output_bytes"`
	MaxPromSeries  int      `yaml:"max_prom_series"`
	MaxPromSamples int      `yaml:"max_prom_samples"`
	MaxRange       Duration `yaml:"max_range"`
	MaxLogLines    int      `yaml:"max_log_lines"`
	MaxEvents      int      `yaml:"max_events"`
	MaxPods        int      `yaml:"max_pods"`
	MaxAlerts      int      `yaml:"max_alerts"`
	MaxQueryBytes  int      `yaml:"max_query_bytes"`
}

// Triage tunes the triage_service tool.
type Triage struct {
	LogLines   int         `yaml:"log_lines"`
	EventLimit int         `yaml:"event_limit"`
	Queries    []PromQuery `yaml:"queries"`
}

// PromQuery is a named PromQL template rendered with Namespace and Service.
type PromQuery struct {
	Name string `yaml:"name"`
	Expr string `yaml:"expr"`
}

// CompiledQuery is a PromQuery whose template has been parsed.
type CompiledQuery struct {
	Name string
	Expr string
	tmpl *template.Template
}

// Render executes the template for one service.
func (q CompiledQuery) Render(namespace, service string) (string, error) {
	var buf bytes.Buffer
	data := struct {
		Namespace string
		Service   string
	}{Namespace: namespace, Service: service}
	if err := q.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// RedactionEnabled reports whether tool output should be scrubbed.
// A nil setting means yes.
func (c Config) RedactionEnabled() bool {
	return c.Redact == nil || *c.Redact
}

// Default returns a configuration that is safe to start with no file.
func Default() Config {
	on := true
	timeout := Duration(20 * time.Second)
	return Config{
		Server: Server{
			Transport: "stdio",
			HTTPAddr:  "127.0.0.1:8080",
		},
		LogLevel: "info",
		Timeout:  timeout,
		Redact:   &on,
		Limits: Limits{
			MaxOutputBytes: 32 * 1024,
			MaxPromSeries:  50,
			MaxPromSamples: 200,
			MaxRange:       Duration(24 * time.Hour),
			MaxLogLines:    200,
			MaxEvents:      50,
			MaxPods:        200,
			MaxAlerts:      100,
			MaxQueryBytes:  8192,
		},
		Prometheus:   Endpoint{Timeout: timeout},
		Alertmanager: Endpoint{Timeout: timeout},
		Kubernetes:   Kubernetes{Timeout: timeout},
		Triage: Triage{
			LogLines:   80,
			EventLimit: 15,
			Queries: []PromQuery{
				{
					Name: "up",
					Expr: `up{namespace="{{.Namespace}}",pod=~"{{.Service | re}}-.+"}`,
				},
				{
					Name: "restarts",
					Expr: `sum by (pod) (increase(kube_pod_container_status_restarts_total{namespace="{{.Namespace}}",pod=~"{{.Service | re}}-.+"}[30m]))`,
				},
			},
		},
	}
}

// Load reads a YAML file and applies environment overrides.
// An empty path searches INCIDENT_MCP_CONFIG, then ./incident-mcp.yaml,
// ./incident-mcp.yml, then the user config directory. A missing optional
// file is not an error.
func Load(path string) (Config, error) {
	cfg := Default()
	file, err := resolvePath(path)
	if err != nil {
		return Config{}, err
	}
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		if err := unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", file, err)
		}
		cfg.Path = file
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func unmarshal(raw []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	return dec.Decode(cfg)
}

// Validate checks the merged configuration.
func (c Config) Validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level %q must be debug, info, warn, or error", c.LogLevel)
	}
	switch c.Server.Transport {
	case "stdio", "http":
	default:
		return fmt.Errorf("server.transport %q must be stdio or http", c.Server.Transport)
	}
	if err := validateTimeout("timeout", c.Timeout.Std()); err != nil {
		return err
	}
	if err := validateAddr(c.Server.HTTPAddr); err != nil {
		return err
	}
	if c.Server.Transport == "http" && !loopback(c.Server.HTTPAddr) && c.Server.BearerToken == "" && c.Server.BearerTokenFile == "" {
		return errors.New("server.http_addr is not loopback; set server.bearer_token or server.bearer_token_file")
	}
	if err := validateLimits(c.Limits); err != nil {
		return err
	}
	if err := validateEndpoint("prometheus", c.Prometheus); err != nil {
		return err
	}
	if err := validateEndpoint("alertmanager", c.Alertmanager); err != nil {
		return err
	}
	if err := validateTimeout("kubernetes.timeout", c.Kubernetes.Timeout.Std()); err != nil {
		return err
	}
	if c.Triage.LogLines < 1 || c.Triage.LogLines > c.Limits.MaxLogLines {
		return fmt.Errorf("triage.log_lines must be between 1 and limits.max_log_lines (%d)", c.Limits.MaxLogLines)
	}
	if c.Triage.EventLimit < 1 || c.Triage.EventLimit > c.Limits.MaxEvents {
		return fmt.Errorf("triage.event_limit must be between 1 and limits.max_events (%d)", c.Limits.MaxEvents)
	}
	if _, err := c.Triage.Compile(); err != nil {
		return err
	}
	return nil
}

// Compile parses triage query templates.
func (t Triage) Compile() ([]CompiledQuery, error) {
	out := make([]CompiledQuery, 0, len(t.Queries))
	seen := map[string]struct{}{}
	for i, q := range t.Queries {
		name := strings.TrimSpace(q.Name)
		expr := strings.TrimSpace(q.Expr)
		if name == "" || expr == "" {
			return nil, fmt.Errorf("triage.queries[%d] needs name and expr", i)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("triage.queries[%d] duplicates name %q", i, name)
		}
		seen[name] = struct{}{}
		tmpl, err := template.New(name).Option("missingkey=error").Funcs(template.FuncMap{
			"re": regexp.QuoteMeta,
		}).Parse(expr)
		if err != nil {
			return nil, fmt.Errorf("triage.queries[%d] %s: %w", i, name, err)
		}
		compiled := CompiledQuery{Name: name, Expr: expr, tmpl: tmpl}
		if _, err := compiled.Render("ns", "svc"); err != nil {
			return nil, fmt.Errorf("triage.queries[%d] %s: %w", i, name, err)
		}
		out = append(out, compiled)
	}
	return out, nil
}

func validateLimits(l Limits) error {
	checks := []struct {
		name string
		n    int
		min  int
		max  int
	}{
		{"limits.max_output_bytes", l.MaxOutputBytes, minOutputBytes, maxOutputBytes},
		{"limits.max_prom_series", l.MaxPromSeries, 1, 2000},
		{"limits.max_prom_samples", l.MaxPromSamples, 1, 10000},
		{"limits.max_log_lines", l.MaxLogLines, 1, 5000},
		{"limits.max_events", l.MaxEvents, 1, 1000},
		{"limits.max_pods", l.MaxPods, 1, 2000},
		{"limits.max_alerts", l.MaxAlerts, 1, 2000},
		{"limits.max_query_bytes", l.MaxQueryBytes, 64, 1 << 20},
	}
	for _, c := range checks {
		if c.n < c.min || c.n > c.max {
			return fmt.Errorf("%s must be between %d and %d", c.name, c.min, c.max)
		}
	}
	if l.MaxRange.Std() < time.Minute || l.MaxRange.Std() > maxRangeLimit {
		return fmt.Errorf("limits.max_range must be between 1m and %s", maxRangeLimit)
	}
	return nil
}

func validateTimeout(name string, d time.Duration) error {
	if d < minTimeout || d > maxTimeout {
		return fmt.Errorf("%s must be between %s and %s", name, minTimeout, maxTimeout)
	}
	return nil
}

func validateEndpoint(name string, e Endpoint) error {
	if e.URL == "" {
		return nil
	}
	u, err := url.Parse(e.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s.url must be an absolute http or https URL", name)
	}
	if u.User != nil {
		return fmt.Errorf("%s.url must not contain credentials; use username, password, or a bearer token", name)
	}
	if e.Timeout.Std() != 0 {
		if err := validateTimeout(name+".timeout", e.Timeout.Std()); err != nil {
			return err
		}
	}
	return nil
}

func validateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("server.http_addr: %w", err)
	}
	if host == "" || port == "" {
		return errors.New("server.http_addr needs a host and port")
	}
	return nil
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func resolvePath(explicit string) (string, error) {
	if explicit != "" {
		if err := existingFile(explicit); err != nil {
			return "", fmt.Errorf("config file: %w", err)
		}
		return explicit, nil
	}
	if env := os.Getenv("INCIDENT_MCP_CONFIG"); env != "" {
		if err := existingFile(env); err != nil {
			return "", fmt.Errorf("INCIDENT_MCP_CONFIG: %w", err)
		}
		return env, nil
	}
	for _, candidate := range searchPaths() {
		if err := existingFile(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", nil
}

func existingFile(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	return nil
}

func searchPaths() []string {
	paths := []string{"incident-mcp.yaml", "incident-mcp.yml"}
	if dir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "incident-mcp", "config.yaml"))
	}
	return paths
}

// HostOf returns the host of a URL, or an empty string when the URL is empty.
func HostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}
