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

package types

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/types"
)

const (
	DockerRuntime     = "docker"
	ContainerDRuntime = "containerd"
)

// RuntimeInterface is the interface that container runtime provide.
type RuntimeInterface interface {
	CachedContainer(containerID string) (c facts.Container, found bool)
	ContainerLastKill(containerID string) time.Time
	Exec(ctx context.Context, containerID string, cmd []string) ([]byte, error)
	Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error)
	Events() <-chan facts.ContainerEvent
	IsRuntimeRunning(ctx context.Context) bool
	ProcessWithCache() facts.ContainerRuntimeProcessQuerier
	Run(ctx context.Context) error
	RuntimeFact(ctx context.Context, currentFact map[string]string) map[string]string
	LastUpdate() time.Time
	Metrics(ctx context.Context, now time.Time) ([]types.MetricPoint, error)
	MetricsMinute(ctx context.Context, now time.Time) ([]types.MetricPoint, error)
	IsContainerNameRecentlyDeleted(name string) bool
	DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error
	ContainerExists(containerID string) bool
	ImageTags(ctx context.Context, imageID, imageName string) ([]string, error)
}

// ExpandRuntimeAddresses adds the host root to the socket addresses if PrefixHostRoot is true.
func ExpandRuntimeAddresses(runtime config.ContainerRuntimeAddresses, hostRoot string) []string {
	if !runtime.PrefixHostRoot {
		return runtime.Addresses
	}

	if hostRoot == "" || hostRoot == "/" {
		return runtime.Addresses
	}

	addresses := make([]string, 0, len(runtime.Addresses)*2)

	for _, path := range runtime.Addresses {
		addresses = append(addresses, path)

		if path == "" {
			// This is a special value that means "use default of the runtime".
			// Prefixing with the hostRoot don't make sense.
			continue
		}

		switch {
		case strings.HasPrefix(path, "unix://"):
			path = strings.TrimPrefix(path, "unix://")
			addresses = append(addresses, "unix://"+filepath.Join(hostRoot, path))
		case strings.HasPrefix(path, "/"): // ignore non-absolute path. This will also ignore URL (like http://localhost:3000)
			addresses = append(addresses, filepath.Join(hostRoot, path))
		}
	}

	return addresses
}
