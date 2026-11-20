// Package triage builds a single read-only summary of one deployment.
package triage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gaurangomkar/incident-mcp/internal/alertmanager"
	"github.com/gaurangomkar/incident-mcp/internal/config"
	"github.com/gaurangomkar/incident-mcp/internal/kube"
	"github.com/gaurangomkar/incident-mcp/internal/mcptool"
	"github.com/gaurangomkar/incident-mcp/internal/prometheus"
)

// Options configures triage_service.
type Options struct {
	Cluster       *kube.Client
	Prom          prometheus.Querier
	Alerts        alertmanager.Client
	Queries       []config.CompiledQuery
	Runtime       mcptool.Runtime
	LogLines      int
	EventLimit    int
	MaxLogLines   int
	MaxQueryBytes int
}

// Register adds triage_service.
func Register(s *mcp.Server, opt Options) {
	if opt.LogLines < 1 {
		opt.LogLines = 80
	}
	if opt.EventLimit < 1 {
		opt.EventLimit = 15
	}
	if opt.MaxLogLines < 1 {
		opt.MaxLogLines = opt.LogLines
	}
	if opt.MaxQueryBytes < 1 {
		opt.MaxQueryBytes = 8192
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "triage_service",
		Title:       "Triage a service",
		Description: "Summarize one Kubernetes deployment for an incident: availability, pods, recent warning events, matching Alertmanager alerts, a few Prometheus queries, and recent logs from the least healthy pod. Start here when a service is paging. Read-only. Prometheus and Alertmanager sections say so when those backends are not configured. A failure in one section does not drop the others.",
		Annotations: mcptool.Annotations("Triage a service"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in input) (*mcp.CallToolResult, any, error) {
		return opt.Runtime.Call(ctx, "triage_service", func(ctx context.Context) (string, error) {
			return summarize(ctx, opt, in)
		})
	})
}

type input struct {
	Namespace string `json:"namespace" jsonschema:"Namespace of the deployment."`
	Service   string `json:"service" jsonschema:"Deployment name."`
	LogLines  int    `json:"log_lines,omitempty" jsonschema:"Log lines to pull from the least healthy pod. Capped by the server."`
}

func summarize(ctx context.Context, opt Options, in input) (string, error) {
	if opt.Cluster == nil || opt.Cluster.Disabled() {
		return "", kube.ErrNotConfigured
	}
	dep, err := opt.Cluster.Deployment(ctx, in.Namespace, in.Service)
	if err != nil {
		return "", err
	}
	selector, selErr := selectorOf(dep)
	var (
		pods           []corev1.Pod
		podsIncomplete bool
		podsErr        error
	)
	if selErr != nil {
		podsErr = selErr
	} else {
		pods, podsIncomplete, podsErr = opt.Cluster.Pods(ctx, in.Namespace, selector)
	}

	now := opt.Runtime.At()
	var b strings.Builder
	fmt.Fprintf(&b, "%s/%s\n%s\n\n", in.Namespace, in.Service, verdict(dep, pods, podsErr))
	b.WriteString(kube.FormatDeployment(dep, now))
	b.WriteString("\n")

	if podsErr != nil {
		fmt.Fprintf(&b, "Pods: %s\n", podsErr)
	} else {
		if podsIncomplete {
			b.WriteString("pod list truncated\n")
		}
		text, _ := kube.FormatPods(in.Namespace, pods, now, 0)
		b.WriteString(text)
		b.WriteString("\n")
	}

	writeEvents(&b, ctx, opt, in.Namespace, dep.Name, podNames(pods), now)
	writeAlerts(&b, ctx, opt, in.Namespace, in.Service, podNames(pods), now)
	writeProm(&b, ctx, opt, in.Namespace, in.Service)
	writeLogs(&b, ctx, opt, in, pods)
	return b.String(), nil
}

