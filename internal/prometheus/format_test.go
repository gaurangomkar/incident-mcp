package prometheus

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/model"
)

func TestFormatVectorOrdersAndCaps(t *testing.T) {
	ts := model.TimeFromUnix(1_700_000_000)
	v := model.Vector{
		{Metric: model.Metric{"job": "api", "code": "200"}, Value: 1, Timestamp: ts},
		{Metric: model.Metric{"job": "api", "code": "500"}, Value: 9, Timestamp: ts},
		{Metric: model.Metric{"job": "api", "code": "404"}, Value: 4, Timestamp: ts},
	}
	got, truncated := Format(v, 2, 10)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(got, "showing 2 of 3 series") {
		t.Fatalf("header: %s", got)
	}
	first := strings.Split(got, "\n")[1]
	if !strings.Contains(first, `code="500"`) {
		t.Fatalf("highest value should be first, got %s", first)
	}
	if strings.Contains(got, `code="200"`) {
		t.Fatalf("dropped series still present:\n%s", got)
	}
}

func TestFormatMatrixKeepsLatestSamples(t *testing.T) {
	base := model.TimeFromUnix(1_700_000_000)
	values := make([]model.SamplePair, 5)
	for i := range values {
		values[i] = model.SamplePair{Timestamp: base.Add(time.Duration(i) * time.Minute), Value: model.SampleValue(i)}
	}
	m := model.Matrix{{
		Metric: model.Metric{"__name__": "up", "job": "api"},
		Values: values,
	}}
	got, truncated := Format(m, 10, 2)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(got, "showing last 2 of 5 samples") {
		t.Fatalf("note missing:\n%s", got)
	}
	if strings.Contains(got, " 0 @") {
		t.Fatalf("oldest sample kept:\n%s", got)
	}
	if !strings.Contains(got, "up{") {
		t.Fatalf("metric name missing:\n%s", got)
	}
}

func TestFormatEmpty(t *testing.T) {
	got, truncated := Format(model.Vector{}, 10, 10)
	if truncated || !strings.Contains(got, "0 series") {
		t.Fatalf("got %q truncated %v", got, truncated)
	}
}
