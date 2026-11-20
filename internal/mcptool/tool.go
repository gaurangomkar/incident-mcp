// Package mcptool holds the small bits every incident-mcp tool shares:
// read-only annotations, a timeout, redaction, and truncation.
package mcptool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaurangomkar/incident-mcp/internal/output"
	"github.com/gaurangomkar/incident-mcp/internal/redact"
)

// Annotations marks a tool as a read-only query against configured systems.
func Annotations(title string) *mcp.ToolAnnotations {
	closed := false
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		DestructiveHint: &closed,
		IdempotentHint:  true,
		OpenWorldHint:   &closed,
	}
}

// Runtime is the per-call policy passed to every tool package.
type Runtime struct {
	Logger   *slog.Logger
	Redactor *redact.Redactor
	Timeout  time.Duration
	MaxBytes int
	Now      func() time.Time
}

// At returns the injected clock, or time.Now.
func (r Runtime) At() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

// Call runs fn with a timeout and turns the string into a tool result.
// Errors are redacted and returned as tool errors, not protocol errors.
func (r Runtime) Call(ctx context.Context, name string, fn func(context.Context) (string, error)) (*mcp.CallToolResult, any, error) {
	logger := r.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	text, err := fn(ctx)
	elapsed := time.Since(started)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s", timeout)
		}
		msg := r.Redactor.Apply(err.Error())
		if msg == "" {
			msg = "unknown error"
		}
		logger.Warn("tool failed", "tool", name, "duration", elapsed, "err", msg)
		return nil, nil, errors.New(msg)
	}
	text = r.Redactor.Apply(text)
	text, _ = output.Truncate(text, r.maxBytes())
	logger.Info("tool completed", "tool", name, "duration", elapsed, "bytes", len(text))
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func (r Runtime) maxBytes() int {
	if r.MaxBytes <= 0 {
		return 32 * 1024
	}
	return r.MaxBytes
}

// ResultText joins the text content of a tool result. Tests use it.
func ResultText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var out string
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			out += text.Text
		}
	}
	return out
}
