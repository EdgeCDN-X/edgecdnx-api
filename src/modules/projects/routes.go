package projects

import (
	"fmt"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	"github.com/gosimple/slug"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var gvr = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "projects",
}

func (m *Module) RegisterRoutes(r *gin.Engine) {
	group := r.Group("/projects", m.middlewares...)

	group.GET("", func(c *gin.Context) {

		objList, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to list projects: " + err.Error()})
			return
		}

		projects := []infrastructurev1alpha1.Project{}

		for _, item := range objList.Items {
			project := &infrastructurev1alpha1.Project{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, project)
			if err != nil {
				c.JSON(500, gin.H{"error": "internal error"})
				return
			}

			allowed, err := m.enforcer.Enforce(c.GetString("user_id"), project.Name, "project", "read")
			if err != nil || !allowed {
				continue
			}

			projects = append(projects, *project)
		}

		c.JSON(200, projects)

		return
	})

	group.GET(":project-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("project").S("user_id").A("read").Build(), func(c *gin.Context) {

		obj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, c.Param("project-id"), metav1.GetOptions{})

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

		var dto ProjectDto
		if err := c.ShouldBind(&dto); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}

		proj, retCode, err := m.createProject(c, dto)
		if err != nil {
			c.JSON(retCode, gin.H{"error": err.Error()})
			return
		}

		c.JSON(retCode, proj)
		return
	})
}

func (m *Module) createProject(c *gin.Context, dto ProjectDto) (infrastructurev1alpha1.Project, int, error) {
	slug.MaxLength = 63
	name := slug.Make(dto.Name)

	obj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, name, metav1.GetOptions{})
	if obj != nil {
		// Project with this name already exists
		return infrastructurev1alpha1.Project{}, 409, fmt.Errorf("project with the same name already exists. Projects must have unique names within the platform")
	}

	if err != nil && !apierrors.IsNotFound(err) {
		// An error other than "not found" occurred
		return infrastructurev1alpha1.Project{}, 500, fmt.Errorf("failed to check for existing project: %w", err)
	}

	project := &infrastructurev1alpha1.Project{
		TypeMeta: metav1.TypeMeta{
			APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
			Kind:       "Project",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"edgecdnx.com/project-name": name,
				"edgecdnx.com/created-by":   slug.Make(strings.ReplaceAll(c.GetString("user_id"), "@", "-at-")),
			},
		},
		Spec: infrastructurev1alpha1.ProjectSpec{
			Name:        dto.Name,
			Description: dto.Description,
			Rbac: infrastructurev1alpha1.RBACSpec{
				Groups: []infrastructurev1alpha1.RuleSpec{
					{
						PType: "g",
						V0:    c.GetString("user_id"),
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

	ns := m.cfg.Namespace

	createdObj, err := m.client.Resource(schema.GroupVersionResource{
		Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
		Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
		Resource: "projects",
	}).Namespace(ns).Create(c, &projectUnstructured, metav1.CreateOptions{})

	if err != nil {
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
