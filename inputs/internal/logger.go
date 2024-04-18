// Copyright 2015-2023 Bleemeo
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

import "github.com/bleemeo/glouton/logger"

// Logger is an implementation of telegraf.Logger.
type Logger struct{}

// Errorf logs an error message, patterned after log.Printf.
func (l Logger) Errorf(format string, args ...interface{}) {
	logger.Printf(format, args...)
}

// Error logs an error message, patterned after log.Print.
func (l Logger) Error(args ...interface{}) {
	logger.V(0).Println(args...)
}

// Debugf logs a debug message, patterned after log.Printf.
func (l Logger) Debugf(format string, args ...interface{}) {
	logger.V(2).Printf(format, args...)
}

// Debug logs a debug message, patterned after log.Print.
func (l Logger) Debug(args ...interface{}) {
	logger.V(2).Println(args...)
}

// Warnf logs a warning message, patterned after log.Printf.
func (l Logger) Warnf(format string, args ...interface{}) {
	logger.Printf(format, args...)
}

// Warn logs a warning message, patterned after log.Print.
func (l Logger) Warn(args ...interface{}) {
	logger.V(0).Println(args...)
}

// Infof logs an information message, patterned after log.Printf.
func (l Logger) Infof(format string, args ...interface{}) {
	logger.V(1).Printf(format, args...)
}

// Info logs an information message, patterned after log.Print.
func (l Logger) Info(args ...interface{}) {
	logger.V(1).Println(args...)
}
