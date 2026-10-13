package apikeys

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

const testNamespace = "edgecdnx"

func TestMain(m *testing.M) {
	logger.Init(false)
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func testProject(name string) *infrastructurev1alpha1.Project {
	return &infrastructurev1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			UID:       types.UID(name + "-uid"),
		},
		Spec: infrastructurev1alpha1.ProjectSpec{
			Name: name,
			Rbac: infrastructurev1alpha1.RBACSpec{
				Groups: []infrastructurev1alpha1.RuleSpec{
					{PType: "g", V0: "user@example.com", V1: "manager", V2: name},
				},
				Rules: []infrastructurev1alpha1.RuleSpec{
					{PType: "p", V0: "viewer", V1: name, V2: "service", V3: "read"},
					{PType: "p", V0: "admin", V1: name, V2: "*", V3: "update"},
				},
			},
		},
	}
}

func newTestModule(t *testing.T, projects ...*infrastructurev1alpha1.Project) (*Module, http.Handler) {
	t.Helper()

	casbinModel, err := model.NewModelFromString(auth.RBACWithDomainModel)
	if err != nil {
		t.Fatalf("create Casbin model: %v", err)
	}
	enforcer, err := casbin.NewEnforcer(casbinModel)
	if err != nil {
		t.Fatalf("create Casbin enforcer: %v", err)
	}
	for _, action := range []string{"create", "read", "update", "delete"} {
		if _, err := enforcer.AddPolicy("user@example.com", "project-a", auth.ResourceTypeAPIKey, action); err != nil {
			t.Fatalf("add apikey policy: %v", err)
		}
	}
	if _, err := enforcer.AddPolicy("user@example.com", "project-a", auth.ResourceTypeService, "read"); err != nil {
		t.Fatalf("add service policy: %v", err)
	}

	// Seed unstructured objects with an empty scheme so the fake client behaves like the
	// real dynamic client for objects created through it.
	seed := []runtime.Object{}
	for _, project := range projects {
		project.TypeMeta = metav1.TypeMeta{APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(), Kind: "Project"}
		obj, err := toUnstructured(project)
		if err != nil {
			t.Fatalf("convert project: %v", err)
		}
		seed = append(seed, obj)
	}

	module := &Module{
		cfg: Config{Namespace: testNamespace},
		client: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
			apiKeyGVR:  "APIKeyList",
			projectGVR: "ProjectList",
		}, seed...),
		enforcer: enforcer,
		middlewares: []gin.HandlerFunc{func(c *gin.Context) {
			c.Set("user_id", "user@example.com")
			c.Next()
		}},
	}
	router := gin.New()
	module.RegisterRoutes(router)
	return module, router
}

func doRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func decode[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return out
}

func futureTime() string {
	return time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
}

func projectGroups(t *testing.T, module *Module, name string) []infrastructurev1alpha1.RuleSpec {
	t.Helper()
	project, err := module.getProject(context.Background(), name)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	return project.Spec.Rbac.Groups
}

