package zones

import (
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"github.com/casbin/casbin/v3"
	"github.com/gin-gonic/gin"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type Config struct {
	Namespace string
}

type Module struct {
	cfg         Config
	client      *dynamic.DynamicClient
	middlewares []gin.HandlerFunc
	enforcer    *casbin.Enforcer
	baseCfg     *rest.Config
}

func New(cfg Config) *Module {
	return &Module{cfg: cfg}
}

func (m *Module) Shutdown() {}

func (m *Module) Init() error {
	logger.L().Info("Initializing module")
	client, baseCfg, err := app.GetK8SDynamicClient()
	if err != nil {
		return err
	}

	m.client = client
	m.baseCfg = baseCfg

	return nil
}

func (m *Module) SetMiddlewares(middlewares ...gin.HandlerFunc) {
	m.middlewares = middlewares
}

func (m *Module) SetEnforcer(enforcer *casbin.Enforcer) {
	m.enforcer = enforcer
}

func (m *Module) RnameToEmail(rname string) string {
	// remove trailing dot
	rname = strings.TrimSuffix(rname, ".")

	var localPart strings.Builder
	var domainPart strings.Builder
	escaped := false
	split := false

	for _, c := range rname {
		if split {
			domainPart.WriteRune(c)
			continue
		}
		if escaped {
			localPart.WriteRune(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '.' {
			// first unescaped dot: split
			split = true
			continue
		}
		localPart.WriteRune(c)
	}

	return localPart.String() + "@" + domainPart.String()
}

// EmailToRname converts an email address to an SOA RNAME, escaping dots in the local part.
func (m *Module) EmailToRname(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return email // invalid email, return as-is
	}
	local := parts[0]
	domain := parts[1]

	// escape dots in the local part
	localEscaped := strings.ReplaceAll(local, ".", `\.`)

	return localEscaped + "." + domain + "."
}
