package locations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

const profilePath = "/project/project-a/healthcheckprofiles"
const validProfileBody = `{"name":"web","probes":[
	{"name":"tcp","type":"TCP","interval":"30s","timeout":"5s","tcp":{"port":443,"stack":"Dual"}},
	{"name":"https","type":"HTTP","http":{"protocol":"https","path":"/health","host":"example.com"}},
	{"name":"static","type":"ASSUME","assume":{"status":"Healthy","stack":"IPv4"}}
]}`

func newProfileTestRouter(t *testing.T) (*Module, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	module := newLocationTestModule(t)
	for _, action := range []string{"read", "create", "update", "delete"} {
		if _, err := module.enforcer.AddPolicy("user@example.com", "project-a", "healthcheckprofile", action); err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	module.RegisterRoutes(router)
	return module, router
}

func decodeProfile(t *testing.T, body []byte) infrastructurev1alpha1.HealthCheckProfile {
	t.Helper()
	var profile infrastructurev1alpha1.HealthCheckProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		t.Fatal(err)
	}
	return profile
}

func TestHealthCheckProfileLifecycle(t *testing.T) {
	module, router := newProfileTestRouter(t)
	response := performJSONRequest(router, http.MethodGet, profilePath, "")
	if response.Code != 200 || response.Body.String() != "[]" {
		t.Fatalf("expected empty array: %d %s", response.Code, response.Body.String())
	}
	response = performJSONRequest(router, http.MethodPost, profilePath, validProfileBody)
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	created := decodeProfile(t, response.Body.Bytes())
	if len(created.Labels) != 1 || created.Labels[locationTenantLabel] != "project-a" || created.Namespace != "edgecdnx" ||
		len(created.Spec.Probes) != 3 || created.Spec.Probes[1].HTTP.Protocol != "https" ||
		created.Spec.Probes[0].Interval.Duration.String() != "30s" || created.Spec.Probes[2].Assume.Status != "Healthy" {
		t.Fatalf("unexpected profile: %#v", created)
	}
	for _, path := range []string{profilePath, profilePath + "/web"} {
		response = performJSONRequest(router, http.MethodGet, path, "")
		if response.Code != 200 {
			t.Fatalf("get: %d %s", response.Code, response.Body.String())
		}
	}
	// Ensure patching the spec leaves status and other metadata untouched.
	object, err := module.client.Resource(healthCheckProfileGVR).Namespace("edgecdnx").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	object.Object["status"] = map[string]interface{}{"status": "Healthy"}
	object.SetAnnotations(map[string]string{"managed-by": "controller"})
	object.SetLabels(map[string]string{locationTenantLabel: "project-a", "legacy": "label"})
	if _, err := module.client.Resource(healthCheckProfileGVR).Namespace("edgecdnx").Update(context.Background(), object, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	response = performJSONRequest(router, http.MethodPatch, profilePath+"/web", `{}`)
	if response.Code != 200 {
		t.Fatalf("patch: %d %s", response.Code, response.Body.String())
	}
	updated := decodeProfile(t, response.Body.Bytes())
	if len(updated.Spec.Probes) != 3 || len(updated.Labels) != 1 || updated.Labels[locationTenantLabel] != "project-a" || updated.Status.Status != "Healthy" || updated.Annotations["managed-by"] != "controller" {
		t.Fatalf("patch failed to preserve fields: %#v", updated)
	}
	response = performJSONRequest(router, http.MethodPatch, profilePath+"/web", `{"probes":[{"name":"down","type":"ASSUME","assume":{"status":"Unhealthy"}}]}`)
	if response.Code != 200 {
		t.Fatalf("patch: %d %s", response.Code, response.Body.String())
	}
	updated = decodeProfile(t, response.Body.Bytes())
	if len(updated.Spec.Probes) != 1 || updated.Spec.Probes[0].Assume.Status != "Unhealthy" || updated.Status.Status != "Healthy" {
		t.Fatalf("probes not replaced: %#v", updated)
	}
	response = performJSONRequest(router, http.MethodDelete, profilePath+"/web", "")
	if response.Code != 204 {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	response = performJSONRequest(router, http.MethodGet, profilePath+"/web", "")
	if response.Code != 404 {
		t.Fatalf("deleted profile still exists: %d", response.Code)
	}
}

func TestHealthCheckProfileTenantIsolation(t *testing.T) {
	module, router := newProfileTestRouter(t)
	for _, tenant := range []string{"project-b", "global", ""} {
		name := "foreign"
		if tenant == "global" {
			name = "shared"
		}
		if tenant == "" {
			name = "unowned"
		}
		profile := &infrastructurev1alpha1.HealthCheckProfile{
			TypeMeta:   metav1.TypeMeta{APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(), Kind: "HealthCheckProfile"},
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "edgecdnx", Labels: map[string]string{locationTenantLabel: tenant}},
		}
		object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(profile)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := module.client.Resource(healthCheckProfileGVR).Namespace("edgecdnx").Create(context.Background(), &unstructured.Unstructured{Object: object}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	response := performJSONRequest(router, http.MethodGet, profilePath, "")
	if response.Code != 200 || response.Body.String() != "[]" {
		t.Fatalf("tenant list leaked: %d %s", response.Code, response.Body.String())
	}
	for _, name := range []string{"foreign", "shared", "unowned", "missing"} {
		for _, method := range []string{"GET", "PATCH", "DELETE"} {
			response = performJSONRequest(router, method, profilePath+"/"+name, `{}`)
			if response.Code != 404 {
				t.Fatalf("%s %s: %d %s", method, name, response.Code, response.Body.String())
			}
		}
	}
	for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
		path := "/project/project-b/healthcheckprofiles"
		if method == "PATCH" || method == "DELETE" {
			path += "/foreign"
		}
		response = performJSONRequest(router, method, path, validProfileBody)
		if response.Code != 403 {
			t.Fatalf("expected forbidden: %d %s", response.Code, response.Body.String())
		}
	}
	response = performJSONRequest(router, "POST", profilePath, `{"name":"other","labels":{"edgecdnx.com/tenant":"project-b"},"probes":[{"name":"up","type":"ASSUME","assume":{"status":"Healthy"}}]}`)
	if response.Code != 400 {
		t.Fatalf("tenant override accepted: %d", response.Code)
	}
}

func TestHealthCheckProfileValidation(t *testing.T) {
	_, router := newProfileTestRouter(t)
	for _, body := range []string{
		`{`, `{}`, `{"name":"INVALID","probes":[{"name":"up","type":"ASSUME","assume":{"status":"Healthy"}}]}`,
		`{"name":"web","probes":[]}`,
		`{"name":"web","probes":[{"name":"","type":"TCP","tcp":{"port":80}}]}`,
		`{"name":"web","probes":[{"name":"x","type":"TCP","tcp":{"port":0}}]}`,
		`{"name":"web","probes":[{"name":"x","type":"TCP","tcp":{"port":65536}}]}`,
		`{"name":"web","probes":[{"name":"x","type":"TCP"}]}`,
		`{"name":"web","probes":[{"name":"x","type":"TCP","tcp":{"port":80},"assume":{"status":"Healthy"}}]}`,
		`{"name":"web","probes":[{"name":"x","type":"HTTP","http":{"protocol":"ftp"}}]}`,
		`{"name":"web","probes":[{"name":"x","type":"HTTPS","http":{"protocol":"https"}}]}`,
		`{"name":"web","probes":[{"name":"x","type":"ASSUME","assume":{"status":"Unknown"}}]}`,
		`{"name":"web","probes":[{"name":"x","type":"ASSUME","assume":{"status":"Healthy","stack":"v4"}}]}`,
		`{"name":"web","probes":[{"name":"x","type":"TCP","interval":"bad","tcp":{"port":80}}]}`,
		`{"name":"web","probes":[{"name":"x","type":"TCP","timeout":"-1s","tcp":{"port":80}}]}`,
		`{"name":"web","probes":[{"name":"x","type":"TCP","tcp":{"port":80}},{"name":"x","type":"TCP","tcp":{"port":81}}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			response := performJSONRequest(router, "POST", profilePath, body)
			if response.Code != 400 {
				t.Fatalf("expected 400: %d %s", response.Code, response.Body.String())
			}
		})
	}
	if response := performJSONRequest(router, "POST", profilePath, validProfileBody); response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	for _, body := range []string{`{"probes":[]}`, `{"labels":{"edgecdnx.com/tenant":"global"}}`, `{"labels":{"bad/key/name":"x"}}`} {
		response := performJSONRequest(router, "PATCH", profilePath+"/web", body)
		if response.Code != 400 {
			t.Fatalf("invalid patch accepted: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestHealthCheckProfileRejectsConfigurableLabels(t *testing.T) {
	_, router := newProfileTestRouter(t)
	if response := performJSONRequest(router, "POST", profilePath, validProfileBody); response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	for _, labels := range []string{`{}`, `null`, `{"region":"eu"}`, `{"edgecdnx.com/tenant":"project-a"}`} {
		for _, method := range []string{"POST", "PATCH"} {
			path := profilePath
			body := `{"name":"other","probes":[{"name":"up","type":"ASSUME","assume":{"status":"Healthy"}}],"labels":` + labels + `}`
			if method == "PATCH" {
				path += "/web"
				body = `{"labels":` + labels + `}`
			}
			response := performJSONRequest(router, method, path, body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s accepted labels %s: %d %s", method, labels, response.Code, response.Body.String())
			}
		}
	}
	response := performJSONRequest(router, "GET", profilePath+"/web", "")
	profile := decodeProfile(t, response.Body.Bytes())
	if len(profile.Labels) != 1 || profile.Labels[locationTenantLabel] != "project-a" {
		t.Fatalf("unexpected labels after rejected requests: %#v", profile.Labels)
	}
}

func TestHealthCheckProfileErrors(t *testing.T) {
	_, router := newProfileTestRouter(t)
	if response := performJSONRequest(router, "POST", profilePath, validProfileBody); response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	if response := performJSONRequest(router, "POST", profilePath, validProfileBody); response.Code != 409 {
		t.Fatalf("duplicate: %d", response.Code)
	}
	for _, test := range []struct {
		verb, method, path string
		err                error
		status             int
	}{
		{"list", "GET", profilePath, errors.New("unavailable"), 500},
		{"get", "GET", profilePath + "/web", apierrors.NewForbidden(healthCheckProfileGVR.GroupResource(), "web", errors.New("denied")), 403},
		{"update", "PATCH", profilePath + "/web", apierrors.NewConflict(healthCheckProfileGVR.GroupResource(), "web", errors.New("stale")), 409},
		{"delete", "DELETE", profilePath + "/web", errors.New("unavailable"), 500},
	} {
		t.Run(test.verb, func(t *testing.T) {
			module, router := newProfileTestRouter(t)
			if response := performJSONRequest(router, "POST", profilePath, validProfileBody); response.Code != 201 {
				t.Fatal(response.Body.String())
			}
			client := module.client.(*dynamicfake.FakeDynamicClient)
			client.PrependReactor(test.verb, "healthcheckprofiles", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, test.err })
			response := performJSONRequest(router, test.method, test.path, `{}`)
			if response.Code != test.status {
				t.Fatalf("expected %d: %d %s", test.status, response.Code, response.Body.String())
			}
		})
	}
}