func TestAPIKeyLifecycle(t *testing.T) {
	module, router := newTestModule(t, testProject("project-a"))

	createResponse := doRequest(router, http.MethodPost, "/project/project-a/apikeys",
		`{"displayName":"ci","expiresAt":"`+futureTime()+`","roles":["viewer"]}`)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", createResponse.Code, createResponse.Body.String())
	}
	if createResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", createResponse.Header().Get("Cache-Control"))
	}
	created := decode[CreatedAPIKeyDto](t, createResponse)

	id, ok := auth.APIKeyIDFromObjectName(created.ID)
	if !ok {
		t.Fatalf("expected generated object name with ak- prefix, got %q", created.ID)
	}
	if created.Name != "ak_"+id {
		t.Fatalf("expected API key name ak_%s, got %q", id, created.Name)
	}
	if !strings.HasPrefix(created.Token, created.Name+"_") {
		t.Fatalf("expected token to be prefixed with %q", created.Name+"_")
	}
	if created.Project != "project-a" || !slices.Equal(created.Roles, []string{"viewer"}) {
		t.Fatalf("unexpected created API key: %+v", created)
	}

	stored, err := module.getProjectAPIKey(context.Background(), "project-a", created.ID)
	if err != nil {
		t.Fatalf("get stored API key: %v", err)
	}
	if stored.Labels[projectLabel] != "project-a" || stored.Spec.ProjectRef == nil || stored.Spec.ProjectRef.Name != "project-a" {
		t.Fatalf("expected project label and projectRef, got labels=%v projectRef=%v", stored.Labels, stored.Spec.ProjectRef)
	}
	if stored.Spec.Credential.Digest != auth.APIKeyDigest(created.Token) ||
		stored.Spec.Credential.Algorithm != "sha256" || stored.Spec.Credential.FormatVersion != "v1" {
		t.Fatalf("unexpected stored credential: %+v", stored.Spec.Credential)
	}
	if strings.Contains(stored.Spec.Credential.Digest, created.Token) {
		t.Fatal("token must not be stored")
	}
	if len(stored.OwnerReferences) != 1 || stored.OwnerReferences[0].UID != "project-a-uid" {
		t.Fatalf("expected project owner reference, got %+v", stored.OwnerReferences)
	}
	if !slices.Contains(projectGroups(t, module, "project-a"), infrastructurev1alpha1.RuleSpec{PType: "g", V0: created.Name, V1: "viewer", V2: "project-a"}) {
		t.Fatal("expected API key to be added to project RBAC groups")
	}

	getResponse := doRequest(router, http.MethodGet, "/project/project-a/apikeys/"+created.ID, "")
	if getResponse.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", getResponse.Code, getResponse.Body.String())
	}
	if strings.Contains(getResponse.Body.String(), "token") || strings.Contains(getResponse.Body.String(), stored.Spec.Credential.Digest) {
		t.Fatalf("read response must not expose credentials: %s", getResponse.Body.String())
	}
	if got := decode[APIKeyDto](t, getResponse); !slices.Equal(got.Roles, []string{"viewer"}) {
		t.Fatalf("expected roles [viewer], got %v", got.Roles)
	}

	listResponse := doRequest(router, http.MethodGet, "/project/project-a/apikeys", "")
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", listResponse.Code, listResponse.Body.String())
	}
	if list := decode[[]APIKeyDto](t, listResponse); len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("unexpected list response: %+v", list)
	}

	patchResponse := doRequest(router, http.MethodPatch, "/project/project-a/apikeys/"+created.ID,
		`{"displayName":"deploy","roles":[]}`)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", patchResponse.Code, patchResponse.Body.String())
	}
	patched := decode[APIKeyDto](t, patchResponse)
	if patched.DisplayName != "deploy" || len(patched.Roles) != 0 {
		t.Fatalf("unexpected patched API key: %+v", patched)
	}

	patchResponse = doRequest(router, http.MethodPatch, "/project/project-a/apikeys/"+created.ID, `{"roles":["viewer"]}`)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", patchResponse.Code, patchResponse.Body.String())
	}

	deleteResponse := doRequest(router, http.MethodDelete, "/project/project-a/apikeys/"+created.ID, "")
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	for _, g := range projectGroups(t, module, "project-a") {
		if g.V0 == created.Name {
			t.Fatalf("expected API key RBAC groups to be removed, found %+v", g)
		}
	}
	if response := doRequest(router, http.MethodGet, "/project/project-a/apikeys/"+created.ID, ""); response.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 after delete, got %d", response.Code)
	}
}

