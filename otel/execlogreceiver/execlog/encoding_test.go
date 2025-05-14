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
	"testing"

	// The fileconsumer package depends on the textutils one, so it makes it available for linkname.
	_ "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/fileconsumer"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
)

func TestLookupEncoding(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		encodingName string
		expectedEnc  encoding.Encoding
	}{
		{
			encodingName: "utf8",
			expectedEnc:  unicode.UTF8,
		},
		{
			encodingName: "utf16",
			expectedEnc:  unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM),
		},
	}

	for _, test := range testCases {
		t.Run(test.encodingName, func(t *testing.T) {
			t.Parallel()

			enc, err := lookupEncoding(test.encodingName)
			if err != nil {
				t.Fatal("Failed to lookup encoding:", err)
			}

			if enc != test.expectedEnc {
				t.Fatalf("Unexpected encoding: want %v, got %v", test.expectedEnc, enc)
			}
		})
	}
}
