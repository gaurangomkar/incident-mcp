package output

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	got, truncated := Truncate("hello", 100)
	if truncated || got != "hello" {
		t.Fatalf("got %q truncated %v", got, truncated)
	}
	got, truncated = Truncate("hello world", 10)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if got != marker {
		t.Fatalf("tiny budget got %q", got)
	}

	in := "héllo world, this is longer than the budget"
	max := len(marker) + 7
	got, truncated = Truncate(in, max)
	if !truncated {
		t.Fatal("expected truncation")
	}
	body := strings.TrimSuffix(got, marker)
	if !utf8.ValidString(body) {
		t.Fatalf("cut mid-rune: %q", body)
	}
	if len(got) > max {
		t.Fatalf("len %d max %d", len(got), max)
	}
}

func TestTruncateDisabled(t *testing.T) {
	got, truncated := Truncate("hello", 0)
	if truncated || got != "hello" {
		t.Fatalf("got %q", got)
	}
}
