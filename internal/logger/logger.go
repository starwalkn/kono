package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(debug bool) *zap.Logger {
	var (
		lvl           zapcore.Level
		encoderConfig zapcore.EncoderConfig
	)

	if debug {
		lvl = zap.DebugLevel
		encoderConfig = zap.NewDevelopmentEncoderConfig()
	} else {
		lvl = zap.InfoLevel
		encoderConfig = zap.NewProductionEncoderConfig()
	}

	encoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder

	config := zap.Config{
		Level:         zap.NewAtomicLevelAt(lvl),
		Development:   debug,
		Encoding:      "json",
		EncoderConfig: encoderConfig,
	}

	log, err := config.Build()
	if err != nil {
		panic(err)
	}
	defer func() {
		if err = log.Sync(); err != nil {
			log.Warn("failed to sync logger", zap.Error(err))
		}
	}()

	return log
}
