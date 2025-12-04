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
	"io"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const timeLayout = "2006-01-02 15:04:05.000"

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
func (l Logger) Printf(fmtArg string, a ...any) {
	if l {
		loggerPrintf(fmtArg, a...)
	} else {
		fmt.Fprintf(logBuffer, fmtArg+"\n", a...)
	}
}

// Println behave like fmt.Println.
func (l Logger) Println(v ...any) {
	if l {
		loggerPrintln(v...)
	} else {
		fmt.Fprintln(logBuffer, v...)
	}
}

func loggerPrintf(fmtArg string, a ...any) {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	if !cfg.useSyslog {
		// Add timestamp to the default writer if not using syslog
		_, _ = fmt.Fprintf(cfg.writer, "%s ", time.Now().Format(timeLayout))
	}

	_, _ = fmt.Fprintf(cfg.teeWriter, fmtArg+"\n", a...)
}

func loggerPrintln(v ...any) {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	if !cfg.useSyslog {
		// Add timestamp to the default writer if not using syslog
		_, _ = fmt.Fprintf(cfg.writer, "%s ", time.Now().Format(timeLayout))
	}

	_, _ = fmt.Fprintln(cfg.teeWriter, v...)
}

// Printf behave like fmt.Printf.
func Printf(fmt string, a ...any) {
	loggerPrintf(fmt, a...)
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
	logBuffer          = &buffer{}
	logCurrLevelBuffer = &buffer{} // buffer for current level of logs
	cfg                = config{
		level:     1, // useful for tests; will be overridden during normal runs
		writer:    os.Stdout,
		teeWriter: io.MultiWriter(logBuffer, logCurrLevelBuffer, os.Stdout),
	}
)

// setLogger calls the function passed as argument, and revert to stdout if there is an error.
func setLogger(cb func() error) error {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	if closer, ok := cfg.writer.(io.WriteCloser); ok && cfg.writer != os.Stdout {
		_ = closer.Close()
	}

	cfg.writer = nil

	err := cb()
	if err != nil {
		cfg.writer = os.Stdout
	}

	cfg.teeWriter = io.MultiWriter(logBuffer, logCurrLevelBuffer, cfg.writer)

	log.SetOutput(cfg.writer)

	return err
}

// UseSyslog enable logging to syslog.
func UseSyslog() error {
	return setLogger(func() error {
		err := cfg.enableSyslog()
		if err == nil {
			cfg.useSyslog = true

			return nil
		}

		cfg.useSyslog = false

		return err
	})
}

// UseFile enable logging to a file, in a given folder, with automatic file rotation (on a daily basis).
func UseFile(filename string) error {
	return setLogger(func() error {
		return cfg.useFile(filename)
	})
}

// Buffer return content of the log buffer.
func Buffer() []byte {
	return logBuffer.Content()
}

// BufferCurrLevel return content of the log buffer for the current level.
func BufferCurrLevel() []byte {
	return logCurrLevelBuffer.Content()
}

// SetBufferCapacity define the size of the buffer
// The buffer had two part, the head (first line ever logger, never dropped) and
// the tail (oldest lines dropped when tail is full).
// Changing capacity will always drop the tail.
func SetBufferCapacity(headSizeBytes int, tailSizeBytes int) {
	logBuffer.SetCapacity(headSizeBytes, tailSizeBytes)
	logCurrLevelBuffer.SetCapacity(5000, 20000)
}

func CompressedSize() int {
	return logBuffer.CompressedSize()
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

	part := strings.SplitSeq(levels, ",")
	for p := range part {
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

// GoKitLoggerWrapper wraps a logger object and can be used wherever a go-kit compatible logger is expected.
type GoKitLoggerWrapper Logger

// Log implements the go-kit/log.Logger interface.
func (wrapper GoKitLoggerWrapper) Log(keyvals ...any) error {
	if len(keyvals)%2 == 1 {
		V(2).Printf("logger: Invalid number of arguments, received an odd number of arguments, '%v' unexpected", keyvals...)
	}

	var res strings.Builder

	for i := range len(keyvals) / 2 {
		fmt.Fprintf(&res, "%v=\"%v\"", keyvals[2*i], keyvals[2*i+1])

		if i != len(keyvals)/2-1 {
			res.WriteByte(' ')
		}
	}

	Logger(wrapper).Println(res.String())

	return nil
}
