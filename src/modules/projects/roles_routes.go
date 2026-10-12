package projects

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const builtInAdminRole = "admin"

var roleNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

func (m *Module) registerRoleRoutes(group *gin.RouterGroup) {
	roles := group.Group("/:project-id/roles")

	roles.GET("", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R(auth.ResourceTypeProject).S("user_id").A("read").Build(), m.listRoles)
	roles.GET("/:role-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R(auth.ResourceTypeProject).S("user_id").A("read").Build(), m.getRole)
	roles.POST("", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R(auth.ResourceTypeProject).S("user_id").A("update").Build(), m.createRole)
	roles.PUT("/:role-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R(auth.ResourceTypeProject).S("user_id").A("update").Build(), m.updateRole)
	roles.DELETE("/:role-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R(auth.ResourceTypeProject).S("user_id").A("update").Build(), m.deleteRole)
}

func (m *Module) listRoles(c *gin.Context) {
	project, ok := m.loadProject(c)
	if !ok {
		return
	}

	names := make(map[string]struct{})
	for _, rule := range project.Spec.Rbac.Rules {
		if rule.PType == "p" && rule.V1 == project.Name && rule.V0 != "" {
			names[rule.V0] = struct{}{}
		}
	}
	for _, group := range project.Spec.Rbac.Groups {
		if group.PType == "g" && group.V2 == project.Name && group.V1 != "" {
			names[group.V1] = struct{}{}
		}
	}

	roleNames := make([]string, 0, len(names))
	for name := range names {
		roleNames = append(roleNames, name)
	}
	sort.Strings(roleNames)

	result := make([]RoleResponse, 0, len(roleNames))
	for _, name := range roleNames {
		result = append(result, roleResponse(project, name))
	}
	c.JSON(http.StatusOK, result)
}

func (m *Module) getRole(c *gin.Context) {
	project, ok := m.loadProject(c)
	if !ok {
		return
	}

	name := c.Param("role-id")
	if !roleExists(project, name) {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}
	c.JSON(http.StatusOK, roleResponse(project, name))
}

func (m *Module) createRole(c *gin.Context) {
	var dto RoleDto
	if err := c.ShouldBindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	if !validRoleName(dto.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role name must be 1-63 characters and contain only letters, numbers, '.', '_' or '-'"})
		return
	}
	if dto.Name == builtInAdminRole {
		c.JSON(http.StatusConflict, gin.H{"error": "the built-in admin role already exists and cannot be created"})
		return
	}
	if err := validatePermissions(dto.Permissions); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	project, ok := m.loadProject(c)
	if !ok {
		return
	}
	if roleExists(project, dto.Name) {
		c.JSON(http.StatusConflict, gin.H{"error": "role already exists"})
		return
	}

	project.Spec.Rbac.Rules = append(project.Spec.Rbac.Rules, policyRules(project.Name, dto.Name, dto.Permissions)...)
	if !m.saveProject(c, project) {
		return
	}
	c.JSON(http.StatusCreated, roleResponse(project, dto.Name))
}

func (m *Module) updateRole(c *gin.Context) {
	name := c.Param("role-id")
	if name == builtInAdminRole {
		c.JSON(http.StatusForbidden, gin.H{"error": "the built-in admin role cannot be edited"})
		return
	}

	var dto UpdateRoleDto
	if err := c.ShouldBindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	if err := validatePermissions(dto.Permissions); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	project, ok := m.loadProject(c)
	if !ok {
		return
	}
	if !roleExists(project, name) {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}

	project.Spec.Rbac.Rules = replaceRoleRules(project, name, policyRules(project.Name, name, dto.Permissions))
	if !m.saveProject(c, project) {
		return
	}
	c.JSON(http.StatusOK, roleResponse(project, name))
}

func (m *Module) deleteRole(c *gin.Context) {
	name := c.Param("role-id")
	if name == builtInAdminRole {
		c.JSON(http.StatusForbidden, gin.H{"error": "the built-in admin role cannot be deleted"})
		return
	}

	project, ok := m.loadProject(c)
	if !ok {
		return
	}
	if !roleExists(project, name) {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}

	project.Spec.Rbac.Rules = filterRoleRules(project, name)
	project.Spec.Rbac.Groups = filterRoleMemberships(project, name)
	if !m.saveProject(c, project) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (m *Module) loadProject(c *gin.Context) (*infrastructurev1alpha1.Project, bool) {
	obj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, c.Param("project-id"), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load project: " + err.Error()})
		return nil, false
	}

	project := &infrastructurev1alpha1.Project{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, project); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode project: " + err.Error()})
		return nil, false
	}
	return project, true
}

func (m *Module) saveProject(c *gin.Context, project *infrastructurev1alpha1.Project) bool {
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(project)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode project: " + err.Error()})
		return false
	}
	_, err = m.client.Resource(gvr).Namespace(m.cfg.Namespace).Update(c, &unstructured.Unstructured{Object: object}, metav1.UpdateOptions{})
	if err != nil {
		if apierrors.IsConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "project was modified concurrently; retry the request"})
			return false
		}
		if apierrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
			return false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update project: " + err.Error()})
		return false
	}
	return true
}

