package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var buf bytes.Buffer
	if code := run([]string{"-version"}, &buf); code != 0 {
		t.Fatalf("code %d output %s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "incident-mcp") {
		t.Fatalf("output %s", buf.String())
	}
}

func TestMissingConfig(t *testing.T) {
	var buf bytes.Buffer
	code := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")}, &buf)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(buf.String(), "config") {
		t.Fatalf("output %s", buf.String())
	}
}
