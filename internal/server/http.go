package server

import (
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler is the streamable HTTP endpoint plus health checks.
// /mcp enforces the bearer token when one is configured. /healthz does not.
func (s *Server) Handler() http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.mcp
	}, &mcp.StreamableHTTPOptions{
		Logger:         s.log,
		SessionTimeout: 30 * time.Minute,
	})
	mux := http.NewServeMux()
	mux.Handle("/mcp", s.auth(mcpHandler))
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("HEAD /healthz", s.health)
	mux.HandleFunc("GET /{$}", s.root)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Server) root(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = fmt.Fprintf(w, "incident-mcp %s\n", s.version)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := loadToken(s.cfg.Server.BearerToken, s.cfg.Server.BearerTokenFile)
		if err != nil {
			s.log.Warn("bearer token file unreadable")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if token != "" && !authorized(r.Header.Get("Authorization"), token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authorized(header, token string) bool {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header[len(prefix):]), []byte(token)) == 1
}

func loadToken(static, path string) (string, error) {
	if path == "" {
		return static, nil
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(buf) > 8192 {
		return "", fmt.Errorf("bearer token file is larger than 8192 bytes")
	}
	return strings.TrimSpace(string(buf)), nil
}