func validRoleName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && roleNamePattern.MatchString(name)
}

func validatePermissions(permissions []RolePermission) error {
	if len(permissions) == 0 {
		return fmt.Errorf("a role must have at least one permission")
	}
	seen := make(map[RolePermission]struct{}, len(permissions))
	for _, permission := range permissions {
		if strings.TrimSpace(permission.Resource) == "" || strings.TrimSpace(permission.Action) == "" {
			return fmt.Errorf("permission resource and action must not be empty")
		}
		if permission.Resource != strings.TrimSpace(permission.Resource) || permission.Action != strings.TrimSpace(permission.Action) {
			return fmt.Errorf("permission resource and action must not have leading or trailing whitespace")
		}
		if _, exists := seen[permission]; exists {
			return fmt.Errorf("duplicate permission for resource %q and action %q", permission.Resource, permission.Action)
		}
		seen[permission] = struct{}{}
	}
	return nil
}

func roleExists(project *infrastructurev1alpha1.Project, name string) bool {
	for _, rule := range project.Spec.Rbac.Rules {
		if rule.PType == "p" && rule.V0 == name && rule.V1 == project.Name {
			return true
		}
	}
	for _, group := range project.Spec.Rbac.Groups {
		if group.PType == "g" && group.V1 == name && group.V2 == project.Name {
			return true
		}
	}
	return false
}

func roleResponse(project *infrastructurev1alpha1.Project, name string) RoleResponse {
	permissions := make([]RolePermission, 0)
	for _, rule := range project.Spec.Rbac.Rules {
		if rule.PType == "p" && rule.V0 == name && rule.V1 == project.Name {
			permissions = append(permissions, RolePermission{Resource: rule.V2, Action: rule.V3})
		}
	}
	sort.Slice(permissions, func(i, j int) bool {
		if permissions[i].Resource == permissions[j].Resource {
			return permissions[i].Action < permissions[j].Action
		}
		return permissions[i].Resource < permissions[j].Resource
	})
	return RoleResponse{Name: name, Permissions: permissions}
}

func policyRules(projectName string, roleName string, permissions []RolePermission) []infrastructurev1alpha1.RuleSpec {
	rules := make([]infrastructurev1alpha1.RuleSpec, 0, len(permissions))
	for _, permission := range permissions {
		rules = append(rules, infrastructurev1alpha1.RuleSpec{
			PType: "p",
			V0:    roleName,
			V1:    projectName,
			V2:    permission.Resource,
			V3:    permission.Action,
		})
	}
	return rules
}

func replaceRoleRules(project *infrastructurev1alpha1.Project, roleName string, replacement []infrastructurev1alpha1.RuleSpec) []infrastructurev1alpha1.RuleSpec {
	rules := filterRoleRules(project, roleName)
	return append(rules, replacement...)
}

func filterRoleRules(project *infrastructurev1alpha1.Project, roleName string) []infrastructurev1alpha1.RuleSpec {
	rules := make([]infrastructurev1alpha1.RuleSpec, 0, len(project.Spec.Rbac.Rules))
	for _, rule := range project.Spec.Rbac.Rules {
		if rule.PType != "p" || rule.V0 != roleName || rule.V1 != project.Name {
			rules = append(rules, rule)
		}
	}
	return rules
}

func filterRoleMemberships(project *infrastructurev1alpha1.Project, roleName string) []infrastructurev1alpha1.RuleSpec {
	groups := make([]infrastructurev1alpha1.RuleSpec, 0, len(project.Spec.Rbac.Groups))
	for _, group := range project.Spec.Rbac.Groups {
		if group.PType != "g" || group.V1 != roleName || group.V2 != project.Name {
			groups = append(groups, group)
		}
	}
	return groups
}
