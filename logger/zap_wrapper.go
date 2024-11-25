package logger

import (
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func ZapLogger() *zap.Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = nil

	return zap.New(
		zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			zapWrapper{},
			zap.DebugLevel,
		),
	)
}

type zapWrapper struct{}

func (zapWrapper) Sync() error {
	return nil
}

func (zapWrapper) Write(buffer []byte) (int, error) {
	msg := strings.TrimRight(string(buffer), "\n\r")
	if strings.HasPrefix(msg, "debug") {
		V(2).Println(msg)
	} else {
		V(1).Println(msg)
	}

	return len(buffer), nil
}
