// Copyright 2015-2026 Bleemeo
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
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func ZapLogger() *zap.Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = nil

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		&zapWrapper{}, // routing only (debug→V(2), else V(1)); no dedup map
		zap.DebugLevel,
	)

	// Sample per 30s tick (mirrors the previous debounce window): log the first
	// 10 occurrences of a given message template in full, then only 1 in 100
	// thereafter until the tick resets. Keeps some visibility into a storm
	// instead of going fully silent, while staying bounded regardless of how
	// many distinct field values (e.g. timestamps) the flooding messages carry.
	return zap.New(zapcore.NewSamplerWithOptions(core, 30*time.Second, 10, 100))
}

// zapWrapper routes zap output to the glouton logger's verbosity levels.
// Sampling/de-duplication is handled upstream by zapcore.NewSamplerWithOptions.
type zapWrapper struct{}

func (*zapWrapper) Sync() error {
	return nil
}

func (z *zapWrapper) Write(buffer []byte) (int, error) {
	msg := strings.TrimRight(string(buffer), "\n\r")

	if strings.HasPrefix(msg, "debug") {
		V(2).Println(msg)
	} else {
		V(1).Println(msg)
	}

	return len(buffer), nil
}
