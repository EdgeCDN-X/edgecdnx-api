package locations

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

var healthCheckProfileGVR = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "healthcheckprofiles",
}

func (m *Module) registerHealthCheckProfileRoutes(r *gin.Engine) {
	group := r.Group("project/:project-id/healthcheckprofiles", m.middlewares...)
	authorize := func(action string) gin.HandlerFunc {
		return auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("healthcheckprofile").S("user_id").A(action).Build()
	}
	group.GET("", authorize("read"), func(c *gin.Context) {
		objects, err := m.client.Resource(healthCheckProfileGVR).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{
			LabelSelector: labels.Set{locationTenantLabel: c.Param("project-id")}.String(),
		})
		if err != nil {
			writeHealthCheckProfileError(c, "list", err)
			return
		}
		profiles := make([]infrastructurev1alpha1.HealthCheckProfile, 0, len(objects.Items))
		for _, object := range objects.Items {
			var profile infrastructurev1alpha1.HealthCheckProfile
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &profile); err != nil {
				writeHealthCheckProfileError(c, "convert", err)
				return
			}
			profiles = append(profiles, profile)
		}
		c.JSON(http.StatusOK, profiles)
	})
	group.GET("/:profile-id", authorize("read"), func(c *gin.Context) {
		if object, ok := m.getProjectHealthCheckProfile(c); ok {
			writeHealthCheckProfileResponse(c, http.StatusOK, object)
		}
	})
	group.POST("", authorize("create"), func(c *gin.Context) {
		var dto CreateHealthCheckProfileDto
		if err := bindHealthCheckProfileJSON(c, &dto); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}
		if problems := validation.IsDNS1123Subdomain(dto.Name); len(problems) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid health check profile name", "details": problems})
			return
		}
		profileLabels, err := mergeLocationLabels(nil, c.Param("project-id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := validateHealthCheckProbes(dto.Probes); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		profile := &infrastructurev1alpha1.HealthCheckProfile{
			TypeMeta:   metav1.TypeMeta{APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(), Kind: "HealthCheckProfile"},
			ObjectMeta: metav1.ObjectMeta{Name: dto.Name, Namespace: m.cfg.Namespace, Labels: profileLabels},
			Spec:       infrastructurev1alpha1.HealthCheckProfileSpec{Probes: dto.Probes},
		}
		object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(profile)
		if err != nil {
			writeHealthCheckProfileError(c, "convert", err)
			return
		}
		created, err := m.client.Resource(healthCheckProfileGVR).Namespace(m.cfg.Namespace).Create(c, &unstructured.Unstructured{Object: object}, metav1.CreateOptions{})
		if err != nil {
			writeHealthCheckProfileError(c, "create", err)
			return
		}
		writeHealthCheckProfileResponse(c, http.StatusCreated, created)
	})
	group.PATCH("/:profile-id", authorize("update"), func(c *gin.Context) {
		var dto UpdateHealthCheckProfileDto
		if err := bindHealthCheckProfileJSON(c, &dto); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}
		object, ok := m.getProjectHealthCheckProfile(c)
		if !ok {
			return
		}
		if dto.Probes != nil {
			if err := validateHealthCheckProbes(*dto.Probes); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			spec, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&infrastructurev1alpha1.HealthCheckProfileSpec{Probes: *dto.Probes})
			if err != nil {
				writeHealthCheckProfileError(c, "convert", err)
				return
			}
			object.Object["spec"] = spec
		}
		object.SetLabels(map[string]string{locationTenantLabel: c.Param("project-id")})
		updated, err := m.client.Resource(healthCheckProfileGVR).Namespace(m.cfg.Namespace).Update(c, object, metav1.UpdateOptions{})
		if err != nil {
			writeHealthCheckProfileError(c, "update", err)
			return
		}
		writeHealthCheckProfileResponse(c, http.StatusOK, updated)
	})
	group.DELETE("/:profile-id", authorize("delete"), func(c *gin.Context) {
		object, ok := m.getProjectHealthCheckProfile(c)
		if !ok {
			return
		}
		uid, version := object.GetUID(), object.GetResourceVersion()
		err := m.client.Resource(healthCheckProfileGVR).Namespace(m.cfg.Namespace).Delete(c, object.GetName(), metav1.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &version},
		})
		if err != nil {
			writeHealthCheckProfileError(c, "delete", err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func bindHealthCheckProfileJSON(c *gin.Context, dto interface{}) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dto); err != nil {
		return err
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		return fmt.Errorf("request body must contain a single JSON object")
	}
	return binding.Validator.ValidateStruct(dto)
}

