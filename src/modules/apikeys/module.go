package apikeys

import (
	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/casbin/casbin/v3"
	"github.com/gin-gonic/gin"
	"k8s.io/client-go/dynamic"
)

type Config struct {
	Namespaced bool
	Namespace  string
}

type Module struct {
	cfg         Config
	client      dynamic.Interface
	middlewares []gin.HandlerFunc
	enforcer    *casbin.Enforcer
}

func New(cfg Config) *Module {
	return &Module{cfg: cfg}
}

func (m *Module) scope() app.Scope {
	return app.Scope{Namespaced: m.cfg.Namespaced, Namespace: m.cfg.Namespace}
}

func (m *Module) Shutdown() { app.CloseResourceClient(m.client) }

func (m *Module) Init() error {
	logger.L().Info("Initializing module")
	client, _, err := app.GetK8SDynamicClient()
	if err != nil {
		return err
	}

	m.client, err = app.NewResourceClient(client, m.scope(), apiKeyGVR)
	return err
}

func (m *Module) SetMiddlewares(middlewares ...gin.HandlerFunc) {
	m.middlewares = middlewares
}

func (m *Module) SetEnforcer(enforcer *casbin.Enforcer) {
	m.enforcer = enforcer
}
