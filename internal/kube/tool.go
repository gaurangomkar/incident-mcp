package kube

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaurangomkar/incident-mcp/internal/mcptool"
)

// Options configures the Kubernetes tools.
type Options struct {
	Client      *Client
	Runtime     mcptool.Runtime
	MaxPods     int
	MaxEvents   int
	MaxLogLines int
}

// Register adds the read-only Kubernetes tools.
func Register(s *mcp.Server, opt Options) {
	if opt.MaxPods < 1 {
		opt.MaxPods = 200
	}
	if opt.MaxEvents < 1 {
		opt.MaxEvents = 50
	}
	if opt.MaxLogLines < 1 {
		opt.MaxLogLines = 200
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "k8s_pods",
		Title:       "Kubernetes pods",
		Description: "List pods in one namespace with status, ready count, restarts, and waiting reasons. Not-ready pods are listed first. The namespace must be on the allowlist when one is configured. Read-only.",
		Annotations: mcptool.Annotations("Kubernetes pods"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in podsInput) (*mcp.CallToolResult, any, error) {
		return opt.Runtime.Call(ctx, "k8s_pods", func(ctx context.Context) (string, error) {
			return pods(ctx, opt, in)
		})
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "k8s_events",
		Title:       "Kubernetes events",
		Description: "List recent events in one namespace, newest first. Filter with involved_object as a name or kind/name, for example Pod/checkout-abc. Read-only.",
		Annotations: mcptool.Annotations("Kubernetes events"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in eventsInput) (*mcp.CallToolResult, any, error) {
		return opt.Runtime.Call(ctx, "k8s_events", func(ctx context.Context) (string, error) {
			return events(ctx, opt, in)
		})
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "k8s_deployment",
		Title:       "Kubernetes deployment",
		Description: "Summarize one deployment: replicas, conditions, images, probes, resources, and environment sources. Literal values of secret-like environment variables are not printed. Read-only.",
		Annotations: mcptool.Annotations("Kubernetes deployment"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deploymentInput) (*mcp.CallToolResult, any, error) {
		return opt.Runtime.Call(ctx, "k8s_deployment", func(ctx context.Context) (string, error) {
			return deployment(ctx, opt, in)
		})
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "k8s_pod_logs",
		Title:       "Kubernetes pod logs",
		Description: "Read the tail of a pod log. Set container when the pod has more than one. previous=true reads the previous terminated container, which is the one you want after a crash. Output is line-capped and truncated. Read-only.",
		Annotations: mcptool.Annotations("Kubernetes pod logs"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in logsInput) (*mcp.CallToolResult, any, error) {
		return opt.Runtime.Call(ctx, "k8s_pod_logs", func(ctx context.Context) (string, error) {
			return podLogs(ctx, opt, in)
		})
	})
}

type podsInput struct {
	Namespace     string `json:"namespace" jsonschema:"Namespace to list. Must be on the allowlist when one is configured."`
	LabelSelector string `json:"label_selector,omitempty" jsonschema:"Kubernetes label selector, for example app=checkout."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Maximum pods to return. Capped by the server limit."`
}

type eventsInput struct {
	Namespace      string `json:"namespace" jsonschema:"Namespace to read. Must be on the allowlist when one is configured."`
	InvolvedObject string `json:"involved_object,omitempty" jsonschema:"Object name or kind/name, for example Pod/checkout-abc."`
	WarningsOnly   bool   `json:"warnings_only,omitempty" jsonschema:"When true, return only Warning events."`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum events to return. Capped by the server limit."`
}

type deploymentInput struct {
	Namespace string `json:"namespace" jsonschema:"Namespace of the deployment."`
	Name      string `json:"name" jsonschema:"Deployment name."`
}

type logsInput struct {
	Namespace string `json:"namespace" jsonschema:"Namespace of the pod."`
	Pod       string `json:"pod" jsonschema:"Pod name."`
	Container string `json:"container,omitempty" jsonschema:"Container name. Required when the pod has more than one container."`
	TailLines int    `json:"tail_lines,omitempty" jsonschema:"Lines to return from the end of the log. Capped by the server limit. Defaults to 100."`
	Previous  bool   `json:"previous,omitempty" jsonschema:"Read the previous terminated container. Use this after a crash."`
}

func pods(ctx context.Context, opt Options, in podsInput) (string, error) {
	c, err := client(opt)
	if err != nil {
		return "", err
	}
	items, incomplete, err := c.Pods(ctx, in.Namespace, in.LabelSelector)
	if err != nil {
		return "", err
	}
	limit, capped := clamp(in.Limit, opt.MaxPods, opt.MaxPods)
	text, cut := FormatPods(in.Namespace, items, opt.Runtime.At(), limit)
	if in.LabelSelector != "" {
		text = "selector: " + in.LabelSelector + "\n" + text
	}
	if incomplete || capped || cut {
		text = "pod list truncated to the configured limit\n" + text
	}
	return text, nil
}

func events(ctx context.Context, opt Options, in eventsInput) (string, error) {
	c, err := client(opt)
	if err != nil {
		return "", err
	}
	kind, name, err := parseInvolved(in.InvolvedObject)
	if err != nil {
		return "", err
	}
	items, incomplete, err := c.Events(ctx, in.Namespace)
	if err != nil {
		return "", err
	}
	limit, capped := clamp(in.Limit, opt.MaxEvents, opt.MaxEvents)
	text, cut := FormatEvents(in.Namespace, items, opt.Runtime.At(), EventView{
		Limit:        limit,
		WarningsOnly: in.WarningsOnly,
		Kind:         kind,
		Name:         name,
	})
	if incomplete || capped || cut {
		text = "event list truncated; the namespace scan is capped and then the newest matches are kept\n" + text
	}
	return text, nil
}

func deployment(ctx context.Context, opt Options, in deploymentInput) (string, error) {
	c, err := client(opt)
	if err != nil {
		return "", err
	}
	got, err := c.Deployment(ctx, in.Namespace, in.Name)
	if err != nil {
		return "", err
	}
	return FormatDeployment(got, opt.Runtime.At()), nil
}

func podLogs(ctx context.Context, opt Options, in logsInput) (string, error) {
	c, err := client(opt)
	if err != nil {
		return "", err
	}
	tail, capped := clamp(in.TailLines, 100, opt.MaxLogLines)
	text, truncated, err := c.Logs(ctx, in.Namespace, in.Pod, in.Container, int64(tail), in.Previous, opt.Runtime.MaxBytes)
	if err != nil {
		return "", err
	}
	if capped && truncated {
		text = "tail capped at the server limit\n" + text
	}
	return text, nil
}

func client(opt Options) (*Client, error) {
	if opt.Client == nil || opt.Client.Disabled() {
		if opt.Client != nil && opt.Client.cause != nil {
			return nil, opt.Client.cause
		}
		return nil, ErrNotConfigured
	}
	return opt.Client, nil
}

func parseInvolved(raw string) (kind, name string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	if k, n, ok := strings.Cut(raw, "/"); ok {
		if k == "" || n == "" || strings.Contains(n, "/") {
			return "", "", fmt.Errorf("involved_object %q must be a name or kind/name", raw)
		}
		return k, n, nil
	}
	return "", raw, nil
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
