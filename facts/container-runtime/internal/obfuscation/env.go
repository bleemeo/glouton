package obfuscation

import "strings"

// ObfuscateEnv replaces in place the values of the keys that might be sensitive with '*****'.
func ObfuscateEnv(env []string) {
	for i, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key, value := parts[0], parts[1]
		if isSensitive(key) && value != "" {
			env[i] = key + "=*****"
		}
	}
}
