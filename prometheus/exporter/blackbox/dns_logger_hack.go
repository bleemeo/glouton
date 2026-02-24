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

package blackbox

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

var errRCodeNotSeen = errors.New("rcode not seen in log message")

type dnsHackLogger struct {
	rootHandler slog.Handler

	l         sync.Mutex
	rcode     int
	rcodeSeen bool
}

type dnsHackHandler struct {
	logger  *dnsHackLogger
	handler slog.Handler
}

// newDNSHackLogger return an dnsLoggerHack which allow to extract RCode from ProbeDNS.
// One dnsLoggerHack should be used only for one ProbeDNS call.
func newDNSHackLogger(extLogger *slog.Logger) *dnsHackLogger {
	return &dnsHackLogger{
		rootHandler: extLogger.Handler(),
	}
}

func (l *dnsHackLogger) Logger() *slog.Logger {
	return slog.New(&dnsHackHandler{logger: l, handler: l.rootHandler})
}

func (l *dnsHackLogger) GetRCode() (int, error) {
	l.l.Lock()
	defer l.l.Unlock()

	if !l.rcodeSeen {
		return 0, errRCodeNotSeen
	}

	return l.rcode, nil
}

func (l *dnsHackLogger) processRCode(attr slog.Attr) {
	l.l.Lock()
	defer l.l.Unlock()

	l.rcodeSeen = true
	l.rcode = int(attr.Value.Int64())
}

func (h *dnsHackHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// We need to accept Debug message to receive the rcode message.
	// dnsLoggerHack.Handle will re-do a check for Enabled.
	if level == slog.LevelDebug {
		return true
	}

	return h.handler.Enabled(ctx, level)
}

func (h *dnsHackHandler) Handle(ctx context.Context, record slog.Record) error {
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "rcode" {
			h.logger.processRCode(a)

			return false
		}

		return true
	})

	if record.Level == slog.LevelDebug && !h.handler.Enabled(ctx, record.Level) {
		return nil
	}

	return h.handler.Handle(ctx, record)
}

func (h *dnsHackHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// If blackbox ProbeDNS used .With() to pass the rcode, it should be handled here.
	// But today it doesn't so don't handle it here.
	// Test_Collect_DNS make sure we are able to extract rcode, so this test should catch if blackbox
	// change its behavior.
	return &dnsHackHandler{
		handler: h.handler.WithAttrs(attrs),
		logger:  h.logger,
	}
}

func (h *dnsHackHandler) WithGroup(name string) slog.Handler {
	return &dnsHackHandler{
		handler: h.handler.WithGroup(name),
		logger:  h.logger,
	}
}
