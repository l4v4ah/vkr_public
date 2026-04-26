// Package logger provides a structured JSON zap logger for microservices.
package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New creates a structured JSON logger suitable for microservice environments.
// Log level is controlled via the LOG_LEVEL environment variable (default: info).
func New(service string) *zap.Logger {
	level := zapcore.InfoLevel
	if lvl := os.Getenv("LOG_LEVEL"); lvl != "" {
		_ = level.UnmarshalText([]byte(lvl))
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(os.Stdout),
		level,
	)

	return zap.New(core, zap.AddCaller(), zap.AddCallerSkip(0)).
		With(zap.String("service", service))
}
