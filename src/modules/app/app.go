package app

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/casbin/casbin/v3"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Module interface {
	SetMiddlewares(...gin.HandlerFunc)
	SetEnforcer(enforcer *casbin.Enforcer)
	RegisterRoutes(r *gin.Engine)
	Init() error
	Shutdown()
}

type PrometheusAware interface {
	SetPrometheus(prometheus *Prometheus)
}

type ModuleBase struct {
	Module
	Name string
}

type Config struct {
	Production         bool
	PrometheusEndpoint string
	HealthcheckDBDSN   string
}

type App struct {
	Engine        *gin.Engine
	Modules       []ModuleBase
	Prometheus    *Prometheus
	HealthcheckDB *HealthcheckDB
}

func New(cfg Config) (*App, error) {
	if cfg.Production {
		gin.SetMode(gin.ReleaseMode)
	}
	g := gin.Default()

	prometheusClient, err := NewPrometheus(PrometheusConfig{
		Endpoint: cfg.PrometheusEndpoint,
	})
	if err != nil {
		return nil, err
	}

	healthcheckDB, err := NewHealthcheckDB(context.Background(), HealthcheckDBConfig{DSN: cfg.HealthcheckDBDSN})
	if err != nil {
		return nil, err
	}
	if healthcheckDB == nil {
		logger.L().Warn("Healthcheck database DSN not set; live healthcheck endpoints are disabled")
	} else if err := healthcheckDB.Ping(context.Background()); err != nil {
		// The pool reconnects lazily, so a temporarily unavailable database must not block the API from starting.
		logger.L().Error("Healthcheck database is not reachable", zap.Error(err))
	} else {
		logger.L().Info("Connected to healthcheck database")
	}

	return &App{
		Engine:        g,
		Modules:       []ModuleBase{},
		Prometheus:    prometheusClient,
		HealthcheckDB: healthcheckDB,
	}, nil
}

func (a *App) RegisterModule(m Module, name string) error {
	a.Modules = append(a.Modules, ModuleBase{Module: m, Name: name})
	if promAware, ok := m.(PrometheusAware); ok {
		promAware.SetPrometheus(a.Prometheus)
	}
	if dbAware, ok := m.(HealthcheckDBAware); ok && a.HealthcheckDB != nil {
		dbAware.SetHealthcheckDB(a.HealthcheckDB)
	}
	err := m.Init()
	if err != nil {
		return err
	}
	m.RegisterRoutes(a.Engine)
	return nil
}

func (a *App) GetModule(name string) Module {
	for _, m := range a.Modules {
		if m.Name == name {
			return m.Module
		}
	}
	return nil
}

func (a *App) Shutdown() {
	for _, m := range a.Modules {
		m.Shutdown()
	}
}

func (a *App) Run(addr string) error {

	srv := &http.Server{
		Addr:    addr,
		Handler: a.Engine.Handler(),
	}

	logger.L().Info("Starting server", zap.String("address", addr))
	go func() {
		// service connections
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.L().Error("ListenAndServe error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)

	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.L().Info("Shutting down server ...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.L().Error("Server Shutdown Error", zap.Error(err))
	}
	logger.L().Info("Server exiting")

	for _, m := range a.Modules {
		m.Shutdown()
	}
	a.HealthcheckDB.Close()

	return nil
}
