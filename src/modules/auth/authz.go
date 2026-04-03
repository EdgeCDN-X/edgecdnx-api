package auth

import (
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/casbin/casbin/v3"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type AuthzBuilder struct {
	Enforcer     *casbin.Enforcer
	Subject      string
	Tenant       string
	StaticTenant string
	Resource     string
	Action       string
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

func (b *AuthzBuilder) ST(tenant string) *AuthzBuilder {
	b.StaticTenant = tenant
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
		if b.StaticTenant != "" {
			tenant = b.StaticTenant
		}

		resource := b.Resource
		action := b.Action
		groupsStr := c.GetString("groups")
		groups := strings.SplitSeq(groupsStr, ",")

		for g := range groups {
			allowed, err := b.Enforcer.Enforce(g, tenant, resource, action)
			if err != nil {
				logger.L().Error("Failed to enforce policy", zap.Error(err))
				c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
				return
			}
			if allowed {
				logger.L().Debug("Access granted via group policy", zap.String("group", g), zap.String("tenant", tenant), zap.String("resource", resource), zap.String("action", action))
				c.Next()
				return
			}
		}

		allowed, err := b.Enforcer.Enforce(subject, tenant, resource, action)

		if err != nil {
			logger.L().Error("Failed to enforce policy", zap.Error(err))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
			return
		}
		if !allowed {
			logger.L().Debug("Access denied", zap.String("subject", subject), zap.String("tenant", tenant), zap.String("resource", resource), zap.String("action", action))
			c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
			return
		}

		c.Next()
	}
}
