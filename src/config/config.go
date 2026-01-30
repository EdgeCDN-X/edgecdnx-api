package config

import (
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/projects"
)

type AppConfig struct {
	Production bool
	Listen     string
	Namespace  string

	CorsAllowOrigins   []string
	CorsAllowedMethods []string
	CorsAllowedHeaders []string
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
	}
}
