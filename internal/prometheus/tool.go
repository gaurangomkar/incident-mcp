package prometheus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"github.com/gaurangomkar/incident-mcp/internal/mcptool"
)

const defaultLookback = time.Hour

// Options configures the Prometheus tools.
type Options struct {
	Querier       Querier
	Runtime       mcptool.Runtime
	MaxSeries     int
	MaxSamples    int
	MaxRange      time.Duration
	MaxQueryBytes int
}

// Register adds prometheus_query and prometheus_query_range.
func Register(s *mcp.Server, opt Options) {
	if opt.MaxSeries < 1 {
		opt.MaxSeries = 50
	}
	if opt.MaxSamples < 1 {
		opt.MaxSamples = 200
	}
	if opt.MaxRange <= 0 {
		opt.MaxRange = 24 * time.Hour
	}
	if opt.MaxQueryBytes < 1 {
		opt.MaxQueryBytes = 8192
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "prometheus_query",
		Title:       "Prometheus instant query",
		Description: "Run an instant PromQL query against the configured Prometheus. Use it for a current value such as an error rate, saturation, or up. The result is capped to the configured series limit. Read-only.",
		Annotations: mcptool.Annotations("Prometheus instant query"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in queryInput) (*mcp.CallToolResult, any, error) {
		return opt.Runtime.Call(ctx, "prometheus_query", func(ctx context.Context) (string, error) {
			return instant(ctx, opt, in)
		})
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "prometheus_query_range",
		Title:       "Prometheus range query",
		Description: "Run a PromQL range query. Set lookback (for example 1h) or start and end in RFC3339, and an optional step such as 30s. When step is omitted the server picks one that stays inside the sample cap. Ranges and resolutions above the configured limits are rejected. Read-only.",
		Annotations: mcptool.Annotations("Prometheus range query"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in rangeInput) (*mcp.CallToolResult, any, error) {
		return opt.Runtime.Call(ctx, "prometheus_query_range", func(ctx context.Context) (string, error) {
			return queryRange(ctx, opt, in)
		})
	})
}

type queryInput struct {
	Query string `json:"query" jsonschema:"PromQL expression"`
	Time  string `json:"time,omitempty" jsonschema:"Evaluation time in RFC3339. Defaults to now."`
}

type rangeInput struct {
	Query    string `json:"query" jsonschema:"PromQL expression"`
	Start    string `json:"start,omitempty" jsonschema:"Range start in RFC3339. Alternative to lookback."`
	End      string `json:"end,omitempty" jsonschema:"Range end in RFC3339. Defaults to now."`
	Lookback string `json:"lookback,omitempty" jsonschema:"Duration such as 30m or 1h, used when start is omitted. Defaults to 1h."`
	Step     string `json:"step,omitempty" jsonschema:"Sample resolution such as 30s. Chosen automatically when omitted."`
}

func instant(ctx context.Context, opt Options, in queryInput) (string, error) {
	q, err := checkQuery(in.Query, opt.MaxQueryBytes)
	if err != nil {
		return "", err
	}
	api, err := querier(opt)
	if err != nil {
		return "", err
	}
	at := opt.Runtime.At()
	if in.Time != "" {
		at, err = parseTime(in.Time)
		if err != nil {
			return "", err
		}
	}
	value, warnings, err := api.Query(ctx, q, at)
	if err != nil {
		return "", fmt.Errorf("prometheus query: %w", err)
	}
	body, truncated := Format(value, opt.MaxSeries, opt.MaxSamples)
	return header(q, warnings, truncated) + fmt.Sprintf("time: %s\n", at.UTC().Format(time.RFC3339)) + body, nil
}

func queryRange(ctx context.Context, opt Options, in rangeInput) (string, error) {
	q, err := checkQuery(in.Query, opt.MaxQueryBytes)
	if err != nil {
		return "", err
	}
	api, err := querier(opt)
	if err != nil {
		return "", err
	}
	window, err := resolveRange(in, opt.Runtime.At(), opt.MaxRange, opt.MaxSamples)
	if err != nil {
		return "", err
	}
	value, warnings, err := api.QueryRange(ctx, q, promv1.Range{Start: window.start, End: window.end, Step: window.step})
	if err != nil {
		return "", fmt.Errorf("prometheus range query: %w", err)
	}
	body, truncated := Format(value, opt.MaxSeries, opt.MaxSamples)
	var b strings.Builder
	b.WriteString(header(q, warnings, truncated))
	fmt.Fprintf(&b, "start: %s\nend: %s\nstep: %s\n",
		window.start.UTC().Format(time.RFC3339),
		window.end.UTC().Format(time.RFC3339),
		window.step)
	if window.defaulted {
		b.WriteString("lookback defaulted to 1h\n")
	}
	b.WriteString(body)
	return b.String(), nil
}

type window struct {
	start, end time.Time
	step       time.Duration
	defaulted  bool
}

func resolveRange(in rangeInput, now time.Time, maxRange time.Duration, maxSamples int) (window, error) {
	if in.Start != "" && in.Lookback != "" {
		return window{}, fmt.Errorf("set start or lookback, not both")
	}
	end := now
	var err error
	if in.End != "" {
		end, err = parseTime(in.End)
		if err != nil {
			return window{}, fmt.Errorf("end: %w", err)
		}
	}
	var w window
	w.end = end
	switch {
	case in.Start != "":
		w.start, err = parseTime(in.Start)
		if err != nil {
			return window{}, fmt.Errorf("start: %w", err)
		}
	case in.Lookback != "":
		lookback, perr := time.ParseDuration(in.Lookback)
		if perr != nil || lookback <= 0 {
			return window{}, fmt.Errorf("lookback %q is not a positive duration", in.Lookback)
		}
		w.start = end.Add(-lookback)
	default:
		w.start = end.Add(-defaultLookback)
		w.defaulted = true
	}
	if !w.end.After(w.start) {
		return window{}, fmt.Errorf("end must be after start")
	}
	span := w.end.Sub(w.start)
	if span > maxRange {
		return window{}, fmt.Errorf("range %s exceeds the configured maximum of %s", span, maxRange)
	}
	if in.Step != "" {
		w.step, err = time.ParseDuration(in.Step)
		if err != nil || w.step <= 0 {
			return window{}, fmt.Errorf("step %q is not a positive duration", in.Step)
		}
		points := int(span / w.step)
		if points > maxSamples {
			return window{}, fmt.Errorf("range would return about %d points; maximum is %d, increase step or shorten the range", points, maxSamples)
		}
	} else {
		w.step = chooseStep(span, maxSamples)
	}
	return w, nil
}

func chooseStep(span time.Duration, maxPoints int) time.Duration {
	if maxPoints < 1 {
		maxPoints = 1
	}
	step := span / time.Duration(maxPoints)
	if step < time.Second {
		return time.Second
	}
	step = step.Truncate(time.Second)
	if step < time.Second {
		step = time.Second
	}
	if span/step > time.Duration(maxPoints) {
		step += time.Second
	}
	return step
}

func checkQuery(q string, maxBytes int) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", fmt.Errorf("query is required")
	}
	if maxBytes <= 0 {
		maxBytes = 8192
	}
	if len(q) > maxBytes {
		return "", fmt.Errorf("query is %d bytes; maximum is %d", len(q), maxBytes)
	}
	return q, nil
}

func querier(opt Options) (Querier, error) {
	if opt.Querier == nil {
		return nil, fmt.Errorf("prometheus is not configured (set prometheus.url or PROMETHEUS_URL)")
	}
	return opt.Querier, nil
}

func parseTime(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("time %q must be RFC3339", raw)
	}
	return t, nil
}

func header(query string, warnings []string, truncated bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "query: %s\n", query)
	if truncated {
		b.WriteString("result truncated to the configured series or sample limit\n")
	}
	if len(warnings) > 0 {
		b.WriteString("warnings:\n")
		for _, w := range warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	return b.String()
}
