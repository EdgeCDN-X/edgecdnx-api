package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultPrometheusTimeout = 10 * time.Second

type PrometheusConfig struct {
	Endpoint string
	Timeout  time.Duration
}

type Prometheus struct {
	client   *http.Client
	endpoint *url.URL
}

type PrometheusQueryResponse struct {
	Status    string              `json:"status"`
	Data      PrometheusQueryData `json:"data"`
	ErrorType string              `json:"errorType,omitempty"`
	Error     string              `json:"error,omitempty"`
}

type PrometheusQueryData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

func NewPrometheus(cfg PrometheusConfig) (*Prometheus, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nil
	}

	endpoint, err := normalizePrometheusEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultPrometheusTimeout
	}

	return &Prometheus{
		client:   &http.Client{Timeout: timeout},
		endpoint: endpoint,
	}, nil
}

func (p *Prometheus) Query(ctx context.Context, query string) (*PrometheusQueryResponse, error) {
	if p == nil {
		return nil, errors.New("prometheus client is not configured")
	}

	requestURL := *p.endpoint
	requestURL.Path = strings.TrimRight(requestURL.Path, "/") + "/api/v1/query"
	if requestURL.Path == "/api/v1/query" {
		requestURL.Path = "/api/v1/query"
	}

	params := requestURL.Query()
	params.Set("query", query)
	requestURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("prometheus query failed with status %d", resp.StatusCode)
	}

	decoded := &PrometheusQueryResponse{}
	if err := json.NewDecoder(resp.Body).Decode(decoded); err != nil {
		return nil, err
	}

	if decoded.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed: %s %s", decoded.ErrorType, decoded.Error)
	}

	return decoded, nil
}

func normalizePrometheusEndpoint(rawEndpoint string) (*url.URL, error) {
	endpoint := strings.TrimSpace(rawEndpoint)
	if endpoint == "" {
		return nil, nil
	}

	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid prometheus endpoint: %w", err)
	}

	if parsed.Scheme != "http" {
		return nil, fmt.Errorf("prometheus endpoint must use http")
	}

	if parsed.Host == "" {
		return nil, fmt.Errorf("prometheus endpoint must include a host")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return parsed, nil
}
