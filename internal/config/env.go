package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func applyEnv(cfg *Config) error {
	set := func(dst *string, key string) {
		if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
			*dst = v
		}
	}
	set(&cfg.Server.Transport, "INCIDENT_MCP_TRANSPORT")
	set(&cfg.Server.HTTPAddr, "INCIDENT_MCP_HTTP_ADDR")
	set(&cfg.Server.BearerToken, "INCIDENT_MCP_BEARER_TOKEN")
	set(&cfg.Server.BearerTokenFile, "INCIDENT_MCP_BEARER_TOKEN_FILE")
	set(&cfg.LogLevel, "INCIDENT_MCP_LOG_LEVEL")
	set(&cfg.Prometheus.URL, "PROMETHEUS_URL")
	set(&cfg.Prometheus.BearerToken, "PROMETHEUS_BEARER_TOKEN")
	set(&cfg.Prometheus.BearerTokenFile, "PROMETHEUS_BEARER_TOKEN_FILE")
	set(&cfg.Prometheus.Username, "PROMETHEUS_USERNAME")
	set(&cfg.Prometheus.Password, "PROMETHEUS_PASSWORD")
	set(&cfg.Alertmanager.URL, "ALERTMANAGER_URL")
	set(&cfg.Alertmanager.BearerToken, "ALERTMANAGER_BEARER_TOKEN")
	set(&cfg.Alertmanager.BearerTokenFile, "ALERTMANAGER_BEARER_TOKEN_FILE")
	set(&cfg.Alertmanager.Username, "ALERTMANAGER_USERNAME")
	set(&cfg.Alertmanager.Password, "ALERTMANAGER_PASSWORD")
	set(&cfg.Kubernetes.Kubeconfig, "INCIDENT_MCP_KUBECONFIG")
	set(&cfg.Kubernetes.Context, "INCIDENT_MCP_KUBE_CONTEXT")

	if v, ok := os.LookupEnv("INCIDENT_MCP_ALLOWED_NAMESPACES"); ok && strings.TrimSpace(v) != "" {
		cfg.Kubernetes.AllowedNamespaces = splitCSV(v)
	}
	if err := envDuration(&cfg.Timeout, "INCIDENT_MCP_TIMEOUT"); err != nil {
		return err
	}
	if err := envBool(&cfg.Redact, "INCIDENT_MCP_REDACT"); err != nil {
		return err
	}
	if err := envInt(&cfg.Limits.MaxOutputBytes, "INCIDENT_MCP_MAX_OUTPUT_BYTES"); err != nil {
		return err
	}
	return nil
}

func envDuration(dst *Duration, key string) error {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return nil
	}
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	if parsed <= 0 {
		return fmt.Errorf("%s must be positive", key)
	}
	*dst = Duration(parsed)
	return nil
}

func envBool(dst **bool, key string) error {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*dst = &parsed
	return nil
}

func envInt(dst *int, key string) error {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return nil
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*dst = parsed
	return nil
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
