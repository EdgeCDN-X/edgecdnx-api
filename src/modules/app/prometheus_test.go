package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewPrometheusNormalizesHTTPAddress(t *testing.T) {
	prometheus, err := NewPrometheus(PrometheusConfig{Endpoint: "prometheus.monitoring.svc:9090"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prometheus == nil {
		t.Fatal("expected prometheus client")
	}
	if got := prometheus.endpoint.String(); got != "http://prometheus.monitoring.svc:9090" {
		t.Fatalf("unexpected endpoint %q", got)
	}
}

func TestNewPrometheusRejectsHTTPS(t *testing.T) {
	_, err := NewPrometheus(PrometheusConfig{Endpoint: "https://prometheus.monitoring.svc:9090"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPrometheusQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("query"); got != "up" {
			t.Fatalf("unexpected query %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	prometheus, err := NewPrometheus(PrometheusConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	response, err := prometheus.Query(context.Background(), "up")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if response.Status != "success" {
		t.Fatalf("unexpected status %q", response.Status)
	}
	if response.Data.ResultType != "vector" {
		t.Fatalf("unexpected result type %q", response.Data.ResultType)
	}
}
