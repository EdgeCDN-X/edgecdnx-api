package logger

import (
	"sync"

	"go.uber.org/zap"
)

var (
	logger *zap.Logger
	once   sync.Once
)

func Init(production bool) {
	once.Do(func() {
		var cfg zap.Config

		if production {
			cfg = zap.NewProductionConfig()
		} else {
			cfg = zap.NewDevelopmentConfig()
		}

		l, err := cfg.Build()
		if err != nil {
			panic("failed to initialize logger: " + err.Error())
		}

		logger = l
	})
}

func L() *zap.Logger {
	if logger == nil {
		panic("logger not initialized, call Init() first")
	}
	return logger
}
