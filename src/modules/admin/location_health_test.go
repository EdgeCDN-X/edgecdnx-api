package admin

import (
	"encoding/json"
	"testing"

	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildLocationHealthDataMatchesLocationsNodesAndSources(t *testing.T) {
	queryResult := []prometheusVectorSample{
		{
			Metric: map[string]string{
				"cluster":  "fra1-ns1",
				"endpoint": "location",
				"instance": "http://74.220.24.46/healthz",
				"location": "nyc1-c1",
				"role":     "routing",
			},
			Value: mustRawMessages(t, `1775650533.969`, `"1"`),
		},
		{
			Metric: map[string]string{
				"cluster":  "fra1-ns1",
				"endpoint": "location",
				"instance": "http://74.220.31.184/healthz",
				"location": "fra1-c1-sub1",
				"role":     "routing",
			},
			Value: mustRawMessages(t, `1775650533.969`, `"0"`),
		},
		{
			Metric: map[string]string{
				"cluster":  "nyc1-ns1",
				"endpoint": "location",
				"instance": "http://74.220.31.184/healthz",
				"location": "fra1-c1-sub1",
				"role":     "routing",
			},
			Value: mustRawMessages(t, `1775650533.969`, `"1"`),
		},
		{
			Metric: map[string]string{
				"cluster":  "fra1-ns1",
				"endpoint": "location",
				"instance": "http://74.220.99.99/healthz",
				"location": "unknown-location",
				"role":     "routing",
			},
			Value: mustRawMessages(t, `1775650533.969`, `"0"`),
		},
	}

	response := buildLocationHealthData([]infrastructurev1alpha1.Location{
		{
			ObjectMeta: mustObjectMeta("nyc1-c1"),
			Spec: infrastructurev1alpha1.LocationSpec{
				Nodes: []infrastructurev1alpha1.NodeSpec{{Name: "nyc-router-1", Ipv4: "74.220.24.46"}},
			},
			Status: infrastructurev1alpha1.LocationStatus{Status: "Healthy"},
		},
		{
			ObjectMeta: mustObjectMeta("fra1-c1-sub1"),
			Spec: infrastructurev1alpha1.LocationSpec{
				MaintenanceMode: true,
				NodeGroups: []infrastructurev1alpha1.NodeGroupSpec{{
					Name:   "routing",
					Flavor: "main",
					Nodes:  []infrastructurev1alpha1.NodeSpec{{Name: "fra-router-1", Ipv4: "74.220.31.184"}},
				}},
			},
			Status: infrastructurev1alpha1.LocationStatus{Status: "Degraded"},
		},
	}, "vector", queryResult, nil)

	if response.Status != "" {
		t.Fatalf("expected empty status before envelope assignment, got %q", response.Status)
	}
	if len(response.Data.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(response.Data.Sources))
	}
	if len(response.Data.Locations) != 2 {
		t.Fatalf("expected 2 matched locations, got %d", len(response.Data.Locations))
	}
	if len(response.Data.UnmatchedMetrics) != 1 {
		t.Fatalf("expected 1 unmatched metric, got %d", len(response.Data.UnmatchedMetrics))
	}

	fraLocation := response.Data.Locations[0]
	if fraLocation.Name != "fra1-c1-sub1" {
		t.Fatalf("unexpected first location %q", fraLocation.Name)
	}
	if !fraLocation.MaintenanceMode {
		t.Fatal("expected fra1-c1-sub1 to be in maintenance mode")
	}
	if len(fraLocation.Sources) != 2 {
		t.Fatalf("expected 2 source groups, got %d", len(fraLocation.Sources))
	}
	if fraLocation.Sources[0].Source != "fra1-ns1" {
		t.Fatalf("unexpected source %q", fraLocation.Sources[0].Source)
	}
	if fraLocation.Sources[0].UnhealthyNodes != 1 {
		t.Fatalf("expected 1 unhealthy node, got %d", fraLocation.Sources[0].UnhealthyNodes)
	}
	if !fraLocation.Sources[0].Nodes[0].Matched {
		t.Fatal("expected matched node")
	}
	if fraLocation.Sources[0].Nodes[0].NodeGroupName != "routing" {
		t.Fatalf("unexpected node group %q", fraLocation.Sources[0].Nodes[0].NodeGroupName)
	}
	if fraLocation.Sources[0].Nodes[0].IP != "74.220.31.184" {
		t.Fatalf("unexpected matched ip %q", fraLocation.Sources[0].Nodes[0].IP)
	}

	if response.Data.UnmatchedMetrics[0].Location != "unknown-location" {
		t.Fatalf("unexpected unmatched location %q", response.Data.UnmatchedMetrics[0].Location)
	}
	if response.Data.UnmatchedMetrics[0].Source != "fra1-ns1" {
		t.Fatalf("unexpected unmatched source %q", response.Data.UnmatchedMetrics[0].Source)
	}
}

