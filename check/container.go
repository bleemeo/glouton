// Copyright 2015-2022 Bleemeo
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

package check

import (
	"context"
	"glouton/types"
)

func NewContainerStopped(
	address string,
	tcpAddresses []string,
	persistenConnection bool,
	labels map[string]string,
	annotations types.MetricAnnotations,
) *ContainerCheck {
	newCheck := &ContainerCheck{}

	newCheck.baseCheck = newBase(address, tcpAddresses, persistenConnection, newCheck.containerStoppedCheck, labels, annotations)

	return newCheck
}

type ContainerCheck struct {
	*baseCheck
}

func (c ContainerCheck) containerStoppedCheck(context.Context) types.StatusDescription {
	return types.StatusDescription{
		CurrentStatus:     types.StatusCritical,
		StatusDescription: "Container is stopped.",
	}
}
