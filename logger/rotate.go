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
	"os"
	"path/filepath"
	"strings"
	"time"
)

const rotatePeriod time.Duration = 24 * time.Hour

// rotatingLogs implements io.WriteCloser.
type rotatingLogs struct {
	location   string
	basename   string
	extension  string
	fd         io.WriteCloser
	lastPeriod time.Time
}

func (r *rotatingLogs) open() error {
	if r.fd != nil {
		return nil
	}

	// this is safe, as calls to the logger are wrapped in a mutex, so no concurrent calls should be made,
	// and no one wil attempt to write to this logger while "closed"
	filename := filepath.Join(r.location, r.basename+r.extension)

	r.lastPeriod = time.Now().Truncate(rotatePeriod)

	// if the destination already exists, and its content is older than the rotation period,
	// move the current content to another file (rotate it)
	fileInfo, err := os.Stat(filename)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		curMtimePeriod := fileInfo.ModTime().Truncate(rotatePeriod)
		if curMtimePeriod != time.Now().Truncate(rotatePeriod) {
			// rotate the file and sets its name in function of its "end time"
			// Note: we're not using a classic time formatting like RC3339 because windows doesn't like some characters, for instance ':'
			oldFilename := filepath.Join(r.location, fmt.Sprintf("%s.%s%s", r.basename, r.lastPeriod.Format("2006-01-02"), r.extension))

			if err := os.Rename(filename, oldFilename); err != nil {
				return err
			}
		} else {
			// we want to rotate the file as soon as its last modification date exits the current "period"
			r.lastPeriod = curMtimePeriod
		}
	}

	fd, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o755) //nolint:gosec
	if err != nil {
		return err
	}

	r.fd = fd

	return err
}

func (r *rotatingLogs) Write(p []byte) (n int, err error) {
	if r.fd == nil || time.Now().Truncate(rotatePeriod) != r.lastPeriod {
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

	return r.fd.Write(p)
}

func (cfg *config) useFile(filename string) error {
	ext := filepath.Ext(filename)
	basename := strings.TrimRight(filepath.Base(filename), ext)

	writer := &rotatingLogs{
		location:  filepath.Dir(filename),
		basename:  basename,
		extension: ext,
	}

	cfg.writer = writer

	return writer.open()
}
