package projects

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func newRoleTestRouter(t *testing.T) (*gin.Engine, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	return newProjectTestRouter(t, func(c *gin.Context) { c.Set("user_id", "test-user") })
}

func newProjectTestRouter(t *testing.T, setContext func(*gin.Context)) (*gin.Engine, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	project := &infrastructurev1alpha1.Project{
		TypeMeta: metav1.TypeMeta{
			APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
			Kind:       "Project",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "edgecdnx"},
		Spec: infrastructurev1alpha1.ProjectSpec{
			Name: "demo",
			Rbac: infrastructurev1alpha1.RBACSpec{
				Groups: []infrastructurev1alpha1.RuleSpec{
					{PType: "g", V0: "test-user", V1: "project-editor", V2: "demo"},
					{PType: "g", V0: "member", V1: "viewer", V2: "demo"},
				},
				Rules: []infrastructurev1alpha1.RuleSpec{
					{PType: "p", V0: "project-editor", V1: "demo", V2: "project", V3: "read"},
					{PType: "p", V0: "project-editor", V1: "demo", V2: "project", V3: "update"},
					{PType: "p", V0: "admin", V1: "demo", V2: "*", V3: "create"},
					{PType: "p", V0: "admin", V1: "demo", V2: "*", V3: "read"},
					{PType: "p", V0: "admin", V1: "demo", V2: "*", V3: "update"},
					{PType: "p", V0: "admin", V1: "demo", V2: "*", V3: "delete"},
					{PType: "p", V0: "viewer", V1: "demo", V2: "service", V3: "read"},
				},
			},
		},
	}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(project)
	assertNoError(t, err)

	scheme := runtime.NewScheme()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{gvr: "ProjectList"},
		&unstructured.Unstructured{Object: object},
	)

	casbinModel, err := model.NewModelFromString(auth.RBACWithDomainModel)
	assertNoError(t, err)
	enforcer, err := casbin.NewEnforcer(casbinModel)
	assertNoError(t, err)
	_, err = enforcer.AddGroupingPolicy("test-user", "project-editor", "demo")
	assertNoError(t, err)
	_, err = enforcer.AddPolicy("project-editor", "demo", "project", "read")
	assertNoError(t, err)
	_, err = enforcer.AddPolicy("project-editor", "demo", "project", "update")
	assertNoError(t, err)

	module := New(Config{Namespace: "edgecdnx"})
	module.client = client
	module.enforcer = enforcer

	router := gin.New()
	router.Use(func(c *gin.Context) {
		setContext(c)
		c.Next()
	})
	module.RegisterRoutes(router)
	return router, client
}

