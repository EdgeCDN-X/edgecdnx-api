package projects

import (
	"net/http"
	"testing"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	"github.com/gin-gonic/gin"
)

func apiKeyContext(project string) func(*gin.Context) {
	return func(c *gin.Context) {
		c.Set(auth.ContextKeyAuthMethod, auth.AuthMethodAPIKey)
		c.Set(auth.ContextKeyUserID, "test-user")
		c.Set(auth.ContextKeyAPIKeyProject, project)
	}
}

func TestAPIKeyCannotCreateProjects(t *testing.T) {
	router, _ := newProjectTestRouter(t, apiKeyContext("demo"))
	response := requestRoleAPI(t, router, http.MethodPost, "/projects", `{"name":"new-project"}`)
	assertStatus(t, http.StatusForbidden, response.Code)
}

func TestAPIKeyListsOnlyItsProject(t *testing.T) {
	router, _ := newProjectTestRouter(t, apiKeyContext("demo"))
	response := requestRoleAPI(t, router, http.MethodGet, "/projects", "")
	assertStatus(t, http.StatusOK, response.Code)
	if response.Body.String() == "[]" {
		t.Fatal("expected own project to be listed")
	}

	router, _ = newProjectTestRouter(t, apiKeyContext("other"))
	response = requestRoleAPI(t, router, http.MethodGet, "/projects", "")
	assertStatus(t, http.StatusOK, response.Code)
	assertJSONEqual(t, "[]", response.Body.String())
}
