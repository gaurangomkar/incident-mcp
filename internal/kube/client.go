// Package kube is the read-only Kubernetes view used by the tools.
// The client exposes get, list, and log reads. It has no create, update, or delete path.
package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/gaurangomkar/incident-mcp/internal/config"
)

const (
	podScanLimit   = 500
	eventScanLimit = 1000
)

// ErrNotConfigured means no kubeconfig and no in-cluster credentials were found.
var ErrNotConfigured = errors.New("kubernetes is not configured: set kubernetes.kubeconfig, KUBECONFIG, or run in a cluster")

// NotFoundError is returned when a namespaced object does not exist.
type NotFoundError struct {
	Kind      string
	Namespace string
	Name      string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %s/%s not found", strings.ToLower(e.Kind), e.Namespace, e.Name)
}

// LogReader streams pod logs. The default calls the pod log subresource.
// The client-go fake does not serve that subresource, so tests pass their own reader.
type LogReader func(ctx context.Context, namespace, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error)

// Client reads pods, events, deployments, and logs inside an allowlist.
type Client struct {
	cs    kubernetes.Interface
	allow *nslist
	logs  LogReader
	cause error
}

// Connect builds a client from kubeconfig, KUBECONFIG, or in-cluster config.
// A missing cluster returns a disabled client and a nil error so the server can still start.
// An explicit but unusable kubeconfig, or an invalid allowlist, returns an error.
func Connect(cfg config.Kubernetes, userAgent string) (*Client, error) {
	allow, err := newNamespaceList(cfg.AllowedNamespaces)
	if err != nil {
		return nil, err
	}
	restCfg, err := loadRESTConfig(cfg)
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return &Client{allow: allow, cause: ErrNotConfigured}, nil
		}
		return nil, err
	}
	restCfg = rest.CopyConfig(restCfg)
	if userAgent != "" {
		restCfg.UserAgent = userAgent
	}
	restCfg.QPS = 20
	restCfg.Burst = 40
	if cfg.Timeout.Std() > 0 {
		restCfg.Timeout = cfg.Timeout.Std()
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: %w", err)
	}
	return newClient(cs, allow, nil), nil
}

// NewClient wraps an existing clientset. namespaces empty means every namespace is allowed.
func NewClient(cs kubernetes.Interface, namespaces []string, logs LogReader) (*Client, error) {
	allow, err := newNamespaceList(namespaces)
	if err != nil {
		return nil, err
	}
	return newClient(cs, allow, logs), nil
}

func newClient(cs kubernetes.Interface, allow *nslist, logs LogReader) *Client {
	c := &Client{cs: cs, allow: allow, logs: logs}
	if c.logs == nil {
		c.logs = c.streamLogs
	}
	return c
}

// Unrestricted reports whether every namespace may be read.
func (c *Client) Unrestricted() bool {
	return c == nil || c.allow == nil || c.allow.unrestricted
}

// Disabled reports whether cluster credentials are missing.
func (c *Client) Disabled() bool {
	return c == nil || c.cs == nil
}

// Pods lists pods in one namespace. incomplete is true when the API had more pages than the scan cap.
func (c *Client) Pods(ctx context.Context, namespace, selector string) ([]corev1.Pod, bool, error) {
	if err := c.gate(namespace); err != nil {
		return nil, false, err
	}
	var sel labels.Selector
	if selector != "" {
		parsed, err := labels.Parse(selector)
		if err != nil {
			return nil, false, fmt.Errorf("label selector: %w", err)
		}
		sel = parsed
	}
	list, err := c.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
		Limit:         podScanLimit,
	})
	if err != nil {
		return nil, false, fmt.Errorf("list pods: %w", err)
	}
	items := make([]corev1.Pod, 0, len(list.Items))
	for i := range list.Items {
		if sel == nil || sel.Matches(labels.Set(list.Items[i].Labels)) {
			items = append(items, list.Items[i])
		}
	}
	return items, list.Continue != "", nil
}