func TestRoleCRUD(t *testing.T) {
	router, client := newRoleTestRouter(t)

	response := requestRoleAPI(t, router, http.MethodPost, "/projects/demo/roles", `{"name":"auditor","permissions":[{"resource":"service","action":"read"}]}`)
	assertStatus(t, http.StatusCreated, response.Code)
	assertJSONEqual(t, `{"name":"auditor","permissions":[{"resource":"service","action":"read"}]}`, response.Body.String())

	response = requestRoleAPI(t, router, http.MethodGet, "/projects/demo/roles/auditor", "")
	assertStatus(t, http.StatusOK, response.Code)
	assertJSONEqual(t, `{"name":"auditor","permissions":[{"resource":"service","action":"read"}]}`, response.Body.String())

	response = requestRoleAPI(t, router, http.MethodPut, "/projects/demo/roles/auditor", `{"permissions":[{"resource":"zone","action":"read"}]}`)
	assertStatus(t, http.StatusOK, response.Code)
	assertJSONEqual(t, `{"name":"auditor","permissions":[{"resource":"zone","action":"read"}]}`, response.Body.String())

	response = requestRoleAPI(t, router, http.MethodGet, "/projects/demo/roles", "")
	assertStatus(t, http.StatusOK, response.Code)
	var roles []RoleResponse
	assertNoError(t, json.Unmarshal(response.Body.Bytes(), &roles))
	if len(roles) != 4 {
		t.Fatalf("expected 4 roles, got %d", len(roles))
	}
	assertEqual(t, []string{"admin", "auditor", "project-editor", "viewer"}, []string{roles[0].Name, roles[1].Name, roles[2].Name, roles[3].Name})

	response = requestRoleAPI(t, router, http.MethodDelete, "/projects/demo/roles/auditor", "")
	assertStatus(t, http.StatusNoContent, response.Code)
	response = requestRoleAPI(t, router, http.MethodGet, "/projects/demo/roles/auditor", "")
	assertStatus(t, http.StatusNotFound, response.Code)

	updatedObject, err := client.Resource(gvr).Namespace("edgecdnx").Get(t.Context(), "demo", metav1.GetOptions{})
	assertNoError(t, err)
	updatedProject := &infrastructurev1alpha1.Project{}
	assertNoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(updatedObject.Object, updatedProject))
	assertContains(t, updatedProject.Spec.Rbac.Rules, infrastructurev1alpha1.RuleSpec{
		PType: "p", V0: "viewer", V1: "demo", V2: "service", V3: "read",
	})
	assertNotContains(t, updatedProject.Spec.Rbac.Rules, infrastructurev1alpha1.RuleSpec{
		PType: "p", V0: "auditor", V1: "demo",
	})
	assertNotContains(t, updatedProject.Spec.Rbac.Groups, infrastructurev1alpha1.RuleSpec{
		PType: "g", V0: "member", V1: "auditor", V2: "demo",
	})
}

func TestProjectPlacement(t *testing.T) {
	for _, namespaced := range []bool{true, false} {
		t.Run(fmt.Sprint(namespaced), func(t *testing.T) {
			scheme := runtime.NewScheme()
			assertNoError(t, corev1.AddToScheme(scheme))
			assertNoError(t, infrastructurev1alpha1.AddToScheme(scheme))
			client := dynamicfake.NewSimpleDynamicClient(scheme)
			module := New(Config{Namespaced: namespaced, Namespace: "shared"})
			module.client = client
			project, code, err := module.createProject(context.Background(), "user", "creator", ProjectDto{Name: "Tenant One"})
			assertNoError(t, err)
			if code != http.StatusCreated || project.Name != "tenant-one" || project.Namespace != module.scope().ResourceNamespace(project.Name) {
				t.Fatalf("unexpected project placement: code=%d project=%+v", code, project.ObjectMeta)
			}
			namespaceCreate, projectCreate := -1, -1
			for index, action := range client.Actions() {
				if action.GetVerb() == "create" && action.GetResource().Resource == "namespaces" {
					namespaceCreate = index
				}
				if action.GetVerb() == "create" && action.GetResource().Resource == "projects" {
					projectCreate = index
				}
			}
			if namespaced && (namespaceCreate < 0 || namespaceCreate >= projectCreate) {
				t.Fatal("namespace must be created before Project")
			}
			if !namespaced && namespaceCreate != -1 {
				t.Fatal("shared mode must not provision tenant namespaces")
			}
			_, code, err = module.createProject(context.Background(), "user", "creator", ProjectDto{Name: "Tenant One"})
			if code != http.StatusConflict || err == nil {
				t.Fatalf("duplicate project must conflict: %d %v", code, err)
			}
		})
	}
}

