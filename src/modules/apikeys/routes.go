package apikeys

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
)

var apiKeyGVR = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "apikeys",
}

var projectGVR = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "projects",
}

const (
	projectLabel      = auth.APIKeyProjectLabel
	maxCreateAttempts = 3
)

type requestError struct {
	status  int
	message string
}

func (e *requestError) Error() string {
	return e.message
}

func newRequestError(status int, format string, args ...any) error {
	return &requestError{status: status, message: fmt.Sprintf(format, args...)}
}

func (m *Module) RegisterRoutes(r *gin.Engine) {
	group := r.Group("project/:project-id/apikeys", m.middlewares...)

	authz := func(action string) gin.HandlerFunc {
		return auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R(auth.ResourceTypeAPIKey).S("user_id").A(action).Build()
	}

	group.GET("", authz("read"), m.listAPIKeys)
	group.GET("/:apikey-id", authz("read"), m.getAPIKey)
	group.POST("", authz("create"), m.createAPIKey)
	group.PATCH("/:apikey-id", authz("update"), m.updateAPIKey)
	group.DELETE("/:apikey-id", authz("delete"), m.deleteAPIKey)
}

func (m *Module) listAPIKeys(c *gin.Context) {
	projectID := c.Param("project-id")

	project, err := m.getProject(c, projectID)
	if err != nil {
		writeError(c, err, "failed to retrieve project")
		return
	}

	objList, err := m.client.Resource(apiKeyGVR).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{
		LabelSelector: projectLabel + "=" + projectID,
	})
	if err != nil {
		writeError(c, err, "failed to list API keys")
		return
	}

	keys := []APIKeyDto{}
	for _, item := range objList.Items {
		key, err := fromUnstructured[infrastructurev1alpha1.APIKey](&item)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if !belongsToProject(key, projectID) {
			continue
		}
		dto, ok := toDto(key, project)
		if !ok {
			continue
		}
		keys = append(keys, dto)
	}

	c.JSON(http.StatusOK, keys)
}

func (m *Module) getAPIKey(c *gin.Context) {
	projectID := c.Param("project-id")

	key, err := m.getProjectAPIKey(c, projectID, c.Param("apikey-id"))
	if err != nil {
		writeError(c, err, "failed to retrieve API key")
		return
	}

	project, err := m.getProject(c, projectID)
	if err != nil {
		writeError(c, err, "failed to retrieve project")
		return
	}

	dto, _ := toDto(key, project)
	c.JSON(http.StatusOK, dto)
}

func (m *Module) createAPIKey(c *gin.Context) {
	projectID := c.Param("project-id")

	var dto CreateAPIKeyDto
	if err := c.ShouldBindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	displayName := strings.TrimSpace(dto.DisplayName)
	if displayName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "displayName must not be empty"})
		return
	}
	if dto.ExpiresAt != nil && !dto.ExpiresAt.After(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expiresAt must be in the future"})
		return
	}
	roles, err := normalizeRoles(dto.Roles)
	if err != nil {
		writeError(c, err, "invalid roles")
		return
	}

	project, err := m.getProject(c, projectID)
	if err != nil {
		writeError(c, err, "failed to retrieve project")
		return
	}
	if err := m.validateRoles(c, project, roles); err != nil {
		writeError(c, err, "failed to validate roles")
		return
	}

	var (
		created    *infrastructurev1alpha1.APIKey
		credential auth.APIKeyCredential
	)
	for attempt := 0; attempt < maxCreateAttempts; attempt++ {
		credential, err = auth.NewAPIKeyCredential()
		if err != nil {
			logger.L().Error("Failed to generate API key credential", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		created, err = m.createAPIKeyObject(c, project, displayName, dto.ExpiresAt, credential)
		if err == nil || !apierrors.IsAlreadyExists(err) {
			break
		}
	}
	if err != nil {
		writeError(c, err, "failed to create API key")
		return
	}

	subject := auth.APIKeySubject(credential.ID)
	if err := m.setKeyRoles(c, projectID, subject, roles, true); err != nil {
		// Roll back so that no unmanaged key remains without the requested RBAC.
		rollbackCtx := context.WithoutCancel(c.Request.Context())
		if delErr := m.client.Resource(apiKeyGVR).Namespace(m.cfg.Namespace).Delete(rollbackCtx, created.Name, metav1.DeleteOptions{}); delErr != nil && !apierrors.IsNotFound(delErr) {
			logger.L().Error("Failed to roll back API key after RBAC update failure", zap.String("apikey", created.Name), zap.Error(delErr))
		}
		writeError(c, err, "failed to assign API key roles")
		return
	}

	keyDto, _ := toDto(created, nil)
	keyDto.Roles = roles

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, CreatedAPIKeyDto{
		APIKeyDto: keyDto,
		Token:     credential.Token,
	})
}

