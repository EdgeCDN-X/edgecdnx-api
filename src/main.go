package main

import (
	"flag"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/projects"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/services"
	"go.uber.org/zap"
)

func main() {
	// Define flags
	production := flag.Bool("production", false, "run in production mode")
	listen := flag.String("listen", ":5555", "Address and port to listen at")
	namespacedProjects := flag.Bool("namespaced-projects", false, "If set, projects will be namespaced")

	// If namespaced-projects is set, the module will expect each project to be in its own namespace. Otherwise, watch for the selected namespace only
	namespace := flag.String("namespace", "edgecdnx", "Kubernetes namespace to watch for resources")

	flag.Parse()

	logger.Init(*production)
	a := app.New(*production)

	// Register Auth module. This exposes our Auth middleware
	authModule, err := auth.New(auth.Config{
		NamespacedProjects: *namespacedProjects,
		Namespace:          *namespace,
	})
	if err != nil {
		logger.L().Error("Module initialization failed", zap.Error(err))
		panic("auth module initialization failed")
	}
	err = a.RegisterModule(authModule)
	if err != nil {
		logger.L().Error("Module registration failed", zap.Error(err))
		panic("auth module registration failed")
	}

	// Register other modules that require authentication
	authenticatedModules := []struct {
		init func() (app.Module, error)
	}{
		{
			func() (app.Module, error) {
				return services.New(services.Config{})
			},
		},
		{
			func() (app.Module, error) {
				return projects.New(projects.Config{
					NamespacedProjects: *namespacedProjects,
					Namespace:          *namespace,
				})
			},
		},
	}

	for _, md := range authenticatedModules {
		mod, err := md.init()
		if err != nil {
			logger.L().Error("Module initialization failed", zap.Error(err))
			panic("module init failed")
		}
		err = a.RegisterModule(mod, authModule.AuthMiddleware())
		if err != nil {
			logger.L().Error("Module registration failed", zap.Error(err))
			panic("module registration failed")
		}
	}

	a.Run(*listen)
}
