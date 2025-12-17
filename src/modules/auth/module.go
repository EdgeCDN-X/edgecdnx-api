package auth

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type Config struct {
	oidc.Config
	verifier       *oidc.IDTokenVerifier
	authMiddleware gin.HandlerFunc

	NamespacedProjects bool
	Namespace          string
}

type Module struct {
	cfg          Config
	client       *dynamic.DynamicClient
	informerChan chan struct{}
	Informer     cache.SharedIndexInformer
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
	logger.L().Info("Shutting down informer")
	close(m.informerChan)
}

func (m *Module) Init() error {

	logger.L().Info("Initializing Auth module")

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

	client, err := app.GetK8SDynamicClient()

	if err != nil {
		return err
	}

	m.client = client

	ns := ""
	if !m.cfg.NamespacedProjects {
		ns = m.cfg.Namespace
	}

	fac := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 60*time.Minute, ns, nil)
	informer := fac.ForResource(schema.GroupVersionResource{
		Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
		Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
		Resource: "projects",
	}).Informer()

	stop := make(chan struct{})
	m.Informer = informer
	m.informerChan = stop
	go informer.Run(stop)

	if !cache.WaitForCacheSync(stop, informer.HasSynced) {
		logger.L().Error("Failed to sync informer cache")
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}
