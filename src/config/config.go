package config

import (
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/admin"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/projects"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/services"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/zones"
)

type AppConfig struct {
	Production          bool
	Listen              string
	Namespace           string
	PrometheusEndpoint  string
	CorsAllowOrigins    []string
	CorsAllowedMethods  []string
	CorsAllowedHeaders  []string
	ServiceBaseDomain   string
	DefaultAdminProject string
	DefaultAdminUser    string
	OIDCGroupMappings   []auth.OIDCGroupMapping
	OIDCGroupPrefix     string
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
	Name string
	Init func() app.Module
}

func (a *AppConfig) GetAuthenticatedModules() []ModuleDef {
	return []ModuleDef{
		{
			Name: "Admin",
			Init: func() app.Module {
				return admin.New(admin.Config{
					Namespace:           a.Namespace,
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
					DefaultAdminProject: a.DefaultAdminProject,
					DefaultAdminUser:    a.DefaultAdminUser,
				})
			},
		},
		{
			Name: "Services",
			Init: func() app.Module {
				return services.New(services.Config{
					Namespace:         a.Namespace,
					ServiceBaseDomain: a.ServiceBaseDomain,
				})
			},
		},
		{
			Name: "Zones",
			Init: func() app.Module {
				return zones.New(zones.Config{
					Namespace: a.Namespace,
				})
			},
		},
	}
}
