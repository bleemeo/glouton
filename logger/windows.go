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

// +build windows

package logger

import (
	"fmt"
	"io"
	"os"
	"time"
)

type rotatingLogs struct {
	location     string
	filename     string
	fd           io.WriteCloser
	rotatePeriod time.Duration
	lastRotate   time.Time
}

func (r *rotatingLogs) moveOldFile() error {
	// rotate the file and sets its name in function of its "end time"
	// Note: we're not using a classic format like RC3339 because windows doesn't like some characters, for instance ':'
	filename := r.location + r.filename + ".log"
	oldFilename := r.location + r.filename + "." + r.lastRotate.Format("2006-01-02T15-04") + ".log"

	oldFile, err := os.OpenFile(oldFilename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}

	defer oldFile.Close()

	curFile, err := os.OpenFile(filename, os.O_RDONLY, 0)
	if err != nil {
		return err
	}

	defer curFile.Close()

	var n int

	// copy the file at the end of the previous log file
	buf := make([]byte, 1<<16)

	for {
		n, err = curFile.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}

			return err
		}

		cur := 0
		for cur < n {
			nOut, err := oldFile.Write(buf[cur:n])
			if err != nil {
				return err
			}

			cur += nOut
		}
	}

	return nil
}

func (r *rotatingLogs) open() error {
	if r.fd != nil {
		return nil
	}

	r.lastRotate = time.Now().Truncate(r.rotatePeriod)

	// this is safe, as calls to the logger are wrapped in a mutex, so no concurrent calls should be made,
	// and no one wil attempt to write to this logger while "closed"
	filename := r.location + r.filename + ".log"

	// if the destination already exists, move the current content to another file (rotate it)
	_, err := os.Stat(filename)
	if err == nil {
		err = r.moveOldFile()
		if err != nil {
			return err
		}
	}

	fd, err := os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}

	r.fd = fd

	return err
}

// rotatingLogs implements io.WriteCloser.
func (r *rotatingLogs) Write(p []byte) (n int, err error) {
	if r.fd == nil || time.Since(r.lastRotate) >= r.rotatePeriod {
		if r.fd != nil {
			_ = r.fd.Close()
			r.fd = nil
		}

		// time to rotate !
		err = r.open()
		if err != nil {
			return 0, err
		}
	}

	return fmt.Fprintf(r.fd, "[%s] %s", time.Now().Format(time.RFC3339), p)
}

func (cfg *config) enableSyslog() error {
	writer := &rotatingLogs{
		location:     `C:\ProgramData\glouton\logs\`,
		filename:     "glouton",
		rotatePeriod: time.Hour,
	}
	writer.lastRotate = time.Now().Truncate(writer.rotatePeriod)

	err := writer.open()
	if err != nil {
		return err
	}

	cfg.writer = writer
	cfg.teeWriter = io.MultiWriter(logBuffer, cfg.writer)

	return nil
}