func TestBuildLocationHealthDataMarksNodeUnhealthyForFiringAlert(t *testing.T) {
	queryResult := []prometheusVectorSample{
		{
			Metric: map[string]string{
				"cluster":  "fra1-ns1",
				"endpoint": "location",
				"instance": "http://74.220.24.46/healthz",
				"location": "nyc1-c1",
				"role":     "routing",
			},
			Value: mustRawMessages(t, `1775650533.969`, `"1"`),
		},
	}
	alertResult := []prometheusVectorSample{
		{
			Metric: map[string]string{
				"alertname":  "PrometheusOutOfOrderTimestamps",
				"alertstate": "firing",
				"cluster":    "nyc1-c1",
			},
			Value: mustRawMessages(t, `1775650535.0`, `"1"`),
		},
	}

	response := buildLocationHealthData([]infrastructurev1alpha1.Location{
		{
			ObjectMeta: mustObjectMeta("nyc1-c1"),
			Spec: infrastructurev1alpha1.LocationSpec{
				NodeGroups: []infrastructurev1alpha1.NodeGroupSpec{{
					Name: "ssd",
					Nodes: []infrastructurev1alpha1.NodeSpec{{
						Name: "n1",
						Ipv4: "74.220.24.46",
						Alerts: []infrastructurev1alpha1.PrometheusAlertMatcherSpec{{
							AlertName: "PrometheusOutOfOrderTimestamps",
							Labels: map[string]string{
								"cluster": "nyc1-c1",
							},
						}},
					}},
				}},
			},
		},
	}, "vector", queryResult, alertResult)

	if len(response.Data.Locations) != 1 {
		t.Fatalf("expected 1 location, got %d", len(response.Data.Locations))
	}

	node := response.Data.Locations[0].Sources[0].Nodes[0]
	if node.Healthy {
		t.Fatal("expected node to be unhealthy when alert is firing")
	}
	if len(node.Alerts) != 1 {
		t.Fatalf("expected 1 node alert, got %d", len(node.Alerts))
	}
	if response.Data.Locations[0].Sources[0].UnhealthyNodes != 1 {
		t.Fatalf("expected 1 unhealthy node, got %d", response.Data.Locations[0].Sources[0].UnhealthyNodes)
	}
}

func TestBuildLocationHealthDataIncludesLocationScopedAlerts(t *testing.T) {
	alertResult := []prometheusVectorSample{
		{
			Metric: map[string]string{
				"alertname":  "LocationClusterDown",
				"alertstate": "firing",
				"cluster":    "fra1-c1",
			},
			Value: mustRawMessages(t, `1775650535.0`, `"1"`),
		},
	}

	response := buildLocationHealthData([]infrastructurev1alpha1.Location{
		{
			ObjectMeta: mustObjectMeta("fra1-c1"),
			Spec: infrastructurev1alpha1.LocationSpec{
				Alerts: []infrastructurev1alpha1.PrometheusAlertMatcherSpec{{
					AlertName: "LocationClusterDown",
					Labels: map[string]string{
						"cluster": "fra1-c1",
					},
				}},
			},
		},
	}, "vector", nil, alertResult)

	if len(response.Data.Locations) != 1 {
		t.Fatalf("expected 1 location, got %d", len(response.Data.Locations))
	}

	location := response.Data.Locations[0]
	if len(location.Alerts) != 1 {
		t.Fatalf("expected 1 location alert, got %d", len(location.Alerts))
	}
	if len(location.Sources) != 1 {
		t.Fatalf("expected 1 synthetic alert source, got %d", len(location.Sources))
	}
	if location.Sources[0].Source != locationHealthAlertSource {
		t.Fatalf("unexpected source %q", location.Sources[0].Source)
	}
	if location.Sources[0].UnhealthyNodes != 1 {
		t.Fatalf("expected 1 unhealthy synthetic alert target, got %d", location.Sources[0].UnhealthyNodes)
	}
	if location.Sources[0].Nodes[0].AlertScope != "location" {
		t.Fatalf("unexpected alert scope %q", location.Sources[0].Nodes[0].AlertScope)
	}
}

func TestNormalizeIPFromInstance(t *testing.T) {
	tests := map[string]string{
		"http://74.220.24.46/healthz":       "74.220.24.46",
		"https://[2001:db8::1]:443/healthz": "2001:db8::1",
		"74.220.24.46:8080":                 "74.220.24.46",
		"not-an-ip":                         "",
	}

	for input, expected := range tests {
		if got := normalizeIPFromInstance(input); got != expected {
			t.Fatalf("normalizeIPFromInstance(%q) = %q, want %q", input, got, expected)
		}
	}
}

func mustRawMessages(t *testing.T, rawValues ...string) []json.RawMessage {
	t.Helper()

	values := make([]json.RawMessage, 0, len(rawValues))
	for _, rawValue := range rawValues {
		values = append(values, json.RawMessage(rawValue))
	}

	return values
}

func mustObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name}
}
