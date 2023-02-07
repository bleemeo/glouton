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

//go:build windows

package fluentbit

import (
	"errors"
	"glouton/config"
	crTypes "glouton/facts/container-runtime/types"
)

var errNotSupported = errors.New("log inputs are not supported on windows")

// New returns an initialized Fluent Bit manager and config warnings.
func New(cfg config.Log, reg registerer, runtime crTypes.RuntimeInterface) (*Manager, []error) {
	_ = validateConfig(cfg) // Fix unused function warning.

	return nil, []error{errNotSupported}
}
