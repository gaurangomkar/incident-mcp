package alertmanager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gaurangomkar/incident-mcp/internal/mcptool"
)

type fakeClient struct {
	alerts   []Alert
	silences []Silence
	err      error
}

func (f *fakeClient) Alerts(context.Context) ([]Alert, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.alerts, nil
}

func (f *fakeClient) Silences(context.Context) ([]Silence, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.silences, nil
}

func TestAlertsFiltersAndOrders(t *testing.T) {
	now := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	fake := &fakeClient{alerts: []Alert{
		{
			Fingerprint: "old",
			Labels:      map[string]string{"alertname": "Old", "severity": "warning", "namespace": "payments"},
			StartsAt:    now.Add(-2 * time.Hour),
			Receivers:   []string{"slack"},
		},
		{
			Fingerprint: "new",
			Labels:      map[string]string{"alertname": "CheckoutDown", "severity": "critical", "namespace": "payments"},
			Annotations: map[string]string{"summary": "checkout is down"},
			StartsAt:    now.Add(-10 * time.Minute),
			Receivers:   []string{"slack"},
		},
		{
			Fingerprint: "silenced",
			Labels:      map[string]string{"alertname": "Silenced", "severity": "critical", "namespace": "payments"},
			StartsAt:    now.Add(-time.Minute),
			SilencedBy:  []string{"s1"},
		},
		{
			Fingerprint: "other",
			Labels:      map[string]string{"alertname": "Other", "severity": "critical", "namespace": "other"},
			StartsAt:    now.Add(-time.Minute),
			Receivers:   []string{"email"},
		},
	}}
	opt := Options{
		Client:    fake,
		MaxAlerts: 10,
		Runtime:   mcptool.Runtime{Timeout: time.Second, Now: func() time.Time { return now }},
	}
	text, err := alerts(context.Background(), opt, alertsInput{
		Matchers: []string{"severity=critical", "namespace=payments"},
		Receiver: "slack",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "Silenced") || strings.Contains(text, "Other") || strings.Contains(text, "Old") {
		t.Fatalf("filter failed:\n%s", text)
	}
	if !strings.Contains(text, "CheckoutDown") || !strings.Contains(text, "checkout is down") {
		t.Fatalf("text:\n%s", text)
	}
	if !strings.Contains(text, "firing for 10m0s") {
		t.Fatalf("age missing:\n%s", text)
	}

	text, err = alerts(context.Background(), opt, alertsInput{IncludeSilenced: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "truncated") || !strings.Contains(text, "Silenced") {
		t.Fatalf("limit text:\n%s", text)
	}
}

func TestSilencesState(t *testing.T) {
	now := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	opt := Options{
		Client: &fakeClient{silences: []Silence{
			{ID: "a", State: "active", EndsAt: now.Add(time.Hour), CreatedBy: "alice", Comment: "deploy", Matchers: []Matcher{{Name: "alertname", Value: "X", IsEqual: true}}},
			{ID: "e", State: "expired", EndsAt: now.Add(-time.Hour), CreatedBy: "bob"},
		}},
		MaxAlerts: 10,
		Runtime:   mcptool.Runtime{Timeout: time.Second, Now: func() time.Time { return now }},
	}
	text, err := silences(context.Background(), opt, silencesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "id=a") || strings.Contains(text, "id=e") {
		t.Fatalf("text:\n%s", text)
	}
	if _, err := silences(context.Background(), opt, silencesInput{State: "nope"}); err == nil {
		t.Fatal("expected bad state")
	}
}

func TestNotConfigured(t *testing.T) {
	if _, err := alerts(context.Background(), Options{}, alertsInput{}); err == nil {
		t.Fatal("expected error")
	}
}
