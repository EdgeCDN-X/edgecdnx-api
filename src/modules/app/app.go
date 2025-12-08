package app

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
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

func New() *App {
	return &App{
		Engine:  gin.Default(),
		Modules: []Module{},
	}
}

func (a *App) RegisterModule(m Module, middlewares ...gin.HandlerFunc) {
	a.Modules = append(a.Modules, m)
	m.Init()
	m.RegisterRoutes(a.Engine, middlewares...)
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

	quit := make(chan os.Signal, 1)

	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutdown Server ...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Println("Server Shutdown:", err)
	}
	log.Println("Server exiting")

	for _, m := range a.Modules {
		m.Shutdown()
	}

	return nil
}
