package prometheus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	"github.com/gaurangomkar/incident-mcp/internal/mcptool"
	"github.com/gaurangomkar/incident-mcp/internal/redact"
)

type fakeQuery struct {
	value    model.Value
	warnings promv1.Warnings
	err      error
	last     string
	rng      promv1.Range
}

func (f *fakeQuery) Query(_ context.Context, query string, _ time.Time) (model.Value, promv1.Warnings, error) {
	f.last = query
	return f.value, f.warnings, f.err
}

func (f *fakeQuery) QueryRange(_ context.Context, query string, rng promv1.Range) (model.Value, promv1.Warnings, error) {
	f.last = query
	f.rng = rng
	return f.value, f.warnings, f.err
}

func TestInstantQueryRedactsAndWarns(t *testing.T) {
	now := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	fake := &fakeQuery{
		warnings: promv1.Warnings{"partial data"},
		value: model.Vector{{
			Metric:    model.Metric{"password": "hunter2secret"},
			Value:     1,
			Timestamp: model.TimeFromUnixNano(now.UnixNano()),
		}},
	}
	res, _, err := call(t, Options{
		Querier:       fake,
		MaxSeries:     10,
		MaxSamples:    10,
		MaxRange:      time.Hour,
		MaxQueryBytes: 1000,
		Runtime: mcptool.Runtime{
			Redactor: redact.New(true),
			Timeout:  time.Second,
			MaxBytes: 4000,
			Now:      func() time.Time { return now },
		},
	}, "prometheus_query", queryInput{Query: "up"})
	if err != nil {
		t.Fatal(err)
	}
	text := mcptool.ResultText(res)
	if strings.Contains(text, "hunter2secret") {
		t.Fatalf("secret leaked: %s", text)
	}
	if !strings.Contains(text, "[REDACTED]") || !strings.Contains(text, "partial data") {
		t.Fatalf("text: %s", text)
	}
	if fake.last != "up" {
		t.Fatalf("query %q", fake.last)
	}
}

func TestRangeDefaultsAndRejectsWideStep(t *testing.T) {
	now := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	fake := &fakeQuery{value: model.Matrix{}}
	opt := Options{
		Querier:       fake,
		MaxSeries:     10,
		MaxSamples:    4,
		MaxRange:      time.Hour,
		MaxQueryBytes: 1000,
		Runtime:       mcptool.Runtime{Timeout: time.Second, Now: func() time.Time { return now }},
	}
	res, _, err := call(t, opt, "prometheus_query_range", rangeInput{Query: "rate(up[1m])"})
	if err != nil {
		t.Fatal(err)
	}
	text := mcptool.ResultText(res)
	if !strings.Contains(text, "lookback defaulted to 1h") {
		t.Fatalf("text: %s", text)
	}
	if fake.rng.End.Sub(fake.rng.Start) != time.Hour {
		t.Fatalf("range %s", fake.rng.End.Sub(fake.rng.Start))
	}
	if _, _, err := call(t, opt, "prometheus_query_range", rangeInput{Query: "up", Lookback: "1h", Step: "1s"}); err == nil {
		t.Fatal("expected step rejection")
	}
	if _, _, err := call(t, opt, "prometheus_query_range", rangeInput{Query: "up", Lookback: "48h"}); err == nil {
		t.Fatal("expected range rejection")
	}
}

func TestNotConfigured(t *testing.T) {
	_, _, err := call(t, Options{Runtime: mcptool.Runtime{Timeout: time.Second}}, "prometheus_query", queryInput{Query: "up"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err %v", err)
	}
}

func call(t *testing.T, opt Options, name string, in any) (*mcp.CallToolResult, any, error) {
	t.Helper()
	ctx := context.Background()
	switch name {
	case "prometheus_query":
		return opt.Runtime.Call(ctx, name, func(ctx context.Context) (string, error) {
			return instant(ctx, opt, in.(queryInput))
		})
	case "prometheus_query_range":
		return opt.Runtime.Call(ctx, name, func(ctx context.Context) (string, error) {
			return queryRange(ctx, opt, in.(rangeInput))
		})
	default:
		t.Fatalf("unknown tool %s", name)
		return nil, nil, nil
	}
}
