// Copyright 2015-2022 Bleemeo
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
	"time"
)

// To avoid inputs spamming log message, we limit the number of messages
// shown to the user to one per minVisibleLogInterval.
const minVisibleLogInterval = time.Hour

type TelegrafLogger struct {
	name string
	// The last time a log was sent with level 0.
	lastVisibleLog time.Time
}

func NewTelegrafLog(name string) *TelegrafLogger {
	if name == "" {
		name = "missing input name"
	}

	return &TelegrafLogger{name: name}
}

// Errorf logs an error message, patterned after log.Printf.
func (t *TelegrafLogger) Errorf(format string, args ...interface{}) {
	V(t.errorLogLevel()).Printf(t.addNameToFormat(format), args...)
}

// Error logs an error message, patterned after log.Print.
func (t *TelegrafLogger) Error(args ...interface{}) {
	V(t.errorLogLevel()).Println(t.addNameToArgs(args...))
}

func (t *TelegrafLogger) errorLogLevel() int {
	// Messages that should have been logged at level 0 are logged
	// at level 1 instead if another one was logged recently.
	level := 1

	if time.Since(t.lastVisibleLog) > minVisibleLogInterval {
		t.lastVisibleLog = time.Now()
		level = 0
	}

	return level
}

// Debugf logs a debug message, patterned after log.Printf.
func (t *TelegrafLogger) Debugf(format string, args ...interface{}) {
	V(3).Printf(t.addNameToFormat(format), args...)
}

// Debug logs a debug message, patterned after log.Print.
func (t *TelegrafLogger) Debug(args ...interface{}) {
	V(3).Println(t.addNameToArgs(args...))
}

// Warnf logs a warning message, patterned after log.Printf.
func (t *TelegrafLogger) Warnf(format string, args ...interface{}) {
	V(1).Printf(t.addNameToFormat(format), args...)
}

// Warn logs a warning message, patterned after log.Print.
func (t *TelegrafLogger) Warn(args ...interface{}) {
	V(1).Println(t.addNameToArgs(args...))
}

// Infof logs an information message, patterned after log.Printf.
func (t *TelegrafLogger) Infof(format string, args ...interface{}) {
	V(2).Printf(t.addNameToFormat(format), args...)
}

// Info logs an information message, patterned after log.Print.
func (t *TelegrafLogger) Info(args ...interface{}) {
	V(2).Println(t.addNameToArgs(args...))
}

// addNameToArgs adds the input name to the log, should be used with Print.
func (t *TelegrafLogger) addNameToArgs(args ...interface{}) string {
	log := fmt.Sprintf("%s: ", t.name)

	for _, arg := range args {
		log += fmt.Sprint(arg)
	}

	return log
}

// addDescriptionToArgs adds the input name to the log, should be used with Printf.
func (t *TelegrafLogger) addNameToFormat(format string) string {
	return fmt.Sprintf("%s: %s", t.name, format)
}
