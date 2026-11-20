package prometheus

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gaurangomkar/incident-mcp/internal/config"
)

func TestClientQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("path %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if !strings.Contains(string(body), "query=up") && !strings.Contains(r.URL.RawQuery, "query=up") {
			t.Errorf("query body %q raw %q", body, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up","job":"api"},"value":[1700000000,"1"]}]}}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(config.Endpoint{URL: srv.URL, Timeout: config.Duration(2 * time.Second)}, "incident-mcp/test")
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := c.Query(context.Background(), "up", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	text, truncated := Format(v, 10, 10)
	if truncated || !strings.Contains(text, `job="api"`) || !strings.Contains(text, " 1 @") {
		t.Fatalf("text %q", text)
	}
}
