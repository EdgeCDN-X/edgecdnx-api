package projects

import (
	"encoding/json"
	"net/http"
	"testing"

	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestMemberCRUD(t *testing.T) {
	router, client := newRoleTestRouter(t)

	response := requestRoleAPI(t, router, http.MethodGet, "/projects/demo/members", "")
	assertStatus(t, http.StatusOK, response.Code)
	assertJSONEqual(t, `[{"user":"test-user","role":"project-editor"},{"user":"member","role":"viewer"}]`, response.Body.String())

	response = requestRoleAPI(t, router, http.MethodPost, "/projects/demo/members", `{"user":"new@example.com","role":"viewer"}`)
	assertStatus(t, http.StatusCreated, response.Code)
	assertJSONEqual(t, `{"user":"new@example.com","role":"viewer"}`, response.Body.String())

	response = requestRoleAPI(t, router, http.MethodPost, "/projects/demo/members", `{"user":"new@example.com","role":"viewer"}`)
	assertStatus(t, http.StatusConflict, response.Code)

	response = requestRoleAPI(t, router, http.MethodGet, "/projects/demo/members", "")
	var members []MemberResponse
	assertNoError(t, json.Unmarshal(response.Body.Bytes(), &members))
	assertContains(t, members, MemberResponse{User: "new@example.com", Role: "viewer"})

	response = requestRoleAPI(t, router, http.MethodDelete, "/projects/demo/members/new@example.com/roles/viewer", "")
	assertStatus(t, http.StatusNoContent, response.Code)
	response = requestRoleAPI(t, router, http.MethodDelete, "/projects/demo/members/new@example.com/roles/viewer", "")
	assertStatus(t, http.StatusNotFound, response.Code)

	object, err := client.Resource(gvr).Namespace("edgecdnx").Get(t.Context(), "demo", metav1.GetOptions{})
	assertNoError(t, err)
	project := &infrastructurev1alpha1.Project{}
	assertNoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, project))
	assertNotContains(t, project.Spec.Rbac.Groups, infrastructurev1alpha1.RuleSpec{PType: "g", V0: "new@example.com", V1: "viewer", V2: "demo"})
	assertContains(t, project.Spec.Rbac.Groups, infrastructurev1alpha1.RuleSpec{PType: "g", V0: "member", V1: "viewer", V2: "demo"})
}

func TestAddMemberValidation(t *testing.T) {
	router, _ := newRoleTestRouter(t)

	response := requestRoleAPI(t, router, http.MethodPost, "/projects/demo/members", `{"user":"new@example.com","role":"missing"}`)
	assertStatus(t, http.StatusNotFound, response.Code)
	response = requestRoleAPI(t, router, http.MethodPost, "/projects/demo/members", `{"user":"bad user","role":"viewer"}`)
	assertStatus(t, http.StatusBadRequest, response.Code)
	response = requestRoleAPI(t, router, http.MethodPost, "/projects/demo/members", `{"role":"viewer"}`)
	assertStatus(t, http.StatusBadRequest, response.Code)
}

func TestLastAdminCannotBeRemoved(t *testing.T) {
	router, _ := newRoleTestRouter(t)

	response := requestRoleAPI(t, router, http.MethodPost, "/projects/demo/members", `{"user":"owner@example.com","role":"admin"}`)
	assertStatus(t, http.StatusCreated, response.Code)
	response = requestRoleAPI(t, router, http.MethodDelete, "/projects/demo/members/owner@example.com/roles/admin", "")
	assertStatus(t, http.StatusConflict, response.Code)

	response = requestRoleAPI(t, router, http.MethodPost, "/projects/demo/members", `{"user":"second@example.com","role":"admin"}`)
	assertStatus(t, http.StatusCreated, response.Code)
	response = requestRoleAPI(t, router, http.MethodDelete, "/projects/demo/members/owner@example.com/roles/admin", "")
	assertStatus(t, http.StatusNoContent, response.Code)
}
