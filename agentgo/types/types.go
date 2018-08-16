package types

const (
	// NoUnit unit
	NoUnit int = iota

	// Bytes unit (B)
	Bytes int = iota

	// Percent unit (%)
	Percent int = iota

	// BytesPerSecond unit (B/s)
	BytesPerSecond int = iota

	// BitsPerSecond unit (b/s)
	BitsPerSecond int = iota

	// MillissecondsPerSecond unit (ms/s)
	MillissecondsPerSecond int = iota

	// ReadsPerSecond unit (reads/s)
	ReadsPerSecond int = iota

	// WritesPerSecond unit (writes/s)
	WritesPerSecond int = iota

	// PacketsPerSecond unit (packets/s)
	PacketsPerSecond int = iota

	// ErrorsPerSecond unit (errors/s)
	ErrorsPerSecond int = iota
)

const (
	// StatusTestOkWithMetric corresponds to a well recevived metric
	StatusTestOkWithMetric int = iota

	// StatusTestOkWithoutMetric coresponds to a etablished communication
	// with the collector but a non received metric
	StatusTestOkWithoutMetric int = iota

	// StatusTestKO : impossible to established the communication with the collector
	StatusTestKO int = iota
)

// Metric contains metric information
type Metric struct {
	// Name of the metric
	Name string

	// Tag list of the metric
	Tag map[string]string

	// Chart name of the metric
	Chart string

	// Unit of the metric
	Unit int
}

// MetricPoint contains a metric and its value.
type MetricPoint struct {
	// Metric of the metric point
	Metric *Metric

	// Value associated with the metric
	Value float64
}