func selectorOf(d *appsv1.Deployment) (string, error) {
	if d.Spec.Selector == nil {
		return "", errors.New("deployment has no selector")
	}
	sel, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return "", fmt.Errorf("deployment selector: %w", err)
	}
	if sel.Empty() {
		return "", errors.New("deployment selector is empty")
	}
	return sel.String(), nil
}

func verdict(dep *appsv1.Deployment, pods []corev1.Pod, podsErr error) string {
	if podsErr != nil {
		return "unknown: deployment was read but its pods could not be listed"
	}
	available := false
	for _, c := range dep.Status.Conditions {
		if c.Type == appsv1.DeploymentAvailable && c.Status == corev1.ConditionTrue {
			available = true
		}
	}
	unready := 0
	var restarts int32
	for i := range pods {
		if !ready(pods[i]) {
			unready++
		}
		restarts += restartCount(pods[i])
	}
	switch {
	case !available:
		return "unhealthy: deployment is not available"
	case unready > 0:
		return fmt.Sprintf("degraded: %d pod(s) not ready", unready)
	case restarts > 0:
		return fmt.Sprintf("watch: deployment is available but pods have %d restart(s)", restarts)
	default:
		return "healthy: deployment is available and pods are ready"
	}
}

func writeEvents(b *strings.Builder, ctx context.Context, opt Options, namespace, deploy string, pods map[string]struct{}, now time.Time) {
	events, incomplete, err := opt.Cluster.Events(ctx, namespace)
	if err != nil {
		fmt.Fprintf(b, "Events: %s\n\n", err)
		return
	}
	matched := make([]corev1.Event, 0, len(events))
	for i := range events {
		e := events[i]
		obj := e.InvolvedObject
		switch {
		case strings.EqualFold(obj.Kind, "Pod"):
			if _, ok := pods[obj.Name]; ok {
				matched = append(matched, e)
			}
		case strings.EqualFold(obj.Kind, "Deployment") && obj.Name == deploy:
			matched = append(matched, e)
		}
	}
	if incomplete {
		b.WriteString("event scan hit the namespace cap\n")
	}
	text, _ := kube.FormatEvents(namespace, matched, now, kube.EventView{
		Limit:         opt.EventLimit,
		WarningsFirst: true,
	})
	b.WriteString("Events for this deployment:\n")
	b.WriteString(text)
	b.WriteString("\n")
}

func writeAlerts(b *strings.Builder, ctx context.Context, opt Options, namespace, service string, pods map[string]struct{}, now time.Time) {
	b.WriteString("Alerts:\n")
	if opt.Alerts == nil {
		b.WriteString("alertmanager is not configured\n\n")
		return
	}
	all, err := opt.Alerts.Alerts(ctx)
	if err != nil {
		fmt.Fprintf(b, "unavailable: %s\n\n", err)
		return
	}
	var matched []alertmanager.Alert
	silenced := 0
	others := 0
	for _, alert := range all {
		nsOK := alert.Labels["namespace"] == namespace || alert.Labels["kubernetes_namespace"] == namespace
		if !alertMatches(alert, namespace, service, pods) {
			if nsOK {
				others++
			}
			continue
		}
		if len(alert.SilencedBy) > 0 {
			silenced++
			continue
		}
		if len(alert.InhibitedBy) > 0 {
			continue
		}
		matched = append(matched, alert)
	}
	if len(matched) == 0 {
		fmt.Fprintf(b, "none matched %s/%s\n", namespace, service)
	} else {
		for _, alert := range matched {
			name := alert.Labels["alertname"]
			severity := alert.Labels["severity"]
			if severity == "" {
				severity = "unknown"
			}
			fmt.Fprintf(b, "- %s %s firing for %s\n", severity, name, since(now, alert.StartsAt))
			if summary := alert.Annotations["summary"]; summary != "" {
				fmt.Fprintf(b, "  summary: %s\n", summary)
			}
		}
	}
	if others > 0 {
		fmt.Fprintf(b, "(%d other firing alert(s) in the namespace)\n", others)
	}
	if silenced > 0 {
		fmt.Fprintf(b, "(%d silenced alert(s) matched and were hidden)\n", silenced)
	}
	b.WriteString("\n")
}

