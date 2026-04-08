package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newTestEnforcer(t *testing.T) *casbin.Enforcer {
	t.Helper()

	casbinModel, err := model.NewModelFromString(auth.RBACWithDomainModel)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	enforcer, err := casbin.NewEnforcer(casbinModel)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	return enforcer
}

func newTestModule(t *testing.T, prometheus *app.Prometheus, locations ...*infrastructurev1alpha1.Location) *Module {
	t.Helper()

	enforcer := newTestEnforcer(t)
	_, err := enforcer.AddPolicy("user@example.com", "admin", "location", "read")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	scheme := runtime.NewScheme()
	if err := infrastructurev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	objects := make([]runtime.Object, 0, len(locations))
	for _, location := range locations {
		objects = append(objects, location)
	}

	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{locationGVR: "LocationList"},
		objects...,
	)

	return &Module{
		cfg:        Config{DefaultAdminProject: "admin"},
		dynClient:  dynClient,
		prometheus: prometheus,
		enforcer:   enforcer,
		middlewares: []gin.HandlerFunc{func(c *gin.Context) {
			c.Set("user_id", "user@example.com")
			c.Set("groups", "")
			c.Next()
		}},
	}
}

func TestLocationHealthWithoutPrometheus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	module := newTestModule(t, nil)
	router := gin.New()
	module.RegisterRoutes(router)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/location-healths", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status code %d", recorder.Code)
	}
}

func TestLocationHealthQueriesPrometheus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("query"); got != `probe_success{endpoint="location"}` {
			t.Fatalf("unexpected query %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"cluster":"fra1-ns1","endpoint":"location","instance":"http://74.220.24.46/healthz","location":"nyc1-c1","role":"routing"},"value":[1775650533.969,"1"]}]}}`))
	}))
	defer server.Close()

	prometheus, err := app.NewPrometheus(app.PrometheusConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	module := newTestModule(t, prometheus, &infrastructurev1alpha1.Location{
		ObjectMeta: metav1.ObjectMeta{Name: "nyc1-c1", Namespace: "edgecdnx"},
		Spec: infrastructurev1alpha1.LocationSpec{
			Nodes: []infrastructurev1alpha1.NodeSpec{{Name: "nyc-router-1", Ipv4: "74.220.24.46"}},
		},
	})
	router := gin.New()
	module.RegisterRoutes(router)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/location-healths", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status code %d", recorder.Code)
	}

	var response locationHealthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response.Status != "success" {
		t.Fatalf("unexpected status %q", response.Status)
	}
	if response.Data.ResultType != "vector" {
		t.Fatalf("unexpected result type %q", response.Data.ResultType)
	}
	if len(response.Data.Locations) != 1 {
		t.Fatalf("expected one matched location, got %d", len(response.Data.Locations))
	}
	if response.Data.Locations[0].Name != "nyc1-c1" {
		t.Fatalf("unexpected location %q", response.Data.Locations[0].Name)
	}
	if response.Data.Locations[0].Sources[0].Nodes[0].NodeName != "nyc-router-1" {
		t.Fatalf("unexpected node name %q", response.Data.Locations[0].Sources[0].Nodes[0].NodeName)
	}
	if len(response.Data.UnmatchedMetrics) != 0 {
		t.Fatalf("expected no unmatched metrics, got %d", len(response.Data.UnmatchedMetrics))
	}
}
