package version

import "fmt"

//nolint:gochecknoglobals
var (
	// BuildHash is the git hash of the build. (local change ignored)
	BuildHash = "unset"

	// Version is the agent version
	Version = "0.1"
)

// UserAgent returns the User-Agent for request performed by the agent
func UserAgent() string {
	return fmt.Sprintf("Bleemeo Agent %s", Version)
}
