package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
	stringadapter "github.com/casbin/casbin/v3/persist/string-adapter"
	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

const testNamespace = "edgecdnx"

func TestMain(m *testing.M) {
	logger.Init(false)
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

type apiKeyFixture struct {
	module   *Module
	router   *gin.Engine
	apiKeys  cache.Store
	projects cache.Store
	now      time.Time
}

func newAPIKeyFixture(t *testing.T) *apiKeyFixture {
	t.Helper()
	casbinModel, err := model.NewModelFromString(RBACWithDomainModel)
	if err != nil {
		t.Fatal(err)
	}
	enforcer, err := casbin.NewEnforcer(casbinModel, stringadapter.NewAdapter("p, viewer, project-a, service, read\np, viewer, project-b, service, read\np, admin, admin-project, location, read"))
	if err != nil {
		t.Fatal(err)
	}

	f := &apiKeyFixture{
		apiKeys:  cache.NewStore(cache.MetaNamespaceKeyFunc),
		projects: cache.NewStore(cache.MetaNamespaceKeyFunc),
		now:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	f.module = &Module{
		cfg:      Config{Namespace: testNamespace},
		Enforcer: enforcer,
		apiKeys:  f.apiKeys,
		projects: f.projects,
	}
	f.module.clock = func() time.Time { return f.now }
	f.addProject(t, "project-a")
	f.addProject(t, "project-b")

	authz := func(resource, action string) gin.HandlerFunc {
		return NewAuthzBuilder().E(enforcer).T("project-id").R(resource).S(ContextKeyUserID).A(action).Build()
	}
	echo := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"user_id":     c.GetString(ContextKeyUserID),
			"groups":      c.GetString(ContextKeyGroups),
			"auth_method": c.GetString(ContextKeyAuthMethod),
			"project":     c.GetString(ContextKeyAPIKeyProject),
		})
	}
	f.router = gin.New()
	group := f.router.Group("/project/:project-id", f.module.AuthMiddleware())
	group.GET("/services", authz(ResourceTypeService, "read"), echo)
	group.POST("/services", authz(ResourceTypeService, "create"), echo)
	f.router.GET("/admin/locations", f.module.AuthMiddleware(),
		NewAuthzBuilder().E(enforcer).ST("admin-project").R(ResourceTypeLocation).S(ContextKeyUserID).A("read").Build(), echo)
	return f
}

func (f *apiKeyFixture) addProject(t *testing.T, name string) {
	t.Helper()
	project := &infrastructurev1alpha1.Project{
		TypeMeta:   metav1.TypeMeta{APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(), Kind: "Project"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace, UID: types.UID(name + "-uid")},
	}
	if err := f.projects.Add(toUnstructuredT(t, project)); err != nil {
		t.Fatal(err)
	}
}

// addKey stores a new key for the project and binds it to the given role in that project.
func (f *apiKeyFixture) addKey(t *testing.T, project string, mutate func(*infrastructurev1alpha1.APIKey)) (APIKeyCredential, *infrastructurev1alpha1.APIKey) {
	t.Helper()
	credential, err := NewAPIKeyCredential()
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := metav1.NewTime(f.now.Add(time.Hour))
	key := &infrastructurev1alpha1.APIKey{
		TypeMeta: metav1.TypeMeta{APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(), Kind: "APIKey"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      APIKeyObjectName(credential.ID),
			Namespace: testNamespace,
			Labels:    map[string]string{APIKeyProjectLabel: project},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
				Kind:       "Project",
				Name:       project,
				UID:        types.UID(project + "-uid"),
			}},
		},
		Spec: infrastructurev1alpha1.APIKeySpec{
			DisplayName: "ci",
			Project:     &corev1.LocalObjectReference{Name: project},
			Credential: infrastructurev1alpha1.CredentialSpec{
				FormatVersion: APIKeyFormatVersion,
				Algorithm:     APIKeyDigestAlgorithm,
				Digest:        credential.Digest,
			},
			ExpiresAt: &expiresAt,
		},
	}
	if mutate != nil {
		mutate(key)
	}
	if err := f.apiKeys.Add(toUnstructuredT(t, key)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.module.Enforcer.AddGroupingPolicy(APIKeySubject(credential.ID), "viewer", project); err != nil {
		t.Fatal(err)
	}
	return credential, key
}

func (f *apiKeyFixture) do(method, path, authorization string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	f.router.ServeHTTP(recorder, request)
	return recorder
}

