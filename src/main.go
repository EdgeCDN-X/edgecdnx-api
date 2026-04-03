package main

import (
	"flag"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/config"
	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/projects"
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
	auth_groups_claim := flag.String("auth_groups_claim", "groups", "OIDC claim to use for user groups")
	cors_allow_origins := flag.String("cors_allow_origins", "*", "Comma-separated list of allowed CORS origins")
	cors_allowed_methods := flag.String("cors_allowed_methods", "GET,PUT,POST,PATCH,DELETE", "Comma-separated list of allowed CORS methods")
	cors_allowed_headers := flag.String("cors_allowed_headers", "Authorization,Content-Type", "Comma-separated list of allowed CORS headers")
	service_base_domain := flag.String("service_base_domain", "democdn.edgecdnx.com", "Base domain for services")
	default_admin_project := flag.String("default_admin_project", "admin", "Name of the default admin project to create if it doesn't exist")
	default_admin_user := flag.String("default_admin_user", "admin@edgecdnx.com", "Email of the default admin user to create if it doesn't exist")
	oidc_group_mappings := flag.String("oidc_group_mappings", "admin:admin:admin", "Comma-separated list of OIDC group to role mappings in the format oidc-group:tenant:group")
	oidc_group_prefix := flag.String("oidc_group_prefix", "oidc-", "Prefix to add to OIDC groups when creating Casbin policies")

	flag.Parse()

	appcfg := config.AppConfig{
		Production:          *production,
		Listen:              *listen,
		Namespace:           *namespace,
		CorsAllowOrigins:    strings.Split(*cors_allow_origins, ","),
		CorsAllowedMethods:  strings.Split(*cors_allowed_methods, ","),
		CorsAllowedHeaders:  strings.Split(*cors_allowed_headers, ","),
		ServiceBaseDomain:   *service_base_domain,
		DefaultAdminProject: *default_admin_project,
		DefaultAdminUser:    *default_admin_user,
		OIDCGroupMappings:   config.ParseOIDCGroupMappings(*oidc_group_mappings, *oidc_group_prefix),
		OIDCGroupPrefix:     *oidc_group_prefix,
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
		Namespace:         appcfg.Namespace,
		AuthClaim:         *auth_user_claim,
		GroupsClaim:       *auth_groups_claim,
		OIDCGroupPrefix:   appcfg.OIDCGroupPrefix,
		OIDCGroupMappings: appcfg.OIDCGroupMappings,
	})

	err := a.RegisterModule(authModule, "Auth")
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
		err = a.RegisterModule(mod, md.Name)
		if err != nil {
			logger.L().Error("Module registration failed", zap.Error(err))
			panic("module registration failed")
		}
	}

	a.Engine.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	projects := a.GetModule("Projects").(*projects.Module)
	if projects == nil {
		logger.L().Error("Projects module not found")
		panic("projects module not found")
	}

	err = projects.EnsureAdmin()
	if err != nil {
		logger.L().Error("Failed to ensure admin project", zap.Error(err))
		panic("failed to ensure admin project")
	}

	a.Run(*listen)
}
