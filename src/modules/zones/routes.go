package zones

import (
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var gvr = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "zones",
}

func (m *Module) RegisterRoutes(r *gin.Engine) {
	group := r.Group("project/:project-id/zones", m.middlewares...)

	group.GET("", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("zone").S("user_id").A("read").Build(), func(c *gin.Context) {
		objList, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{
			LabelSelector: "project=" + c.Param("project-id"),
		})

		if err != nil {
			c.JSON(500, gin.H{"error": "failed to list zones: " + err.Error()})
			return
		}

		zones := []infrastructurev1alpha1.Zone{}

		for _, item := range objList.Items {
			zone := &infrastructurev1alpha1.Zone{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), zone); err != nil {
				c.JSON(500, gin.H{"error": "failed to convert zone: " + err.Error()})
				return
			}
			zones = append(zones, *zone)
		}

		c.JSON(200, zones)
		return
	})

	group.POST("", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("zone").S("user_id").A("create").Build(), func(c *gin.Context) {
		var dto CreteZoneDto
		if err := c.ShouldBindJSON(&dto); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}

		zone := &infrastructurev1alpha1.Zone{
			TypeMeta: metav1.TypeMeta{
				APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
				Kind:       "Zone",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      dto.Zone,
				Namespace: m.cfg.Namespace,
				Labels: map[string]string{
					"project": c.Param("project-id"),
				},
			},
			Spec: infrastructurev1alpha1.ZoneSpec{
				Email: m.EmailToRname(dto.Email),
				Zone:  dto.Zone,
			},
		}

		objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(zone)
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				c.JSON(409, gin.H{"error": "zone with the same name already exists. Zones must have unique names within the platform."})
				return
			}

			if apierrors.IsBadRequest(err) {
				c.JSON(400, gin.H{"error": "bad request: " + err.Error()})
				return
			}

			c.JSON(500, gin.H{"error": "failed to convert zone: " + err.Error()})
			return
		}

		createdObj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Create(c, &unstructured.Unstructured{Object: objMap}, metav1.CreateOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to create zone: " + err.Error()})
			return
		}

		createdZone := &infrastructurev1alpha1.Zone{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(createdObj.UnstructuredContent(), createdZone); err != nil {
			c.JSON(500, gin.H{"error": "failed to convert created zone: " + err.Error()})
			return
		}

		c.JSON(201, createdZone)
		return
	})
}