func toUnstructuredT(t *testing.T, obj any) *unstructured.Unstructured {
	t.Helper()
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: raw}
}

func TestAPIKeyAuthenticationSetsAuthzContext(t *testing.T) {
	f := newAPIKeyFixture(t)
	credential, _ := f.addKey(t, "project-a", nil)

	for _, scheme := range []string{"ApiKey", "apikey", "APIKEY"} {
		response := f.do(http.MethodGet, "/project/project-a/services", scheme+" "+credential.Token)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", scheme, response.Code, response.Body.String())
		}
		want := `{"auth_method":"apikey","groups":"","project":"project-a","user_id":"` + APIKeySubject(credential.ID) + `"}`
		if response.Body.String() != want {
			t.Fatalf("unexpected context: %s", response.Body.String())
		}
	}

	// RBAC still applies: the viewer role has no create permission.
	if response := f.do(http.MethodPost, "/project/project-a/services", "ApiKey "+credential.Token); response.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing permission, got %d", response.Code)
	}
}

func TestAPIKeyNeverExpires(t *testing.T) {
	f := newAPIKeyFixture(t)
	credential, _ := f.addKey(t, "project-a", func(k *infrastructurev1alpha1.APIKey) { k.Spec.ExpiresAt = nil })
	f.now = f.now.AddDate(100, 0, 0)

	if response := f.do(http.MethodGet, "/project/project-a/services", "ApiKey "+credential.Token); response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestAPIKeyNamespacedLookup(t *testing.T) {
	fixture := newAPIKeyFixture(t)
	fixture.module.cfg.Namespaced = true
	fixture.apiKeys = cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{app.ResourceNameIndex: app.ResourceNameIndexFunc})
	fixture.module.apiKeys = fixture.apiKeys
	for _, object := range fixture.projects.List() {
		resource := object.(*unstructured.Unstructured).DeepCopy()
		if err := fixture.projects.Delete(object); err != nil {
			t.Fatal(err)
		}
		resource.SetNamespace(resource.GetName())
		if err := fixture.projects.Add(resource); err != nil {
			t.Fatal(err)
		}
	}
	credential, key := fixture.addKey(t, "project-a", func(key *infrastructurev1alpha1.APIKey) {
		key.Namespace = "project-a"
		key.Labels = nil
	})
	if response := fixture.do(http.MethodGet, "/project/project-a/services", "ApiKey "+credential.Token); response.Code != http.StatusOK {
		t.Fatalf("namespaced key lookup failed: %d %s", response.Code, response.Body.String())
	}
	duplicate := key.DeepCopy()
	duplicate.Namespace = "project-b"
	duplicate.Spec.Project.Name = "project-b"
	duplicate.Spec.Credential.Digest = "different-secret"
	if err := fixture.apiKeys.Add(toUnstructuredT(t, duplicate)); err != nil {
		t.Fatal(err)
	}
	if response := fixture.do(http.MethodGet, "/project/project-a/services", "ApiKey "+credential.Token); response.Code != http.StatusOK {
		t.Fatal("unrelated key ID collision must not select another tenant")
	}
	duplicate.Spec.Credential.Digest = key.Spec.Credential.Digest
	if err := fixture.apiKeys.Update(toUnstructuredT(t, duplicate)); err != nil {
		t.Fatal(err)
	}
	if response := fixture.do(http.MethodGet, "/project/project-a/services", "ApiKey "+credential.Token); response.Code != http.StatusUnauthorized {
		t.Fatalf("ambiguous credentials must fail closed, got %d", response.Code)
	}
}

func TestProjectPoliciesStayWithinTenant(t *testing.T) {
	fixture := newAPIKeyFixture(t)
	project := &infrastructurev1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "project-a", Namespace: testNamespace},
		Spec: infrastructurev1alpha1.ProjectSpec{Rbac: infrastructurev1alpha1.RBACSpec{Rules: []infrastructurev1alpha1.RuleSpec{
			{PType: "p", V0: "actor", V1: "project-a", V2: "service", V3: "read"},
			{PType: "p", V0: "actor", V1: "project-b", V2: "service", V3: "read"},
		}}},
	}
	object := toUnstructuredT(t, project)
	fixture.module.applyProjectPolicies(object, false)
	if allowed, err := fixture.module.Enforcer.Enforce("actor", "project-a", "service", "read"); err != nil || !allowed {
		t.Fatalf("own-tenant rule was not loaded: %v", err)
	}
	if allowed, err := fixture.module.Enforcer.Enforce("actor", "project-b", "service", "read"); err != nil || allowed {
		t.Fatalf("cross-tenant rule was loaded: %v", err)
	}
	fixture.module.applyProjectPolicies(cache.DeletedFinalStateUnknown{Obj: object}, true)
	if allowed, err := fixture.module.Enforcer.Enforce("actor", "project-a", "service", "read"); err != nil || allowed {
		t.Fatalf("deleted Project retained authorization: %v", err)
	}
}

