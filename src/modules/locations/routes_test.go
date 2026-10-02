package locations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func newLocationTestModule(t *testing.T) *Module {
	t.Helper()
	logger.Init(false)
	casbinModel, err := model.NewModelFromString(auth.RBACWithDomainModel)
	if err != nil {
		t.Fatalf("create Casbin model: %v", err)
	}
	enforcer, err := casbin.NewEnforcer(casbinModel)
	if err != nil {
		t.Fatalf("create Casbin enforcer: %v", err)
	}
	scheme := runtime.NewScheme()
	if err := infrastructurev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add infrastructure scheme: %v", err)
	}
	for _, action := range []string{"read", "create", "update", "delete"} {
		if _, err := enforcer.AddPolicy("user@example.com", "project-a", "location", action); err != nil {
			t.Fatalf("add location %s policy: %v", action, err)
		}
	}
	return &Module{
		cfg:      Config{Namespace: "edgecdnx"},
		client:   dynamicfake.NewSimpleDynamicClient(scheme),
		enforcer: enforcer,
		middlewares: []gin.HandlerFunc{func(c *gin.Context) {
			c.Set("user_id", "user@example.com")
			c.Next()
		}},
	}
}

func performJSONRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func seedTestLocation(t *testing.T, module *Module, name, tenant string) {
	t.Helper()
	location := &infrastructurev1alpha1.Location{
		TypeMeta: metav1.TypeMeta{APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(), Kind: "Location"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: module.cfg.Namespace,
			Labels: map[string]string{locationTenantLabel: tenant},
		},
	}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(location)
	if err != nil {
		t.Fatalf("convert test location: %v", err)
	}
	if _, err := module.client.Resource(locationGVR).Namespace(module.cfg.Namespace).Create(context.Background(), &unstructured.Unstructured{Object: object}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed location: %v", err)
	}
}

func TestLocationLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	module := newLocationTestModule(t)
	seedTestLocation(t, module, "foreign", "project-b")
	seedTestLocation(t, module, "shared", "global")
	seedTestLocation(t, module, "unowned", "")
	router := gin.New()
	module.RegisterRoutes(router)
	path := "/project/project-a/locations"

	response := performJSONRequest(router, http.MethodPost, path, `{
		"name":"fra1",
		"labels":{"edgecdnx.com/tenant":"project-b"},
		"nodeGroups":[{
			"name":"nginx","flavor":"standard",
			"labels":{"region":"eu"},"metadata":{"maxSize":"10g"},
			"nodes":[{"name":"cache-1","ipv4":"192.0.2.1","ipv6":"2001:db8::1"}]
		}],
		"geoLookup":{"weight":100,"attributes":{"country":{"weight":50,"values":[{"value":"DE","weight":20}]}}},
		"weight":75,"fallbackLocations":["ams1"]
	}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create returned %d: %s", response.Code, response.Body.String())
	}
	var created infrastructurev1alpha1.Location
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created location: %v", err)
	}
	if created.Labels[locationTenantLabel] != "project-a" || created.Namespace != module.cfg.Namespace {
		t.Fatalf("unexpected ownership: %#v", created.ObjectMeta)
	}
	if len(created.Spec.NodeGroups) != 1 || created.Spec.NodeGroups[0].Metadata["maxSize"] != "10g" ||
		created.Spec.NodeGroups[0].Labels["region"] != "eu" || len(created.Spec.NodeGroups[0].Nodes) != 1 ||
		created.Spec.NodeGroups[0].Nodes[0].Ipv6 != "2001:db8::1" {
		t.Fatalf("unexpected node groups: %#v", created.Spec.NodeGroups)
	}
	attribute := created.Spec.GeoLookup.Attributes["country"]
	if created.Spec.Weight != 75 || created.Spec.GeoLookup.Weight != 100 || attribute.Weight != 50 ||
		len(attribute.Values) != 1 || attribute.Values[0].Value != "DE" || attribute.Values[0].Weight != 20 ||
		len(created.Spec.FallbackLocations) != 1 || created.Spec.FallbackLocations[0] != "ams1" {
		t.Fatalf("unexpected location settings: %#v", created.Spec)
	}

	response = performJSONRequest(router, http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("list returned %d: %s", response.Code, response.Body.String())
	}
	var listed []infrastructurev1alpha1.Location
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode location list: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != created.Name {
		t.Fatalf("expected only project-owned locations: %#v", listed)
	}
	itemPath := path + "/" + created.Name
	response = performJSONRequest(router, http.MethodGet, itemPath, "")
	if response.Code != http.StatusOK {
		t.Fatalf("get returned %d: %s", response.Code, response.Body.String())
	}

	response = performJSONRequest(router, http.MethodPatch, itemPath, `{"weight":0,"fallbackLocations":[]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("patch returned %d: %s", response.Code, response.Body.String())
	}
	var updated infrastructurev1alpha1.Location
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated location: %v", err)
	}
	if updated.Spec.Weight != 0 || len(updated.Spec.FallbackLocations) != 0 ||
		len(updated.Spec.NodeGroups) != 1 || updated.Spec.GeoLookup.Weight != 100 ||
		updated.Labels[locationTenantLabel] != "project-a" {
		t.Fatalf("patch did not preserve omitted fields or clear explicit values: %#v", updated)
	}
	response = performJSONRequest(router, http.MethodPatch, itemPath, `{"nodeGroups":[],"geoLookup":{}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("clear settings returned %d: %s", response.Code, response.Body.String())
	}
	updated = infrastructurev1alpha1.Location{}
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode cleared location: %v", err)
	}
	if len(updated.Spec.NodeGroups) != 0 || updated.Spec.GeoLookup.Weight != 0 || len(updated.Spec.GeoLookup.Attributes) != 0 {
		t.Fatalf("settings were not cleared: %#v", updated.Spec)
	}

	response = performJSONRequest(router, http.MethodDelete, itemPath, "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete returned %d: %s", response.Code, response.Body.String())
	}
	if _, err := module.client.Resource(locationGVR).Namespace(module.cfg.Namespace).Get(context.Background(), created.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected deleted location to be absent, got %v", err)
	}
}

func TestLocationTenantIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	module := newLocationTestModule(t)
	seedTestLocation(t, module, "foreign", "project-b")
	seedTestLocation(t, module, "shared", "global")
	seedTestLocation(t, module, "unowned", "")
	router := gin.New()
	module.RegisterRoutes(router)
	for _, name := range []string{"foreign", "shared", "unowned", "missing"} {
		for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
			t.Run(name+"/"+method, func(t *testing.T) {
				response := performJSONRequest(router, method, "/project/project-a/locations/"+name, `{"weight":99}`)
				if response.Code != http.StatusNotFound {
					t.Fatalf("expected 404, got %d: %s", response.Code, response.Body.String())
				}
			})
		}
	}
	object, err := module.client.Resource(locationGVR).Namespace(module.cfg.Namespace).Get(context.Background(), "foreign", metav1.GetOptions{})
	if err != nil || object.GetLabels()[locationTenantLabel] != "project-b" {
		t.Fatalf("foreign location was mutated or deleted: %v, %#v", err, object)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		path := "/project/project-b/locations"
		if method == http.MethodPatch || method == http.MethodDelete {
			path += "/foreign"
		}
		response := performJSONRequest(router, method, path, `{"name":"new","weight":99}`)
		if response.Code != http.StatusForbidden {
			t.Fatalf("unauthorized %s returned %d: %s", method, response.Code, response.Body.String())
		}
	}
}

func TestLocationValidationAndErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	module := newLocationTestModule(t)
	seedTestLocation(t, module, "fra1", "project-a")
	router := gin.New()
	module.RegisterRoutes(router)
	path := "/project/project-a/locations"
	for _, body := range []string{
		`{`,
		`{}`,
		`{"name":"Invalid_Name"}`,
		`{"name":"test","geoLookup":{"weight":1001}}`,
		`{"name":"test","geoLookup":{"weight":-1}}`,
		`{"name":"test","nodeGroups":[{}]}`,
		`{"name":"test","nodeGroups":[{"name":"nginx","flavor":"standard","labels":{"edgecdnx.com/tenant":"project-b"}}]}`,
		`{"name":"test","nodeGroups":[{"name":"nginx","flavor":"standard","labels":{"invalid label":"test"}}]}`,
		`{"name":"test","nodeGroups":[{"name":"nginx","flavor":"standard"},{"name":"nginx","flavor":"standard"}]}`,
		`{"name":"test","nodeGroups":[{"name":"nginx","flavor":"standard","nodes":[{}]}]}`,
		`{"name":"test","nodeGroups":[{"name":"nginx","flavor":"standard","nodes":[{"name":"cache"},{"name":"cache"}]}]}`,
	} {
		response := performJSONRequest(router, http.MethodPost, path, body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %s: expected 400, got %d: %s", body, response.Code, response.Body.String())
		}
	}
	for _, body := range []string{
		`{"geoLookup":{"weight":1001}}`,
		`{"nodeGroups":[{"name":"nginx","flavor":"standard","labels":{"edgecdnx.com/tenant":"project-b"}}]}`,
		`{"weight":"invalid"}`,
	} {
		response := performJSONRequest(router, http.MethodPatch, path+"/fra1", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("patch %s: expected 400, got %d: %s", body, response.Code, response.Body.String())
		}
	}
	response := performJSONRequest(router, http.MethodPost, path, `{"name":"fra1"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate create returned %d: %s", response.Code, response.Body.String())
	}
	module.client.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "locations", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	response = performJSONRequest(router, http.MethodGet, path, "")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "connection refused") {
		t.Fatalf("unexpected list failure response %d: %s", response.Code, response.Body.String())
	}
}

func TestLocationEmptyNodeGroupFlavor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	module := newLocationTestModule(t)
	router := gin.New()
	module.RegisterRoutes(router)
	path := "/project/project-a/locations"

	response := performJSONRequest(router, http.MethodPost, path, `{"name":"fra1","nodeGroups":[{"name":"nginx"}]}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create with omitted flavor returned %d: %s", response.Code, response.Body.String())
	}
	var created infrastructurev1alpha1.Location
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode location: %v", err)
	}
	if len(created.Spec.NodeGroups) != 1 || created.Spec.NodeGroups[0].Flavor != "" {
		t.Fatalf("expected default empty flavor, got %#v", created.Spec.NodeGroups)
	}

	response = performJSONRequest(router, http.MethodPatch, path+"/fra1", `{"nodeGroups":[{"name":"nginx","flavor":""}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("update with empty flavor returned %d: %s", response.Code, response.Body.String())
	}
	response = performJSONRequest(router, http.MethodPatch, path+"/fra1", `{"nodeGroups":[{"name":"nginx","flavor":""},{"name":"nginx","flavor":""}]}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate groups with empty flavors returned %d: %s", response.Code, response.Body.String())
	}
}
