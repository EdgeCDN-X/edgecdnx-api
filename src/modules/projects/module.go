package projects

import (
	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/casbin/casbin/v3"
	"github.com/gin-gonic/gin"
	"k8s.io/client-go/dynamic"
)

type Config struct {
	Namespace string
}

type Module struct {
	cfg         Config
	client      *dynamic.DynamicClient
	middlewares []gin.HandlerFunc
	enforcer    *casbin.Enforcer
}

func New(cfg Config) *Module {
	return &Module{cfg: cfg}
}

func (m *Module) Shutdown() {}

func (m *Module) Init() error {
	logger.L().Info("Initializing module")
	client, err := app.GetK8SDynamicClient()
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
