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

const (
	ResourceTypeAll                = "*"
	ResourceTypeProject            = "project"
	ResourceTypeService            = "service"
	ResourceTypeZone               = "zone"
	ResourceTypeLocation           = "location"
	ResourceTypeHealthCheckProfile = "healthcheckprofile"
	ResourceTypePrefixList         = "prefixlist"
	ResourceTypeAPIKey             = "apikey"
)

var resourceTypes = []string{
	ResourceTypeAll,
	ResourceTypeAPIKey,
	ResourceTypeHealthCheckProfile,
	ResourceTypeLocation,
	ResourceTypePrefixList,
	ResourceTypeProject,
	ResourceTypeService,
	ResourceTypeZone,
}

func ResourceTypes() []string {
	return append([]string(nil), resourceTypes...)
}

type OIDCGroupMapping struct {
	OIDCGroup string
	Tenant    string
	Group     string
}

type Config struct {
	oidc.Config
	verifier          *oidc.IDTokenVerifier
	authMiddleware    gin.HandlerFunc
	Namespace         string
	Namespaced        bool
	AuthClaim         string
	GroupsClaim       string
	OIDCGroupMappings []OIDCGroupMapping
	OIDCGroupPrefix   string
}

// APIKeyProjectLabel is the label linking an APIKey CR to its project.
const APIKeyProjectLabel = "project"

type Module struct {
	cfg            Config
	client         *dynamic.DynamicClient
	informerChan   chan struct{}
	Informer       cache.SharedIndexInformer
	APIKeyInformer cache.SharedIndexInformer
	casbinModel    model.Model
	Adapter        persist.Adapter
	Enforcer       *casbin.Enforcer

	// Caches used for API key authentication.
	apiKeys  cache.Store
	projects cache.Store
	clock    func() time.Time
}

func (m *Module) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing Authorization header"})
			return
		}

		scheme, tokenString, ok := parseAuthorization(authHeader)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid Authorization header"})
			return
		}

		switch scheme {
		case authSchemeAPIKey:
			m.handleAPIKeyAuth(c, tokenString)
			return
		case authSchemeBearer:
		default:
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid Authorization header"})
			return
		}

		// Validate with OIDC
		idToken, err := m.cfg.verifier.Verify(c.Request.Context(), tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token: " + err.Error()})
			return
		}

		// extract claims if you need them
		var claims map[string]interface{}
		if err := idToken.Claims(&claims); err == nil {
			c.Set(ContextKeyAuthMethod, AuthMethodOIDC)
			c.Set("claims", claims)
			c.Set("user_id", claims[m.cfg.AuthClaim])
			// Convert groups claim array to comma-separated string. Seems like there's bug retreiving it as a slice directly from the context, maybe due to how Gin stores values.
			if groups, ok := claims[m.cfg.GroupsClaim].([]interface{}); ok {
				groupStrs := make([]string, len(groups))
				for i, g := range groups {
					groupStrs[i] = fmt.Sprintf("%s%s", m.cfg.OIDCGroupPrefix, g)
				}
				c.Set("groups", strings.Join(groupStrs, ","))
			}

		}

		c.Next()
	}
}

func New(cfg Config) *Module {
	return &Module{cfg: cfg}
}

