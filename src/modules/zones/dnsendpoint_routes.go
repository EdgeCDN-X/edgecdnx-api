package zones

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var dnsEndpointGVR = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "dnsendpoints",
}

var serviceGVR = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "services",
}

const serviceDNSEndpointRecordTTL = 10

func (m *Module) registerDNSEndpointRoutes(group *gin.RouterGroup) {
	group.GET("/:zone-id/dns-endpoints", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("zone").S("user_id").A("read").Build(), func(c *gin.Context) {
		zone, ok := m.getProjectZone(c)
		if !ok {
			return
		}

		objList, err := m.client.Resource(dnsEndpointGVR).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("project=%s,zone=%s", c.Param("project-id"), c.Param("zone-id")),
		})
		if err != nil {
			if writeAPIStatusError(c, err) {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list DNS endpoints: " + err.Error()})
			return
		}

		dnsEndpoints := make([]infrastructurev1alpha1.DNSEndpoint, 0, len(objList.Items))
		for _, item := range objList.Items {
			dnsEndpoint := &infrastructurev1alpha1.DNSEndpoint{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, dnsEndpoint); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to convert DNS endpoint: " + err.Error()})
				return
			}
			dnsEndpoints = append(dnsEndpoints, *dnsEndpoint)
		}

		serviceEndpoints, ok := m.serviceHostAliasDNSEndpoints(c, zone)
		if !ok {
			return
		}

		existing := make(map[string]struct{}, len(dnsEndpoints))
		for _, dnsEndpoint := range dnsEndpoints {
			existing[dnsEndpoint.Name] = struct{}{}
		}
		for _, dnsEndpoint := range serviceEndpoints {
			if _, found := existing[dnsEndpoint.Name]; found {
				continue
			}
			dnsEndpoints = append(dnsEndpoints, dnsEndpoint)
		}

		c.JSON(http.StatusOK, dnsEndpoints)
	})

	group.POST("/:zone-id/dns-endpoints", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("zone").S("user_id").A("update").Build(), func(c *gin.Context) {
		var dto CreateDNSEndpointDto
		if err := c.ShouldBindJSON(&dto); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}

		zone, ok := m.getProjectZone(c)
		if !ok {
			return
		}
		if !dnsNameBelongsToZone(dto.DNSName, zone.Spec.Zone) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("dnsName %q is outside zone %q", dto.DNSName, zone.Spec.Zone)})
			return
		}

		dnsEndpoint := &infrastructurev1alpha1.DNSEndpoint{
			TypeMeta: metav1.TypeMeta{
				APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
				Kind:       "DNSEndpoint",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      dnsEndpointResourceName(dto.DNSName, dto.RecordType),
				Namespace: m.cfg.Namespace,
				Labels: map[string]string{
					"project": c.Param("project-id"),
					"zone":    c.Param("zone-id"),
				},
			},
			Spec: infrastructurev1alpha1.DNSEndpointSpec{
				DNSName:       dto.DNSName,
				RoutingPolicy: "Simple",
				RecordTTL:     dto.RecordTTL,
				RecordType:    dto.RecordType,
				Targets:       dto.Targets,
			},
		}

		objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(dnsEndpoint)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to convert DNS endpoint: " + err.Error()})
			return
		}

		createdObj, err := m.client.Resource(dnsEndpointGVR).Namespace(m.cfg.Namespace).Create(c, &unstructured.Unstructured{Object: objMap}, metav1.CreateOptions{})
		if err != nil {
			if writeAPIStatusError(c, err) {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create DNS endpoint: " + err.Error()})
			return
		}

		createdDNSEndpoint := &infrastructurev1alpha1.DNSEndpoint{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(createdObj.Object, createdDNSEndpoint); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to convert created DNS endpoint: " + err.Error()})
			return
		}

		c.JSON(http.StatusCreated, createdDNSEndpoint)
	})

	group.PATCH("/:zone-id/dns-endpoints/:dns-endpoint-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("zone").S("user_id").A("update").Build(), func(c *gin.Context) {
		var dto UpdateDNSEndpointDto
		if err := c.ShouldBindJSON(&dto); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}

		zone, ok := m.getProjectZone(c)
		if !ok {
			return
		}
		obj, ok := m.getProjectZoneDNSEndpoint(c)
		if !ok {
			return
		}

		dnsEndpoint := &infrastructurev1alpha1.DNSEndpoint{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, dnsEndpoint); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to convert DNS endpoint: " + err.Error()})
			return
		}

		if dto.DNSName != "" {
			dnsEndpoint.Spec.DNSName = dto.DNSName
		}
		if dto.RecordTTL != nil {
			dnsEndpoint.Spec.RecordTTL = *dto.RecordTTL
		}
		if dto.RecordType != "" {
			dnsEndpoint.Spec.RecordType = dto.RecordType
		}
		if dto.Targets != nil {
			dnsEndpoint.Spec.Targets = dto.Targets
		}
		dnsEndpoint.Spec.RoutingPolicy = "Simple"
		dnsEndpoint.Spec.RouteSelector = nil

		if !dnsNameBelongsToZone(dnsEndpoint.Spec.DNSName, zone.Spec.Zone) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("dnsName %q is outside zone %q", dnsEndpoint.Spec.DNSName, zone.Spec.Zone)})
			return
		}

		objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(dnsEndpoint)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to convert DNS endpoint: " + err.Error()})
			return
		}

		updatedObj, err := m.client.Resource(dnsEndpointGVR).Namespace(m.cfg.Namespace).Update(c, &unstructured.Unstructured{Object: objMap}, metav1.UpdateOptions{})
		if err != nil {
			if writeAPIStatusError(c, err) {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update DNS endpoint: " + err.Error()})
			return
		}

		updatedDNSEndpoint := &infrastructurev1alpha1.DNSEndpoint{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(updatedObj.Object, updatedDNSEndpoint); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to convert updated DNS endpoint: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, updatedDNSEndpoint)
	})

	group.DELETE("/:zone-id/dns-endpoints/:dns-endpoint-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("zone").S("user_id").A("update").Build(), func(c *gin.Context) {
		if _, ok := m.getProjectZone(c); !ok {
			return
		}
		if _, ok := m.getProjectZoneDNSEndpoint(c); !ok {
			return
		}

		if err := m.client.Resource(dnsEndpointGVR).Namespace(m.cfg.Namespace).Delete(c, c.Param("dns-endpoint-id"), metav1.DeleteOptions{}); err != nil {
			if writeAPIStatusError(c, err) {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete DNS endpoint: " + err.Error()})
			return
		}

		c.Status(http.StatusNoContent)
	})
}

