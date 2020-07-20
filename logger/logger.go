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

package logger

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Logger allow to print message.
type Logger bool

// V return a Level which will only log (Printf do something) if logger is configured to log this level.
// 0 is always logger.
func V(level int) Logger {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	if level <= cfg.level {
		return Logger(true)
	}

	if _, file, _, ok := runtime.Caller(1); ok {
		// file is something like a/b/package/file.go
		// We only want package
		part := strings.Split(file, "/")
		if len(part) < 2 {
			return Logger(false)
		}

		pkg := part[len(part)-2]

		if level <= cfg.pkgLevels[pkg] {
			return Logger(true)
		}
	}

	return Logger(false)
}

// Printf behave like fmt.Printf.
func (l Logger) Printf(fmtArg string, a ...interface{}) {
	if l {
		printf(fmtArg, a...)
	} else {
		fmt.Fprintf(logBuffer, fmtArg+"\n", a...)
	}
}

// Println behave like fmt.Println.
func (l Logger) Println(v ...interface{}) {
	if l {
		println(v...)
	} else {
		fmt.Fprintln(logBuffer, v...)
	}
}

func printf(fmtArg string, a ...interface{}) {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	if !cfg.useSyslog {
		_, _ = fmt.Fprintf(cfg.writer, "%s ", time.Now().Format("2006/01/02 15:04:05"))
	}

	_, _ = fmt.Fprintf(cfg.teeWriter, fmtArg+"\n", a...)
}

func println(v ...interface{}) {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	if !cfg.useSyslog {
		_, _ = fmt.Fprintf(cfg.writer, "%s ", time.Now().Format("2006/01/02 15:04:05"))
	}

	_, _ = fmt.Fprintln(cfg.teeWriter, v...)
}

// Printf behave like fmt.Printf.
func Printf(fmt string, a ...interface{}) {
	printf(fmt, a...)
}

type config struct {
	l         sync.Mutex
	level     int
	pkgLevels map[string]int
	useSyslog bool

	writer    io.Writer
	teeWriter io.Writer
}

//nolint:gochecknoglobals
var (
	logBuffer = &buffer{}
	cfg       = config{
		writer:    os.Stderr,
		teeWriter: io.MultiWriter(logBuffer, os.Stderr),
	}
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
		err = cfg.enableSyslog()
		if err != nil {
			cfg.writer = os.Stderr
			cfg.useSyslog = false
		}
	} else {
		cfg.writer = os.Stderr
	}

	cfg.teeWriter = io.MultiWriter(logBuffer, cfg.writer)

	return err
}

// Buffer return content of the log buffer.
func Buffer() []byte {
	return logBuffer.Content()
}

// SetBufferCapacity define the size of the buffer
// The buffer had two part, the head (first line ever logger, never dropped) and
// the tail (oldest line dropped when tail is full).
// Changing capacity will always drop the tail.
func SetBufferCapacity(headSize int, tailSize int) {
	logBuffer.SetCapacity(headSize, tailSize)
}

// SetLevel configure the log level.
func SetLevel(level int) {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	cfg.level = level
}

// SetPkgLevels configure the log level per package.
// The format is "package=level,package2=level2".
func SetPkgLevels(levels string) {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	pkgLevels := make(map[string]int)

	part := strings.Split(levels, ",")
	for _, p := range part {
		tmp := strings.Split(p, "=")
		if len(tmp) != 2 {
			continue
		}

		pkg := tmp[0]

		level, err := strconv.ParseInt(tmp[1], 10, 0)
		if err != nil {
			continue
		}

		pkgLevels[pkg] = int(level)
	}

	cfg.pkgLevels = pkgLevels
}
