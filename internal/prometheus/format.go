package prometheus

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/common/model"
)

// Format renders a Prometheus value with series and sample caps.
// The bool is true when a cap dropped data.
func Format(v model.Value, maxSeries, maxSamples int) (string, bool) {
	if maxSeries < 1 {
		maxSeries = 1
	}
	if maxSamples < 1 {
		maxSamples = 1
	}
	if v == nil {
		return "empty result\n", false
	}
	switch x := v.(type) {
	case model.Vector:
		return formatVector(x, maxSeries)
	case model.Matrix:
		return formatMatrix(x, maxSeries, maxSamples)
	case *model.Scalar:
		return fmt.Sprintf("scalar %s @ %s\n", x.Value, formatTime(x.Timestamp)), false
	case *model.String:
		return fmt.Sprintf("string %q @ %s\n", x.Value, formatTime(x.Timestamp)), false
	default:
		return x.String() + "\n", false
	}
}

func formatVector(v model.Vector, maxSeries int) (string, bool) {
	series := append(model.Vector(nil), v...)
	slices.SortStableFunc(series, func(a, b *model.Sample) int {
		an, bn := math.IsNaN(float64(a.Value)), math.IsNaN(float64(b.Value))
		if an != bn {
			if an {
				return 1
			}
			return -1
		}
		if a.Value != b.Value {
			if a.Value > b.Value {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Metric.String(), b.Metric.String())
	})
	shown := series
	truncated := false
	if len(shown) > maxSeries {
		shown = shown[:maxSeries]
		truncated = true
	}
	var b strings.Builder
	if truncated {
		fmt.Fprintf(&b, "vector, showing %d of %d series\n", len(shown), len(series))
	} else {
		fmt.Fprintf(&b, "vector, %d series\n", len(series))
	}
	for _, sample := range shown {
		fmt.Fprintf(&b, "%s %s @ %s\n", sample.Metric, sample.Value, formatTime(sample.Timestamp))
	}
	return b.String(), truncated
}

func formatMatrix(m model.Matrix, maxSeries, maxSamples int) (string, bool) {
	series := append(model.Matrix(nil), m...)
	slices.SortStableFunc(series, func(a, b *model.SampleStream) int {
		return strings.Compare(a.Metric.String(), b.Metric.String())
	})
	truncated := false
	shown := series
	if len(shown) > maxSeries {
		shown = shown[:maxSeries]
		truncated = true
	}
	var b strings.Builder
	if truncated {
		fmt.Fprintf(&b, "matrix, showing %d of %d series\n", len(shown), len(series))
	} else {
		fmt.Fprintf(&b, "matrix, %d series\n", len(series))
	}
	for _, stream := range shown {
		fmt.Fprintf(&b, "%s\n", stream.Metric)
		values := stream.Values
		if len(values) > maxSamples {
			fmt.Fprintf(&b, "  showing last %d of %d samples\n", maxSamples, len(values))
			values = values[len(values)-maxSamples:]
			truncated = true
		}
		for _, pair := range values {
			fmt.Fprintf(&b, "  %s @ %s\n", pair.Value, formatTime(pair.Timestamp))
		}
	}
	return b.String(), truncated
}

func formatTime(t model.Time) string {
	return t.Time().UTC().Format(time.RFC3339)
}
