package auth

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
)

type Config struct {
	oidc.Config
	verifier       *oidc.IDTokenVerifier
	authMiddleware gin.HandlerFunc
}

type Module struct {
	cfg Config
}

func (m *Module) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing Authorization header"})
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid Authorization header"})
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		// Validate with OIDC
		idToken, err := m.cfg.verifier.Verify(c.Request.Context(), tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token: " + err.Error()})
			return
		}

		// extract claims if you need them
		var claims map[string]interface{}
		if err := idToken.Claims(&claims); err == nil {
			c.Set("claims", claims)
		}

		c.Next()
	}
}

func New(cfg Config) (*Module, error) {
	return &Module{cfg: cfg}, nil
}

func (m *Module) Shutdown() {
	// No-op
}

func (m *Module) Init() error {
	provider, err := oidc.NewProvider(context.Background(), os.Getenv("OIDC_ISSUER_URL"))
	if err != nil {
		return fmt.Errorf("failed to get provider: %w", err)
	}

	// Configure an OpenID Connect aware OAuth2 client.
	oidcConfig := &oidc.Config{
		ClientID: os.Getenv("OIDC_CLIENT_ID"),
	}
	m.cfg.Config = *oidcConfig
	m.cfg.verifier = provider.Verifier(oidcConfig)

	return nil
}
