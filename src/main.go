package main

import (
	"os"

	"flag"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/services"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
)

func main() {
	// Define flags
	production := flag.Bool("production", false, "run in production mode")
	listen := flag.String("listen", ":5555", "Address and port to listen at")
	flag.Parse()

	logger.Init(*production)
	a := app.New(*production)

	// Cookie store and Session middleware initialization
	cookie_secret, set := os.LookupEnv("EDGECDNX_API_COOKIE_SECRET")
	if !set || cookie_secret == "" {
		logger.L().Fatal("EDGECDNX_API_COOKIE_SECRET environment variable not set")
		os.Exit(1)
	}

	// Register global middleware for sessions
	store := cookie.NewStore([]byte(cookie_secret))
	a.Engine.Use(sessions.Sessions("dex", store))

	// Register Auth module. This exposes our Auth middleware
	authModule, err := auth.New(auth.Config{})
	if err != nil {
		panic("auth module initialization failed")
	}
	a.RegisterModule(authModule)

	// Register other modules that require authentication
	authenticatedModules := []struct {
		init func() (app.Module, error)
	}{
		{
			func() (app.Module, error) {
				return services.New(services.Config{})
			},
		},
	}

	for _, md := range authenticatedModules {
		mod, err := md.init()
		if err != nil {
			panic("module init failed")
		}
		a.RegisterModule(mod, authModule.Auth.AuthRequired())
	}

	a.Run(*listen)
}
