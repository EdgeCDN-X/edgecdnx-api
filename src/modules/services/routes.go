package services

import (
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var gvr = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "services",
}

func (m *Module) RegisterRoutes(r *gin.Engine) {
	group := r.Group("project/:project-id/services", m.middlewares...)

	group.GET("", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("service").S("user_id").A("read").Build(), func(c *gin.Context) {
		objList, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{
			LabelSelector: "project=" + c.Param("project-id"),
		})

		if err != nil {
			c.JSON(500, gin.H{"error": "failed to list services: " + err.Error()})
			return
		}

		services := []infrastructurev1alpha1.Service{}

		for _, item := range objList.Items {
			service := &infrastructurev1alpha1.Service{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, service)
			if err != nil {
				c.JSON(500, gin.H{"error": "internal error"})
				return
			}
			services = append(services, *service)
		}

		c.JSON(200, services)
		return
	})
}
