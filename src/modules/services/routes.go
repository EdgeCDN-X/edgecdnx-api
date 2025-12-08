package services

import (
	"fmt"
	"net/http"

	oidcauth "github.com/TJM/gin-gonic-oidcauth"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func (m *Module) RegisterRoutes(r *gin.Engine, middleWares ...gin.HandlerFunc) {
	group := r.Group("/services", middleWares...)

	group.GET("", func(c *gin.Context) {
		var name, email, out string
		login := c.GetString(oidcauth.AuthUserKey)
		session := sessions.Default(c)
		n := session.Get("name")
		if n == nil {
			name = "Someone without a name?"
		} else {
			name = n.(string)
		}
		e := session.Get("email")
		if e != nil {
			email = e.(string)
		}
		out = fmt.Sprintf("Hello, %s <%s>.\nLogin: %s\n", name, email, login)
		c.String(http.StatusOK, out)
		return
	})
}
