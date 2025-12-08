package auth

import "github.com/gin-gonic/gin"

func (m *Module) RegisterRoutes(r *gin.Engine, middleWares ...gin.HandlerFunc) {
	group := r.Group("/auth", middleWares...)
	group.GET("login", m.Auth.Login)
	group.GET("callback", m.Auth.AuthCallback)
	group.GET("logout", m.Auth.Logout)
}