func (m *Module) getProjectHealthCheckProfile(c *gin.Context) (*unstructured.Unstructured, bool) {
	object, err := m.client.Resource(healthCheckProfileGVR).Namespace(m.cfg.Namespace).Get(c, c.Param("profile-id"), metav1.GetOptions{})
	if err != nil {
		writeHealthCheckProfileError(c, "get", err)
		return nil, false
	}
	if object.GetLabels()[locationTenantLabel] != c.Param("project-id") {
		writeHealthCheckProfileError(c, "get", apierrors.NewNotFound(healthCheckProfileGVR.GroupResource(), c.Param("profile-id")))
		return nil, false
	}
	return object, true
}

func writeHealthCheckProfileResponse(c *gin.Context, status int, object *unstructured.Unstructured) {
	var profile infrastructurev1alpha1.HealthCheckProfile
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &profile); err != nil {
		writeHealthCheckProfileError(c, "convert", err)
		return
	}
	c.JSON(status, profile)
}

func writeHealthCheckProfileError(c *gin.Context, operation string, err error) {
	if !writeAPIStatusError(c, err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to " + operation + " health check profile: " + err.Error()})
	}
}

func validateHealthCheckProbes(probes []infrastructurev1alpha1.HealthCheckProbeSpec) error {
	if len(probes) == 0 {
		return fmt.Errorf("at least one probe is required")
	}
	names := make(map[string]bool, len(probes))
	validStack := func(stack infrastructurev1alpha1.StackType) bool {
		return stack == "" || stack == "IPv4" || stack == "IPv6" || stack == "Dual"
	}
	validPort := func(port int32) bool { return port >= 1 && port <= 65535 }
	for _, probe := range probes {
		if strings.TrimSpace(probe.Name) == "" || names[probe.Name] {
			return fmt.Errorf("probe names must be nonempty and unique")
		}
		names[probe.Name] = true
		if probe.Interval.Duration < 0 || probe.Timeout.Duration < 0 {
			return fmt.Errorf("probe %s: interval and timeout cannot be negative", probe.Name)
		}
		switch probe.Type {
		case infrastructurev1alpha1.HealthCheckProbeTypeTCP:
			if probe.TCP == nil || probe.HTTP != nil || probe.Assume != nil {
				return fmt.Errorf("probe %s: TCP requires only tcp configuration", probe.Name)
			}
			if !validPort(probe.TCP.Port) || !validStack(probe.TCP.Stack) {
				return fmt.Errorf("probe %s: invalid TCP port or stack", probe.Name)
			}
		case infrastructurev1alpha1.HealthCheckProbeTypeHTTP:
			if probe.HTTP == nil || probe.TCP != nil || probe.Assume != nil {
				return fmt.Errorf("probe %s: HTTP requires only http configuration", probe.Name)
			}
			if (probe.HTTP.Protocol != "http" && probe.HTTP.Protocol != "https") ||
				(probe.HTTP.Port != 0 && !validPort(probe.HTTP.Port)) || !validStack(probe.HTTP.Stack) {
				return fmt.Errorf("probe %s: invalid HTTP protocol, port or stack", probe.Name)
			}
		case infrastructurev1alpha1.HealthCheckProbeTypeASSUME:
			if probe.Assume == nil || probe.TCP != nil || probe.HTTP != nil {
				return fmt.Errorf("probe %s: ASSUME requires only assume configuration", probe.Name)
			}
			if (probe.Assume.Status != infrastructurev1alpha1.AssumedHealthStatusHealthy &&
				probe.Assume.Status != infrastructurev1alpha1.AssumedHealthStatusUnhealthy) || !validStack(probe.Assume.Stack) {
				return fmt.Errorf("probe %s: invalid assumed status or stack", probe.Name)
			}
		default:
			return fmt.Errorf("probe %s: type must be TCP, HTTP (including HTTPS), or ASSUME", probe.Name)
		}
	}
	return nil
}
