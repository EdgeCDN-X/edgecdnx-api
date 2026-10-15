package utils

import (
	"net/http"
	"time"

	"github.com/casbin/casbin/v3"
	"github.com/gin-gonic/gin"
)

type Module struct {
	client      *http.Client
	middlewares []gin.HandlerFunc
}

func New() *Module {
	return &Module{
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (m *Module) Init() error { return nil }

func (m *Module) Shutdown() { m.client.CloseIdleConnections() }

func (m *Module) SetMiddlewares(middlewares ...gin.HandlerFunc) {
	m.middlewares = middlewares
}

// Utilities require authentication but do not operate on tenant resources.
func (m *Module) SetEnforcer(_ *casbin.Enforcer) {}