func (m *Module) updateAPIKey(c *gin.Context) {
	projectID := c.Param("project-id")

	var dto UpdateAPIKeyDto
	if err := c.ShouldBindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	var displayName string
	if dto.DisplayName != nil {
		displayName = strings.TrimSpace(*dto.DisplayName)
		if displayName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "displayName must not be empty"})
			return
		}
	}
	if dto.ExpiresAt.Value != nil && !dto.ExpiresAt.Value.After(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expiresAt must be in the future"})
		return
	}
	var roles []string
	if dto.Roles != nil {
		var err error
		roles, err = normalizeRoles(*dto.Roles)
		if err != nil {
			writeError(c, err, "invalid roles")
			return
		}
	}

	key, err := m.getProjectAPIKey(c, projectID, c.Param("apikey-id"))
	if err != nil {
		writeError(c, err, "failed to retrieve API key")
		return
	}
	id, _ := auth.APIKeyIDFromObjectName(key.Name)

	if dto.Roles != nil {
		if err := m.setKeyRoles(c, projectID, auth.APIKeySubject(id), roles, true); err != nil {
			writeError(c, err, "failed to assign API key roles")
			return
		}
	}

	if dto.DisplayName != nil || dto.ExpiresAt.Set {
		if dto.DisplayName != nil {
			key.Spec.DisplayName = displayName
		}
		if dto.ExpiresAt.Set {
			key.Spec.ExpiresAt = toMetaTime(dto.ExpiresAt.Value)
		}

		obj, err := toUnstructured(key)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		updatedObj, err := m.client.Resource(apiKeyGVR).Namespace(m.cfg.Namespace).Update(c, obj, metav1.UpdateOptions{})
		if err != nil {
			writeError(c, err, "failed to update API key")
			return
		}
		key, err = fromUnstructured[infrastructurev1alpha1.APIKey](updatedObj)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
	}

	project, err := m.getProject(c, projectID)
	if err != nil {
		writeError(c, err, "failed to retrieve project")
		return
	}

	keyDto, _ := toDto(key, project)
	c.JSON(http.StatusOK, keyDto)
}

func (m *Module) deleteAPIKey(c *gin.Context) {
	projectID := c.Param("project-id")

	key, err := m.getProjectAPIKey(c, projectID, c.Param("apikey-id"))
	if err != nil {
		writeError(c, err, "failed to retrieve API key")
		return
	}
	id, _ := auth.APIKeyIDFromObjectName(key.Name)

	// Revoke permissions first so a partially failed delete never leaves a privileged key behind.
	if err := m.setKeyRoles(c, projectID, auth.APIKeySubject(id), nil, false); err != nil {
		writeError(c, err, "failed to remove API key roles")
		return
	}

	err = m.client.Resource(apiKeyGVR).Namespace(m.cfg.Namespace).Delete(c, key.Name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		writeError(c, err, "failed to delete API key")
		return
	}

	c.Status(http.StatusNoContent)
}

