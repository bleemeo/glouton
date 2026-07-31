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

package logsource

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
)

const (
	attrContainerID        = "container.id"
	attrContainerImageName = "container.image.name"
	attrContainerImageTags = "container.image.tags"
	attrContainerName      = "container.name"
	attrContainerRuntime   = "container.runtime"
	attrContainerNamespace = "k8s.namespace.name"
	attrContainerPod       = "k8s.pod.name"
)

// ContainerAttributes carries a container's identity, stamped as stanza
// attributes on every log record its tail produces (see AsMap), so shipping
// and metrics both see which container/pod/image a record came from.
type ContainerAttributes struct {
	Runtime   string
	ID        string
	Name      string
	ImageName string
	ImageTags string
	Namespace string `json:",omitempty"`
	Pod       string `json:",omitempty"`
}

// BuildContainerAttributes resolves ctr's attributes, including its image
// tags (which may require a runtime/API call, hence ctx). A failure to
// resolve image tags is logged, not fatal: the rest of the attributes are
// still returned.
func BuildContainerAttributes(ctx context.Context, ctr facts.Container) ContainerAttributes {
	attributes := ContainerAttributes{
		Runtime:   ctr.RuntimeName(),
		ID:        ctr.ID(),
		Name:      ctr.ContainerName(),
		ImageName: strings.SplitN(ctr.ImageName(), ":", 2)[0],
	}

	imageTags, err := ctr.ImageTags(ctx)
	if err != nil {
		logger.V(1).Printf("logsource: can't get tags for image %q (%s): %v", ctr.ImageName(), ctr.ImageID(), err)
	} else if imageTagsJSON, err := json.Marshal(imageTags); err != nil {
		logger.V(1).Printf("logsource: can't marshal tags for image %q (%s): %v", ctr.ImageName(), ctr.ImageID(), err)
	} else {
		attributes.ImageTags = string(imageTagsJSON)
	}

	namespace := ctr.PodNamespace()
	pod := ctr.PodName()

	if namespace != "" && pod != "" {
		attributes.Namespace = namespace
		attributes.Pod = pod
	}

	return attributes
}

// AsMap turns attrs into the stanza attribute config SetupLogReceiverFactories
// stamps on every log record produced by this container's tail.
func (attrs ContainerAttributes) AsMap() map[string]helper.ExprStringConfig {
	out := map[string]helper.ExprStringConfig{
		attrContainerID:        helper.ExprStringConfig(attrs.ID),
		attrContainerImageName: helper.ExprStringConfig(attrs.ImageName),
		attrContainerName:      helper.ExprStringConfig(attrs.Name),
		attrContainerRuntime:   helper.ExprStringConfig(attrs.Runtime),
	}

	if attrs.ImageTags != "" {
		out[attrContainerImageTags] = helper.ExprStringConfig(attrs.ImageTags)
	}

	if attrs.Namespace != "" {
		out[attrContainerNamespace] = helper.ExprStringConfig(attrs.Namespace)
	}

	if attrs.Pod != "" {
		out[attrContainerPod] = helper.ExprStringConfig(attrs.Pod)
	}

	return out
}
