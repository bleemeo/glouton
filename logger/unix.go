// Copyright 2015-2019 Bleemeo
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

// +build !windows

package logger

import (
	"io"
	"log"
	"log/syslog"
	"os"
)

// UseSyslog enable or disable logging to syslog. If syslog is not used, message
// are sent to StdErr.
func UseSyslog(useSyslog bool) error {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	cfg.useSyslog = useSyslog

	if closer, ok := cfg.writer.(io.WriteCloser); ok && cfg.writer != os.Stderr {
		closer.Close()
	}

	cfg.writer = nil

	var err error

	if useSyslog {
		cfg.writer, err = syslog.New(syslog.LOG_INFO|syslog.LOG_DAEMON, "")

		if err != nil {
			cfg.writer = os.Stderr
			cfg.useSyslog = false
		}
	} else {
		cfg.writer = os.Stderr
	}

	cfg.teeWriter = io.MultiWriter(logBuffer, cfg.writer)

	log.SetOutput(cfg.writer)

	return err
}
