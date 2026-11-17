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

package types

import (
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
)

// LogThrottle logs a message at most once per hour and per key, all other logs are dropped
// to prevent spam. Different keys could each log a message the same hour.
type LogThrottle struct {
	now func() time.Time

	l            sync.Mutex
	lastLogByKey map[string]time.Time
}

func NewLogThrottle(now func() time.Time) *LogThrottle {
	return &LogThrottle{
		now:          now,
		lastLogByKey: make(map[string]time.Time),
	}
}

// Log logs msg at the given level, unless a message with the same key was logged during the
// last hour.
func (t *LogThrottle) Log(key string, level int, msg string) {
	t.l.Lock()
	defer t.l.Unlock()

	now := t.now()

	if now.Sub(t.lastLogByKey[key]) > time.Hour {
		logger.V(level).Println(msg)

		t.lastLogByKey[key] = now
	}
}
