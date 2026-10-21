package admin

import (
	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/casbin/casbin/v3"
	"github.com/gin-gonic/gin"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type Config struct {
	Namespaced bool
	Namespace  string

	DefaultAdminProject string
	DefaultAdminUser    string
}

type Module struct {
	cfg         Config
	dynClient   dynamic.Interface
	client      *kubernetes.Clientset
	prometheus  *app.Prometheus
	middlewares []gin.HandlerFunc
	enforcer    *casbin.Enforcer
}

func New(cfg Config) *Module {
	return &Module{cfg: cfg}
}

func (m *Module) scope() app.Scope {
	return app.Scope{Namespaced: m.cfg.Namespaced, Namespace: m.cfg.Namespace}
}

func (m *Module) Shutdown() { app.CloseResourceClient(m.dynClient) }

func (m *Module) Init() error {
	logger.L().Info("Initializing module")
	dynClient, _, err := app.GetK8SDynamicClient()
	if err != nil {
		return err
	}

	client, _, err := app.GetK8SClient()
	if err != nil {
		return err
	}

	m.dynClient, err = app.NewResourceClient(dynClient, m.scope(), prefixListGVR, zoneGVR, locationGVR)
	if err != nil {
		return err
	}
	m.client = client

	return nil
}

func (m *Module) SetMiddlewares(middlewares ...gin.HandlerFunc) {
	m.middlewares = middlewares
}

func (m *Module) SetEnforcer(enforcer *casbin.Enforcer) {
	m.enforcer = enforcer
}

func (m *Module) SetPrometheus(prometheus *app.Prometheus) {
	m.prometheus = prometheus
}
