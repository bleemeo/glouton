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

package execlog

import (
	"errors"
	"fmt"
	"strings"
	_ "unsafe" //nolint: revive,nolintlint

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
)

//go:linkname encodingOverrides github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/textutils.encodingOverrides
var encodingOverrides map[string]encoding.Encoding //nolint:gochecknoglobals

var (
	errUnsupportedEncoding = errors.New("unsupported encoding")
	errNoCharMapDefined    = errors.New("no char map defined")
)

// lookupEncoding is a copy of github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/textutils.LookupEncoding,
// which is in an internal package, thus inaccessible.
func lookupEncoding(enc string) (encoding.Encoding, error) {
	if e, ok := encodingOverrides[strings.ToLower(enc)]; ok {
		return e, nil
	}

	e, err := ianaindex.IANA.Encoding(enc)
	if err != nil {
		return nil, fmt.Errorf("%w %q", errUnsupportedEncoding, enc)
	}

	if e == nil {
		return nil, fmt.Errorf("%w for encoding %q", errNoCharMapDefined, enc)
	}

	return e, nil
}
