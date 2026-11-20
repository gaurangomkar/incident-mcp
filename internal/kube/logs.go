package kube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const logHardCap = 1 << 20

// Logs reads the tail of one container. truncated is true when a cap dropped bytes or lines.
func (c *Client) Logs(ctx context.Context, namespace, pod, container string, tail int64, previous bool, maxBytes int) (string, bool, error) {
	obj, err := c.Pod(ctx, namespace, pod)
	if err != nil {
		return "", false, err
	}
	resolved, err := resolveContainer(*obj, container)
	if err != nil {
		return "", false, err
	}
	if tail <= 0 {
		tail = 100
	}
	opts := &corev1.PodLogOptions{
		Container: resolved,
		TailLines: &tail,
		Previous:  previous,
	}
	rc, err := c.logs(ctx, namespace, pod, opts)
	if err != nil {
		return "", false, fmt.Errorf("read logs for %s/%s: %w", namespace, pod, err)
	}
	defer func() { _ = rc.Close() }()
	text, truncated, err := readTail(rc, int(tail), maxBytes)
	if err != nil {
		return "", false, fmt.Errorf("read logs for %s/%s: %w", namespace, pod, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "pod %s/%s container %s", namespace, pod, resolved)
	if previous {
		b.WriteString(" previous")
	}
	fmt.Fprintf(&b, " tail %d\n", tail)
	if truncated {
		b.WriteString("log truncated to the configured line or byte limit\n")
	}
	b.WriteString(text)
	return b.String(), truncated, nil
}

func resolveContainer(pod corev1.Pod, want string) (string, error) {
	regular := containerNames(pod.Spec.Containers)
	init := containerNames(pod.Spec.InitContainers)
	if want != "" {
		if errs := validation.IsDNS1123Label(want); len(errs) > 0 {
			return "", fmt.Errorf("container %q: %s", want, strings.Join(errs, "; "))
		}
		for _, name := range append(append([]string{}, regular...), init...) {
			if name == want {
				return want, nil
			}
		}
		have := regular
		if len(have) == 0 {
			have = init
		}
		return "", fmt.Errorf("container %q not found on pod %s (have %s)", want, pod.Name, strings.Join(have, ", "))
	}
	switch len(regular) {
	case 1:
		return regular[0], nil
	case 0:
		if len(init) == 1 {
			return init[0], nil
		}
		return "", fmt.Errorf("pod %s has no containers", pod.Name)
	default:
		return "", fmt.Errorf("pod %s has multiple containers (%s); set container", pod.Name, strings.Join(regular, ", "))
	}
}

func containerNames(containers []corev1.Container) []string {
	names := make([]string, 0, len(containers))
	for _, c := range containers {
		names = append(names, c.Name)
	}
	return names
}

// readTail keeps the last lines and the last maxBytes of r.
func readTail(r io.Reader, lines, maxBytes int) (string, bool, error) {
	buf, err := io.ReadAll(io.LimitReader(r, int64(logHardCap)+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(buf) > logHardCap
	if truncated {
		buf = buf[len(buf)-logHardCap:]
		if i := bytes.IndexByte(buf, '\n'); i >= 0 && i < len(buf)-1 {
			buf = buf[i+1:]
		}
		buf = trimPartialRune(buf)
	}
	text := string(buf)
	parts := strings.Split(text, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if lines > 0 && len(parts) > lines {
		parts = parts[len(parts)-lines:]
		truncated = true
	}
	out := strings.Join(parts, "\n")
	if maxBytes > 0 && len(out) > maxBytes {
		out = out[len(out)-maxBytes:]
		out = string(trimPartialRune([]byte(out)))
		if i := strings.IndexByte(out, '\n'); i >= 0 && i < len(out)-1 {
			out = out[i+1:]
		}
		truncated = true
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, truncated, nil
}

func trimPartialRune(buf []byte) []byte {
	for len(buf) > 0 && !utf8.RuneStart(buf[0]) {
		buf = buf[1:]
	}
	return buf
}
