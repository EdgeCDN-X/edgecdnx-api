package locations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

type fakeHealthcheckReader struct {
	records  []app.HealthcheckRecord
	err      error
	location string
	since    time.Time
}

func (f *fakeHealthcheckReader) RecentHealthchecks(_ context.Context, location string, since time.Time) ([]app.HealthcheckRecord, error) {
	f.location, f.since = location, since
	return f.records, f.err
}

func seedLocationWithNodes(t *testing.T, module *Module) {
	t.Helper()
	seedActiveProfile(t, module, "default", "project-a", []infrastructurev1alpha1.HealthCheckProbeSpec{{
		Name: "http", Type: infrastructurev1alpha1.HealthCheckProbeTypeHTTP,
		HTTP: &infrastructurev1alpha1.HTTPHealthCheckProbeSpec{Stack: infrastructurev1alpha1.StackTypeIPv4},
	}})
	location := &infrastructurev1alpha1.Location{
		TypeMeta: metav1.TypeMeta{APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(), Kind: "Location"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "fra1-c1", Namespace: module.cfg.Namespace,
			Labels: map[string]string{locationTenantLabel: "project-a"},
		},
		Spec: infrastructurev1alpha1.LocationSpec{NodeGroups: []infrastructurev1alpha1.NodeGroupSpec{{
			Name: "nginx", Flavor: "default", HealthCheck: &corev1.LocalObjectReference{Name: "default"},
			Nodes: []infrastructurev1alpha1.NodeSpec{{Name: "n2", Ipv4: "74.220.31.184"}, {Name: "n1", Ipv4: "74.220.31.183"}, {Name: "n3"}},
		}}},
	}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(location)
	if err != nil {
		t.Fatalf("convert test location: %v", err)
	}
	if _, err := module.client.Resource(locationGVR).Namespace(module.cfg.Namespace).Create(context.Background(), &unstructured.Unstructured{Object: object}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed location: %v", err)
	}
}

