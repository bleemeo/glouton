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
	"context"
	"log/slog"
	"strings"
)

var slogLevelToGloutonLevel = map[slog.Level]int{ //nolint:gochecknoglobals
	slog.LevelDebug: 3,
	slog.LevelInfo:  2,
	slog.LevelWarn:  1,
	slog.LevelError: 0,
}

type slogAdapter struct {
	level int
	group string
	attrs []slog.Attr
}

func (sa slogAdapter) Enabled(_ context.Context, level slog.Level) bool {
	return slogLevelToGloutonLevel[level] <= sa.level
}

func (sa slogAdapter) Handle(_ context.Context, record slog.Record) error {
	record.AddAttrs(sa.attrs...)

	attrPairs := make([]string, 0, record.NumAttrs())

	record.Attrs(func(attr slog.Attr) bool {
		attrPairs = append(attrPairs, sa.prefixAttr(attr).String())

		return true
	})

	var attrs string

	if len(attrPairs) > 0 {
		attrs = " " + strings.Join(attrPairs, " ")
	}

	V(slogLevelToGloutonLevel[record.Level]).Println(record.Message + attrs)

	return nil
}

func (sa slogAdapter) WithAttrs(attrs []slog.Attr) slog.Handler {
	return slogAdapter{
		level: sa.level,
		group: sa.group,
		attrs: append(sa.attrs, attrs...),
	}
}

func (sa slogAdapter) WithGroup(name string) slog.Handler {
	if sa.group != "" {
		name = sa.group + "." + name
	}

	return slogAdapter{
		level: sa.level,
		group: name,
		attrs: sa.attrs,
	}
}

func (sa slogAdapter) prefixAttr(attr slog.Attr) slog.Attr {
	if sa.group == "" {
		return attr
	}

	return slog.Attr{
		Key:   sa.group + "." + attr.Key,
		Value: attr.Value,
	}
}

func NewSlog() *slog.Logger {
	cfg.l.Lock()
	level := cfg.level
	cfg.l.Unlock()

	return slog.New(slogAdapter{level: level})
}