// Events lists events in one namespace, capped by the scan limit.
func (c *Client) Events(ctx context.Context, namespace string) ([]corev1.Event, bool, error) {
	if err := c.gate(namespace); err != nil {
		return nil, false, err
	}
	list, err := c.cs.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{Limit: eventScanLimit})
	if err != nil {
		return nil, false, fmt.Errorf("list events: %w", err)
	}
	return list.Items, list.Continue != "", nil
}

// Deployment fetches one deployment.
func (c *Client) Deployment(ctx context.Context, namespace, name string) (*appsv1.Deployment, error) {
	if err := c.gate(namespace); err != nil {
		return nil, err
	}
	if err := validateSubdomain("deployment", name); err != nil {
		return nil, err
	}
	got, err := c.cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, &NotFoundError{Kind: "Deployment", Namespace: namespace, Name: name}
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	return got, nil
}

// Pod fetches one pod.
func (c *Client) Pod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	if err := c.gate(namespace); err != nil {
		return nil, err
	}
	if err := validateSubdomain("pod", name); err != nil {
		return nil, err
	}
	got, err := c.cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, &NotFoundError{Kind: "Pod", Namespace: namespace, Name: name}
	}
	if err != nil {
		return nil, fmt.Errorf("get pod: %w", err)
	}
	return got, nil
}

func (c *Client) gate(namespace string) error {
	if c == nil || c.allow == nil {
		return ErrNotConfigured
	}
	if err := c.allow.Check(namespace); err != nil {
		return err
	}
	if c.cs == nil {
		if c.cause != nil {
			return c.cause
		}
		return ErrNotConfigured
	}
	return nil
}

func (c *Client) streamLogs(ctx context.Context, namespace, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error) {
	return c.cs.CoreV1().Pods(namespace).GetLogs(pod, opts).Stream(ctx)
}

func loadRESTConfig(cfg config.Kubernetes) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if cfg.Kubeconfig != "" {
		rules.ExplicitPath = cfg.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if cfg.Context != "" {
		overrides.CurrentContext = cfg.Context
	}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err == nil {
		return restCfg, nil
	}
	if cfg.Kubeconfig == "" && cfg.Context == "" && clientcmd.IsEmptyConfig(err) {
		if ic, icErr := rest.InClusterConfig(); icErr == nil {
			return ic, nil
		}
		return nil, ErrNotConfigured
	}
	if cfg.Kubeconfig != "" {
		return nil, fmt.Errorf("kubernetes kubeconfig %s: %w", cfg.Kubeconfig, err)
	}
	return nil, fmt.Errorf("kubernetes config: %w", err)
}

type nslist struct {
	names        map[string]struct{}
	order        []string
	unrestricted bool
}

func newNamespaceList(namespaces []string) (*nslist, error) {
	if len(namespaces) == 0 {
		return &nslist{unrestricted: true}, nil
	}
	list := &nslist{names: map[string]struct{}{}}
	for _, ns := range namespaces {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			return nil, errors.New("allowed namespace is empty")
		}
		if err := validateNamespace(ns); err != nil {
			return nil, err
		}
		if _, ok := list.names[ns]; ok {
			continue
		}
		list.names[ns] = struct{}{}
		list.order = append(list.order, ns)
	}
	return list, nil
}

func (n *nslist) Check(namespace string) error {
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	if n.unrestricted {
		return nil
	}
	if _, ok := n.names[namespace]; ok {
		return nil
	}
	return fmt.Errorf("namespace %q is not in the allowlist (%s)", namespace, strings.Join(n.order, ", "))
}

func validateNamespace(namespace string) error {
	if namespace == "" {
		return errors.New("namespace is required")
	}
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return fmt.Errorf("namespace %q: %s", namespace, strings.Join(errs, "; "))
	}
	return nil
}

func validateSubdomain(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s is required", kind)
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return fmt.Errorf("%s %q: %s", kind, name, strings.Join(errs, "; "))
	}
	return nil
}
