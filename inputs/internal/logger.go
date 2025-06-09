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

package internal

import (
	"fmt"
	"sync"

	"github.com/bleemeo/glouton/logger"

	"github.com/influxdata/telegraf"
)

// Logger is an implementation of telegraf.Logger.
type Logger struct {
	l          sync.Mutex
	attributes map[string]any
}

func NewLogger() *Logger {
	return &Logger{
		attributes: make(map[string]any),
	}
}

func (l *Logger) Level() telegraf.LogLevel {
	return telegraf.Debug
}

func (l *Logger) AddAttribute(key string, value any) {
	l.l.Lock()
	defer l.l.Unlock()

	l.attributes[key] = value
}

// Errorf logs an error message, patterned after log.Printf.
func (l *Logger) Errorf(format string, args ...any) {
	logger.Printf(l.formatWithAttrs(format), args...)
}

// Error logs an error message, patterned after log.Print.
func (l *Logger) Error(args ...any) {
	logger.V(0).Println(l.argsWithAttrs(args))
}

// Warnf logs a warning message, patterned after log.Printf.
func (l *Logger) Warnf(format string, args ...any) {
	logger.Printf(l.formatWithAttrs(format), args...)
}

// Warn logs a warning message, patterned after log.Print.
func (l *Logger) Warn(args ...any) {
	logger.V(0).Println(l.argsWithAttrs(args))
}

// Infof logs an information message, patterned after log.Printf.
func (l *Logger) Infof(format string, args ...any) {
	logger.V(1).Printf(l.formatWithAttrs(format), args...)
}

// Info logs an information message, patterned after log.Print.
func (l *Logger) Info(args ...any) {
	logger.V(1).Println(l.argsWithAttrs(args))
}

// Debugf logs a debug message, patterned after log.Printf.
func (l *Logger) Debugf(format string, args ...any) {
	logger.V(2).Printf(l.formatWithAttrs(format), args...)
}

// Debug logs a debug message, patterned after log.Print.
func (l *Logger) Debug(args ...any) {
	logger.V(2).Println(l.argsWithAttrs(args))
}

// Tracef logs a trace message, patterned after log.Printf.
func (l *Logger) Tracef(format string, args ...any) {
	logger.V(3).Printf(l.formatWithAttrs(format), args...)
}

// Trace logs a trace message, patterned after log.Print.
func (l *Logger) Trace(args ...any) {
	logger.V(3).Println(l.argsWithAttrs(args))
}

func (l *Logger) argsWithAttrs(args []any) string {
	l.l.Lock()
	defer l.l.Unlock()

	log := fmt.Sprint(args)

	for key, value := range l.attributes {
		log += fmt.Sprintf(" %s=%v", key, value)
	}

	return log
}

func (l *Logger) formatWithAttrs(format string) string {
	l.l.Lock()
	defer l.l.Unlock()

	for key, value := range l.attributes {
		format += fmt.Sprintf(" %s=%v", key, value)
	}

	return format
}