func seedActiveProfile(t *testing.T, module *Module, name, tenant string, probes []infrastructurev1alpha1.HealthCheckProbeSpec) {
	t.Helper()
	profile := &infrastructurev1alpha1.HealthCheckProfile{
		TypeMeta: metav1.TypeMeta{APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(), Kind: "HealthCheckProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: module.cfg.Namespace,
			Labels: map[string]string{locationTenantLabel: tenant},
		},
		Spec: infrastructurev1alpha1.HealthCheckProfileSpec{Probes: probes},
	}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.client.Resource(healthCheckProfileGVR).Namespace(module.cfg.Namespace).Create(context.Background(), &unstructured.Unstructured{Object: object}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestLocationHealthchecksGroupsByNodeAndCheck(t *testing.T) {
	module := newLocationTestModule(t)
	seedLocationWithNodes(t, module)
	now := time.Now().UTC()
	reader := &fakeHealthcheckReader{records: []app.HealthcheckRecord{
		// newest first, as returned by the database
		{Time: now, Name: "http", Type: "HTTP", Node: "n2", Code: ptr(int32(-1)), Message: "connection refused", Target: "74.220.31.184", Alive: false, Duration: ptr(int64(3599415))},
		{Time: now.Add(-10 * time.Second), Name: "http", Type: "HTTP", Node: "n1", Code: ptr(int32(200)), Message: "OK", Target: "74.220.31.183", Alive: true},
		{Time: now.Add(-20 * time.Second), Name: "http", Type: "HTTP", Node: "n2", Code: ptr(int32(200)), Message: "OK", Target: "74.220.31.184", Alive: true},
		{Time: now.Add(-30 * time.Second), Name: "http", Type: "HTTP", Node: "n2", Code: ptr(int32(200)), Message: "OK", Target: "74.220.31.184", Alive: true},
		{Time: now.Add(-40 * time.Second), Name: "healthz", Type: "HTTP", Node: "n9", Code: ptr(int32(200)), Message: "OK", Target: "10.0.0.9", Alive: true},
	}}
	module.SetHealthcheckDB(reader)
	router := gin.New()
	module.RegisterRoutes(router)

	recorder := performJSONRequest(router, http.MethodGet, "/project/project-a/locations/fra1-c1/healthchecks?window=5m&limit=2", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if reader.location != "edgecdnx/fra1-c1" {
		t.Fatalf("expected namespaced location key, got %q", reader.location)
	}
	if since := now.Sub(reader.since); since < 4*time.Minute || since > 6*time.Minute {
		t.Fatalf("expected window of 5m, got %s", since)
	}

	var response locationHealthchecksResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Nodes) != 3 {
		t.Fatalf("expected only 3 configured nodes, got %d", len(response.Nodes))
	}
	names := []string{response.Nodes[0].Name, response.Nodes[1].Name, response.Nodes[2].Name}
	if names[0] != "n2" || names[1] != "n1" || names[2] != "n3" {
		t.Fatalf("unexpected node order %v", names)
	}

	n2 := response.Nodes[0]
	if n2.Status != nodeHealthUnhealthy || !n2.Configured || n2.NodeGroup != "nginx" || len(n2.Checks) != 1 {
		t.Fatalf("unexpected n2 report %+v", n2)
	}
	results := n2.Checks[0].Results
	if len(results) != 2 {
		t.Fatalf("expected limit of 2 results, got %d", len(results))
	}
	if !results[0].Alive || results[1].Alive {
		t.Fatalf("expected results oldest to newest, got %+v", results)
	}
	if results[1].DurationMs == nil || *results[1].DurationMs < 3.59 || *results[1].DurationMs > 3.6 {
		t.Fatalf("expected duration converted to milliseconds, got %v", results[1].DurationMs)
	}
	if response.Nodes[1].Status != nodeHealthHealthy {
		t.Fatalf("expected n1 healthy, got %s", response.Nodes[1].Status)
	}
	if response.Nodes[2].Status != nodeHealthUnknown || len(response.Nodes[2].Checks) != 0 {
		t.Fatalf("expected n3 unknown without checks, got %+v", response.Nodes[2])
	}
}

func TestLocationHealthchecksRefreshActiveProfiles(t *testing.T) {
	module := newLocationTestModule(t)
	seedLocationWithNodes(t, module)
	reader := &fakeHealthcheckReader{records: []app.HealthcheckRecord{
		{Time: time.Now(), Node: "n2", Name: "removed", Type: "HTTP", Target: "74.220.31.184", Alive: false},
		{Time: time.Now(), Node: "n2", Name: "http", Type: "HTTP", Target: "74.220.31.184", Alive: true},
	}}
	module.SetHealthcheckDB(reader)
	resource := module.client.Resource(healthCheckProfileGVR).Namespace(module.cfg.Namespace)
	profile, err := resource.Get(context.Background(), "default", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	original := profile.DeepCopy()
	probes, _, err := unstructured.NestedSlice(profile.Object, "spec", "probes")
	if err != nil {
		t.Fatal(err)
	}
	removed := runtime.DeepCopyJSONValue(probes[0]).(map[string]interface{})
	removed["name"] = "removed"
	if err := unstructured.SetNestedSlice(profile.Object, append(probes, removed), "spec", "probes"); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Update(context.Background(), profile, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	module.RegisterRoutes(router)
	readReport := func() nodeHealthcheckReport {
		t.Helper()
		recorder := performJSONRequest(router, http.MethodGet, "/project/project-a/locations/fra1-c1/healthchecks", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
		}
		var response locationHealthchecksResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Nodes[0]
	}
	before := readReport()
	if before.Status != nodeHealthUnhealthy || len(before.Checks) != 2 {
		t.Fatalf("expected failing probe to affect status before removal: %+v", before)
	}
	if _, err := resource.Update(context.Background(), original, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	after := readReport()
	if after.Status != nodeHealthHealthy || len(after.Checks) != 1 || after.Checks[0].Name != "http" {
		t.Fatalf("removed probe must not appear or affect status on next poll: %+v", after)
	}
	locations := module.client.Resource(locationGVR).Namespace(module.cfg.Namespace)
	location, err := locations.Get(context.Background(), "fra1-c1", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	groups, _, err := unstructured.NestedSlice(location.Object, "spec", "nodeGroups")
	if err != nil {
		t.Fatal(err)
	}
	delete(groups[0].(map[string]interface{}), "healthCheck")
	if err := unstructured.SetNestedSlice(location.Object, groups, "spec", "nodeGroups"); err != nil {
		t.Fatal(err)
	}
	if _, err := locations.Update(context.Background(), location, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	disabled := readReport()
	if disabled.Status != nodeHealthUnknown || len(disabled.Checks) != 0 {
		t.Fatalf("clearing a profile reference must exclude historical checks: %+v", disabled)
	}
}

func TestActiveLocationChecksUseNodeProfilesAndCurrentTargets(t *testing.T) {
	module := newLocationTestModule(t)
	seedActiveProfile(t, module, "group", "project-a", []infrastructurev1alpha1.HealthCheckProbeSpec{
		{Name: "http", Type: infrastructurev1alpha1.HealthCheckProbeTypeHTTP, HTTP: &infrastructurev1alpha1.HTTPHealthCheckProbeSpec{Stack: infrastructurev1alpha1.StackTypeDual}},
		{Name: "tcp", Type: infrastructurev1alpha1.HealthCheckProbeTypeTCP, TCP: &infrastructurev1alpha1.TCPHealthCheckProbeSpec{Stack: infrastructurev1alpha1.StackTypeIPv4, Target: "current.example"}},
		{Name: "assume", Type: infrastructurev1alpha1.HealthCheckProbeTypeASSUME, Assume: &infrastructurev1alpha1.AssumeHealthCheckProbeSpec{Status: infrastructurev1alpha1.AssumedHealthStatusHealthy}},
	})
	seedActiveProfile(t, module, "override", "project-a", []infrastructurev1alpha1.HealthCheckProbeSpec{
		{Name: "custom", Type: infrastructurev1alpha1.HealthCheckProbeTypeHTTP, HTTP: &infrastructurev1alpha1.HTTPHealthCheckProbeSpec{Stack: infrastructurev1alpha1.StackTypeIPv4, Target: "custom.example"}},
	})
	location := infrastructurev1alpha1.Location{
		ObjectMeta: metav1.ObjectMeta{Namespace: module.cfg.Namespace, Labels: map[string]string{locationTenantLabel: "project-a"}},
		Spec: infrastructurev1alpha1.LocationSpec{NodeGroups: []infrastructurev1alpha1.NodeGroupSpec{
			{HealthCheck: &corev1.LocalObjectReference{Name: "group"}, Nodes: []infrastructurev1alpha1.NodeSpec{
				{Name: "inherited", Ipv4: "1.2.3.4", Ipv6: "::1"},
				{Name: "overridden", Ipv4: "1.2.3.5", HealthCheck: &corev1.LocalObjectReference{Name: "override"}},
				{Name: "ipv4only", Ipv4: "1.2.3.6"},
			}},
			{Nodes: []infrastructurev1alpha1.NodeSpec{{Name: "disabled"}}},
		}},
	}
	active, err := module.activeLocationChecks(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		node, name, typ, target string
		want                    bool
	}{
		{"inherited", "http", "HTTP", "1.2.3.4", true},
		{"inherited", "http", "HTTP", "::1", true},
		{"inherited", "http", "HTTP", "old.example", false},
		{"inherited", "http", "TCP", "1.2.3.4", false},
		{"inherited", "tcp", "TCP", "current.example", true},
		{"inherited", "tcp", "TCP", "old.example", false},
		{"inherited", "assume", "ASSUME", "Healthy", true},
		{"inherited", "assume", "ASSUME", "Unhealthy", false},
		{"inherited", "removed", "HTTP", "1.2.3.4", false},
		{"overridden", "http", "HTTP", "1.2.3.5", false},
		{"overridden", "custom", "HTTP", "custom.example", true},
		{"ipv4only", "http", "HTTP", "1.2.3.6", true},
		{"ipv4only", "http", "HTTP", "::1", false},
		{"disabled", "http", "HTTP", "1.2.3.4", false},
		{"deleted", "http", "HTTP", "1.2.3.4", false},
	} {
		t.Run(tc.node+"/"+tc.name+"/"+tc.typ+"/"+tc.target, func(t *testing.T) {
			record := app.HealthcheckRecord{Node: tc.node, Name: tc.name, Type: tc.typ, Target: tc.target}
			got := filterActiveHealthchecks([]app.HealthcheckRecord{record}, active)
			if (len(got) == 1) != tc.want {
				t.Fatalf("active = %v, want %v", len(got) == 1, tc.want)
			}
		})
	}
}

func TestLocationHealthchecksProfileLookupErrors(t *testing.T) {
	for _, mode := range []string{"missing", "foreign", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			module := newLocationTestModule(t)
			seedLocationWithNodes(t, module)
			module.SetHealthcheckDB(&fakeHealthcheckReader{})
			resource := module.client.Resource(healthCheckProfileGVR).Namespace(module.cfg.Namespace)
			if err := resource.Delete(context.Background(), "default", metav1.DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "foreign":
				seedActiveProfile(t, module, "default", "project-b", nil)
			case "unavailable":
				module.client.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "healthcheckprofiles", func(ktesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("Kubernetes unavailable")
				})
			}
			router := gin.New()
			module.RegisterRoutes(router)
			recorder := performJSONRequest(router, http.MethodGet, "/project/project-a/locations/fra1-c1/healthchecks", "")
			if recorder.Code != http.StatusBadGateway {
				t.Fatalf("expected profile lookup error, got %d: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestLocationHealthchecksErrors(t *testing.T) {
	module := newLocationTestModule(t)
	seedLocationWithNodes(t, module)
	seedTestLocation(t, module, "other", "project-b")
	router := gin.New()
	module.RegisterRoutes(router)

	if recorder := performJSONRequest(router, http.MethodGet, "/project/project-a/locations/fra1-c1/healthchecks", ""); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without database, got %d", recorder.Code)
	}

	module.SetHealthcheckDB(&fakeHealthcheckReader{err: errors.New("boom")})
	if recorder := performJSONRequest(router, http.MethodGet, "/project/project-a/locations/fra1-c1/healthchecks", ""); recorder.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on query error, got %d", recorder.Code)
	}
	for _, query := range []string{"window=10s", "window=48h", "window=abc", "limit=0", "limit=501"} {
		if recorder := performJSONRequest(router, http.MethodGet, "/project/project-a/locations/fra1-c1/healthchecks?"+query, ""); recorder.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d", query, recorder.Code)
		}
	}
	if recorder := performJSONRequest(router, http.MethodGet, "/project/project-a/locations/other/healthchecks", ""); recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for other tenant location, got %d", recorder.Code)
	}
}
