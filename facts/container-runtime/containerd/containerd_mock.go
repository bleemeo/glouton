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

package containerd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/facts"
	"os"
	"reflect"
	"syscall"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/tasks/v1"
	containerdTypes "github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/platforms"
	prototypes "github.com/gogo/protobuf/types"
	"github.com/google/go-cmp/cmp"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
)

var (
	// ErrMockNotImplemented is returned when a mock does not implement a method.
	ErrMockNotImplemented = errors.New("mock does not implement this method")
	// ErrWrongNamespace is returned when the namespace is mismatched.
	ErrWrongNamespace = errors.New("missmatch namespace")
	errIncorrectValue = errors.New("incorrect value")
	errNotImplemented = errors.New("not implemented")
)

// MockClient is a fake containerd client.
type MockClient struct {
	closed         bool
	Data           MockJSON
	EventChanMaker func() <-chan *events.Envelope
}

// MockJSON store all information that MockClient can provide.
type MockJSON struct {
	Namespaces []MockNamespace
	Version    containerd.Version
}

// MockNamespace contains namespaced information.
type MockNamespace struct {
	MockNamespace  string
	MockContainers []MockContainer
}

// MockContainer contains information about a container.
type MockContainer struct {
	MockInfo     ContainerOCISpec
	MockImageOCI ocispec.Descriptor
	MockTask     MockTask

	namespace string
}

// MockImage is an implementation of containerd.Image.
type MockImage struct {
	MockName   string
	MockTarget ocispec.Descriptor
}

// MockTask is an implementation of containerd.Task.
type MockTask struct {
	MockID     string
	MockPID    uint32
	MockStatus containerd.Status
	MockPids   []containerd.ProcessInfo

	namespace string
}

// DumpToJSON dump to a json all information required to build a MockClient.
// It will dump from all namespace: containers and their task + PIDs.
func DumpToJSON(ctx context.Context, address string) ([]byte, error) {
	client, err := containerd.New(address)
	if err != nil {
		return nil, err
	}

	defer client.Close()

	namespaces, err := client.NamespaceService().List(ctx)
	if err != nil {
		return nil, err
	}

	version, err := client.Version(ctx)
	if err != nil {
		return nil, err
	}

	result := MockJSON{
		Namespaces: make([]MockNamespace, len(namespaces)),
		Version:    version,
	}

	for i, ns := range namespaces {
		result.Namespaces[i].MockNamespace = ns

		if err := result.Namespaces[i].fill(ctx, client); err != nil {
			return nil, err
		}
	}

	return json.MarshalIndent(result, "", "  ")
}

func (j *MockNamespace) fill(ctx context.Context, client *containerd.Client) error {
	ctx = namespaces.WithNamespace(ctx, j.MockNamespace)

	containers, err := client.Containers(ctx)
	if err != nil {
		return err
	}

	for _, c := range containers {
		img, err := c.Image(ctx)
		if err != nil {
			return err
		}

		lbls, err := c.Labels(ctx)
		if err != nil {
			return err
		}

		spec, err := c.Spec(ctx)
		if err != nil {
			return err
		}

		mi := MockTask{}

		task, err := c.Task(ctx, nil)
		mi, err = getTaskInfo(ctx, err, task, mi)

		if err != nil {
			return err
		}

		info, err := c.Info(ctx)
		if err != nil {
			return err
		}

		// Ensure that information from Info() and other method match
		if c.ID() != info.ID {
			return fmt.Errorf("%w for ID() = %v, want %v", errIncorrectValue, c.ID(), info.ID)
		}

		if img.Name() != info.Image {
			return fmt.Errorf("%w img.Name() = %v, want %v", errIncorrectValue, img.Name(), info.Image)
		}

		if !reflect.DeepEqual(lbls, info.Labels) {
			return fmt.Errorf("%w, labels = %v, want %v", errIncorrectValue, lbls, info.Labels)
		}

		if info.Spec.TypeUrl != expectedSpecType {
			return fmt.Errorf("%w, TypeUrl = %v, want %v", errIncorrectValue, info.Spec.TypeUrl, expectedSpecType)
		}

		var infoSpec oci.Spec

		err = json.Unmarshal(info.Spec.Value, &infoSpec)
		if err != nil {
			return err
		}

		if diff := cmp.Diff(&infoSpec, spec); diff != "" {
			return fmt.Errorf("%w, spec don't match: %s", errIncorrectValue, diff)
		}

		mc := MockContainer{
			namespace: j.MockNamespace,
			MockInfo: ContainerOCISpec{
				Container: info,
				Spec:      &infoSpec,
			},
			MockImageOCI: img.Target(),
			MockTask:     mi,
		}

		j.MockContainers = append(j.MockContainers, mc)
	}

	return nil
}