func TestProjectNamespaceFailures(t *testing.T) {
	for _, mode := range []string{"unmanaged", "namespace-denied", "project-denied"} {
		t.Run(mode, func(t *testing.T) {
			scheme := runtime.NewScheme()
			assertNoError(t, corev1.AddToScheme(scheme))
			assertNoError(t, infrastructurev1alpha1.AddToScheme(scheme))
			client := dynamicfake.NewSimpleDynamicClient(scheme)
			module := New(Config{Namespaced: true})
			module.client = client
			namespaces := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
			if mode == "unmanaged" {
				_, err := client.Resource(namespaces).Create(context.Background(), &unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]interface{}{"name": "tenant"},
				}}, metav1.CreateOptions{})
				assertNoError(t, err)
			} else {
				resource := "namespaces"
				if mode == "project-denied" {
					resource = "projects"
				}
				client.PrependReactor("create", resource, func(action ktesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewForbidden(action.GetResource().GroupResource(), "tenant", errors.New("denied"))
				})
			}
			_, code, err := module.createProject(context.Background(), "user", "creator", ProjectDto{Name: "tenant"})
			if err == nil || (mode == "unmanaged" && code != http.StatusConflict) || (mode != "unmanaged" && code != http.StatusInternalServerError) {
				t.Fatalf("expected provisioning failure: %d %v", code, err)
			}
			if mode == "project-denied" {
				if _, err := client.Resource(namespaces).Get(context.Background(), "tenant", metav1.GetOptions{}); err != nil {
					t.Fatal("failed Project creation must not delete the namespace")
				}
			}
		})
	}
}

func TestAdminRoleCannotBeModified(t *testing.T) {
	router, client := newRoleTestRouter(t)

	response := requestRoleAPI(t, router, http.MethodPost, "/projects/demo/roles", `{"name":"admin","permissions":[{"resource":"service","action":"read"}]}`)
	assertStatus(t, http.StatusConflict, response.Code)
	response = requestRoleAPI(t, router, http.MethodPut, "/projects/demo/roles/admin", `{"permissions":[{"resource":"service","action":"read"}]}`)
	assertStatus(t, http.StatusForbidden, response.Code)
	response = requestRoleAPI(t, router, http.MethodDelete, "/projects/demo/roles/admin", "")
	assertStatus(t, http.StatusForbidden, response.Code)

	object, err := client.Resource(gvr).Namespace("edgecdnx").Get(t.Context(), "demo", metav1.GetOptions{})
	assertNoError(t, err)
	project := &infrastructurev1alpha1.Project{}
	assertNoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, project))
	assertContains(t, project.Spec.Rbac.Rules, infrastructurev1alpha1.RuleSpec{
		PType: "p", V0: "admin", V1: "demo", V2: "*", V3: "delete",
	})
}

func TestCreateRoleRejectsEmptyOrDuplicatePermissions(t *testing.T) {
	router, _ := newRoleTestRouter(t)

	response := requestRoleAPI(t, router, http.MethodPost, "/projects/demo/roles", `{"name":"empty","permissions":[]}`)
	assertStatus(t, http.StatusBadRequest, response.Code)
	response = requestRoleAPI(t, router, http.MethodPost, "/projects/demo/roles", `{"name":"auditor","permissions":[{"resource":"service","action":"read"},{"resource":"service","action":"read"}]}`)
	assertStatus(t, http.StatusBadRequest, response.Code)
}

func requestRoleAPI(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertStatus(t *testing.T, want, got int) {
	t.Helper()
	if want != got {
		t.Fatalf("expected status %d, got %d", want, got)
	}
}

func assertJSONEqual(t *testing.T, want, got string) {
	t.Helper()
	var wantValue, gotValue any
	assertNoError(t, json.Unmarshal([]byte(want), &wantValue))
	assertNoError(t, json.Unmarshal([]byte(got), &gotValue))
	assertEqual(t, wantValue, gotValue)
}

func assertEqual(t *testing.T, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func assertContains[T comparable](t *testing.T, values []T, want T) {
	t.Helper()
	if !slices.Contains(values, want) {
		t.Fatalf("expected %v to contain %v", values, want)
	}
}

func assertNotContains[T comparable](t *testing.T, values []T, want T) {
	t.Helper()
	if slices.Contains(values, want) {
		t.Fatalf("expected %v not to contain %v", values, want)
	}
}
