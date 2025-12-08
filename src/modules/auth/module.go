package auth

import (
	oidcauth "github.com/TJM/gin-gonic-oidcauth"
)

type Config struct {
	oidcauth.Config
}

type Module struct {
	cfg  Config
	Auth *oidcauth.OidcAuth
}

func New(cfg Config) (*Module, error) {
	if cfg.Config.ClientID == "" {
		cfg.Config = *oidcauth.DefaultConfig()
	}

	auth, err := oidcauth.GetOidcAuth(&cfg.Config)
	if err != nil {
		return nil, err
	}

	cfg.Config.DefaultAuthenticatedURL = "/services"
	cfg.Config.LogoutURL = "/auth/login"

	return &Module{cfg: cfg, Auth: auth}, nil
}

func (m *Module) Shutdown() {
	// No-op
}

func (m *Module) Init() error {
	return nil
}