func getTaskInfo(ctx context.Context, err error, task containerd.Task, mi MockTask) (MockTask, error) {
	if err == nil {
		status, err := task.Status(ctx)
		if err != nil {
			return mi, err
		}

		pids, err := task.Pids(ctx)
		if err != nil {
			return mi, err
		}

		mi.MockID = task.ID()
		mi.MockPID = task.Pid()
		mi.MockStatus = status
		mi.MockPids = pids
	}

	return mi, nil
}

// NewMockFromFile create a MockClient from JSON file. Use DumpToJSON to build such JSON.
func NewMockFromFile(filename string) (*MockClient, error) {
	result := &MockClient{}

	data, err := os.ReadFile(filename)
	if err == nil {
		err = json.Unmarshal(data, &result.Data)
		if err != nil {
			return result, err
		}
	}

	return result, err
}

// FakeContainerd return a Containerd runtime connector that use a mock client.
func FakeContainerd(client *MockClient, isContainerIgnored func(facts.Container) bool) *Containerd {
	return &Containerd{
		openConnection: func(_ context.Context, _ string) (cl containerdClient, err error) {
			return client, nil
		},
		Addresses:          []string{"unused"},
		IsContainerIgnored: isContainerIgnored,
	}
}

// Containers do Containers.
func (m *MockClient) Containers(ctx context.Context) ([]containerd.Container, error) {
	if m.closed {
		panic("already closed")
	}

	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	for _, d := range m.Data.Namespaces {
		if d.MockNamespace == namespace {
			result := make([]containerd.Container, len(d.MockContainers))

			for i, c := range d.MockContainers {
				c.namespace = d.MockNamespace

				result[i] = c
			}

			return result, nil
		}
	}

	return nil, fmt.Errorf("namespace %w", errNotFound)
}

// LoadContainer do LoadContainer.
func (m *MockClient) LoadContainer(ctx context.Context, id string) (containerd.Container, error) {
	if m.closed {
		panic("already closed")
	}

	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	for _, d := range m.Data.Namespaces {
		if d.MockNamespace == namespace {
			for _, c := range d.MockContainers {
				if c.MockInfo.ID == id {
					c.namespace = d.MockNamespace

					return c, nil
				}
			}
		}
	}

	return nil, errNotFound
}

// Version do version.
func (m *MockClient) Version(ctx context.Context) (containerd.Version, error) {
	if m.closed {
		panic("already closed")
	}

	return m.Data.Version, nil
}

// Metrics do metrics.
func (m *MockClient) Metrics(ctx context.Context, filters []string) (*tasks.MetricsResponse, error) {
	return nil, ErrMockNotImplemented
}

// Namespaces do namespaces.
func (m *MockClient) Namespaces(ctx context.Context) ([]string, error) {
	if m.closed {
		panic("already closed")
	}

	namespaces := make([]string, len(m.Data.Namespaces))

	for i, d := range m.Data.Namespaces {
		namespaces[i] = d.MockNamespace
	}

	return namespaces, nil
}

// Events do events.
func (m *MockClient) Events(ctx context.Context) (<-chan *events.Envelope, <-chan error) {
	if m.closed {
		panic("already closed")
	}

	if m.EventChanMaker != nil {
		return m.EventChanMaker(), nil
	}

	ch := make(chan error, 1)
	ch <- fmt.Errorf("ContainerTop %w", errNotImplemented)

	return nil, ch
}

// Close close.
func (m *MockClient) Close() error {
	if m.closed {
		panic("already closed")
	}

	m.closed = true

	return nil
}

