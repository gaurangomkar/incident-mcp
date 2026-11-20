// Package upstream builds HTTP clients for Prometheus and Alertmanager.
package upstream

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gaurangomkar/incident-mcp/internal/config"
)

const maxTokenBytes = 8192

// Auth is the credential set shared by the read-only upstreams.
type Auth struct {
	BearerToken        string
	BearerTokenFile    string
	Username           string
	Password           string
	InsecureSkipVerify bool
	Timeout            time.Duration
	UserAgent          string
}

// AuthFromEndpoint copies credentials off a configured endpoint.
func AuthFromEndpoint(e config.Endpoint, userAgent string) Auth {
	return Auth{
		BearerToken:        e.BearerToken,
		BearerTokenFile:    e.BearerTokenFile,
		Username:           e.Username,
		Password:           e.Password,
		InsecureSkipVerify: e.InsecureSkipVerify,
		Timeout:            e.Timeout.Std(),
		UserAgent:          userAgent,
	}
}

// NewHTTPClient returns a client that adds auth on every request.
// A bearer token file is read per request so rotated in-cluster tokens work.
func NewHTTPClient(a Auth) (*http.Client, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		return nil, errors.New("default http transport is not available")
	}
	transport := base.Clone()
	if a.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // operator opt-in for lab clusters
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &authTransport{
			base:      transport,
			token:     a.BearerToken,
			tokenFile: a.BearerTokenFile,
			username:  a.Username,
			password:  a.Password,
			userAgent: a.UserAgent,
		},
	}, nil
}

type authTransport struct {
	base      http.RoundTripper
	token     string
	tokenFile string
	username  string
	password  string
	userAgent string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token := t.token
	if t.tokenFile != "" {
		loaded, err := readToken(t.tokenFile)
		if err != nil {
			return nil, err
		}
		token = loaded
	}
	cloned := req.Clone(req.Context())
	if token != "" {
		cloned.Header.Set("Authorization", "Bearer "+token)
	}
	if t.username != "" {
		cloned.SetBasicAuth(t.username, t.password)
	}
	if t.userAgent != "" {
		cloned.Header.Set("User-Agent", t.userAgent)
	}
	return t.base.RoundTrip(cloned)
}

func readToken(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read bearer token file: %w", err)
	}
	defer func() { _ = f.Close() }()
	buf, err := io.ReadAll(io.LimitReader(f, maxTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("read bearer token file: %w", err)
	}
	if len(buf) > maxTokenBytes {
		return "", errors.New("bearer token file is larger than 8192 bytes")
	}
	return strings.TrimSpace(string(buf)), nil
}

// ReadErrorBody returns a short, single-line excerpt of an error response.
func ReadErrorBody(r io.Reader) string {
	buf, _ := io.ReadAll(io.LimitReader(r, 512))
	text := strings.TrimSpace(string(buf))
	text = strings.ReplaceAll(text, "\n", " ")
	return text
}