func (m *Module) getProjectZone(c *gin.Context) (*infrastructurev1alpha1.Zone, bool) {
	obj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, c.Param("zone-id"), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "zone not found"})
			return nil, false
		}
		if writeAPIStatusError(c, err) {
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve zone: " + err.Error()})
		return nil, false
	}
	if obj.GetLabels()["project"] != c.Param("project-id") {
		c.JSON(http.StatusNotFound, gin.H{"error": "zone not found"})
		return nil, false
	}

	zone := &infrastructurev1alpha1.Zone{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, zone); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to convert zone: " + err.Error()})
		return nil, false
	}
	return zone, true
}

// serviceHostAliasDNSEndpoints builds in-memory DNSEndpoints for project service host aliases that fall inside the zone.
func (m *Module) serviceHostAliasDNSEndpoints(c *gin.Context, zone *infrastructurev1alpha1.Zone) ([]infrastructurev1alpha1.DNSEndpoint, bool) {
	objList, err := m.client.Resource(serviceGVR).Namespace(m.cfg.Namespace).List(c, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("project=%s", c.Param("project-id")),
	})
	if err != nil {
		if writeAPIStatusError(c, err) {
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list services: " + err.Error()})
		return nil, false
	}

	dnsEndpoints := []infrastructurev1alpha1.DNSEndpoint{}
	for _, item := range objList.Items {
		service := &infrastructurev1alpha1.Service{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, service); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to convert service: " + err.Error()})
			return nil, false
		}

		for _, hostAlias := range service.Spec.HostAliases {
			if !dnsNameBelongsToZone(hostAlias.Name, zone.Spec.Zone) {
				continue
			}

			for _, recordType := range []string{"A", "AAAA"} {
				dnsEndpoints = append(dnsEndpoints, infrastructurev1alpha1.DNSEndpoint{
					TypeMeta: metav1.TypeMeta{
						APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
						Kind:       "DNSEndpoint",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      dnsEndpointResourceName(hostAlias.Name, recordType),
						Namespace: m.cfg.Namespace,
						Labels: map[string]string{
							"project": c.Param("project-id"),
							"zone":    c.Param("zone-id"),
							"service": service.Name,
						},
					},
					Spec: infrastructurev1alpha1.DNSEndpointSpec{
						DNSName:       hostAlias.Name,
						RoutingPolicy: "Geolocation",
						RecordTTL:     serviceDNSEndpointRecordTTL,
						RecordType:    recordType,
						// Placeholder consumed by the UI to render a link to the owning service.
						Targets:       []string{fmt.Sprintf("LINK: %s", service.Spec.Name)},
						RouteSelector: service.Spec.RouteSelector.DeepCopy(),
					},
				})
			}
		}
	}

	return dnsEndpoints, true
}

func (m *Module) getProjectZoneDNSEndpoint(c *gin.Context) (*unstructured.Unstructured, bool) {
	obj, err := m.client.Resource(dnsEndpointGVR).Namespace(m.cfg.Namespace).Get(c, c.Param("dns-endpoint-id"), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "DNS endpoint not found"})
			return nil, false
		}
		if writeAPIStatusError(c, err) {
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve DNS endpoint: " + err.Error()})
		return nil, false
	}

	labels := obj.GetLabels()
	if labels["project"] != c.Param("project-id") || labels["zone"] != c.Param("zone-id") {
		c.JSON(http.StatusNotFound, gin.H{"error": "DNS endpoint not found"})
		return nil, false
	}
	return obj, true
}

func dnsNameBelongsToZone(dnsName, zoneName string) bool {
	dnsName = strings.TrimSuffix(strings.ToLower(dnsName), ".")
	zoneName = strings.TrimSuffix(strings.ToLower(zoneName), ".")
	return dnsName == zoneName || strings.HasSuffix(dnsName, "."+zoneName)
}

func dnsEndpointResourceName(dnsName, recordType string) string {
	name := strings.TrimSuffix(strings.ToLower(dnsName), ".") + "-" + strings.ToLower(recordType)
	if len(name) <= 253 {
		return name
	}

	digest := sha256.Sum256([]byte(name))
	suffix := fmt.Sprintf("-%x", digest[:6])
	prefix := strings.TrimRight(name[:253-len(suffix)], ".-")
	return prefix + suffix
}
