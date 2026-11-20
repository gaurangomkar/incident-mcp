package kube

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gaurangomkar/incident-mcp/internal/mcptool"
	"github.com/gaurangomkar/incident-mcp/internal/redact"
)

func TestPodsEventsAndDeployment(t *testing.T) {
	now := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)
	cs := fake.NewClientset(
		healthyPod("payments", "checkout-a"),
		crashPod("payments", "checkout-b"),
		func() *corev1.Pod {
			p := healthyPod("payments", "other-app")
			p.Labels = map[string]string{"app": "other"}
			return p
		}(),
		healthyPod("other", "ignored"),
		&corev1.Event{
			TypeMeta:       metav1.TypeMeta{APIVersion: "v1", Kind: "Event"},
			ObjectMeta:     metav1.ObjectMeta{Name: "old", Namespace: "payments"},
			Type:           corev1.EventTypeNormal,
			Reason:         "Pulled",
			Message:        "image pulled",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "checkout-a"},
			LastTimestamp:  metav1.NewTime(now.Add(-2 * time.Hour)),
			Count:          1,
		},
		&corev1.Event{
			TypeMeta:       metav1.TypeMeta{APIVersion: "v1", Kind: "Event"},
			ObjectMeta:     metav1.ObjectMeta{Name: "boom", Namespace: "payments"},
			Type:           corev1.EventTypeWarning,
			Reason:         "BackOff",
			Message:        "Back-off restarting failed container app",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "checkout-b"},
			LastTimestamp:  metav1.NewTime(now.Add(-4 * time.Minute)),
			Count:          12,
		},
		sampleDeployment(),
	)
	c, err := NewClient(cs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{Client: c, MaxPods: 50, MaxEvents: 20, MaxLogLines: 100, Runtime: mcptool.Runtime{
		Timeout: time.Second,
		Now:     func() time.Time { return now },
	}}

	text, err := pods(context.Background(), opt, podsInput{Namespace: "payments", LabelSelector: "app=checkout"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "checkout-b") || !strings.Contains(text, "CrashLoopBackOff") || strings.Contains(text, "ignored") || strings.Contains(text, "other-app") {
		t.Fatalf("pods:\n%s", text)
	}
	if !strings.HasPrefix(lineAfterSelector(text), "checkout-b") && !strings.Contains(text, "checkout-b  CrashLoopBackOff") {
		t.Fatalf("unhealthy pod should be listed: %s", text)
	}
	bPos := strings.Index(text, "checkout-b")
	aPos := strings.Index(text, "checkout-a")
	if bPos < 0 || aPos < 0 || bPos > aPos {
		t.Fatalf("order:\n%s", text)
	}

	text, err = events(context.Background(), opt, eventsInput{Namespace: "payments", InvolvedObject: "Pod/checkout-b", WarningsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "BackOff") || strings.Contains(text, "Pulled") {
		t.Fatalf("events:\n%s", text)
	}
	if !strings.Contains(text, "x12") {
		t.Fatalf("count missing:\n%s", text)
	}

	text, err = deployment(context.Background(), opt, deploymentInput{Namespace: "payments", Name: "checkout"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 desired", "1 ready", "Available=False", "example/checkout:1.8.3", "LOG_LEVEL=info", "API_TOKEN=[REDACTED]", "from secret/checkout key password"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "supersecretvalue") {
		t.Fatalf("secret value leaked:\n%s", text)
	}

	if _, err := deployment(context.Background(), opt, deploymentInput{Namespace: "payments", Name: "missing"}); err == nil {
		t.Fatal("expected not found")
	}
}

func TestPodLogsTailPreviousAndRedaction(t *testing.T) {
	var got *corev1.PodLogOptions
	cs := fake.NewClientset(multiPod())
	c, err := NewClient(cs, []string{"payments"}, func(_ context.Context, namespace, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error) {
		if namespace != "payments" || pod != "checkout" {
			t.Fatalf("log target %s/%s", namespace, pod)
		}
		got = opts
		body := "line1\nline2\npassword=hunter2secret\nline4\n"
		return io.NopCloser(strings.NewReader(body)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{
		Client: c, MaxPods: 10, MaxEvents: 10, MaxLogLines: 50,
		Runtime: mcptool.Runtime{Timeout: time.Second, MaxBytes: 4000, Redactor: redact.New(true)},
	}
	res, _, err := opt.Runtime.Call(context.Background(), "k8s_pod_logs", func(ctx context.Context) (string, error) {
		return podLogs(ctx, opt, logsInput{Namespace: "payments", Pod: "checkout", Container: "app", TailLines: 2, Previous: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	text := mcptool.ResultText(res)
	if strings.Contains(text, "hunter2secret") || !strings.Contains(text, "password=[REDACTED]") {
		t.Fatalf("redaction:\n%s", text)
	}
	if strings.Contains(text, "line1") {
		t.Fatalf("tail did not drop the head:\n%s", text)
	}
	if got == nil || !got.Previous || got.Container != "app" || got.TailLines == nil || *got.TailLines != 2 {
		t.Fatalf("log options %+v", got)
	}

	if _, err := podLogs(context.Background(), opt, logsInput{Namespace: "payments", Pod: "checkout"}); err == nil {
		t.Fatal("expected container required")
	}
	if _, err := podLogs(context.Background(), opt, logsInput{Namespace: "other", Pod: "checkout", Container: "app"}); err == nil {
		t.Fatal("expected allowlist rejection")
	}
}

func lineAfterSelector(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "checkout-") {
			return line
		}
	}
	return ""
}

func healthyPod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         ns,
			Labels:            map[string]string{"app": "checkout"},
			CreationTimestamp: metav1.NewTime(time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)),
		},
		Spec: corev1.PodSpec{
			NodeName:   "node-a",
			Containers: []corev1.Container{{Name: "app", Image: "example/checkout:1.8.3"}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "app",
				Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
}

func crashPod(ns, name string) *corev1.Pod {
	p := healthyPod(ns, name)
	p.Status.Conditions[0].Status = corev1.ConditionFalse
	p.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:         "app",
		Ready:        false,
		RestartCount: 14,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason:   "OOMKilled",
			ExitCode: 137,
		}},
	}}
	return p
}

func multiPod() *corev1.Pod {
	p := healthyPod("payments", "checkout")
	p.Spec.Containers = []corev1.Container{
		{Name: "app", Image: "example/checkout:1.8.3"},
		{Name: "sidecar", Image: "example/sidecar:1"},
	}
	return p
}

func sampleDeployment() *appsv1.Deployment {
	replicas := int32(2)
	maxUnavailable := intstr.FromString("25%")
	maxSurge := intstr.FromString("25%")
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "checkout",
			Namespace:         "payments",
			CreationTimestamp: metav1.NewTime(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)),
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": "12"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "checkout"}},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
					MaxSurge:       &maxSurge,
				},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "app",
						Image: "example/checkout:1.8.3",
						Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
						Env: []corev1.EnvVar{
							{Name: "LOG_LEVEL", Value: "info"},
							{Name: "API_TOKEN", Value: "supersecretvalue"},
							{Name: "DB_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "checkout"},
								Key:                  "password",
							}}},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
								Path: "/readyz",
								Port: intstr.FromInt32(8080),
							}},
							PeriodSeconds: 5,
						},
					}},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			Replicas:            2,
			UpdatedReplicas:     2,
			ReadyReplicas:       1,
			AvailableReplicas:   1,
			UnavailableReplicas: 1,
			ObservedGeneration:  1,
			Conditions: []appsv1.DeploymentCondition{{
				Type:    appsv1.DeploymentAvailable,
				Status:  corev1.ConditionFalse,
				Reason:  "MinimumReplicasUnavailable",
				Message: "Deployment does not have minimum availability.",
			}},
		},
	}
}
