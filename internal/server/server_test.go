package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gaurangomkar/incident-mcp/internal/config"
	"github.com/gaurangomkar/incident-mcp/internal/kube"
)

func TestRegisteredToolsAreReadOnly(t *testing.T) {
	now := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	cluster, err := kube.NewClient(fake.NewClientset(), []string{"payments"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Prometheus.URL = ""
	cfg.Alertmanager.URL = ""
	srv, err := New(Options{
		Config:     cfg,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:    "test",
		Now:        func() time.Time { return now },
		Prometheus: fakeQuerier{password: "hunter2secret"},
		Cluster:    cluster,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	session, err := srv.mcp.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"prometheus_query":       false,
		"prometheus_query_range": false,
		"alertmanager_alerts":    false,
		"alertmanager_silences":  false,
		"k8s_pods":               false,
		"k8s_events":             false,
		"k8s_deployment":         false,
		"k8s_pod_logs":           false,
		"triage_service":         false,
	}
	if len(listed.Tools) != len(want) {
		t.Fatalf("got %d tools", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if _, ok := want[tool.Name]; !ok {
			t.Fatalf("unexpected tool %s", tool.Name)
		}
		want[tool.Name] = true
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("%s is not marked read-only", tool.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("missing tool %s", name)
		}
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "prometheus_query",
		Arguments: map[string]any{"query": "up"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", text(res))
	}
	if strings.Contains(text(res), "hunter2secret") || !strings.Contains(text(res), "[REDACTED]") {
		t.Fatalf("redaction failed: %s", text(res))
	}

	denied, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "k8s_pods",
		Arguments: map[string]any{"namespace": "kube-system"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !denied.IsError || !strings.Contains(text(denied), "allowlist") {
		t.Fatalf("expected allowlist error, got %s isError=%v", text(denied), denied.IsError)
	}
}

func TestHTTPAuth(t *testing.T) {
	cfg := config.Default()
	cfg.Prometheus.URL = ""
	cfg.Alertmanager.URL = ""
	cfg.Server.Transport = "http"
	cfg.Server.BearerToken = "secret"
	cluster, err := kube.NewClient(fake.NewClientset(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{
		Config:  cfg,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test",
		Cluster: cluster,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Fatalf("health %d %q", res.StatusCode, body)
	}

	res, err = http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "incident-mcp test") {
		t.Fatalf("root %d %q", res.StatusCode, body)
	}

	res, err = http.Post(ts.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status %d", res.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wrong")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token status %d", res.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	respBody, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		t.Fatalf("valid token rejected: %s", respBody)
	}
}

type fakeQuerier struct {
	password string
}

func (f fakeQuerier) Query(context.Context, string, time.Time) (model.Value, promv1.Warnings, error) {
	return model.Vector{{
		Metric:    model.Metric{"password": model.LabelValue(f.password)},
		Value:     1,
		Timestamp: model.TimeFromUnix(1_763_600_000),
	}}, nil, nil
}

func (f fakeQuerier) QueryRange(context.Context, string, promv1.Range) (model.Value, promv1.Warnings, error) {
	return model.Matrix{}, nil, nil
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}
