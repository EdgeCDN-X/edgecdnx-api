package config

import (
	"encoding/json"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/admin"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/apikeys"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/locations"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/projects"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/services"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/utils"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/zones"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AppConfig struct {
	Production           bool
	Listen               string
	Namespace            string
	Namespaced           bool
	FeatureGates         string
	PrometheusEndpoint   string
	CorsAllowOrigins     []string
	CorsAllowedMethods   []string
	CorsAllowedHeaders   []string
	ServiceBaseDomain    string
	DefaultRouteSelector *metav1.LabelSelector
	DefaultAdminProject  string
	DefaultAdminUser     string
	OIDCGroupMappings    []auth.OIDCGroupMapping
	OIDCGroupPrefix      string
}

func ParseDefaultRouteSelector(s string) (*metav1.LabelSelector, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}

	selector := &metav1.LabelSelector{}
	if err := json.Unmarshal([]byte(s), selector); err != nil {
		return nil, err
	}
	return selector, nil
}

func ParseOIDCGroupMappings(s string, prefix string) []auth.OIDCGroupMapping {
	mappings := []auth.OIDCGroupMapping{}
	if s == "" {
		return mappings
	}

	entries := splitAndTrim(s, ",")
	for _, entry := range entries {
		parts := splitAndTrim(entry, ":")
		if len(parts) != 3 {
			continue
		}
		mappings = append(mappings, auth.OIDCGroupMapping{
			OIDCGroup: prefix + parts[0],
			Tenant:    parts[1],
			Group:     parts[2],
		})
	}

	return mappings
}

func splitAndTrim(s1, s2 string) []string {
	parts := strings.Split(s1, s2)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

type ModuleDef struct {
	Name        string
	FeatureGate string
	Init        func() app.Module
}

func (a *AppConfig) GetAuthenticatedModules() []ModuleDef {
	modules := []ModuleDef{
		{
			Name: "Utils",
			Init: func() app.Module {
				return utils.New()
			},
		},
		{
			Name: "Admin",
			Init: func() app.Module {
				return admin.New(admin.Config{
					Namespace:           a.Namespace,
					Namespaced:          a.Namespaced,
					DefaultAdminProject: a.DefaultAdminProject,
					DefaultAdminUser:    a.DefaultAdminUser,
				})
			},
		},
		{
			Name: "Projects",
			Init: func() app.Module {
				return projects.New(projects.Config{
					Namespace:           a.Namespace,
					Namespaced:          a.Namespaced,
					DefaultAdminProject: a.DefaultAdminProject,
					DefaultAdminUser:    a.DefaultAdminUser,
				})
			},
		},
		{
			Name: "APIKeys",
			Init: func() app.Module {
				return apikeys.New(apikeys.Config{
					Namespace:  a.Namespace,
					Namespaced: a.Namespaced,
				})
			},
		},
		{
			Name:        "Services",
			FeatureGate: "cachingreconciler",
			Init: func() app.Module {
				return services.New(services.Config{
					Namespace:            a.Namespace,
					Namespaced:           a.Namespaced,
					ServiceBaseDomain:    a.ServiceBaseDomain,
					DefaultRouteSelector: a.DefaultRouteSelector,
				})
			},
		},
		{
			Name: "Locations",
			Init: func() app.Module {
				return locations.New(locations.Config{
					Namespace:  a.Namespace,
					Namespaced: a.Namespaced,
				})
			},
		},
		{
			Name: "Zones",
			Init: func() app.Module {
				return zones.New(zones.Config{
					Namespace:  a.Namespace,
					Namespaced: a.Namespaced,
				})
			},
		},
	}
	enabledGates := make(map[string]bool)
	for _, gate := range strings.Split(a.FeatureGates, ",") {
		enabledGates[strings.TrimSpace(gate)] = true
	}
	enabledModules := make([]ModuleDef, 0, len(modules))
	for _, module := range modules {
		if module.FeatureGate == "" || enabledGates[module.FeatureGate] {
			enabledModules = append(enabledModules, module)
		}
	}
	return enabledModules
}