func alertMatches(a alertmanager.Alert, namespace, service string, pods map[string]struct{}) bool {
	if pod := a.Labels["pod"]; pod != "" {
		if _, ok := pods[pod]; ok {
			return true
		}
	}
	nsOK := a.Labels["namespace"] == namespace || a.Labels["kubernetes_namespace"] == namespace
	svc := a.Labels["service"]
	if svc == "" {
		svc = a.Labels["app"]
	}
	if svc == "" {
		svc = a.Labels["job"]
	}
	nameOK := svc == service || (service != "" && strings.Contains(a.Labels["alertname"], service))
	return nsOK && nameOK
}

func writeProm(b *strings.Builder, ctx context.Context, opt Options, namespace, service string) {
	b.WriteString("Prometheus:\n")
	if opt.Prom == nil {
		b.WriteString("prometheus is not configured\n\n")
		return
	}
	if len(opt.Queries) == 0 {
		b.WriteString("no triage queries configured\n\n")
		return
	}
	for _, q := range opt.Queries {
		expr, err := q.Render(namespace, service)
		if err != nil {
			fmt.Fprintf(b, "- %s: template: %s\n", q.Name, err)
			continue
		}
		if len(expr) > opt.MaxQueryBytes {
			fmt.Fprintf(b, "- %s: rendered query is too long\n", q.Name)
			continue
		}
		value, warnings, err := opt.Prom.Query(ctx, expr, opt.Runtime.At())
		if err != nil {
			fmt.Fprintf(b, "- %s: failed: %s\n", q.Name, err)
			continue
		}
		text, _ := prometheus.Format(value, 5, 2)
		fmt.Fprintf(b, "- %s\n  expr: %s\n", q.Name, expr)
		if len(warnings) > 0 {
			fmt.Fprintf(b, "  warnings: %s\n", strings.Join(warnings, "; "))
		}
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			fmt.Fprintf(b, "  %s\n", line)
		}
	}
	b.WriteString("\n")
}

func writeLogs(b *strings.Builder, ctx context.Context, opt Options, in input, pods []corev1.Pod) {
	b.WriteString("Logs:\n")
	pod, unhealthy := worst(pods)
	if !unhealthy {
		b.WriteString("no unhealthy pods; logs not pulled\n")
		return
	}
	lines := opt.LogLines
	if in.LogLines > 0 {
		lines = in.LogLines
	}
	if lines > opt.MaxLogLines {
		lines = opt.MaxLogLines
	}
	if lines < 1 {
		lines = 1
	}
	text, _, err := opt.Cluster.Logs(ctx, in.Namespace, pod.Name, "", int64(lines), false, opt.Runtime.MaxBytes)
	if err != nil {
		fmt.Fprintf(b, "%s: %s\n", pod.Name, err)
		return
	}
	b.WriteString(text)
}

func worst(pods []corev1.Pod) (corev1.Pod, bool) {
	var found corev1.Pod
	best := -1
	for i := range pods {
		s := score(pods[i])
		if s > best || (s == best && (found.Name == "" || pods[i].Name < found.Name)) {
			best = s
			found = pods[i]
		}
	}
	if best <= 0 {
		return corev1.Pod{}, false
	}
	return found, true
}

func score(p corev1.Pod) int {
	s := int(restartCount(p))
	if !ready(p) {
		s += 100
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			s += 50
		}
		if t := cs.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" {
			s += 20
		}
	}
	return s
}

func ready(p corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func restartCount(p corev1.Pod) int32 {
	var n int32
	for _, cs := range p.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	return n
}

func podNames(pods []corev1.Pod) map[string]struct{} {
	out := make(map[string]struct{}, len(pods))
	for i := range pods {
		out[pods[i].Name] = struct{}{}
	}
	return out
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
