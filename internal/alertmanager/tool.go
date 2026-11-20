package alertmanager

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaurangomkar/incident-mcp/internal/mcptool"
)

// Options configures the Alertmanager tools.
type Options struct {
	Client    Client
	Runtime   mcptool.Runtime
	MaxAlerts int
}

// Register adds alertmanager_alerts and alertmanager_silences.
func Register(s *mcp.Server, opt Options) {
	if opt.MaxAlerts < 1 {
		opt.MaxAlerts = 100
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "alertmanager_alerts",
		Title:       "Alertmanager alerts",
		Description: "List alerts currently firing in Alertmanager. Silenced and inhibited alerts are omitted unless requested. Matchers are exact label matches in key=value form. Read-only. This tool cannot silence or resolve alerts.",
		Annotations: mcptool.Annotations("Alertmanager alerts"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in alertsInput) (*mcp.CallToolResult, any, error) {
		return opt.Runtime.Call(ctx, "alertmanager_alerts", func(ctx context.Context) (string, error) {
			return alerts(ctx, opt, in)
		})
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "alertmanager_silences",
		Title:       "Alertmanager silences",
		Description: "List Alertmanager silences. State is active, pending, expired, or all. The default is active. Read-only. This tool cannot create or expire a silence.",
		Annotations: mcptool.Annotations("Alertmanager silences"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in silencesInput) (*mcp.CallToolResult, any, error) {
		return opt.Runtime.Call(ctx, "alertmanager_silences", func(ctx context.Context) (string, error) {
			return silences(ctx, opt, in)
		})
	})
}

type alertsInput struct {
	Matchers         []string `json:"matchers,omitempty" jsonschema:"Exact label matches, each key=value. Example: severity=critical."`
	Receiver         string   `json:"receiver,omitempty" jsonschema:"Only alerts routed to this receiver."`
	IncludeSilenced  bool     `json:"include_silenced,omitempty" jsonschema:"Include silenced alerts. Default false."`
	IncludeInhibited bool     `json:"include_inhibited,omitempty" jsonschema:"Include inhibited alerts. Default false."`
	Limit            int      `json:"limit,omitempty" jsonschema:"Maximum alerts to return. Capped by the server limit."`
}

type silencesInput struct {
	State string `json:"state,omitempty" jsonschema:"active, pending, expired, or all. Default active."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum silences to return. Capped by the server limit."`
}

func alerts(ctx context.Context, opt Options, in alertsInput) (string, error) {
	client, err := api(opt)
	if err != nil {
		return "", err
	}
	matchers, err := parseMatchers(in.Matchers)
	if err != nil {
		return "", err
	}
	all, err := client.Alerts(ctx)
	if err != nil {
		return "", err
	}
	kept := make([]Alert, 0, len(all))
	for _, alert := range all {
		if !in.IncludeSilenced && len(alert.SilencedBy) > 0 {
			continue
		}
		if !in.IncludeInhibited && len(alert.InhibitedBy) > 0 {
			continue
		}
		if in.Receiver != "" && !slices.Contains(alert.Receivers, in.Receiver) {
			continue
		}
		if !labelsMatch(alert.Labels, matchers) {
			continue
		}
		kept = append(kept, alert)
	}
	slices.SortStableFunc(kept, func(a, b Alert) int {
		return b.StartsAt.Compare(a.StartsAt)
	})
	limit, capped := clamp(in.Limit, opt.MaxAlerts, opt.MaxAlerts)
	truncated := capped || len(kept) > limit
	if len(kept) > limit {
		kept = kept[:limit]
	}
	return formatAlerts(kept, opt.Runtime.At(), truncated), nil
}

func silences(ctx context.Context, opt Options, in silencesInput) (string, error) {
	client, err := api(opt)
	if err != nil {
		return "", err
	}
	state := strings.ToLower(strings.TrimSpace(in.State))
	if state == "" {
		state = "active"
	}
	switch state {
	case "active", "pending", "expired", "all":
	default:
		return "", fmt.Errorf("state %q must be active, pending, expired, or all", in.State)
	}
	all, err := client.Silences(ctx)
	if err != nil {
		return "", err
	}
	kept := make([]Silence, 0, len(all))
	for _, silence := range all {
		if state != "all" && silence.State != state {
			continue
		}
		kept = append(kept, silence)
	}
	slices.SortStableFunc(kept, func(a, b Silence) int {
		return a.EndsAt.Compare(b.EndsAt)
	})
	limit, capped := clamp(in.Limit, opt.MaxAlerts, opt.MaxAlerts)
	truncated := capped || len(kept) > limit
	if len(kept) > limit {
		kept = kept[:limit]
	}
	return formatSilences(kept, state, truncated), nil
}

func api(opt Options) (Client, error) {
	if opt.Client == nil {
		return nil, fmt.Errorf("alertmanager is not configured (set alertmanager.url or ALERTMANAGER_URL)")
	}
	return opt.Client, nil
}

func parseMatchers(raw []string) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	for _, m := range raw {
		key, value, ok := strings.Cut(strings.TrimSpace(m), "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, "!~ ") {
			return nil, fmt.Errorf("matcher %q must be key=value", m)
		}
		out[key] = value
	}
	return out, nil
}

