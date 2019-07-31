package logger

import (
	"fmt"
	"io"
	"log"
	"log/syslog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Logger allow to print message
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

// Printf behave like fmt.Printf
func (l Logger) Printf(fmtArg string, a ...interface{}) {
	if l {
		printf(fmtArg, a...)
	}
}

func printf(fmtArg string, a ...interface{}) {
	cfg.l.Lock()
	defer cfg.l.Unlock()
	if !cfg.useSyslog {
		_, _ = fmt.Fprintf(cfg.writer, "%s ", time.Now().Format("2006/01/02 15:04:05"))
	}
	_, _ = fmt.Fprintf(cfg.writer, fmtArg+"\n", a...)
}

// Printf behave like fmt.Printf
func Printf(fmt string, a ...interface{}) {
	printf(fmt, a...)
}

type config struct {
	l         sync.Mutex
	level     int
	pkgLevels map[string]int
	useSyslog bool

	writer io.Writer
}

//nolint:gochecknoglobals
var cfg = config{writer: os.Stderr}

// UseSyslog enable or disable logging to syslog. If syslog is not used, message
// are sent to StdErr
func UseSyslog(useSyslog bool) {
	cfg.l.Lock()
	defer cfg.l.Unlock()
	cfg.useSyslog = useSyslog
	if closer, ok := cfg.writer.(io.WriteCloser); ok && cfg.writer != os.Stderr {
		closer.Close()
	}
	cfg.writer = nil
	if useSyslog {
		var err error
		cfg.writer, err = syslog.New(syslog.LOG_INFO|syslog.LOG_DAEMON, "")
		if err != nil {
			cfg.writer = os.Stderr
		}
	} else {
		cfg.writer = os.Stderr
	}
	log.SetOutput(cfg.writer)
}

// SetLevel configure the log level
func SetLevel(level int) {
	cfg.l.Lock()
	defer cfg.l.Unlock()
	cfg.level = level
}

// SetPkgLevels configure the log level per package.
// The format is "package=level,package2=level2"
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
