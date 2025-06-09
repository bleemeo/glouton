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
	"fmt"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
)

// To avoid inputs spamming log message, we limit the number of messages
// shown to the user to one per minVisibleLogInterval.
const minVisibleLogInterval = time.Hour

type TelegrafLogger struct {
	name string
	l    sync.Mutex
	// The last time a log was sent with level 0.
	lastVisibleLog time.Time

	attributes map[string]any
}

func NewTelegrafLog(name string) telegraf.Logger {
	if name == "" {
		name = "missing input name"
	}

	return &TelegrafLogger{
		name:       name,
		attributes: make(map[string]any),
	}
}

func (t *TelegrafLogger) Level() telegraf.LogLevel {
	t.l.Lock()
	defer t.l.Unlock()

	if time.Since(t.lastVisibleLog) < minVisibleLogInterval {
		return telegraf.Info
	}

	return telegraf.Debug
}

func (t *TelegrafLogger) AddAttribute(key string, value any) {
	t.l.Lock()
	defer t.l.Unlock()

	t.attributes[key] = value
}

// Errorf logs an error message, patterned after log.Printf.
func (t *TelegrafLogger) Errorf(format string, args ...any) {
	V(t.errorLogLevel()).Printf(t.addNameAndAttrsToFormat(format), args...)
}

// Error logs an error message, patterned after log.Print.
func (t *TelegrafLogger) Error(args ...any) {
	V(t.errorLogLevel()).Println(t.addNameAndAttrsToArgs(args...))
}

// Warnf logs a warning message, patterned after log.Printf.
func (t *TelegrafLogger) Warnf(format string, args ...any) {
	V(1).Printf(t.addNameAndAttrsToFormat(format), args...)
}

// Warn logs a warning message, patterned after log.Print.
func (t *TelegrafLogger) Warn(args ...any) {
	V(1).Println(t.addNameAndAttrsToArgs(args...))
}

// Infof logs an information message, patterned after log.Printf.
func (t *TelegrafLogger) Infof(format string, args ...any) {
	V(2).Printf(t.addNameAndAttrsToFormat(format), args...)
}

// Info logs an information message, patterned after log.Print.
func (t *TelegrafLogger) Info(args ...any) {
	V(2).Println(t.addNameAndAttrsToArgs(args...))
}

// Debugf logs a debug message, patterned after log.Printf.
func (t *TelegrafLogger) Debugf(format string, args ...any) {
	V(3).Printf(t.addNameAndAttrsToFormat(format), args...)
}

// Debug logs a debug message, patterned after log.Print.
func (t *TelegrafLogger) Debug(args ...any) {
	V(3).Println(t.addNameAndAttrsToArgs(args...))
}

func (t *TelegrafLogger) Tracef(format string, args ...any) {
	V(4).Printf(t.addNameAndAttrsToFormat(format), args...)
}

func (t *TelegrafLogger) Trace(args ...any) {
	V(4).Println(t.addNameAndAttrsToArgs(args...))
}

func (t *TelegrafLogger) errorLogLevel() int {
	t.l.Lock()
	defer t.l.Unlock()

	// Messages that should have been logged at level 0 are logged
	// at level 1 instead if another one was logged recently.
	level := 1

	if time.Since(t.lastVisibleLog) > minVisibleLogInterval {
		t.lastVisibleLog = time.Now()
		level = 0
	}

	return level
}

// addNameAndAttrsToArgs adds the input name and registered attributes to the log, should be used with Print.
func (t *TelegrafLogger) addNameAndAttrsToArgs(args ...any) string {
	log := t.name + ": "

	for _, arg := range args {
		log += fmt.Sprint(arg)
	}

	t.l.Lock()
	defer t.l.Unlock()

	for key, value := range t.attributes {
		log += fmt.Sprintf(" %s=%v", key, value)
	}

	return log
}

// addDescriptionToArgs adds the input name and registered attributes to the log, should be used with Printf.
func (t *TelegrafLogger) addNameAndAttrsToFormat(format string) string {
	log := t.name + ": " + format

	t.l.Lock()
	defer t.l.Unlock()

	for key, value := range t.attributes {
		log += fmt.Sprintf(" %s=%v", key, value)
	}

	return log
}