func labelsMatch(labels, want map[string]string) bool {
	for k, v := range want {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func clamp(requested, fallback, max int) (int, bool) {
	if requested <= 0 {
		requested = fallback
	}
	if requested > max {
		return max, true
	}
	return requested, false
}

func formatAlerts(alerts []Alert, now time.Time, truncated bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d firing alert", len(alerts))
	if len(alerts) != 1 {
		b.WriteString("s")
	}
	if truncated {
		b.WriteString(" (list truncated)")
	}
	b.WriteString("\n")
	if len(alerts) == 0 {
		return b.String()
	}
	b.WriteString("\n")
	for _, alert := range alerts {
		name := alert.Labels["alertname"]
		if name == "" {
			name = alert.Fingerprint
		}
		severity := alert.Labels["severity"]
		if severity == "" {
			severity = "unknown"
		}
		fmt.Fprintf(&b, "%s  %s  firing for %s", severity, name, since(now, alert.StartsAt))
		if alert.Fingerprint != "" {
			fmt.Fprintf(&b, "  fp=%s", alert.Fingerprint)
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "  labels: %s\n", formatLabels(alert.Labels))
		if summary := alert.Annotations["summary"]; summary != "" {
			fmt.Fprintf(&b, "  summary: %s\n", summary)
		} else if desc := alert.Annotations["description"]; desc != "" {
			fmt.Fprintf(&b, "  description: %s\n", desc)
		}
		if len(alert.Receivers) > 0 {
			fmt.Fprintf(&b, "  receivers: %s\n", strings.Join(alert.Receivers, ", "))
		}
		if len(alert.SilencedBy) > 0 {
			fmt.Fprintf(&b, "  silenced by: %s\n", strings.Join(alert.SilencedBy, ", "))
		}
		if len(alert.InhibitedBy) > 0 {
			fmt.Fprintf(&b, "  inhibited by: %s\n", strings.Join(alert.InhibitedBy, ", "))
		}
	}
	return b.String()
}

func formatSilences(silences []Silence, state string, truncated bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s silence", len(silences), state)
	if len(silences) != 1 {
		b.WriteString("s")
	}
	if truncated {
		b.WriteString(" (list truncated)")
	}
	b.WriteString("\n")
	if len(silences) == 0 {
		return b.String()
	}
	b.WriteString("\n")
	for _, silence := range silences {
		fmt.Fprintf(&b, "id=%s  %s  until %s  by %s\n", silence.ID, silence.State, silence.EndsAt.UTC().Format(time.RFC3339), silence.CreatedBy)
		parts := make([]string, 0, len(silence.Matchers))
		for _, m := range silence.Matchers {
			parts = append(parts, m.String())
		}
		fmt.Fprintf(&b, "  matchers: %s\n", strings.Join(parts, " "))
		if silence.Comment != "" {
			fmt.Fprintf(&b, "  comment: %s\n", silence.Comment)
		}
	}
	return b.String()
}

func formatLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.Join(parts, " ")
}

func since(now, then time.Time) string {
	if then.IsZero() {
		return "unknown"
	}
	d := now.Sub(then).Round(time.Second)
	if d < 0 {
		d = 0
	}
	return d.String()
}
