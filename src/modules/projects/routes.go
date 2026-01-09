package projects

import (
	"fmt"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (m *Module) RegisterRoutes(r *gin.Engine, middleWares ...gin.HandlerFunc) {
	group := r.Group("/projects", middleWares...)

	group.GET("", func(c *gin.Context) {
		p := m.Informer.GetIndexer().List()
		projectList := make([]*infrastructurev1alpha1.Project, 0, len(p))
		for _, obj := range p {
			p_raw, ok := obj.(*unstructured.Unstructured)
			if !ok {
				c.JSON(500, gin.H{"error": "internal error"})
				return
			}

			project := &infrastructurev1alpha1.Project{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(p_raw.Object, project)
			if err != nil {
				c.JSON(500, gin.H{"error": "internal error"})
				return
			}

			projectList = append(projectList, project)
		}

		claims, _ := c.Get("claims")
		// Try to log claims as is, or assert to a slice/map if needed
		if claimsMap, ok := claims.(map[string]interface{}); ok {
			logger.L().Info("Claims from token", zap.Any("claim", claimsMap["email"]))
		} else {
			logger.L().Info("Claims from token: unable to assert claims type", zap.Any("claim", claims))
		}

		c.JSON(200, projectList)
		return
	})

	group.GET(":project", func(c *gin.Context) {
		name := c.Param("projectName")
		p, _ := m.Informer.GetIndexer().ByIndex("projectName", name)

		if len(p) == 0 {
			c.JSON(404, gin.H{"error": "project not found"})
			return
		}

		if len(p) > 1 {
			logger.L().Error("Multiple projects found with the same name", zap.String("name", name))
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		c.JSON(200, p[0].(*v1.Namespace))
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
			c.JSON(retCode, gin.H{"error": "failed to create project: " + err.Error()})
			return
		}

		c.JSON(retCode, proj)
		return
	})
}

func (m *Module) createProject(c *gin.Context, dto ProjectDto) (infrastructurev1alpha1.Project, int, error) {

	p, err := m.Informer.GetIndexer().ByIndex("projectName", dto.Name)

	if err != nil {
		return infrastructurev1alpha1.Project{}, 500, err
	}

	if len(p) > 0 {
		return infrastructurev1alpha1.Project{}, 409, fmt.Errorf("project with name %s already exists", dto.Name)
	}

	// Generate UUID for project name
	uuid := uuid.New().String()
	project := &infrastructurev1alpha1.Project{
		TypeMeta: metav1.TypeMeta{
			APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
			Kind:       "Project",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: uuid,
		},
		Spec: infrastructurev1alpha1.ProjectSpec{
			Name:        dto.Name,
			Description: dto.Description,
			Rbac: infrastructurev1alpha1.RBACSpec{
				Groups: []infrastructurev1alpha1.RuleSpec{},
				Rules: []infrastructurev1alpha1.RuleSpec{
					{
						PType: "p",
						V0:    "admin",
						V1:    uuid,
						V2:    "*",
						V3:    "create",
					},
					{
						PType: "p",
						V0:    "admin",
						V1:    uuid,
						V2:    "*",
						V3:    "read",
					},
					{
						PType: "p",
						V0:    "admin",
						V1:    uuid,
						V2:    "*",
						V3:    "write",
					},
					{
						PType: "p",
						V0:    "admin",
						V1:    uuid,
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
		return infrastructurev1alpha1.Project{}, 500, err
	}

	returnedProject := &infrastructurev1alpha1.Project{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(createdObj.Object, returnedProject)
	if err != nil {
		return infrastructurev1alpha1.Project{}, 500, err
	}

	return *returnedProject, 201, nil
}
