package prometheus

import (
	"context"
	"fmt"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	"github.com/gaurangomkar/incident-mcp/internal/config"
	"github.com/gaurangomkar/incident-mcp/internal/upstream"
)

// Querier is the read API the tools use. Tests and alternate backends implement it.
type Querier interface {
	Query(ctx context.Context, query string, ts time.Time) (model.Value, promv1.Warnings, error)
	QueryRange(ctx context.Context, query string, rng promv1.Range) (model.Value, promv1.Warnings, error)
}

// Client talks to a Prometheus HTTP API.
type Client struct {
	api promv1.API
}

// New builds a client. The URL is not dialed until the first query.
func New(cfg config.Endpoint, userAgent string) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("prometheus url is empty")
	}
	hc, err := upstream.NewHTTPClient(upstream.AuthFromEndpoint(cfg, userAgent))
	if err != nil {
		return nil, err
	}
	raw, err := promapi.NewClient(promapi.Config{Address: cfg.URL, Client: hc})
	if err != nil {
		return nil, fmt.Errorf("prometheus client: %w", err)
	}
	return &Client{api: promv1.NewAPI(raw)}, nil
}

// Query runs an instant query.
func (c *Client) Query(ctx context.Context, query string, ts time.Time) (model.Value, promv1.Warnings, error) {
	return c.api.Query(ctx, query, ts)
}

// QueryRange runs a range query.
func (c *Client) QueryRange(ctx context.Context, query string, rng promv1.Range) (model.Value, promv1.Warnings, error) {
	return c.api.QueryRange(ctx, query, rng)
}