func (m *Module) createAPIKeyObject(ctx context.Context, project *infrastructurev1alpha1.Project, displayName string, expiresAt *time.Time, credential auth.APIKeyCredential) (*infrastructurev1alpha1.APIKey, error) {
	apiKey := &infrastructurev1alpha1.APIKey{
		TypeMeta: metav1.TypeMeta{
			APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
			Kind:       "APIKey",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      auth.APIKeyObjectName(credential.ID),
			Namespace: m.cfg.Namespace,
			Labels: map[string]string{
				projectLabel: project.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
					Kind:       "Project",
					Name:       project.Name,
					UID:        project.UID,
				},
			},
		},
		Spec: infrastructurev1alpha1.APIKeySpec{
			DisplayName: displayName,
			ProjectRef:  &corev1.LocalObjectReference{Name: project.Name},
			Credential: infrastructurev1alpha1.CredentialSpec{
				FormatVersion: auth.APIKeyFormatVersion,
				Algorithm:     auth.APIKeyDigestAlgorithm,
				Digest:        credential.Digest,
			},
			ExpiresAt: toMetaTime(expiresAt),
		},
	}

	obj, err := toUnstructured(apiKey)
	if err != nil {
		return nil, err
	}

	createdObj, err := m.client.Resource(apiKeyGVR).Namespace(m.cfg.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return fromUnstructured[infrastructurev1alpha1.APIKey](createdObj)
}

// setKeyRoles replaces all RBAC group assignments of the API key subject in the project.
func (m *Module) setKeyRoles(c *gin.Context, projectID string, subject string, roles []string, validate bool) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		project, err := m.getProject(c, projectID)
		if err != nil {
			var reqErr *requestError
			if !validate && len(roles) == 0 && errors.As(err, &reqErr) && reqErr.status == http.StatusNotFound {
				return nil
			}
			return err
		}

		if validate {
			if err := m.validateRoles(c, project, roles); err != nil {
				return err
			}
		}

		groups := []infrastructurev1alpha1.RuleSpec{}
		for _, g := range project.Spec.Rbac.Groups {
			if isKeyGroup(g, subject, project.Name) {
				continue
			}
			groups = append(groups, g)
		}
		for _, role := range roles {
			groups = append(groups, infrastructurev1alpha1.RuleSpec{
				PType: "g",
				V0:    subject,
				V1:    role,
				V2:    project.Name,
			})
		}

		if slices.Equal(groups, project.Spec.Rbac.Groups) {
			return nil
		}
		project.Spec.Rbac.Groups = groups

		obj, err := toUnstructured(project)
		if err != nil {
			return err
		}
		_, err = m.client.Resource(projectGVR).Namespace(m.cfg.Namespace).Update(c, obj, metav1.UpdateOptions{})
		return err
	})
}

// validateRoles ensures each role exists in the project and that the caller already holds
// every permission the role grants, preventing privilege escalation through API keys.
func (m *Module) validateRoles(c *gin.Context, project *infrastructurev1alpha1.Project, roles []string) error {
	policies := map[string][]infrastructurev1alpha1.RuleSpec{}
	for _, rule := range project.Spec.Rbac.Rules {
		if rule.PType == "p" && rule.V1 == project.Name {
			policies[rule.V0] = append(policies[rule.V0], rule)
		}
	}

	for _, role := range roles {
		rolePolicies, ok := policies[role]
		if !ok {
			return newRequestError(http.StatusBadRequest, "unknown role %q in project %q", role, project.Name)
		}
		for _, policy := range rolePolicies {
			allowed, err := m.callerAllowed(c, project.Name, policy.V2, policy.V3)
			if err != nil {
				return err
			}
			if !allowed {
				return newRequestError(http.StatusForbidden, "cannot assign role %q: it grants permissions you do not have", role)
			}
		}
	}

	return nil
}

func (m *Module) callerAllowed(c *gin.Context, domain, resource, action string) (bool, error) {
	subjects := []string{c.GetString("user_id")}
	for g := range strings.SplitSeq(c.GetString("groups"), ",") {
		subjects = append(subjects, g)
	}

	for _, subject := range subjects {
		if subject == "" {
			continue
		}
		allowed, err := m.enforcer.Enforce(subject, domain, resource, action)
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
	}
	return false, nil
}

func (m *Module) getProject(ctx context.Context, projectID string) (*infrastructurev1alpha1.Project, error) {
	obj, err := m.client.Resource(projectGVR).Namespace(m.cfg.Namespace).Get(ctx, projectID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, newRequestError(http.StatusNotFound, "project not found")
		}
		return nil, err
	}
	return fromUnstructured[infrastructurev1alpha1.Project](obj)
}

