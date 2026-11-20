package upstream

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBearerTokenFilePerRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("first-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		if r.Header.Get("User-Agent") != "incident-mcp/test" {
			t.Errorf("user agent %q", r.Header.Get("User-Agent"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	client, err := NewHTTPClient(Auth{
		BearerTokenFile: path,
		Timeout:         5 * time.Second,
		UserAgent:       "incident-mcp/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	get(t, client, srv.URL)
	if err := os.WriteFile(path, []byte("second-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	get(t, client, srv.URL)
	if len(got) != 2 || got[0] != "Bearer first-token" || got[1] != "Bearer second-token" {
		t.Fatalf("auth headers %v", got)
	}
}

func TestBasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "alice" || pass != "s3cret" {
			t.Errorf("basic auth %q %q ok %v", user, pass, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	client, err := NewHTTPClient(Auth{Username: "alice", Password: "s3cret", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	get(t, client, srv.URL)
}

func get(t *testing.T, client *http.Client, url string) {
	t.Helper()
	res, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	_, _ = io.Copy(io.Discard, res.Body)
}
