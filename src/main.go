package main

import (
	"fmt"
	"os"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/services"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
)

func main() {
	a := app.New()

	// Cookie store and Session middleware initialization
	cookie_secret, set := os.LookupEnv("EDGECDNX_API_COOKIE_SECRET")
	if !set || cookie_secret == "" {
		fmt.Println("EDGECDNX_API_COOKIE_SECRET environment variable not set, cannot start.")
		os.Exit(1)
	}

	// Register global middleware for sessions
	store := cookie.NewStore([]byte(cookie_secret))
	a.Engine.Use(sessions.Sessions("dex", store))

	// Register Auth module. This exposes our Auth middleware
	authModule, err := auth.New(auth.Config{})
	if err != nil {
		panic("auth module init failed")
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

	a.Run(":5555")
}
