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
