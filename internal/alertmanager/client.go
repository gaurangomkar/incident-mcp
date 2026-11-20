// Package alertmanager reads firing alerts and silences.
package alertmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gaurangomkar/incident-mcp/internal/config"
	"github.com/gaurangomkar/incident-mcp/internal/upstream"
)

// Alert is one Alertmanager alert.
type Alert struct {
	Fingerprint string
	Labels      map[string]string
	Annotations map[string]string
	StartsAt    time.Time
	EndsAt      time.Time
	State       string
	Receivers   []string
	SilencedBy  []string
	InhibitedBy []string
}

// Matcher is one silence matcher.
type Matcher struct {
	Name    string
	Value   string
	IsRegex bool
	IsEqual bool
}

// String renders the matcher the way Alertmanager displays it.
func (m Matcher) String() string {
	op := "="
	switch {
	case m.IsRegex && m.IsEqual:
		op = "=~"
	case m.IsRegex:
		op = "!~"
	case !m.IsEqual:
		op = "!="
	}
	return m.Name + op + m.Value
}

// Silence is one Alertmanager silence.
type Silence struct {
	ID        string
	State     string
	Matchers  []Matcher
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedBy string
	Comment   string
}

// Client reads alerts and silences. The HTTP implementation and tests both satisfy it.
type Client interface {
	Alerts(ctx context.Context) ([]Alert, error)
	Silences(ctx context.Context) ([]Silence, error)
}

// Remote is the Alertmanager HTTP API v2.
type Remote struct {
	base      string
	http      *http.Client
	userAgent string
}

// New builds a remote client. The URL is not dialed until the first call.
func New(cfg config.Endpoint, userAgent string) (*Remote, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("alertmanager url is empty")
	}
	hc, err := upstream.NewHTTPClient(upstream.AuthFromEndpoint(cfg, userAgent))
	if err != nil {
		return nil, err
	}
	return &Remote{base: strings.TrimRight(cfg.URL, "/"), http: hc, userAgent: userAgent}, nil
}

// Alerts returns alerts the API currently considers active, including silenced and inhibited ones.
// Callers filter.
func (r *Remote) Alerts(ctx context.Context) ([]Alert, error) {
	q := url.Values{}
	q.Set("active", "true")
	q.Set("silenced", "true")
	q.Set("inhibited", "true")
	var wire []wireAlert
	if err := r.get(ctx, "/api/v2/alerts", q, &wire); err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(wire))
	for _, a := range wire {
		receivers := make([]string, 0, len(a.Receivers))
		for _, recv := range a.Receivers {
			if recv.Name != "" {
				receivers = append(receivers, recv.Name)
			}
		}
		out = append(out, Alert{
			Fingerprint: a.Fingerprint,
			Labels:      a.Labels,
			Annotations: a.Annotations,
			StartsAt:    a.StartsAt,
			EndsAt:      a.EndsAt,
			State:       a.Status.State,
			Receivers:   receivers,
			SilencedBy:  a.Status.SilencedBy,
			InhibitedBy: a.Status.InhibitedBy,
		})
	}
	return out, nil
}

// Silences returns every silence the API knows about.
func (r *Remote) Silences(ctx context.Context) ([]Silence, error) {
	var wire []wireSilence
	if err := r.get(ctx, "/api/v2/silences", nil, &wire); err != nil {
		return nil, err
	}
	out := make([]Silence, 0, len(wire))
	for _, s := range wire {
		matchers := make([]Matcher, 0, len(s.Matchers))
		for _, m := range s.Matchers {
			equal := true
			if m.IsEqual != nil {
				equal = *m.IsEqual
			}
			matchers = append(matchers, Matcher{Name: m.Name, Value: m.Value, IsRegex: m.IsRegex, IsEqual: equal})
		}
		out = append(out, Silence{
			ID:        s.ID,
			State:     s.Status.State,
			Matchers:  matchers,
			StartsAt:  s.StartsAt,
			EndsAt:    s.EndsAt,
			CreatedBy: s.CreatedBy,
			Comment:   s.Comment,
		})
	}
	return out, nil
}

func (r *Remote) get(ctx context.Context, p string, q url.Values, dest any) error {
	u, err := url.Parse(r.base + p)
	if err != nil {
		return fmt.Errorf("alertmanager url: %w", err)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if r.userAgent != "" {
		req.Header.Set("User-Agent", r.userAgent)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("alertmanager: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("alertmanager status %d: %s", resp.StatusCode, upstream.ReadErrorBody(resp.Body))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dest); err != nil {
		return fmt.Errorf("alertmanager response: %w", err)
	}
	return nil
}

type wireAlert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
	Status      struct {
		State       string   `json:"state"`
		SilencedBy  []string `json:"silencedBy"`
		InhibitedBy []string `json:"inhibitedBy"`
	} `json:"status"`
	Receivers []struct {
		Name string `json:"name"`
	} `json:"receivers"`
	Fingerprint string `json:"fingerprint"`
}

type wireSilence struct {
	ID     string `json:"id"`
	Status struct {
		State string `json:"state"`
	} `json:"status"`
	Matchers []struct {
		Name    string `json:"name"`
		Value   string `json:"value"`
		IsRegex bool   `json:"isRegex"`
		IsEqual *bool  `json:"isEqual"`
	} `json:"matchers"`
	StartsAt  time.Time `json:"startsAt"`
	EndsAt    time.Time `json:"endsAt"`
	CreatedBy string    `json:"createdBy"`
	Comment   string    `json:"comment"`
}
