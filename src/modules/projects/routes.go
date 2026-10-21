package projects

import (
	"context"
	"fmt"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	"github.com/gosimple/slug"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

var gvr = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "projects",
}

func (m *Module) RegisterRoutes(r *gin.Engine) {
	group := r.Group("/projects", m.middlewares...)
	m.registerRoleRoutes(group)
	m.registerMemberRoutes(group)

	group.GET("", func(c *gin.Context) {

		objList, err := app.ListResources(c, m.client, gvr, m.scope().WatchNamespace())
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to list projects: " + err.Error()})
			return
		}

		projects := []infrastructurev1alpha1.Project{}
		apiKeyProject, isAPIKey := auth.APIKeyProject(c)

		for _, item := range objList.Items {
			if m.cfg.Namespaced && item.GetName() != item.GetNamespace() {
				continue
			}
			project := &infrastructurev1alpha1.Project{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, project)
			if err != nil {
				c.JSON(500, gin.H{"error": "internal error"})
				return
			}
			if isAPIKey && project.Name != apiKeyProject {
				continue
			}

			allowed, err := m.enforcer.Enforce(c.GetString("user_id"), project.Name, "project", "read")
			if err != nil {
				continue
			}
			if !allowed {
				groups := c.GetString("groups")
				logger.L().Debug("User does not have access to project, checking group permissions", zap.String("project", project.Name), zap.String("groups", groups))
				for g := range strings.SplitSeq(groups, ",") {
					logger.L().Debug("Checking access for oidc group", zap.String("group", g), zap.String("project", project.Name))
					allowed, err := m.enforcer.Enforce(g, project.Name, "project", "read")
					if err != nil {
						continue
					}

					if allowed {
						projects = append(projects, *project)
						break
					}
				}
				continue
			}

			projects = append(projects, *project)
		}

		c.JSON(200, projects)

		return
	})

	group.GET(":project-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R(auth.ResourceTypeProject).S("user_id").A("read").Build(), func(c *gin.Context) {

		obj, err := m.client.Resource(gvr).Namespace(m.scope().ResourceNamespace(c.Param("project-id"))).Get(c, c.Param("project-id"), metav1.GetOptions{})

		if err != nil {
			c.JSON(404, gin.H{"error": "project not found"})
			return
		}

		project := &infrastructurev1alpha1.Project{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, project)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		c.JSON(200, project)
		return
	})

	group.POST("", func(c *gin.Context) {
		if auth.IsAPIKeyRequest(c) {
			c.JSON(403, gin.H{"error": "API keys cannot create projects"})
			return
		}

		var dto ProjectDto
		if err := c.ShouldBind(&dto); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}
		created_by := slug.Make(strings.ReplaceAll(c.GetString("user_id"), "@", "-at-"))
		user_id := c.GetString("user_id")
		proj, retCode, err := m.createProject(c, user_id, created_by, dto)
		if err != nil {
			c.JSON(retCode, gin.H{"error": err.Error()})
			return
		}

		c.JSON(retCode, proj)
		return
	})
}

func (m *Module) createProject(ctx context.Context, user_id string, created_by string, dto ProjectDto) (infrastructurev1alpha1.Project, int, error) {
	name := slug.Make(dto.Name)
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	if problems := validation.IsDNS1123Label(name); len(problems) != 0 {
		return infrastructurev1alpha1.Project{}, 400, fmt.Errorf("invalid project name: %v", problems)
	}
	namespace := m.scope().ResourceNamespace(name)

	obj, err := m.client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if obj != nil {
		// Project with this name already exists
		return infrastructurev1alpha1.Project{}, 409, apierrors.NewAlreadyExists(gvr.GroupResource(), name)
	}

	if err != nil && !apierrors.IsNotFound(err) {
		// An error other than "not found" occurred
		return infrastructurev1alpha1.Project{}, 500, fmt.Errorf("failed to check for existing project: %w", err)
	}
	if m.cfg.Namespaced {
		namespaces := m.client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"})
		managedNamespace := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Namespace",
			"metadata": map[string]interface{}{"name": namespace, "labels": map[string]interface{}{
				"app.kubernetes.io/managed-by": "edgecdnx-api", "edgecdnx.com/project-name": name,
			}},
		}}
		if _, err := namespaces.Create(ctx, managedNamespace, metav1.CreateOptions{}); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return infrastructurev1alpha1.Project{}, 500, fmt.Errorf("failed to create project namespace: %w", err)
			}
			existing, err := namespaces.Get(ctx, namespace, metav1.GetOptions{})
			if err != nil {
				return infrastructurev1alpha1.Project{}, 500, fmt.Errorf("failed to inspect project namespace: %w", err)
			}
			if existing.GetLabels()["app.kubernetes.io/managed-by"] != "edgecdnx-api" || existing.GetLabels()["edgecdnx.com/project-name"] != name || existing.GetDeletionTimestamp() != nil {
				return infrastructurev1alpha1.Project{}, 409, fmt.Errorf("namespace %q already exists and cannot be adopted", namespace)
			}
		}
	}

	project := &infrastructurev1alpha1.Project{
		TypeMeta: metav1.TypeMeta{
			APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
			Kind:       "Project",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"edgecdnx.com/project-name": name,
				"edgecdnx.com/created-by":   created_by,
			},
		},
		Spec: infrastructurev1alpha1.ProjectSpec{
			Name:        dto.Name,
			Description: dto.Description,
			Rbac: infrastructurev1alpha1.RBACSpec{
				Groups: []infrastructurev1alpha1.RuleSpec{
					{
						PType: "g",
						V0:    user_id,
						V1:    "admin",
						V2:    name,
					},
				},
				Rules: []infrastructurev1alpha1.RuleSpec{
					{
						PType: "p",
						V0:    "admin",
						V1:    name,
						V2:    "*",
						V3:    "create",
					},
					{
						PType: "p",
						V0:    "admin",
						V1:    name,
						V2:    "*",
						V3:    "read",
					},
					{
						PType: "p",
						V0:    "admin",
						V1:    name,
						V2:    "*",
						V3:    "update",
					},
					{
						PType: "p",
						V0:    "admin",
						V1:    name,
						V2:    "*",
						V3:    "delete",
					},
				},
			},
		},
	}

	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(project)
	if err != nil {
		return infrastructurev1alpha1.Project{}, 500, err
	}

	projectUnstructured := unstructured.Unstructured{
		Object: objMap,
	}

	createdObj, err := m.client.Resource(schema.GroupVersionResource{
		Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
		Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
		Resource: "projects",
	}).Namespace(namespace).Create(ctx, &projectUnstructured, metav1.CreateOptions{})

	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return infrastructurev1alpha1.Project{}, 409, err
		}
		if apierrors.IsBadRequest(err) {
			return infrastructurev1alpha1.Project{}, 409, err
		}

		return infrastructurev1alpha1.Project{}, 500, err
	}

	returnedProject := &infrastructurev1alpha1.Project{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(createdObj.Object, returnedProject)
	if err != nil {
		return infrastructurev1alpha1.Project{}, 500, err
	}

	return *returnedProject, 201, nil
}
