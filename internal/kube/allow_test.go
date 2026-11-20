package kube

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gaurangomkar/incident-mcp/internal/config"
)

func TestNamespaceAllowlist(t *testing.T) {
	cs := fake.NewClientset(healthyPod("payments", "checkout-a"), healthyPod("kube-system", "dns"))
	c, err := NewClient(cs, []string{"payments", "payments"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Pods(context.Background(), "payments", ""); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Pods(context.Background(), "kube-system", "")
	if err == nil || !strings.Contains(err.Error(), "not in the allowlist") || !strings.Contains(err.Error(), "payments") {
		t.Fatalf("err %v", err)
	}
	if _, _, err := c.Events(context.Background(), "Bad_Name"); err == nil {
		t.Fatal("expected invalid namespace")
	}
	if _, err := c.Deployment(context.Background(), "payments", "Not_A_Name"); err == nil {
		t.Fatal("expected invalid name")
	}
	if _, err := NewClient(cs, []string{"Bad_NS"}, nil); err == nil {
		t.Fatal("expected invalid allowlist")
	}

	open, err := NewClient(cs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !open.Unrestricted() {
		t.Fatal("empty allowlist should be unrestricted")
	}
	pods, _, err := open.Pods(context.Background(), "kube-system", "")
	if err != nil || len(pods) != 1 {
		t.Fatalf("pods %d err %v", len(pods), err)
	}
}

func TestReadOnlyVerbs(t *testing.T) {
	cs := fake.NewClientset(healthyPod("payments", "checkout"), sampleDeployment())
	c, err := NewClient(cs, []string{"payments"}, func(context.Context, string, string, *corev1.PodLogOptions) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("ok\n")), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := c.Pods(ctx, "payments", "app=checkout"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Events(ctx, "payments"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Deployment(ctx, "payments", "checkout"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Logs(ctx, "payments", "checkout", "app", 10, false, 1000); err != nil {
		t.Fatal(err)
	}
	for _, action := range cs.Actions() {
		switch action.GetVerb() {
		case "get", "list":
		default:
			t.Fatalf("unexpected %s on %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
}

func TestConnectNotConfigured(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	c, err := Connect(config.Kubernetes{}, "incident-mcp/test")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Disabled() {
		t.Fatal("expected disabled client")
	}
	_, _, err = c.Pods(context.Background(), "default", "")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err %v", err)
	}
}
