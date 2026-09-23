// Copyright 2015-2026 Bleemeo
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

//go:build linux

package facts

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// statLockInode returns the inode of a lock file, which is how decodeProcLocks keys the
// locks held on the system.
//
// It only needs to traverse the parent directory, not to open the file: /var/lib/dpkg
// is 0755 while the lock files are 0640 root:root, so this works as the "glouton" user.
func statLockInode(path string) (uint64, error) {
	var stat unix.Stat_t

	if err := unix.Stat(path, &stat); err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}

	return stat.Ino, nil
}
