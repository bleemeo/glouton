package facts

// ConfidenceLevel describe how confident a provided is about information returned.
type ConfidenceLevel int

// List of possible confidence level.
const (
	ConfidenceHigh ConfidenceLevel = iota + 1
	ConfidenceMedium
	ConfidenceLow
)
