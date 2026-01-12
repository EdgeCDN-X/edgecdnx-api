package auth

import (
	"github.com/casbin/casbin/v3"
	"github.com/gin-gonic/gin"
)

type AuthzBuilder struct {
	Enforcer *casbin.Enforcer
	Subject  string
	Tenant   string
	Resource string
	Action   string
}

func NewAuthzBuilder() *AuthzBuilder {
	return &AuthzBuilder{}
}

func (b *AuthzBuilder) E(enforcer *casbin.Enforcer) *AuthzBuilder {
	b.Enforcer = enforcer
	return b
}

func (b *AuthzBuilder) S(subject string) *AuthzBuilder {
	b.Subject = subject
	return b
}

func (b *AuthzBuilder) T(tenant string) *AuthzBuilder {
	b.Tenant = tenant
	return b
}

func (b *AuthzBuilder) R(resource string) *AuthzBuilder {
	b.Resource = resource
	return b
}

func (b *AuthzBuilder) A(action string) *AuthzBuilder {
	b.Action = action
	return b
}

func (b *AuthzBuilder) Build() gin.HandlerFunc {
	return func(c *gin.Context) {
		subject := c.GetString(b.Subject)
		tenant := c.Param(b.Tenant)
		resource := b.Resource
		action := b.Action

		allowed, err := b.Enforcer.Enforce(subject, tenant, resource, action)
		if err != nil {
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
			return
		}
		if !allowed {
			c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
			return
		}

		c.Next()
	}
}
