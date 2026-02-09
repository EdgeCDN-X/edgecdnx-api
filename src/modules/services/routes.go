package services

import (
	"fmt"
	"math/rand"
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
}
