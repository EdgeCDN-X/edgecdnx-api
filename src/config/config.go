package config

import (
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/projects"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/services"
)

type AppConfig struct {
	Production bool
	Listen     string
	Namespace  string
}

type ModuleDef struct {
	Init func() app.Module
}

func (a *AppConfig) GetAuthenticatedModules() []ModuleDef {
	return []ModuleDef{
		{
			func() app.Module {
				return services.New(services.Config{})
			},
		},
		{
			func() app.Module {
				return projects.New(projects.Config{
					Namespace: a.Namespace,
				})
			},
		},
	}
}
