package auth

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"go.uber.org/zap"

	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
	"github.com/casbin/casbin/v3/persist"
	stringadapter "github.com/casbin/casbin/v3/persist/string-adapter"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

const RBACWithDomainModel = `
[request_definition]
r = sub, dom, res, act

[policy_definition]
p = sub, dom, res, act

[role_definition]
g = _, _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub, r.dom) && r.dom == p.dom && (keyMatch2(r.res, p.res)) && r.act == p.act
`

type Config struct {
	oidc.Config
	verifier       *oidc.IDTokenVerifier
	authMiddleware gin.HandlerFunc
	Namespace      string
	AuthClaim      string
}

type Module struct {
	cfg          Config
	client       *dynamic.DynamicClient
	informerChan chan struct{}
	Informer     cache.SharedIndexInformer
	casbinModel  model.Model
	Adapter      persist.Adapter
	Enforcer     *casbin.Enforcer
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
			c.Set("user_id", claims[m.cfg.AuthClaim])
		}

		c.Next()
	}
}

func New(cfg Config) *Module {
	return &Module{cfg: cfg}
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

	// Initialize RBAC Model
	m.casbinModel, err = model.NewModelFromString(RBACWithDomainModel)
	if err != nil {
		log.Fatalf("failed to load model: %v", err)
		return err
	}

	// Initialize with super admin rights
	adapter := stringadapter.NewAdapter("p, portal-admin, *, *, *")

	client, _, err := app.GetK8SDynamicClient()
	if err != nil {
		return err
	}

	m.client = client
	fac := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 60*time.Minute, m.cfg.Namespace, nil)
	informer := fac.ForResource(schema.GroupVersionResource{
		Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
		Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
		Resource: "projects",
	}).Informer()

	m.Adapter = adapter
	m.Enforcer, err = casbin.NewEnforcer(m.casbinModel, m.Adapter)

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			p_raw, ok := obj.(*unstructured.Unstructured)
			if !ok {
				logger.L().Error("Failed to cast added project to Unstructured")
				return
			}

			project := &infrastructurev1alpha1.Project{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(p_raw.Object, project)
			if err != nil {
				logger.L().Error("Failed to convert added project from Unstructured", zap.Error(err))
				return
			}

			for _, r := range project.Spec.Rbac.Rules {
				logger.L().Debug("Adding policy", zap.String("sub", r.V0), zap.String("dom", r.V1), zap.String("res", r.V2), zap.String("act", r.V3))
				m.Enforcer.AddPolicy(r.V0, r.V1, r.V2, r.V3)
			}

			for _, g := range project.Spec.Rbac.Groups {
				logger.L().Debug("Adding grouping policy", zap.String("user", g.V0), zap.String("role", g.V1), zap.String("domain", g.V2))
				m.Enforcer.AddGroupingPolicy(g.V0, g.V1, g.V2)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			old_raw, ok := oldObj.(*unstructured.Unstructured)
			if !ok {
				logger.L().Error("Failed to cast old project to Unstructured")
				return
			}
			new_raw, ok := newObj.(*unstructured.Unstructured)
			if !ok {
				logger.L().Error("Failed to cast new project to Unstructured")
				return
			}

			oldProject := &infrastructurev1alpha1.Project{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(old_raw.Object, oldProject)
			if err != nil {
				logger.L().Error("Failed to convert old project from Unstructured", zap.Error(err))
				return
			}

			newProject := &infrastructurev1alpha1.Project{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(new_raw.Object, newProject)
			if err != nil {
				logger.L().Error("Failed to convert new project from Unstructured", zap.Error(err))
				return
			}

			// Remove old policies
			for _, r := range oldProject.Spec.Rbac.Rules {
				logger.L().Debug("Removing policy", zap.String("sub", r.V0), zap.String("dom", r.V1), zap.String("res", r.V2), zap.String("act", r.V3))
				m.Enforcer.RemovePolicy(r.V0, r.V1, r.V2, r.V3)
			}
			for _, g := range oldProject.Spec.Rbac.Groups {
				logger.L().Debug("Removing grouping policy", zap.String("user", g.V0), zap.String("role", g.V1), zap.String("domain", g.V2))
				m.Enforcer.RemoveGroupingPolicy(g.V0, g.V1, g.V2)
			}

			// Add new policies
			for _, r := range newProject.Spec.Rbac.Rules {
				logger.L().Debug("Adding policy", zap.String("sub", r.V0), zap.String("dom", r.V1), zap.String("res", r.V2), zap.String("act", r.V3))
				m.Enforcer.AddPolicy(r.V0, r.V1, r.V2, r.V3)
			}
			for _, g := range newProject.Spec.Rbac.Groups {
				logger.L().Debug("Adding grouping policy", zap.String("user", g.V0), zap.String("role", g.V1), zap.String("domain", g.V2))
				m.Enforcer.AddGroupingPolicy(g.V0, g.V1, g.V2)
			}
		},
	})

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

func (a *Module) SetMiddlewares(middlewares ...gin.HandlerFunc) {
	// No-op for this module
}

func (m *Module) SetEnforcer(enforcer *casbin.Enforcer) {
	// No-op for this module
}
