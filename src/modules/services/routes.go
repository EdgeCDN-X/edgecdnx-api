package services

import (
	"fmt"
	"math/rand"
	"slices"
	"time"

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

	group.POST("", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("service").S("user_id").A("create").Build(), func(c *gin.Context) {
		var dto ServiceDto
		if err := c.ShouldBindJSON(&dto); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}

		const letters = "abcdefghijklmnopqrstuvwxyz"
		b := make([]byte, 16)
		for i := range b {
			b[i] = letters[rand.Intn(len(letters))]
		}
		generatedDomainHost := string(b)
		name := slug.Make(dto.Name)

		service := &infrastructurev1alpha1.Service{
			TypeMeta: metav1.TypeMeta{
				APIVersion: infrastructurev1alpha1.SchemeGroupVersion.String(),
				Kind:       "Service",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: m.cfg.Namespace,
				Labels: map[string]string{
					"project": c.Param("project-id"),
				},
			},
			Spec: infrastructurev1alpha1.ServiceSpec{
				Name:       dto.Name,
				Domain:     fmt.Sprintf("%s.%s", generatedDomainHost, m.cfg.ServiceBaseDomain),
				OriginType: dto.OriginType,
				StaticOrigins: func() []infrastructurev1alpha1.StaticOriginSpec {
					if dto.OriginType == "static" {
						return []infrastructurev1alpha1.StaticOriginSpec{
							{
								Upstream:   dto.StaticOrigin.Upstream,
								Port:       dto.StaticOrigin.Port,
								HostHeader: dto.StaticOrigin.HostHeader,
								Scheme:     dto.StaticOrigin.Scheme,
							},
						}
					}
					return nil
				}(),
				S3OriginSpec: func() []infrastructurev1alpha1.S3OriginSpec {
					if dto.OriginType == "s3" {
						return []infrastructurev1alpha1.S3OriginSpec{
							{
								AwsSigsVersion: dto.S3OriginSpec.AwsSigsVersion,
								S3AccessKeyId:  dto.S3OriginSpec.S3AccessKeyId,
								S3SecretKey:    dto.S3OriginSpec.S3SecretKey,
								S3BucketName:   dto.S3OriginSpec.S3BucketName,
								S3Region:       dto.S3OriginSpec.S3Region,
								S3Server:       dto.S3OriginSpec.S3Server,
								S3ServerProto:  dto.S3OriginSpec.S3ServerProto,
								S3ServerPort:   dto.S3OriginSpec.S3ServerPort,
								S3Style:        dto.S3OriginSpec.S3Style,
							},
						}
					}
					return nil
				}(),
				SecureKeys: func() []infrastructurev1alpha1.SecureKeySpec {
					if dto.SignedUrlsEnabled {
						return []infrastructurev1alpha1.SecureKeySpec{
							{
								Name: "key1",
								Value: func() string {
									b := make([]byte, 32)
									for i := range b {
										b[i] = letters[rand.Intn(len(letters))]
									}
									return string(b)
								}(),
								CreatedAt: metav1.Time{Time: time.Now()},
							},
						}
					}
					return nil
				}(),
				Cache: dto.Cache,
				CacheKeySpec: infrastructurev1alpha1.CacheKeySpec{
					Headers:     dto.CacheKey.Headers,
					QueryParams: dto.CacheKey.QueryParams,
				},
				HostAliases: func() []infrastructurev1alpha1.HostAliasSpec {
					aliases := []infrastructurev1alpha1.HostAliasSpec{}
					for _, alias := range dto.HostAliases {
						aliases = append(aliases, infrastructurev1alpha1.HostAliasSpec{Name: alias.Name})
					}
					return aliases
				}(),
				Waf: infrastructurev1alpha1.WafSpec{
					Enabled: dto.WafEnabled,
				},
			},
		}

		objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(service)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		serviceUnstructured := unstructured.Unstructured{
			Object: objMap,
		}

		ns := m.cfg.Namespace

		createdObj, err := m.client.Resource(gvr).Namespace(ns).Create(c, &serviceUnstructured, metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				c.JSON(409, gin.H{"error": "service with the same name already exists. Services must have unique names within the platform."})
				return
			}

			if apierrors.IsBadRequest(err) {
				c.JSON(400, gin.H{"error": "bad request: " + err.Error()})
				return
			}

			c.JSON(500, gin.H{"error": "failed to create service: " + err.Error()})
			return
		}

		returnedService := &infrastructurev1alpha1.Service{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(createdObj.Object, returnedService)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		c.JSON(201, returnedService)
		return
	})

	group.PATCH("/:service-id", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("service").S("user_id").A("update").Build(), func(c *gin.Context) {
		var dto ServiceUpdateDto
		if err := c.ShouldBindJSON(&dto); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}

		serviceId := c.Param("service-id")

		obj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, serviceId, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				c.JSON(404, gin.H{"error": "service not found"})
				return
			}
			c.JSON(500, gin.H{"error": "failed to retrieve service: " + err.Error()})
			return
		}

		service := &infrastructurev1alpha1.Service{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, service)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		if dto.Cache != "" {
			service.Spec.Cache = dto.Cache
		}

		if dto.OriginType != "" {
			service.Spec.OriginType = dto.OriginType
			if dto.OriginType == "static" {
				service.Spec.S3OriginSpec = nil
				if dto.StaticOrigin == nil {
					c.JSON(400, gin.H{"error": "staticOrigin must be provided when originType is static"})
					return
				}
			} else if dto.OriginType == "s3" {
				service.Spec.StaticOrigins = nil
				if dto.S3OriginSpec == nil {
					c.JSON(400, gin.H{"error": "s3OriginSpec must be provided when originType is s3"})
					return
				}
			}
		}

		if dto.StaticOrigin != nil {
			service.Spec.StaticOrigins = []infrastructurev1alpha1.StaticOriginSpec{
				{
					Upstream:   dto.StaticOrigin.Upstream,
					Port:       dto.StaticOrigin.Port,
					HostHeader: dto.StaticOrigin.HostHeader,
					Scheme:     dto.StaticOrigin.Scheme,
				},
			}
		}

		if dto.S3OriginSpec != nil {
			service.Spec.S3OriginSpec = []infrastructurev1alpha1.S3OriginSpec{
				{
					AwsSigsVersion: dto.S3OriginSpec.AwsSigsVersion,
					S3AccessKeyId:  dto.S3OriginSpec.S3AccessKeyId,
					S3SecretKey:    dto.S3OriginSpec.S3SecretKey,
					S3BucketName:   dto.S3OriginSpec.S3BucketName,
					S3Region:       dto.S3OriginSpec.S3Region,
					S3Server:       dto.S3OriginSpec.S3Server,
					S3ServerProto:  dto.S3OriginSpec.S3ServerProto,
					S3ServerPort:   dto.S3OriginSpec.S3ServerPort,
					S3Style:        dto.S3OriginSpec.S3Style,
				},
			}
		}

		if dto.WafEnabled != nil {
			service.Spec.Waf.Enabled = *dto.WafEnabled
		}

		if dto.CacheKey != nil {
			service.Spec.CacheKeySpec = infrastructurev1alpha1.CacheKeySpec{
				Headers:     dto.CacheKey.Headers,
				QueryParams: dto.CacheKey.QueryParams,
			}
		}

		objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(service)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		serviceUnstructured := unstructured.Unstructured{
			Object: objMap,
		}

		updatedObj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Update(c, &serviceUnstructured, metav1.UpdateOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to update service: " + err.Error()})
			return
		}

		returnedService := &infrastructurev1alpha1.Service{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(updatedObj.Object, returnedService)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		c.JSON(200, returnedService)
		return
	})

	group.POST("/:service-id/keys", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("service").S("user_id").A("update").Build(), func(c *gin.Context) {
		var dto CreateKeyDto
		if err := c.ShouldBindJSON(&dto); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}

		keyName := dto.Name
		if keyName == "" {
			c.JSON(400, gin.H{"error": "key name is required"})
			return
		}

		serviceId := c.Param("service-id")

		// Check if service exists
		_, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, serviceId, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				c.JSON(404, gin.H{"error": "service not found"})
				return
			}
			c.JSON(500, gin.H{"error": "failed to retrieve service: " + err.Error()})
			return
		}

		newKey := &infrastructurev1alpha1.SecureKeySpec{
			Name: keyName,
			Value: func() string {
				const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
				b := make([]byte, 32)
				for i := range b {
					b[i] = letters[rand.Intn(len(letters))]
				}
				return string(b)
			}(),
			CreatedAt: metav1.Time{Time: time.Now()},
		}

		// Create the new key
		obj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, serviceId, metav1.GetOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to retrieve service: " + err.Error()})
			return
		}

		service := &infrastructurev1alpha1.Service{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, service)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		service.Spec.SecureKeys = append(service.Spec.SecureKeys, *newKey)

		objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(service)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		serviceUnstructured := unstructured.Unstructured{
			Object: objMap,
		}

		updatedObj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Update(c, &serviceUnstructured, metav1.UpdateOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to update service with new key: " + err.Error()})
			return
		}

		returnedService := &infrastructurev1alpha1.Service{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(updatedObj.Object, returnedService)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		c.JSON(200, returnedService)
		return
	})

	group.DELETE("/:service-id/keys/:key-name", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("service").S("user_id").A("update").Build(), func(c *gin.Context) {
		serviceId := c.Param("service-id")
		keyName := c.Param("key-name")

		// Check if service exists
		_, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, serviceId, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				c.JSON(404, gin.H{"error": "service not found"})
				return
			}
			c.JSON(500, gin.H{"error": "failed to retrieve service: " + err.Error()})
			return
		}

		obj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, serviceId, metav1.GetOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to retrieve service: " + err.Error()})
			return
		}

		service := &infrastructurev1alpha1.Service{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, service)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		keys := service.Spec.SecureKeys
		newKeys := []infrastructurev1alpha1.SecureKeySpec{}
		for _, key := range keys {
			if key.Name != keyName {
				newKeys = append(newKeys, key)
			}
		}

		if len(keys) == len(newKeys) {
			c.JSON(404, gin.H{"error": "key not found"})
			return
		}

		service.Spec.SecureKeys = newKeys

		objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(service)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		serviceUnstructured := unstructured.Unstructured{
			Object: objMap,
		}

		updatedObj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Update(c, &serviceUnstructured, metav1.UpdateOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to update service after deleting key: " + err.Error()})
			return
		}

		returnedService := &infrastructurev1alpha1.Service{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(updatedObj.Object, returnedService)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		c.JSON(200, returnedService)
		return
	})

	group.POST("/:service-id/host-alias", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("service").S("user_id").A("update").Build(), func(c *gin.Context) {
		var dto HostAliasDto
		if err := c.ShouldBindJSON(&dto); err != nil {

			fmt.Printf("%v", dto)

			c.JSON(400, gin.H{"error": "invalid request body: " + err.Error()})
			return
		}

		serviceId := c.Param("service-id")

		// Check if service exists
		service, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, serviceId, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				c.JSON(404, gin.H{"error": "service not found"})
				return
			}
			c.JSON(500, gin.H{"error": "failed to retrieve service: " + err.Error()})
			return
		}

		serviceObj := &infrastructurev1alpha1.Service{}

		err = runtime.DefaultUnstructuredConverter.FromUnstructured(service.Object, serviceObj)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
		}

		if slices.ContainsFunc(serviceObj.Spec.HostAliases, func(n infrastructurev1alpha1.HostAliasSpec) bool {
			return n.Name == dto.Name
		}) {
			c.JSON(409, gin.H{"error": "Host Alias already registered with the service"})
		}

		serviceObj.Spec.HostAliases = append(serviceObj.Spec.HostAliases, infrastructurev1alpha1.HostAliasSpec{
			Name: dto.Name,
		})

		objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(serviceObj)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		serviceUnstructured := unstructured.Unstructured{
			Object: objMap,
		}

		updatedObj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Update(c, &serviceUnstructured, metav1.UpdateOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to add hostAlias to service: " + err.Error()})
			return
		}

		returnedService := &infrastructurev1alpha1.Service{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(updatedObj.Object, returnedService)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		c.JSON(200, returnedService)
		return
	})

	group.DELETE("/:service-id/host-alias/:alias-name", auth.NewAuthzBuilder().E(m.enforcer).T("project-id").R("service").S("user_id").A("update").Build(), func(c *gin.Context) {
		serviceId := c.Param("service-id")
		aliasName := c.Param("alias-name")

		// Check if service exists
		obj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Get(c, serviceId, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				c.JSON(404, gin.H{"error": "service not found"})
				return
			}
			c.JSON(500, gin.H{"error": "failed to retrieve service: " + err.Error()})
			return
		}

		service := &infrastructurev1alpha1.Service{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, service)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		aliases := service.Spec.HostAliases
		newAliases := []infrastructurev1alpha1.HostAliasSpec{}
		for _, alias := range aliases {
			if alias.Name != aliasName {
				newAliases = append(newAliases, alias)
			}
		}

		if len(aliases) == len(newAliases) {
			c.JSON(404, gin.H{"error": "Alias not found"})
			return
		}

		service.Spec.HostAliases = newAliases

		objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(service)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		serviceUnstructured := unstructured.Unstructured{
			Object: objMap,
		}

		updatedObj, err := m.client.Resource(gvr).Namespace(m.cfg.Namespace).Update(c, &serviceUnstructured, metav1.UpdateOptions{})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to update service after removing host Alias: " + err.Error()})
			return
		}

		returnedService := &infrastructurev1alpha1.Service{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(updatedObj.Object, returnedService)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		c.JSON(200, returnedService)
		return
	})

}