func TestAPIKeyRejections(t *testing.T) {
	f := newAPIKeyFixture(t)
	valid, _ := f.addKey(t, "project-a", nil)
	expired, _ := f.addKey(t, "project-a", func(k *infrastructurev1alpha1.APIKey) {
		past := metav1.NewTime(f.now.Add(-time.Second))
		k.Spec.ExpiresAt = &past
	})
	wrongFormat, _ := f.addKey(t, "project-a", func(k *infrastructurev1alpha1.APIKey) { k.Spec.Credential.FormatVersion = "v0" })
	staleOwner, _ := f.addKey(t, "project-a", func(k *infrastructurev1alpha1.APIKey) { k.OwnerReferences[0].UID = "old-uid" })
	labelMismatch, _ := f.addKey(t, "project-a", func(k *infrastructurev1alpha1.APIKey) { k.Labels[APIKeyProjectLabel] = "project-b" })
	noProject, _ := f.addKey(t, "project-a", func(k *infrastructurev1alpha1.APIKey) { k.Spec.Project = nil })
	deleting, _ := f.addKey(t, "project-a", func(k *infrastructurev1alpha1.APIKey) {
		now := metav1.NewTime(f.now)
		k.DeletionTimestamp = &now
	})
	orphan, _ := f.addKey(t, "project-gone", nil)

	unknown, err := NewAPIKeyCredential()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		authorization string
	}{
		{"malformed", "ApiKey not-a-key"},
		{"wrong secret", "ApiKey " + valid.Token[:len(valid.Token)-2] + "AA"},
		{"unknown key", "ApiKey " + unknown.Token},
		{"expired", "ApiKey " + expired.Token},
		{"unsupported format", "ApiKey " + wrongFormat.Token},
		{"recreated project", "ApiKey " + staleOwner.Token},
		{"missing project", "ApiKey " + noProject.Token},
		{"being deleted", "ApiKey " + deleting.Token},
		{"project missing", "ApiKey " + orphan.Token},
		{"missing credentials", "ApiKey "},
		{"unknown scheme", "Basic " + valid.Token},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := f.do(http.MethodGet, "/project/project-a/services", tt.authorization)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", response.Code, response.Body.String())
			}
		})
	}

	// A valid key still works after all rejections.
	if response := f.do(http.MethodGet, "/project/project-a/services", "ApiKey "+labelMismatch.Token); response.Code != http.StatusOK {
		t.Fatalf("labels must not override spec.project ownership, got %d", response.Code)
	}
	if response := f.do(http.MethodGet, "/project/project-a/services", "ApiKey "+valid.Token); response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
}

func TestAPIKeyIsConfinedToItsProject(t *testing.T) {
	f := newAPIKeyFixture(t)
	credential, _ := f.addKey(t, "project-a", nil)
	subject := APIKeySubject(credential.ID)
	// Even an explicit binding in another project must not grant access.
	if _, err := f.module.Enforcer.AddGroupingPolicy(subject, "viewer", "project-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.module.Enforcer.AddGroupingPolicy(subject, "admin", "admin-project"); err != nil {
		t.Fatal(err)
	}

	if response := f.do(http.MethodGet, "/project/project-b/services", "ApiKey "+credential.Token); response.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for other project, got %d", response.Code)
	}
	if response := f.do(http.MethodGet, "/admin/locations", "ApiKey "+credential.Token); response.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for static tenant route, got %d", response.Code)
	}
}

func TestParseAPIKeyToken(t *testing.T) {
	credential, err := NewAPIKeyCredential()
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := parseAPIKeyToken(credential.Token); !ok || id != credential.ID {
		t.Fatalf("expected valid token to parse, got %q %v", id, ok)
	}
	for _, token := range []string{
		"",
		"ak_",
		credential.Token + "A",
		"xx" + credential.Token[2:],
		"ak_SHORT_" + credential.Token[len(credential.Token)-43:],
		credential.Token[:len(credential.Token)-1] + "=",
	} {
		if _, ok := parseAPIKeyToken(token); ok {
			t.Fatalf("expected token %q to be rejected", token)
		}
	}
}
