package projects

import (
	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
)

func (m *Module) RegisterRoutes(r *gin.Engine, middleWares ...gin.HandlerFunc) {
	group := r.Group("/projects", middleWares...)

	group.GET("", func(c *gin.Context) {
		p := m.Informer.GetIndexer().List()
		nsList := make([]*v1.Namespace, 0, len(p))
		for _, obj := range p {
			ns, ok := obj.(*v1.Namespace)
			if !ok {
				c.JSON(500, gin.H{"error": "internal error"})
				return
			}
			nsList = append(nsList, ns)
		}

		c.JSON(200, nsList)
		return
	})

	group.GET(":projectName", func(c *gin.Context) {
		name := c.Param("projectName")
		p, _ := m.Informer.GetIndexer().ByIndex("projectName", name)

		if len(p) == 0 {
			c.JSON(404, gin.H{"error": "project not found"})
			return
		}

		if len(p) > 1 {
			logger.L().Error("Multiple projects found with the same name", zap.String("name", name))
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		c.JSON(200, p[0].(*v1.Namespace))
		return
	})
}
