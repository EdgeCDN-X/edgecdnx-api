package locations

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	locationProjectLabel = "project"
)

var locationGVR = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "locations",
}

func (m *Module) RegisterRoutes(r *gin.Engine) {
	m.registerHealthCheckProfileRoutes(r)
	group := r.Group("project/:project-id/locations", m.middlewares...)
	group.GET("", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("location").S("user_id").A("read").Build(), func(c *gin.Context) {
		objects, err := m.client.Resource(locationGVR).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{
			LabelSelector: labels.Set{locationProjectLabel: c.Param("project-id")}.String(),
		})
		if err != nil {
			writeLocationError(c, "list", err)
			return
		}
		locations := make([]infrastructurev1alpha1.Location, 0, len(objects.Items))
		for _, object := range objects.Items {
			var location infrastructurev1alpha1.Location
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &location); err != nil {
				writeLocationError(c, "convert", err)
				return
			}
			locations = append(locations, location)
		}
		c.JSON(http.StatusOK, locations)
	})

	group.GET("/:location-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("location").S("user_id").A("read").Build(), func(c *gin.Context) {
		object, ok := m.getProjectLocation(c)
		if !ok {
			return
		}
		writeLocationResponse(c, http.StatusOK, object)
	})

	group.GET("/:location-id/healthchecks", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("location").S("user_id").A("read").Build(), m.getLocationHealthchecks)

	group.POST("", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("location").S("user_id").A("create").Build(), func(c *gin.Context) {
		var dto CreateLocationDto
		if err := c.ShouldBindJSON(&dto); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}
		if problems := validation.IsDNS1123Subdomain(dto.Name); len(problems) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid location name", "details": problems})
			return
		}
		locationLabels, err := mergeLocationLabels(dto.Labels, c.Param("project-id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		location := &infrastructurev1alpha1.Location{
			TypeMeta: metav1.TypeMeta{
				APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
				Kind:       "Location",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: dto.Name, Namespace: m.cfg.Namespace,
				Labels: locationLabels,
			},
			Spec: infrastructurev1alpha1.LocationSpec{
				NodeGroups: dto.NodeGroups, GeoLookup: dto.GeoLookup,
				Weight: dto.Weight, FallbackLocations: dto.FallbackLocations,
			},
		}
		if err := validateLocationSpec(location.Spec, c.Param("project-id")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(location)
		if err != nil {
			writeLocationError(c, "convert", err)
			return
		}
		created, err := m.client.Resource(locationGVR).Namespace(m.cfg.Namespace).Create(c, &unstructured.Unstructured{Object: object}, metav1.CreateOptions{})
		if err != nil {
			writeLocationError(c, "create", err)
			return
		}
		writeLocationResponse(c, http.StatusCreated, created)
	})

	group.PATCH("/:location-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("location").S("user_id").A("update").Build(), func(c *gin.Context) {
		var dto UpdateLocationDto
		if err := c.ShouldBindJSON(&dto); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}
		object, ok := m.getProjectLocation(c)
		if !ok {
			return
		}
		var location infrastructurev1alpha1.Location
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &location); err != nil {
			writeLocationError(c, "convert", err)
			return
		}
		if dto.NodeGroups != nil {
			location.Spec.NodeGroups = *dto.NodeGroups
		}
		if dto.GeoLookup != nil {
			location.Spec.GeoLookup = *dto.GeoLookup
		}
		if dto.Weight != nil {
			location.Spec.Weight = *dto.Weight
		}
		if dto.FallbackLocations != nil {
			location.Spec.FallbackLocations = *dto.FallbackLocations
		}
		if err := validateLocationSpec(location.Spec, c.Param("project-id")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		spec, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&location.Spec)
		if err != nil {
			writeLocationError(c, "convert", err)
			return
		}
		object.Object["spec"] = spec
		if dto.Labels != nil {
			locationLabels, err := mergeLocationLabels(*dto.Labels, c.Param("project-id"))
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			object.SetLabels(locationLabels)
		}
		updated, err := m.client.Resource(locationGVR).Namespace(m.cfg.Namespace).Update(c, object, metav1.UpdateOptions{})
		if err != nil {
			writeLocationError(c, "update", err)
			return
		}
		writeLocationResponse(c, http.StatusOK, updated)
	})

	group.DELETE("/:location-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("location").S("user_id").A("delete").Build(), func(c *gin.Context) {
		object, ok := m.getProjectLocation(c)
		if !ok {
			return
		}
		uid, version := object.GetUID(), object.GetResourceVersion()
		err := m.client.Resource(locationGVR).Namespace(m.cfg.Namespace).Delete(c, object.GetName(), metav1.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &version},
		})
		if err != nil {
			writeLocationError(c, "delete", err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func (m *Module) getProjectLocation(c *gin.Context) (*unstructured.Unstructured, bool) {
	object, err := m.client.Resource(locationGVR).Namespace(m.cfg.Namespace).Get(c, c.Param("location-id"), metav1.GetOptions{})
	if err != nil {
		writeLocationError(c, "get", err)
		return nil, false
	}
	if object.GetLabels()[locationProjectLabel] != c.Param("project-id") {
		writeLocationError(c, "get", apierrors.NewNotFound(locationGVR.GroupResource(), c.Param("location-id")))
		return nil, false
	}
	return object, true
}

func writeLocationResponse(c *gin.Context, status int, object *unstructured.Unstructured) {
	var location infrastructurev1alpha1.Location
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &location); err != nil {
		writeLocationError(c, "convert", err)
		return
	}
	c.JSON(status, location)
}

func writeLocationError(c *gin.Context, operation string, err error) {
	if !writeAPIStatusError(c, err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to " + operation + " location: " + err.Error()})
	}
}

func mergeLocationLabels(requested map[string]string, projectID string) (map[string]string, error) {
	if problems := validation.IsValidLabelValue(projectID); len(problems) > 0 {
		return nil, fmt.Errorf("invalid project ID for project label: %v", problems)
	}
	merged := make(map[string]string, len(requested)+2)
	for key, value := range requested {
		if key == locationProjectLabel {
			if value != projectID {
				return nil, fmt.Errorf("label %s is managed automatically and cannot be changed", key)
			}
			continue
		}
		if len(validation.IsQualifiedName(key)) > 0 || len(validation.IsValidLabelValue(value)) > 0 {
			return nil, fmt.Errorf("invalid location label: %s=%s", key, value)
		}
		merged[key] = value
	}
	merged[locationProjectLabel] = projectID
	return merged, nil
}

func validateLocationSpec(spec infrastructurev1alpha1.LocationSpec, projectID string) error {
	if problems := validation.IsValidLabelValue(projectID); len(problems) > 0 {
		return fmt.Errorf("invalid project ID for project label: %v", problems)
	}
	if spec.GeoLookup.Weight < 0 || spec.GeoLookup.Weight > 1000 {
		return fmt.Errorf("geoLookup.weight must be between 0 and 1000")
	}
	groups := make(map[string]struct{}, len(spec.NodeGroups))
	for _, group := range spec.NodeGroups {
		if group.Name == "" {
			return fmt.Errorf("node groups require a name")
		}
		key := group.Name + "\x00" + group.Flavor
		if _, exists := groups[key]; exists {
			return fmt.Errorf("duplicate node group name and flavor: %s/%s", group.Name, group.Flavor)
		}
		groups[key] = struct{}{}
		for key, value := range group.Labels {
			if len(validation.IsQualifiedName(key)) > 0 || len(validation.IsValidLabelValue(value)) > 0 {
				return fmt.Errorf("invalid node group label: %s=%s", key, value)
			}
			if key == locationProjectLabel && value != projectID {
				return fmt.Errorf("node group project label must match the project ID")
			}
		}
		nodes := make(map[string]struct{}, len(group.Nodes))
		for _, node := range group.Nodes {
			if node.Name == "" {
				return fmt.Errorf("nodes require a name")
			}
			if _, exists := nodes[node.Name]; exists {
				return fmt.Errorf("duplicate node name: %s", node.Name)
			}
			nodes[node.Name] = struct{}{}
		}
	}
	return nil
}

func writeAPIStatusError(c *gin.Context, err error) bool {
	var statusErr *apierrors.StatusError
	if !errors.As(err, &statusErr) {
		return false
	}

	c.JSON(int(statusErr.ErrStatus.Code), gin.H{
		"error":   statusErr.ErrStatus.Message,
		"reason":  statusErr.ErrStatus.Reason,
		"details": statusErr.ErrStatus.Details,
	})
	return true
}
