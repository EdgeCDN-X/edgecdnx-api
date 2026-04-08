package admin

import (
	"net/http"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var prefixListGVR = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "prefixlists",
}

var zoneGVR = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "zones",
}

var locationGVR = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "locations",
}

func (m *Module) RegisterRoutes(r *gin.Engine) {
	group := r.Group("/admin", m.middlewares...)

	group.GET("/prefixlists", auth.NewAuthzBuilder().E(m.enforcer).ST(m.cfg.DefaultAdminProject).R("prefixlist").S("user_id").A("read").Build(), func(c *gin.Context) {
		objList, err := m.dynClient.Resource(prefixListGVR).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to list prefixlists: " + err.Error()})
			return
		}
		items := []infrastructurev1alpha1.PrefixList{}
		for _, item := range objList.Items {
			pl := &infrastructurev1alpha1.PrefixList{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, pl); err != nil {
				c.JSON(500, gin.H{"error": "failed to convert prefixlist: " + err.Error()})
				return
			}
			items = append(items, *pl)
		}
		c.JSON(200, items)
	})

	group.GET("/zones", auth.NewAuthzBuilder().E(m.enforcer).ST(m.cfg.DefaultAdminProject).R("zone").S("user_id").A("read").Build(), func(c *gin.Context) {
		objList, err := m.dynClient.Resource(zoneGVR).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to list zones: " + err.Error()})
			return
		}
		items := []infrastructurev1alpha1.Zone{}
		for _, item := range objList.Items {
			z := &infrastructurev1alpha1.Zone{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, z); err != nil {
				c.JSON(500, gin.H{"error": "failed to convert zone: " + err.Error()})
				return
			}
			items = append(items, *z)
		}
		c.JSON(200, items)
	})

	group.GET("/locations", auth.NewAuthzBuilder().E(m.enforcer).ST(m.cfg.DefaultAdminProject).R("location").S("user_id").A("read").Build(), func(c *gin.Context) {
		objList, err := m.dynClient.Resource(locationGVR).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to list locations: " + err.Error()})
			return
		}
		items := []infrastructurev1alpha1.Location{}
		for _, item := range objList.Items {
			l := &infrastructurev1alpha1.Location{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, l); err != nil {
				c.JSON(500, gin.H{"error": "failed to convert location: " + err.Error()})
				return
			}
			items = append(items, *l)
		}
		c.JSON(200, items)
	})

	group.GET("/location-healths", auth.NewAuthzBuilder().E(m.enforcer).ST(m.cfg.DefaultAdminProject).R("location").S("user_id").A("read").Build(), func(c *gin.Context) {
		if m.prometheus == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "prometheus client is not configured"})
			return
		}

		response, err := m.prometheus.Query(c.Request.Context(), `probe_success{endpoint="location"}`)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to query prometheus: " + err.Error()})
			return
		}

		healthResponse, err := m.buildLocationHealthResponse(c.Request.Context(), response)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build location health response: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, healthResponse)
	})
}
