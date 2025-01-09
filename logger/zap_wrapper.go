// Copyright 2015-2025 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
