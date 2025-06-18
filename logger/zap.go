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
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	logDebouncePeriod      = 30 * time.Second
	logDebouncePurgePeriod = 10 * time.Minute
)

func ZapLogger() *zap.Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = nil

	return zap.New(
		zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			&zapWrapper{
				lastPurge: time.Now(),
				m:         make(map[string]time.Time),
			},
			zap.DebugLevel,
		),
	)
}

type zapWrapper struct {
	l         sync.Mutex
	lastPurge time.Time
	m         map[string]time.Time
}

func (*zapWrapper) Sync() error {
	return nil
}

func (z *zapWrapper) Write(buffer []byte) (int, error) {
	msg := strings.TrimRight(string(buffer), "\n\r")

	z.l.Lock()

	if time.Since(z.lastPurge) >= logDebouncePurgePeriod {
		z.purgeDebounceCache(time.Now())
	}

	lastPrint, found := z.m[msg]
	if found && time.Since(lastPrint) < logDebouncePeriod {
		z.l.Unlock()

		return len(buffer), nil
	}

	z.m[msg] = time.Now()

	z.l.Unlock()

	if strings.HasPrefix(msg, "debug") {
		V(2).Println(msg)
	} else {
		V(1).Println(msg)
	}

	return len(buffer), nil
}

func (z *zapWrapper) purgeDebounceCache(now time.Time) {
	z.lastPurge = now

	for msg, ts := range z.m {
		if now.Sub(ts) > logDebouncePeriod {
			delete(z.m, msg)
		}
	}
}
