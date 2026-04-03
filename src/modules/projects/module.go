package projects

import (
	"context"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/casbin/casbin/v3"
	"github.com/gin-gonic/gin"
	"github.com/gosimple/slug"
	"k8s.io/client-go/dynamic"
)

type Config struct {
	Namespace           string
	DefaultAdminProject string
	DefaultAdminUser    string
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
	client, _, err := app.GetK8SDynamicClient()
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

func (m *Module) EnsureAdmin() error {
	// TODO handle updates
	createProjectDto := ProjectDto{
		Name:        m.cfg.DefaultAdminProject,
		Description: "Admin project",
	}

	ctx := context.Background()

	created_by := slug.Make(strings.ReplaceAll(m.cfg.DefaultAdminUser, "@", "-at-"))
	_, code, err := m.createProject(ctx, m.cfg.DefaultAdminUser, created_by, createProjectDto)
	if err != nil {
		if code == 409 {
			logger.L().Info("Admin project already exists, skipping creation")
			return nil
		}
		return err
	}

	return nil
}
