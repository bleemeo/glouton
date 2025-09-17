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

package obfuscation

import "strings"

func isSensitive(key string) bool {
	key = strings.ToLower(key)

	// sensitiveKeywords keys are potentially sensitive keywords,
	// unless they're part of the slice they map to.
	// It can be seen as a map [sensitive] -> [in fact, not that much].
	sensitiveKeywords := map[string][]string{
		"key":      {"pubkey", "publickey", "public-key", "public_key", "keyfile", "key-file", "key_file"},
		"secret":   {},
		"password": {},
		"passwd":   {},
		"token":    {},
	}

	for potentiallySensitive, unless := range sensitiveKeywords {
		keyWithoutNonSensitives := key
		for _, nonSensitive := range unless {
			keyWithoutNonSensitives = strings.ReplaceAll(keyWithoutNonSensitives, nonSensitive, "")
		}

		if strings.Contains(keyWithoutNonSensitives, potentiallySensitive) {
			return true
		}
	}

	return false
}
