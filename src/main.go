package main

import (
	"flag"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/config"
	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func main() {
	// Define flags
	production := flag.Bool("production", false, "run in production mode")
	listen := flag.String("listen", ":5555", "Address and port to listen at")
	namespace := flag.String("namespace", "edgecdnx", "Kubernetes namespace to watch for resources")
	auth_user_claim := flag.String("auth_user_claim", "email", "OIDC claim to use as the user identifier")
	cors_allow_origins := flag.String("cors_allow_origins", "*", "Comma-separated list of allowed CORS origins")
	cors_allowed_methods := flag.String("cors_allowed_methods", "GET,PUT,POST,PATCH,DELETE", "Comma-separated list of allowed CORS methods")
	cors_allowed_headers := flag.String("cors_allowed_headers", "Authorization,Content-Type", "Comma-separated list of allowed CORS headers")

	flag.Parse()

	appcfg := config.AppConfig{
		Production:         *production,
		Listen:             *listen,
		Namespace:          *namespace,
		CorsAllowOrigins:   strings.Split(*cors_allow_origins, ","),
		CorsAllowedMethods: strings.Split(*cors_allowed_methods, ","),
		CorsAllowedHeaders: strings.Split(*cors_allowed_headers, ","),
	}

	logger.Init(appcfg.Production)
	a := app.New(appcfg.Production)

	a.Engine.Use(cors.New(cors.Config{
		AllowOrigins:     appcfg.CorsAllowOrigins,
		AllowMethods:     appcfg.CorsAllowedMethods,
		AllowHeaders:     appcfg.CorsAllowedHeaders,
		AllowCredentials: true,
	}))

	// Register Auth module. This exposes our Auth middleware
	authModule := auth.New(auth.Config{
		Namespace: appcfg.Namespace,
		AuthClaim: *auth_user_claim,
	})

	err := a.RegisterModule(authModule)
	if err != nil {
		logger.L().Error("Module registration failed", zap.Error(err))
		panic("auth module registration failed")
	}

	// Register other modules that require authentication
	authenticatedModules := appcfg.GetAuthenticatedModules()

	for _, md := range authenticatedModules {
		mod := md.Init()
		mod.SetMiddlewares(authModule.AuthMiddleware())
		mod.SetEnforcer(authModule.Enforcer)
		err = a.RegisterModule(mod)
		if err != nil {
			logger.L().Error("Module registration failed", zap.Error(err))
			panic("module registration failed")
		}
	}

	a.Engine.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	a.Run(*listen)
}
