package app

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Module interface {
	RegisterRoutes(r *gin.Engine, middlewares ...gin.HandlerFunc)
	Init() error
	Shutdown()
}

type App struct {
	Engine  *gin.Engine
	Modules []Module
}

func New(production bool) *App {
	if production {
		gin.SetMode(gin.ReleaseMode)
	}
	g := gin.Default()

	return &App{
		Engine:  g,
		Modules: []Module{},
	}
}

func (a *App) RegisterModule(m Module, middlewares ...gin.HandlerFunc) error {
	a.Modules = append(a.Modules, m)
	err := m.Init()
	if err != nil {
		return err
	}
	m.RegisterRoutes(a.Engine, middlewares...)
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

	return nil
}
