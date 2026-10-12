package projects

import (
	"net/http"
	"sort"
	"strings"
	"unicode"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
)

const maxMemberUserLength = 254

func (m *Module) registerMemberRoutes(group *gin.RouterGroup) {
	members := group.Group("/:project-id/members")

	members.GET("", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R(auth.ResourceTypeProject).S("user_id").A("read").Build(), m.listMembers)
	members.POST("", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R(auth.ResourceTypeProject).S("user_id").A("update").Build(), m.addMember)
	members.DELETE("/:user-id/roles/:role-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R(auth.ResourceTypeProject).S("user_id").A("update").Build(), m.removeMember)
}

func (m *Module) listMembers(c *gin.Context) {
	project, ok := m.loadProject(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, memberBindings(project))
}

func (m *Module) addMember(c *gin.Context) {
	var dto MemberDto
	if err := c.ShouldBindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	if !validMemberUser(dto.User) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user must be a non-empty identifier without whitespace"})
		return
	}

	project, ok := m.loadProject(c)
	if !ok {
		return
	}
	if dto.Role != builtInAdminRole && !roleExists(project, dto.Role) {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}
	if memberBindingExists(project, dto.User, dto.Role) {
		c.JSON(http.StatusConflict, gin.H{"error": "user is already assigned to this role"})
		return
	}

	project.Spec.Rbac.Groups = append(project.Spec.Rbac.Groups, infrastructurev1alpha1.RuleSpec{
		PType: "g",
		V0:    dto.User,
		V1:    dto.Role,
		V2:    project.Name,
	})
	if !m.saveProject(c, project) {
		return
	}
	c.JSON(http.StatusCreated, MemberResponse{User: dto.User, Role: dto.Role})
}

func (m *Module) removeMember(c *gin.Context) {
	user := c.Param("user-id")
	role := c.Param("role-id")

	project, ok := m.loadProject(c)
	if !ok {
		return
	}
	if !memberBindingExists(project, user, role) {
		c.JSON(http.StatusNotFound, gin.H{"error": "member binding not found"})
		return
	}
	if role == builtInAdminRole && countRoleMembers(project, builtInAdminRole) <= 1 {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot remove the last admin of the project"})
		return
	}

	groups := make([]infrastructurev1alpha1.RuleSpec, 0, len(project.Spec.Rbac.Groups))
	for _, group := range project.Spec.Rbac.Groups {
		if isMemberBinding(group, project.Name) && group.V0 == user && group.V1 == role {
			continue
		}
		groups = append(groups, group)
	}
	project.Spec.Rbac.Groups = groups
	if !m.saveProject(c, project) {
		return
	}
	c.Status(http.StatusNoContent)
}

func validMemberUser(user string) bool {
	if user == "" || len(user) > maxMemberUserLength {
		return false
	}
	for _, r := range user {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return !strings.ContainsAny(user, "/")
}

func isMemberBinding(group infrastructurev1alpha1.RuleSpec, projectName string) bool {
	return group.PType == "g" && group.V2 == projectName && group.V0 != "" && group.V1 != ""
}

func memberBindingExists(project *infrastructurev1alpha1.Project, user string, role string) bool {
	for _, group := range project.Spec.Rbac.Groups {
		if isMemberBinding(group, project.Name) && group.V0 == user && group.V1 == role {
			return true
		}
	}
	return false
}

func countRoleMembers(project *infrastructurev1alpha1.Project, role string) int {
	users := make(map[string]struct{})
	for _, group := range project.Spec.Rbac.Groups {
		if isMemberBinding(group, project.Name) && group.V1 == role {
			users[group.V0] = struct{}{}
		}
	}
	return len(users)
}

func memberBindings(project *infrastructurev1alpha1.Project) []MemberResponse {
	seen := make(map[MemberResponse]struct{})
	result := make([]MemberResponse, 0)
	for _, group := range project.Spec.Rbac.Groups {
		if !isMemberBinding(group, project.Name) {
			continue
		}
		binding := MemberResponse{User: group.V0, Role: group.V1}
		if _, exists := seen[binding]; exists {
			continue
		}
		seen[binding] = struct{}{}
		result = append(result, binding)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Role == result[j].Role {
			return result[i].User < result[j].User
		}
		return result[i].Role < result[j].Role
	})
	return result
}
