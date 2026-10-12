package auth

import "github.com/gin-gonic/gin"

func (m *Module) RegisterRoutes(r *gin.Engine) {
	r.GET("/auth/resource-types", func(c *gin.Context) {
		c.JSON(200, ResourceTypes())
	})
}
