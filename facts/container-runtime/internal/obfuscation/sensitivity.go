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
	SensitivityCheck:
		if sensitiveIdx := strings.Index(key, potentiallySensitive); sensitiveIdx >= 0 {
			for _, nonSensitive := range unless {
				nonSensitiveIdx := strings.Index(key, nonSensitive)
				if nonSensitiveIdx >= 0 {
					// Ensuring that the non-sensitive part is actually the same part as the sensitive one,
					// not to consider the key as non-sensitive when it contains
					// both and distinct sensitive and non-sensitives keywords.
					posDelta := nonSensitiveIdx - sensitiveIdx
					if nonSensitiveIdx <= sensitiveIdx && posDelta < len(nonSensitive) {
						// In fact, this part isn't that much sensitive.
						// We remove it not to handle it again.
						key = key[:nonSensitiveIdx] + key[nonSensitiveIdx+len(nonSensitive):]

						// Is the key still potentially sensitive ?
						goto SensitivityCheck
					}
				}
			}

			return true
		}
	}

	return false
}