// ID implement containerd.Container.
func (c MockContainer) ID() string {
	return c.MockInfo.ID
}

// Info implement containerd.Container.
func (c MockContainer) Info(ctx context.Context, opts ...containerd.InfoOpts) (containers.Container, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return containers.Container{}, err
	}

	if ns != c.namespace {
		return containers.Container{}, fmt.Errorf("%w: %s != %s", ErrWrongNamespace, ns, c.namespace)
	}

	buffer, err := json.Marshal(c.MockInfo.Spec)
	if err != nil {
		return containers.Container{}, err
	}

	info := c.MockInfo.Container
	info.Spec = &prototypes.Any{
		TypeUrl: expectedSpecType,
		Value:   buffer,
	}

	return info, nil
}

// Delete implement containerd.Container.
func (c MockContainer) Delete(context.Context, ...containerd.DeleteOpts) error {
	return ErrMockNotImplemented
}

// NewTask implement containerd.Container.
func (c MockContainer) NewTask(context.Context, cio.Creator, ...containerd.NewTaskOpts) (containerd.Task, error) {
	return nil, ErrMockNotImplemented
}

// Spec implement containerd.Container.
func (c MockContainer) Spec(ctx context.Context) (*oci.Spec, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	if ns != c.namespace {
		return nil, fmt.Errorf("%w: %s != %s", ErrWrongNamespace, ns, c.namespace)
	}

	return c.MockInfo.Spec, nil
}

// Task implement containerd.Container.
func (c MockContainer) Task(ctx context.Context, io cio.Attach) (containerd.Task, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	if ns != c.namespace {
		return nil, fmt.Errorf("%w: %s != %s", ErrWrongNamespace, ns, c.namespace)
	}

	c.MockTask.namespace = c.namespace

	if c.MockTask.MockID == "" {
		return nil, errNotFound
	}

	return c.MockTask, nil
}

// Image implement containerd.Container.
func (c MockContainer) Image(ctx context.Context) (containerd.Image, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	if ns != c.namespace {
		return nil, fmt.Errorf("%w: %s != %s", ErrWrongNamespace, ns, c.namespace)
	}

	return MockImage{MockName: c.MockInfo.Image, MockTarget: c.MockImageOCI}, nil
}

// Labels implement containerd.Container.
func (c MockContainer) Labels(ctx context.Context) (map[string]string, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	if ns != c.namespace {
		return nil, fmt.Errorf("%w: %s != %s", ErrWrongNamespace, ns, c.namespace)
	}

	return c.MockInfo.Labels, nil
}

// SetLabels implement containerd.Container.
func (c MockContainer) SetLabels(context.Context, map[string]string) (map[string]string, error) {
	return nil, ErrMockNotImplemented
}

// Extensions implement containerd.Container.
func (c MockContainer) Extensions(context.Context) (map[string]prototypes.Any, error) {
	return nil, ErrMockNotImplemented
}

// Update implement containerd.Container.
func (c MockContainer) Update(context.Context, ...containerd.UpdateContainerOpts) error {
	return ErrMockNotImplemented
}

// Checkpoint implement containerd.Container.
func (c MockContainer) Checkpoint(context.Context, string, ...containerd.CheckpointOpts) (containerd.Image, error) {
	return nil, ErrMockNotImplemented
}

// Name implement containerd.Image.
func (i MockImage) Name() string {
	return i.MockName
}

// Target implement containerd.Image.
func (i MockImage) Target() ocispec.Descriptor {
	return i.MockTarget
}

// Labels implement containerd.Image.
func (i MockImage) Labels() map[string]string {
	panic(ErrMockNotImplemented)
}

// Unpack implement containerd.Image.
func (i MockImage) Unpack(context.Context, string, ...containerd.UnpackOpt) error {
	return ErrMockNotImplemented
}

// RootFS implement containerd.Image.
func (i MockImage) RootFS(ctx context.Context) ([]digest.Digest, error) {
	return nil, ErrMockNotImplemented
}

// Size implement containerd.Image.
func (i MockImage) Size(ctx context.Context) (int64, error) {
	return 0, ErrMockNotImplemented
}

