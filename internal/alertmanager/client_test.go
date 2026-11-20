package alertmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gaurangomkar/incident-mcp/internal/config"
)

func TestRemoteParsesAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/alertmanager/api/v2/alerts":
			if r.URL.Query().Get("silenced") != "true" {
				t.Errorf("query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{
				"labels": {"alertname": "CheckoutDown", "severity": "critical"},
				"annotations": {"summary": "checkout is down"},
				"startsAt": "2026-11-20T01:00:00Z",
				"endsAt": "2026-11-20T02:00:00Z",
				"status": {"state": "active", "silencedBy": [], "inhibitedBy": ["abc"]},
				"receivers": [{"name": "slack"}],
				"fingerprint": "fp1"
			}]`))
		case "/alertmanager/api/v2/silences":
			_, _ = w.Write([]byte(`[{
				"id": "sil1",
				"status": {"state": "active"},
				"matchers": [{"name": "alertname", "value": "CheckoutDown", "isRegex": false}],
				"startsAt": "2026-11-20T01:00:00Z",
				"endsAt": "2026-11-20T04:00:00Z",
				"createdBy": "alice",
				"comment": "deploy"
			}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	remote, err := New(config.Endpoint{
		URL:         srv.URL + "/alertmanager",
		Timeout:     config.Duration(2 * time.Second),
		BearerToken: "t",
	}, "incident-mcp/test")
	if err != nil {
		t.Fatal(err)
	}
	alerts, err := remote.Alerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].Labels["alertname"] != "CheckoutDown" || alerts[0].Receivers[0] != "slack" {
		t.Fatalf("alerts %+v", alerts)
	}
	if len(alerts[0].InhibitedBy) != 1 {
		t.Fatalf("inhibited %+v", alerts[0].InhibitedBy)
	}
	silences, err := remote.Silences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(silences) != 1 || !silences[0].Matchers[0].IsEqual || silences[0].Matchers[0].String() != "alertname=CheckoutDown" {
		t.Fatalf("silences %+v", silences)
	}
}

func TestRemoteStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	remote, err := New(config.Endpoint{URL: srv.URL, Timeout: config.Duration(time.Second)}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Alerts(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
