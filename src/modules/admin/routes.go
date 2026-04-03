package admin

import (
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	"github.com/gin-gonic/gin"
)

func (m *Module) RegisterRoutes(r *gin.Engine) {
	group := r.Group("/admin", m.middlewares...)

	group.GET("", auth.NewAuthzBuilder().E(m.enforcer).ST(m.cfg.DefaultAdminProject).R("location").S("user_id").A("read").Build(), func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "Welcome to the admin area!"})
		return
	})
}
