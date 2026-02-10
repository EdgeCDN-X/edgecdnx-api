package config

import (
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/projects"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/services"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/zones"
)

type AppConfig struct {
	Production bool
	Listen     string
	Namespace  string

	CorsAllowOrigins   []string
	CorsAllowedMethods []string
	CorsAllowedHeaders []string

	ServiceBaseDomain string
}

type ModuleDef struct {
	Init func() app.Module
}

func (a *AppConfig) GetAuthenticatedModules() []ModuleDef {
	return []ModuleDef{
		{
			func() app.Module {
				return projects.New(projects.Config{
					Namespace: a.Namespace,
				})
			},
		},
		{
			func() app.Module {
				return services.New(services.Config{
					Namespace:         a.Namespace,
					ServiceBaseDomain: a.ServiceBaseDomain,
				})
			},
		},
		{
			func() app.Module {
				return zones.New(zones.Config{
					Namespace: a.Namespace,
				})
			},
		},
	}
}