// Usage implement containerd.Image.
func (i MockImage) Usage(context.Context, ...containerd.UsageOpt) (int64, error) {
	return 0, ErrMockNotImplemented
}

// Config implement containerd.Image.
func (i MockImage) Config(ctx context.Context) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, ErrMockNotImplemented
}

// IsUnpacked implement containerd.Image.
func (i MockImage) IsUnpacked(context.Context, string) (bool, error) {
	return false, ErrMockNotImplemented
}

// ContentStore implement containerd.Image.
func (i MockImage) ContentStore() content.Store {
	return nil
}

// Metadata implement containerd.Image.
func (i MockImage) Metadata() images.Image {
	panic(ErrMockNotImplemented)
}

// Platform implement containerd.Image.
func (i MockImage) Platform() platforms.MatchComparer {
	panic(ErrMockNotImplemented)
}

// ID implements containerd.Task.
func (t MockTask) ID() string {
	return t.MockID
}

// Pid implements containerd.Task.
func (t MockTask) Pid() uint32 {
	return t.MockPID
}

// Start implements containerd.Task.
func (t MockTask) Start(context.Context) error {
	return ErrMockNotImplemented
}

// Delete implements containerd.Task.
func (t MockTask) Delete(context.Context, ...containerd.ProcessDeleteOpts) (*containerd.ExitStatus, error) {
	return nil, ErrMockNotImplemented
}

// Kill implements containerd.Task.
func (t MockTask) Kill(context.Context, syscall.Signal, ...containerd.KillOpts) error {
	return ErrMockNotImplemented
}

// Wait implements containerd.Task.
func (t MockTask) Wait(context.Context) (<-chan containerd.ExitStatus, error) {
	return nil, ErrMockNotImplemented
}

// CloseIO implements containerd.Task.
func (t MockTask) CloseIO(context.Context, ...containerd.IOCloserOpts) error {
	return ErrMockNotImplemented
}

// Resize implements containerd.Task.
func (t MockTask) Resize(ctx context.Context, w, h uint32) error {
	return ErrMockNotImplemented
}

// IO implements containerd.Task.
func (t MockTask) IO() cio.IO {
	return nil
}

// Status implements containerd.Task.
func (t MockTask) Status(ctx context.Context) (containerd.Status, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return containerd.Status{}, err
	}

	if ns != t.namespace {
		return containerd.Status{}, fmt.Errorf("%w: %s != %s", ErrWrongNamespace, ns, t.namespace)
	}

	return t.MockStatus, nil
}

// Pause implements containerd.Task.
func (t MockTask) Pause(context.Context) error {
	return ErrMockNotImplemented
}

// Resume implements containerd.Task.
func (t MockTask) Resume(context.Context) error {
	return ErrMockNotImplemented
}

// Exec implements containerd.Task.
func (t MockTask) Exec(context.Context, string, *specs.Process, cio.Creator) (containerd.Process, error) {
	return nil, ErrMockNotImplemented
}

// Pids implements containerd.Task.
func (t MockTask) Pids(ctx context.Context) ([]containerd.ProcessInfo, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	if ns != t.namespace {
		return nil, fmt.Errorf("%w: %s != %s", ErrWrongNamespace, ns, t.namespace)
	}

	return t.MockPids, nil
}

// Checkpoint implements containerd.Task.
func (t MockTask) Checkpoint(context.Context, ...containerd.CheckpointTaskOpts) (containerd.Image, error) {
	return nil, ErrMockNotImplemented
}

// Update implements containerd.Task.
func (t MockTask) Update(context.Context, ...containerd.UpdateTaskOpts) error {
	return ErrMockNotImplemented
}

// LoadProcess implements containerd.Task.
func (t MockTask) LoadProcess(context.Context, string, cio.Attach) (containerd.Process, error) {
	return nil, ErrMockNotImplemented
}

// Metrics implements containerd.Task.
func (t MockTask) Metrics(context.Context) (*containerdTypes.Metric, error) {
	return nil, ErrMockNotImplemented
}

// Spec implements containerd.Task.
func (t MockTask) Spec(context.Context) (*oci.Spec, error) {
	return nil, ErrMockNotImplemented
}
