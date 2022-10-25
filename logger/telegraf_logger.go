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

import "fmt"

type TelegrafLogger struct {
	name string
}

func NewTelegrafLog(name string) TelegrafLogger {
	if name == "" {
		name = "missing input name"
	}

	return TelegrafLogger{name}
}

// Errorf logs an error message, patterned after log.Printf.
func (t TelegrafLogger) Errorf(format string, args ...interface{}) {
	V(0).Printf(t.addNameToFormat(format), args...)
}

// Error logs an error message, patterned after log.Print.
func (t TelegrafLogger) Error(args ...interface{}) {
	V(0).Println(t.addNameToArgs(args...))
}

// Debugf logs a debug message, patterned after log.Printf.
func (t TelegrafLogger) Debugf(format string, args ...interface{}) {
	V(3).Printf(t.addNameToFormat(format), args...)
}

// Debug logs a debug message, patterned after log.Print.
func (t TelegrafLogger) Debug(args ...interface{}) {
	V(3).Println(t.addNameToArgs(args...))
}

// Warnf logs a warning message, patterned after log.Printf.
func (t TelegrafLogger) Warnf(format string, args ...interface{}) {
	V(1).Printf(t.addNameToFormat(format), args...)
}

// Warn logs a warning message, patterned after log.Print.
func (t TelegrafLogger) Warn(args ...interface{}) {
	V(1).Println(t.addNameToArgs(args...))
}

// Infof logs an information message, patterned after log.Printf.
func (t TelegrafLogger) Infof(format string, args ...interface{}) {
	V(2).Printf(t.addNameToFormat(format), args...)
}

// Info logs an information message, patterned after log.Print.
func (t TelegrafLogger) Info(args ...interface{}) {
	V(2).Println(t.addNameToArgs(args...))
}

// addNameToArgs adds the input name to the log, should be used with Print.
func (t TelegrafLogger) addNameToArgs(args ...interface{}) interface{} {
	return append([]interface{}{fmt.Sprintf("%s: ", t.name)}, args...)
}

// addDescriptionToArgs adds the input name to the log, should be used with Printf.
func (t TelegrafLogger) addNameToFormat(format string) string {
	return fmt.Sprintf("%s: %s", t.name, format)
}
