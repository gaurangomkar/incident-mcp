package kube

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// FormatPods renders pods with not-ready workloads first.
// The bool is true when limit dropped pods.
func FormatPods(namespace string, pods []corev1.Pod, now time.Time, limit int) (string, bool) {
	items := append([]corev1.Pod(nil), pods...)
	slices.SortStableFunc(items, func(a, b corev1.Pod) int {
		if d := cmp.Compare(podRank(b), podRank(a)); d != 0 {
			return d
		}
		return cmp.Compare(a.Name, b.Name)
	})
	truncated := false
	if limit > 0 && len(items) > limit {
		items = items[:limit]
		truncated = true
	}
	notReady := 0
	for i := range items {
		if !podReady(items[i]) {
			notReady++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "namespace %s: %d pod", namespace, len(items))
	if len(items) != 1 {
		b.WriteString("s")
	}
	if notReady > 0 {
		fmt.Fprintf(&b, ", %d not ready", notReady)
	}
	if truncated {
		b.WriteString(" (list truncated)")
	}
	b.WriteString("\n")
	if len(items) == 0 {
		return b.String(), truncated
	}
	b.WriteString("\n")
	for i := range items {
		p := items[i]
		ready, total := readyCounts(p)
		node := p.Spec.NodeName
		if node == "" {
			node = "-"
		}
		fmt.Fprintf(&b, "%s  %s  ready %d/%d  restarts %d  age %s  node %s\n",
			p.Name, podStatus(p), ready, total, restartCount(p), age(now, p.CreationTimestamp.Time), node)
		for _, cs := range p.Status.ContainerStatuses {
			if line := containerDetail(cs); line != "" {
				b.WriteString(line)
			}
		}
	}
	return b.String(), truncated
}

// EventView controls how events are filtered and ordered.
type EventView struct {
	Limit         int
	WarningsOnly  bool
	WarningsFirst bool
	Kind          string
	Name          string
}

// FormatEvents renders events, newest first.
func FormatEvents(namespace string, events []corev1.Event, now time.Time, view EventView) (string, bool) {
	items := make([]corev1.Event, 0, len(events))
	for i := range events {
		e := events[i]
		if view.WarningsOnly && e.Type != corev1.EventTypeWarning {
			continue
		}
		if !involvedMatch(e, view.Kind, view.Name) {
			continue
		}
		items = append(items, e)
	}
	slices.SortStableFunc(items, func(a, b corev1.Event) int {
		if view.WarningsFirst {
			if d := cmp.Compare(warnRank(b), warnRank(a)); d != 0 {
				return d
			}
		}
		return eventTime(b).Compare(eventTime(a))
	})
	truncated := false
	if view.Limit > 0 && len(items) > view.Limit {
		items = items[:view.Limit]
		truncated = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "namespace %s: %d event", namespace, len(items))
	if len(items) != 1 {
		b.WriteString("s")
	}
	if truncated {
		b.WriteString(" (list truncated)")
	}
	b.WriteString("\n")
	if len(items) == 0 {
		return b.String(), truncated
	}
	b.WriteString("\n")
	for i := range items {
		e := items[i]
		fmt.Fprintf(&b, "%s ago  %s  %s  %s/%s", age(now, eventTime(e)), blank(e.Type, "Normal"), blank(e.Reason, "Unknown"), blank(e.InvolvedObject.Kind, "Object"), e.InvolvedObject.Name)
		if e.Count > 1 {
			fmt.Fprintf(&b, " x%d", e.Count)
		}
		b.WriteString("\n")
		if e.Message != "" {
			fmt.Fprintf(&b, "  %s\n", e.Message)
		}
	}
	return b.String(), truncated
}

// FormatDeployment renders a describe-style summary.
// Secret values and env values whose names look sensitive are not printed.
func FormatDeployment(d *appsv1.Deployment, now time.Time) string {
	if d == nil {
		return "deployment: none\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Deployment %s/%s\n", d.Namespace, d.Name)
	fmt.Fprintf(&b, "created %s ago\n", age(now, d.CreationTimestamp.Time))
	if d.Status.ObservedGeneration < d.Generation {
		fmt.Fprintf(&b, "rollout in progress: generation %d, observed %d\n", d.Generation, d.Status.ObservedGeneration)
	} else {
		fmt.Fprintf(&b, "generation %d\n", d.Generation)
	}
	if rev := d.Annotations["deployment.kubernetes.io/revision"]; rev != "" {
		fmt.Fprintf(&b, "revision %s\n", rev)
	}
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	fmt.Fprintf(&b, "replicas: %d desired, %d updated, %d ready, %d available",
		desired, d.Status.UpdatedReplicas, d.Status.ReadyReplicas, d.Status.AvailableReplicas)
	if d.Status.UnavailableReplicas > 0 {
		fmt.Fprintf(&b, ", %d unavailable", d.Status.UnavailableReplicas)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "strategy: %s\n", strategy(d.Spec.Strategy))
	fmt.Fprintf(&b, "selector: %s\n", selectorString(d.Spec.Selector))
	if len(d.Status.Conditions) > 0 {
		b.WriteString("conditions:\n")
		conds := append([]appsv1.DeploymentCondition(nil), d.Status.Conditions...)
		slices.SortStableFunc(conds, func(a, b appsv1.DeploymentCondition) int {
			return cmp.Compare(string(a.Type), string(b.Type))
		})
		for _, c := range conds {
			fmt.Fprintf(&b, "  %s=%s", c.Type, c.Status)
			if c.Reason != "" {
				fmt.Fprintf(&b, " %s", c.Reason)
			}
			if c.Message != "" {
				fmt.Fprintf(&b, " %s", c.Message)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("containers:\n")
	for i := range d.Spec.Template.Spec.Containers {
		writeContainer(&b, d.Spec.Template.Spec.Containers[i])
	}
	if len(d.Spec.Template.Spec.InitContainers) > 0 {
		b.WriteString("init containers:\n")
		for i := range d.Spec.Template.Spec.InitContainers {
			writeContainer(&b, d.Spec.Template.Spec.InitContainers[i])
		}
	}
	return b.String()
}

func writeContainer(b *strings.Builder, c corev1.Container) {
	fmt.Fprintf(b, "  %s\n", c.Name)
	fmt.Fprintf(b, "    image: %s\n", c.Image)
	if c.ImagePullPolicy != "" {
		fmt.Fprintf(b, "    pull: %s\n", c.ImagePullPolicy)
	}
	fmt.Fprintf(b, "    ports: %s\n", formatPorts(c.Ports))
	fmt.Fprintf(b, "    resources: requests %s limits %s\n", formatResources(c.Resources.Requests), formatResources(c.Resources.Limits))
	if line := formatProbe("readiness", c.ReadinessProbe); line != "" {
		fmt.Fprintf(b, "    %s\n", line)
	}
	if line := formatProbe("liveness", c.LivenessProbe); line != "" {
		fmt.Fprintf(b, "    %s\n", line)
	}
	if len(c.Command) > 0 {
		fmt.Fprintf(b, "    command: %s\n", strings.Join(c.Command, " "))
	}
	if len(c.Args) > 0 {
		fmt.Fprintf(b, "    args: %s\n", strings.Join(c.Args, " "))
	}
	if len(c.Env) == 0 && len(c.EnvFrom) == 0 {
		return
	}
	b.WriteString("    env:\n")
	for _, env := range c.EnvFrom {
		switch {
		case env.SecretRef != nil:
			fmt.Fprintf(b, "      envFrom secret/%s\n", env.SecretRef.Name)
		case env.ConfigMapRef != nil:
			fmt.Fprintf(b, "      envFrom configmap/%s\n", env.ConfigMapRef.Name)
		}
	}
	for _, env := range c.Env {
		fmt.Fprintf(b, "      %s\n", formatEnv(env))
	}
}

func formatEnv(env corev1.EnvVar) string {
	if env.ValueFrom != nil {
		switch {
		case env.ValueFrom.SecretKeyRef != nil:
			ref := env.ValueFrom.SecretKeyRef
			return fmt.Sprintf("%s from secret/%s key %s", env.Name, ref.Name, ref.Key)
		case env.ValueFrom.ConfigMapKeyRef != nil:
			ref := env.ValueFrom.ConfigMapKeyRef
			return fmt.Sprintf("%s from configmap/%s key %s", env.Name, ref.Name, ref.Key)
		default:
			return env.Name + " from valueFrom"
		}
	}
	if sensitiveEnv(env.Name) {
		return env.Name + "=[REDACTED]"
	}
	return env.Name + "=" + env.Value
}

func sensitiveEnv(name string) bool {
	n := strings.ToLower(name)
	for _, word := range []string{"password", "passwd", "secret", "token", "credential", "api_key", "apikey", "access_key", "private_key"} {
		if strings.Contains(n, word) {
			return true
		}
	}
	return false
}

func strategy(s appsv1.DeploymentStrategy) string {
	if s.Type == appsv1.RecreateDeploymentStrategyType {
		return "Recreate"
	}
	if s.RollingUpdate == nil {
		return "RollingUpdate"
	}
	return fmt.Sprintf("RollingUpdate maxUnavailable %s maxSurge %s", intOrString(s.RollingUpdate.MaxUnavailable), intOrString(s.RollingUpdate.MaxSurge))
}

func intOrString(v *intstr.IntOrString) string {
	if v == nil {
		return "unset"
	}
	return v.String()
}

func selectorString(sel *metav1.LabelSelector) string {
	if sel == nil {
		return "<none>"
	}
	text := metav1.FormatLabelSelector(sel)
	if text == "" || text == "<error>" {
		return "<none>"
	}
	return text
}

func formatPorts(ports []corev1.ContainerPort) string {
	if len(ports) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = corev1.ProtocolTCP
		}
		parts = append(parts, fmt.Sprintf("%d/%s", p.ContainerPort, proto))
	}
	return strings.Join(parts, ", ")
}

func formatResources(list corev1.ResourceList) string {
	if len(list) == 0 {
		return "none"
	}
	keys := make([]corev1.ResourceName, 0, len(list))
	for k := range list {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		qty := list[k]
		parts = append(parts, string(k)+"="+qty.String())
	}
	return strings.Join(parts, ",")
}

func formatProbe(name string, p *corev1.Probe) string {
	if p == nil {
		return ""
	}
	var detail string
	switch {
	case p.HTTPGet != nil:
		detail = fmt.Sprintf("http %s port %s", p.HTTPGet.Path, p.HTTPGet.Port.String())
	case p.TCPSocket != nil:
		detail = fmt.Sprintf("tcp port %s", p.TCPSocket.Port.String())
	case p.Exec != nil:
		detail = "exec"
	case p.GRPC != nil:
		detail = fmt.Sprintf("grpc port %d", p.GRPC.Port)
	default:
		detail = "configured"
	}
	return fmt.Sprintf("%s: %s period %ds timeout %ds", name, detail, p.PeriodSeconds, p.TimeoutSeconds)
}

func podStatus(p corev1.Pod) string {
	if p.DeletionTimestamp != nil {
		return "Terminating"
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
	}
	if p.Status.Phase == "" {
		return "Unknown"
	}
	return string(p.Status.Phase)
}

func podReady(p corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func readyCounts(p corev1.Pod) (ready, total int) {
	total = len(p.Spec.Containers)
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return ready, total
}

func restartCount(p corev1.Pod) int32 {
	var n int32
	for _, cs := range p.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	for _, cs := range p.Status.InitContainerStatuses {
		n += cs.RestartCount
	}
	return n
}

func podRank(p corev1.Pod) int {
	rank := int(restartCount(p))
	if !podReady(p) {
		rank += 1000
	}
	if podStatus(p) == "CrashLoopBackOff" {
		rank += 100
	}
	return rank
}

func containerDetail(cs corev1.ContainerStatus) string {
	var parts []string
	switch {
	case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
		parts = append(parts, "waiting "+cs.State.Waiting.Reason)
	case cs.State.Terminated != nil:
		parts = append(parts, fmt.Sprintf("terminated %s (%d)", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode))
	}
	if t := cs.LastTerminationState.Terminated; t != nil && !cs.Ready {
		parts = append(parts, fmt.Sprintf("last exit %s (%d)", t.Reason, t.ExitCode))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + cs.Name + " " + strings.Join(parts, ", ") + "\n"
}

func eventTime(e corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	return e.FirstTimestamp.Time
}

func warnRank(e corev1.Event) int {
	if e.Type == corev1.EventTypeWarning {
		return 1
	}
	return 0
}

func involvedMatch(e corev1.Event, kind, name string) bool {
	if kind == "" && name == "" {
		return true
	}
	if name != "" && e.InvolvedObject.Name != name {
		return false
	}
	if kind != "" && !strings.EqualFold(e.InvolvedObject.Kind, kind) {
		return false
	}
	return true
}

func age(now, then time.Time) string {
	if then.IsZero() {
		return "?"
	}
	d := now.Sub(then)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}

func blank(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