func (m *Module) scope() app.Scope {
	return app.Scope{Namespaced: m.cfg.Namespaced, Namespace: m.cfg.Namespace}
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
	fac := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 60*time.Minute, m.scope().WatchNamespace(), nil)
	informer := fac.ForResource(schema.GroupVersionResource{
		Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
		Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
		Resource: "projects",
	}).Informer()

	apiKeyInformer := fac.ForResource(schema.GroupVersionResource{
		Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
		Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
		Resource: "apikeys",
	}).Informer()
	if err := apiKeyInformer.AddIndexers(cache.Indexers{app.ProjectIndex: app.ProjectIndexFunc, app.ResourceNameIndex: app.ResourceNameIndexFunc}); err != nil {
		return fmt.Errorf("index API keys: %w", err)
	}

	m.Adapter = adapter
	m.Enforcer, err = casbin.NewEnforcer(m.casbinModel, m.Adapter)
	if err != nil {
		return err
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			m.applyProjectPolicies(obj, false)
		},
		UpdateFunc: func(oldObj, newObj any) {
			m.applyProjectPolicies(oldObj, true)
			m.applyProjectPolicies(newObj, false)
		},
		DeleteFunc: func(obj any) {
			m.applyProjectPolicies(obj, true)
		},
	})

	stop := make(chan struct{})
	m.Informer = informer
	m.informerChan = stop
	m.APIKeyInformer = apiKeyInformer
	m.projects = informer.GetStore()
	m.apiKeys = apiKeyInformer.GetStore()
	go informer.Run(stop)
	go apiKeyInformer.Run(stop)

	if !cache.WaitForCacheSync(stop, informer.HasSynced, apiKeyInformer.HasSynced) {
		logger.L().Error("Failed to sync informer cache")
		return fmt.Errorf("failed to sync informer cache")
	}

	// Caches synced. Add OIDC mappings
	logger.L().Info("Informer cache synced, loading OIDC policies")

	for _, mapping := range m.cfg.OIDCGroupMappings {
		logger.L().Info("Adding OIDC group mapping", zap.String("oidc_group", mapping.OIDCGroup), zap.String("tenant", mapping.Tenant), zap.String("group", mapping.Group))
		m.Enforcer.AddGroupingPolicy(mapping.OIDCGroup, mapping.Group, mapping.Tenant)
	}

	pols, err := m.Enforcer.GetGroupingPolicy()
	if err != nil {
		logger.L().Error("Failed to get policies", zap.Error(err))
		return fmt.Errorf("failed to get policies: %w", err)
	}
	logger.L().Debug("Current policies after sync:")
	for _, p := range pols {
		logger.L().Debug("Policy", zap.String("sub", p[0]), zap.String("dom", p[1]), zap.String("group", p[2]))
	}

	return nil
}

func (m *Module) applyProjectPolicies(object any, remove bool) {
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		object = tombstone.Obj
	}
	resource, ok := object.(*unstructured.Unstructured)
	if !ok {
		logger.L().Error("Unexpected Project informer object", zap.Any("object", object))
		return
	}
	project := &infrastructurev1alpha1.Project{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, project); err != nil {
		logger.L().Error("Failed to convert Project", zap.Error(err))
		return
	}
	if project.Name == "" || project.Namespace != m.scope().ResourceNamespace(project.Name) {
		return
	}
	for _, rule := range project.Spec.Rbac.Rules {
		if rule.PType != "p" || rule.V1 != project.Name {
			continue
		}
		var err error
		if remove {
			_, err = m.Enforcer.RemovePolicy(rule.V0, rule.V1, rule.V2, rule.V3)
		} else {
			_, err = m.Enforcer.AddPolicy(rule.V0, rule.V1, rule.V2, rule.V3)
		}
		if err != nil {
			logger.L().Error("Failed to synchronize Project policy", zap.String("project", project.Name), zap.Error(err))
		}
	}
	for _, group := range project.Spec.Rbac.Groups {
		if group.PType != "g" || group.V2 != project.Name {
			continue
		}
		var err error
		if remove {
			_, err = m.Enforcer.RemoveGroupingPolicy(group.V0, group.V1, group.V2)
		} else {
			_, err = m.Enforcer.AddGroupingPolicy(group.V0, group.V1, group.V2)
		}
		if err != nil {
			logger.L().Error("Failed to synchronize Project group", zap.String("project", project.Name), zap.Error(err))
		}
	}
}

func (a *Module) SetMiddlewares(middlewares ...gin.HandlerFunc) {
	// No-op for this module
}

func (m *Module) SetEnforcer(enforcer *casbin.Enforcer) {
	// No-op for this module
}