func (m *Module) getProjectAPIKey(ctx context.Context, projectID, name string) (*infrastructurev1alpha1.APIKey, error) {
	if _, ok := auth.APIKeyIDFromObjectName(name); !ok {
		return nil, newRequestError(http.StatusNotFound, "API key not found")
	}

	obj, err := m.client.Resource(apiKeyGVR).Namespace(m.cfg.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, newRequestError(http.StatusNotFound, "API key not found")
		}
		return nil, err
	}

	key, err := fromUnstructured[infrastructurev1alpha1.APIKey](obj)
	if err != nil {
		return nil, err
	}
	if !belongsToProject(key, projectID) {
		return nil, newRequestError(http.StatusNotFound, "API key not found")
	}
	return key, nil
}

func belongsToProject(key *infrastructurev1alpha1.APIKey, projectID string) bool {
	return key.Labels[projectLabel] == projectID &&
		key.Spec.ProjectRef != nil &&
		key.Spec.ProjectRef.Name == projectID
}

func isKeyGroup(rule infrastructurev1alpha1.RuleSpec, subject, projectID string) bool {
	return rule.PType == "g" && rule.V0 == subject && rule.V2 == projectID
}

func normalizeRoles(roles []string) ([]string, error) {
	normalized := []string{}
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			return nil, newRequestError(http.StatusBadRequest, "roles must not contain empty values")
		}
		if !slices.Contains(normalized, role) {
			normalized = append(normalized, role)
		}
	}
	slices.Sort(normalized)
	return normalized, nil
}

// toDto never exposes the credential digest. Roles are resolved from the project RBAC groups
// when a project is given.
func toDto(key *infrastructurev1alpha1.APIKey, project *infrastructurev1alpha1.Project) (APIKeyDto, bool) {
	id, ok := auth.APIKeyIDFromObjectName(key.Name)
	if !ok {
		return APIKeyDto{}, false
	}
	subject := auth.APIKeySubject(id)

	roles := []string{}
	if project != nil {
		for _, g := range project.Spec.Rbac.Groups {
			if isKeyGroup(g, subject, project.Name) && !slices.Contains(roles, g.V1) {
				roles = append(roles, g.V1)
			}
		}
		slices.Sort(roles)
	}

	projectName := ""
	if key.Spec.ProjectRef != nil {
		projectName = key.Spec.ProjectRef.Name
	}

	var expiresAt *time.Time
	if key.Spec.ExpiresAt != nil {
		t := key.Spec.ExpiresAt.UTC()
		expiresAt = &t
	}

	return APIKeyDto{
		ID:          key.Name,
		Name:        subject,
		DisplayName: key.Spec.DisplayName,
		Project:     projectName,
		Roles:       roles,
		ExpiresAt:   expiresAt,
		CreatedAt:   key.CreationTimestamp.Time,
	}, true
}

// toMetaTime converts an optional expiry; nil means the key never expires.
func toMetaTime(t *time.Time) *metav1.Time {
	if t == nil {
		return nil
	}
	mt := metav1.NewTime(t.UTC())
	return &mt
}

func writeError(c *gin.Context, err error, fallback string) {
	var reqErr *requestError
	if errors.As(err, &reqErr) {
		c.JSON(reqErr.status, gin.H{"error": reqErr.message})
		return
	}

	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		c.JSON(int(statusErr.ErrStatus.Code), gin.H{
			"error":   statusErr.ErrStatus.Message,
			"reason":  statusErr.ErrStatus.Reason,
			"details": statusErr.ErrStatus.Details,
		})
		return
	}

	logger.L().Error(fallback, zap.Error(err))
	c.JSON(http.StatusInternalServerError, gin.H{"error": fallback})
}

func toUnstructured(obj any) (*unstructured.Unstructured, error) {
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: objMap}, nil
}

func fromUnstructured[T any](obj *unstructured.Unstructured) (*T, error) {
	out := new(T)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, out); err != nil {
		return nil, err
	}
	return out, nil
}
