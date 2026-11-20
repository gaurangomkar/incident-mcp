package triage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	"github.com/gaurangomkar/incident-mcp/internal/alertmanager"
	"github.com/gaurangomkar/incident-mcp/internal/config"
	"github.com/gaurangomkar/incident-mcp/internal/kube"
	"github.com/gaurangomkar/incident-mcp/internal/mcptool"
	"github.com/gaurangomkar/incident-mcp/internal/redact"
)

func TestSummarizeUnhealthyService(t *testing.T) {
	now := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)
	logsCalled := false
	cluster, err := kube.NewClient(fake.NewClientset(
		pod("payments", "checkout-a", true, 0, ""),
		pod("payments", "checkout-b", false, 14, "CrashLoopBackOff"),
		&corev1.Event{
			TypeMeta:       metav1.TypeMeta{APIVersion: "v1", Kind: "Event"},
			ObjectMeta:     metav1.ObjectMeta{Name: "boom", Namespace: "payments"},
			Type:           corev1.EventTypeWarning,
			Reason:         "BackOff",
			Message:        "Back-off restarting failed container",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "checkout-b", Namespace: "payments"},
			LastTimestamp:  metav1.NewTime(now.Add(-4 * time.Minute)),
		},
		deployment(false),
	), []string{"payments"}, func(_ context.Context, _, podName string, _ *corev1.PodLogOptions) (io.ReadCloser, error) {
		logsCalled = true
		if podName != "checkout-b" {
			t.Fatalf("logs from %s", podName)
		}
		return io.NopCloser(strings.NewReader("panic: password=hunter2secret\n")), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	queries, err := config.Default().Triage.Compile()
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{
		Cluster: cluster,
		Prom:    fakeProm{errOn: "restarts"},
		Alerts: fakeAlerts{alerts: []alertmanager.Alert{
			{
				Labels:      map[string]string{"alertname": "CheckoutDown", "severity": "critical", "namespace": "payments", "service": "checkout"},
				Annotations: map[string]string{"summary": "checkout is down"},
				StartsAt:    now.Add(-10 * time.Minute),
			},
			{
				Labels:   map[string]string{"alertname": "BillingSlow", "severity": "warning", "namespace": "payments", "service": "billing"},
				StartsAt: now,
			},
			{
				Labels:     map[string]string{"alertname": "CheckoutSilenced", "namespace": "payments", "service": "checkout"},
				SilencedBy: []string{"s1"},
				StartsAt:   now,
			},
		}},
		Queries:       queries,
		LogLines:      40,
		EventLimit:    10,
		MaxLogLines:   80,
		MaxQueryBytes: 2000,
		Runtime: mcptool.Runtime{
			Timeout:  time.Second,
			MaxBytes: 8000,
			Redactor: redact.New(true),
			Now:      func() time.Time { return now },
		},
	}
	res, _, err := opt.Runtime.Call(context.Background(), "triage_service", func(ctx context.Context) (string, error) {
		return summarize(ctx, opt, input{Namespace: "payments", Service: "checkout"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !logsCalled {
		t.Fatal("expected logs from the crashing pod")
	}
	text := mcptool.ResultText(res)
	for _, want := range []string{
		"unhealthy: deployment is not available",
		"checkout-b",
		"BackOff",
		"CheckoutDown",
		"checkout is down",
		"1 other firing alert",
		"1 silenced alert",
		"restarts: failed",
		"- up",
		"password=[REDACTED]",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "hunter2secret") || strings.Contains(text, "BillingSlow") {
		t.Fatalf("unexpected content:\n%s", text)
	}
}

func TestHealthySkipsLogsAndMissingBackends(t *testing.T) {
	now := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)
	cluster, err := kube.NewClient(fake.NewClientset(
		pod("payments", "checkout-a", true, 0, ""),
		deployment(true),
	), []string{"payments"}, func(context.Context, string, string, *corev1.PodLogOptions) (io.ReadCloser, error) {
		t.Fatal("logs should not be read")
		return nil, errors.New("unused")
	})
	if err != nil {
		t.Fatal(err)
	}
	text, err := summarize(context.Background(), Options{
		Cluster:    cluster,
		LogLines:   20,
		EventLimit: 5,
		Runtime:    mcptool.Runtime{Now: func() time.Time { return now }},
	}, input{Namespace: "payments", Service: "checkout"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"healthy:", "logs not pulled", "prometheus is not configured", "alertmanager is not configured"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
}

func TestAllowlistAndNotFound(t *testing.T) {
	cluster, err := kube.NewClient(fake.NewClientset(deployment(true)), []string{"payments"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{Cluster: cluster, Runtime: mcptool.Runtime{Now: time.Now}}
	if _, err := summarize(context.Background(), opt, input{Namespace: "kube-system", Service: "checkout"}); err == nil {
		t.Fatal("expected allowlist error")
	}
	if _, err := summarize(context.Background(), opt, input{Namespace: "payments", Service: "missing"}); err == nil {
		t.Fatal("expected not found")
	}
}

type fakeProm struct {
	errOn string
}

func (f fakeProm) Query(_ context.Context, query string, _ time.Time) (model.Value, promv1.Warnings, error) {
	if strings.Contains(query, f.errOn) {
		return nil, nil, errors.New("server error: boom")
	}
	return model.Vector{{
		Metric:    model.Metric{"pod": "checkout-a"},
		Value:     1,
		Timestamp: model.TimeFromUnix(1_763_600_000),
	}}, nil, nil
}

func (f fakeProm) QueryRange(context.Context, string, promv1.Range) (model.Value, promv1.Warnings, error) {
	return nil, nil, errors.New("not used")
}

type fakeAlerts struct {
	alerts []alertmanager.Alert
}

func (f fakeAlerts) Alerts(context.Context) ([]alertmanager.Alert, error) { return f.alerts, nil }
func (f fakeAlerts) Silences(context.Context) ([]alertmanager.Silence, error) {
	return nil, nil
}

func pod(ns, name string, isReady bool, restarts int32, waiting string) *corev1.Pod {
	status := corev1.ConditionTrue
	state := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
	if !isReady {
		status = corev1.ConditionFalse
		state = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: waiting}}
	}
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         ns,
			Labels:            map[string]string{"app": "checkout"},
			CreationTimestamp: metav1.NewTime(time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)),
		},
		Spec: corev1.PodSpec{NodeName: "node-a", Containers: []corev1.Container{{Name: "app", Image: "example/checkout:1"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: status,
			}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "app",
				Ready:        isReady,
				RestartCount: restarts,
				State:        state,
			}},
		},
	}
}

func deployment(available bool) *appsv1.Deployment {
	replicas := int32(1)
	status := corev1.ConditionFalse
	ready := int32(0)
	if available {
		status = corev1.ConditionTrue
		ready = 1
	}
	return &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "checkout", Namespace: "payments"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "checkout"}},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example/checkout:1"}}}},
		},
		Status: appsv1.DeploymentStatus{
			Replicas:          1,
			ReadyReplicas:     ready,
			AvailableReplicas: ready,
			Conditions: []appsv1.DeploymentCondition{{
				Type:   appsv1.DeploymentAvailable,
				Status: status,
			}},
		},
	}
}
