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

package client

import (
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type LogWrapper struct {
	l                 sync.Mutex
	lastPingCheckSeen time.Time
}

type printWrapper struct {
	w *LogWrapper
}

//nolint:gochecknoglobals // paho.DEBUG & co are global, that's why we need global.
var (
	globalL sync.Mutex
	globalW *LogWrapper
)

func GetOrCreateWrapper() *LogWrapper {
	globalL.Lock()
	defer globalL.Unlock()

	if globalW == nil {
		globalW = &LogWrapper{}

		paho.ERROR = globalW.logFunction()
		paho.CRITICAL = globalW.logFunction()
		paho.WARN = globalW.logFunction()
		paho.DEBUG = globalW.logFunction()
	}

	return globalW
}

func (w *LogWrapper) logFunction() *printWrapper {
	return &printWrapper{w: w}
}

func (w *LogWrapper) updateLastPing() {
	w.l.Lock()
	defer w.l.Unlock()

	w.lastPingCheckSeen = time.Now()
}

func (w *LogWrapper) LastPingAt() time.Time {
	w.l.Lock()
	defer w.l.Unlock()

	return w.lastPingCheckSeen
}

func (p *printWrapper) Println(v ...any) {
	logger.V(2).Println(v...)
	// hack to track if keepalive gorouting is running.
	// We track the log DEBUG.Println(PNG, "keepalive stopped")
	if len(v) == 3 && v[0] == paho.PNG && v[1] == "ping check" {
		p.w.updateLastPing()
	}
}

func (p *printWrapper) Printf(format string, v ...any) {
	logger.V(2).Printf(format, v...)
}
