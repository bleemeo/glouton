package obfuscation

import (
	"strings"

	"github.com/bleemeo/glouton/config"
)

// ObfuscateEnv replaces in place the values of the keys that might be sensible with '*****'.
func ObfuscateEnv(env []string) {
	for i, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key, value := parts[0], parts[1]
		if config.IsSecret(strings.ToLower(key)) && value != "" {
			env[i] = key + "=*****"
		}
	}
}