func TestAPIKeyOptionalExpiry(t *testing.T) {
	module, router := newTestModule(t, testProject("project-a"))

	createResponse := doRequest(router, http.MethodPost, "/project/project-a/apikeys", `{"displayName":"forever"}`)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", createResponse.Code, createResponse.Body.String())
	}
	if !strings.Contains(createResponse.Body.String(), `"expiresAt":null`) {
		t.Fatalf("expected expiresAt null for non-expiring key: %s", createResponse.Body.String())
	}
	created := decode[CreatedAPIKeyDto](t, createResponse)
	stored, err := module.getProjectAPIKey(context.Background(), "project-a", created.ID)
	if err != nil {
		t.Fatalf("get stored API key: %v", err)
	}
	if stored.Spec.ExpiresAt != nil {
		t.Fatalf("expected no stored expiry, got %v", stored.Spec.ExpiresAt)
	}

	expiry := futureTime()
	patched := decode[APIKeyDto](t, doRequest(router, http.MethodPatch, "/project/project-a/apikeys/"+created.ID, `{"expiresAt":"`+expiry+`"}`))
	if patched.ExpiresAt == nil || patched.ExpiresAt.Format(time.RFC3339) != expiry {
		t.Fatalf("expected expiry %s, got %v", expiry, patched.ExpiresAt)
	}

	// Omitting expiresAt keeps the current expiry.
	patched = decode[APIKeyDto](t, doRequest(router, http.MethodPatch, "/project/project-a/apikeys/"+created.ID, `{"displayName":"renamed"}`))
	if patched.ExpiresAt == nil {
		t.Fatal("expected expiry to be unchanged when omitted")
	}

	// An explicit null removes the expiry.
	patched = decode[APIKeyDto](t, doRequest(router, http.MethodPatch, "/project/project-a/apikeys/"+created.ID, `{"expiresAt":null}`))
	if patched.ExpiresAt != nil {
		t.Fatalf("expected expiry to be cleared, got %v", patched.ExpiresAt)
	}
	stored, err = module.getProjectAPIKey(context.Background(), "project-a", created.ID)
	if err != nil {
		t.Fatalf("get stored API key: %v", err)
	}
	if stored.Spec.ExpiresAt != nil {
		t.Fatalf("expected stored expiry to be cleared, got %v", stored.Spec.ExpiresAt)
	}

	if response := doRequest(router, http.MethodPatch, "/project/project-a/apikeys/"+created.ID, `{"expiresAt":"2000-01-01T00:00:00Z"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for past expiry, got %d", response.Code)
	}
}

func TestCreateAPIKeyValidation(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"missing display name", `{"expiresAt":"` + futureTime() + `"}`, http.StatusBadRequest},
		{"expired", `{"displayName":"ci","expiresAt":"2000-01-01T00:00:00Z"}`, http.StatusBadRequest},
		{"unknown role", `{"displayName":"ci","expiresAt":"` + futureTime() + `","roles":["missing"]}`, http.StatusBadRequest},
		{"privilege escalation", `{"displayName":"ci","expiresAt":"` + futureTime() + `","roles":["admin"]}`, http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module, router := newTestModule(t, testProject("project-a"))

			response := doRequest(router, http.MethodPost, "/project/project-a/apikeys", tt.body)
			if response.Code != tt.status {
				t.Fatalf("expected status %d, got %d: %s", tt.status, response.Code, response.Body.String())
			}

			list, err := module.client.Resource(apiKeyGVR).Namespace(testNamespace).List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatalf("list API keys: %v", err)
			}
			if len(list.Items) != 0 {
				t.Fatalf("expected no API key to be created, got %d", len(list.Items))
			}
		})
	}
}

func TestAPIKeyProjectIsolation(t *testing.T) {
	module, router := newTestModule(t, testProject("project-a"), testProject("project-b"))
	if _, err := module.enforcer.AddPolicy("user@example.com", "project-b", auth.ResourceTypeAPIKey, "create"); err != nil {
		t.Fatalf("add policy: %v", err)
	}

	response := doRequest(router, http.MethodPost, "/project/project-b/apikeys",
		`{"displayName":"other","expiresAt":"`+futureTime()+`"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	other := decode[CreatedAPIKeyDto](t, response)

	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		response := doRequest(router, method, "/project/project-a/apikeys/"+other.ID, `{"displayName":"x"}`)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s: expected status 404 for another project's key, got %d", method, response.Code)
		}
	}

	listResponse := doRequest(router, http.MethodGet, "/project/project-a/apikeys", "")
	if list := decode[[]APIKeyDto](t, listResponse); len(list) != 0 {
		t.Fatalf("expected no keys in project-a, got %+v", list)
	}
}

func TestAPIKeyRequiresPermission(t *testing.T) {
	_, router := newTestModule(t, testProject("project-b"))

	response := doRequest(router, http.MethodGet, "/project/project-b/apikeys", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", response.Code)
	}
}
