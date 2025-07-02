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

package hostrootsymlink

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeFileInfo struct {
	name      string
	IsSymlink bool
}

func (fi fakeFileInfo) Name() string {
	return fi.name
}

func (fi fakeFileInfo) Size() int64 {
	return 0
}

func (fi fakeFileInfo) Mode() fs.FileMode {
	if fi.IsSymlink {
		return fs.ModeSymlink
	}

	return 0
}

func (fi fakeFileInfo) ModTime() time.Time {
	return time.Now()
}

func (fi fakeFileInfo) IsDir() bool {
	return false
}

func (fi fakeFileInfo) Sys() any {
	return 0
}

func Test_evalSymlinks(t *testing.T) {
	t.Parallel()

	const hostroot = "/hostroot"

	tests := []struct {
		name      string
		links     map[string]string // key is the absolute path without hostroot
		statError map[string]bool
		hostroot  string
		input     string
		want      string
	}{
		{
			name:     "no-symlink",
			hostroot: hostroot,
			input:    "/var/log/syslog",
			want:     "/var/log/syslog",
		},
		{
			name:     "relative-symlink",
			hostroot: hostroot,
			links: map[string]string{
				"/var/log/syslog": "message",
			},
			input: "/var/log/syslog",
			want:  "/var/log/message",
		},
		{
			name:     "no-hostroot-relative-symlink",
			hostroot: "/",
			links: map[string]string{
				"/var/log/syslog": "message",
			},
			input: "/var/log/syslog",
			want:  "/var/log/message",
		},
		{
			name:     "var-containers",
			hostroot: hostroot,
			links: map[string]string{
				"/var/log/containers/etcd-minikube_kube-system_etcd-c8c60ab8d4d018d931ef268134e65b991609dc13c2e9b81f6d63eb125ffceaa0.log": "/var/log/pods/kube-system_etcd-minikube_3924ef3609584191d8d09190210d2d78/etcd/0.log",
			},
			input: "/var/log/containers/etcd-minikube_kube-system_etcd-c8c60ab8d4d018d931ef268134e65b991609dc13c2e9b81f6d63eb125ffceaa0.log",
			want:  "/var/log/pods/kube-system_etcd-minikube_3924ef3609584191d8d09190210d2d78/etcd/0.log",
		},
		{
			name:     "relative-stay-in-hostroot",
			hostroot: hostroot,
			links: map[string]string{
				"/var/log/syslog": "../../../../../../../../tmp/syslog",
			},
			input: "/var/log/syslog",
			want:  "/tmp/syslog",
		},
		{
			name:     "two-links",
			hostroot: hostroot,
			links: map[string]string{
				"/var/log/syslog": "../../../../../../../../tmp/link2",
				"/tmp/link2":      "/home/log/file.log",
			},
			input: "/var/log/syslog",
			want:  "/home/log/file.log",
		},
		{
			name:     "link-in-path-component",
			hostroot: hostroot,
			links: map[string]string{
				"/var":                         "/opt/realvar",
				"/opt/realvar/log":             "./logs-dir",
				"/opt/realvar/logs-dir/syslog": "../sys.log",
			},
			input: "/var/log/syslog",
			want:  "/opt/realvar/sys.log",
		},
		{
			name:     "infinite-loop",
			hostroot: hostroot,
			links: map[string]string{
				"/var/log/syslog":   "/var/log/message1",
				"/var/log/message1": "/var/log/message2",
				"/var/log/message2": "/var/log/message1",
			},
			input: "/var/log/syslog",
			// Note: it could be message1 or message2. It depends on maxDepth. The test case don't care which one is used
			// only that: 1) is finish and don't loop forever 2) it return whatever path it has when maxDepth is reached
			want: "/var/log/message2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hostrootLinks := make(map[string]string, len(tt.links))

			for absPath, target := range tt.links {
				if !filepath.IsAbs(absPath) {
					t.Fatalf("Key of links must be absolute path")
				}

				hostrootLinks[filepath.Join(tt.hostroot, absPath)] = target
			}

			hostrootStatError := make(map[string]bool, len(tt.statError))

			for absPath, target := range tt.statError {
				if !filepath.IsAbs(absPath) {
					t.Fatalf("Key of statError must be absolute path")
				}

				hostrootStatError[filepath.Join(tt.hostroot, absPath)] = target
			}

			statImpl := func(path string) (os.FileInfo, error) {
				if hostrootStatError[path] {
					return nil, os.ErrNotExist
				}

				_, found := hostrootLinks[path]

				return fakeFileInfo{name: path, IsSymlink: found}, nil
			}

			readLinkImpl := func(path string) (string, error) {
				target, found := hostrootLinks[path]
				if !found {
					return "", &fs.PathError{Op: "readlink", Path: path, Err: fs.ErrInvalid}
				}

				return target, nil
			}

			got := evalSymlinks(tt.hostroot, tt.input, statImpl, readLinkImpl)
			if got != tt.want {
				t.Errorf("evalSymlinks() = %v, want %v", got, tt.want)
			}
		})
	}
}
