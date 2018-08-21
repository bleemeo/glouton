package types

// iota
const (
	// NoUnit unit
	NoUnit int = iota

	// Bytes unit (B)
	Bytes

	// Percent unit (%)
	Percent

	// BytesPerSecond unit (B/s)
	BytesPerSecond

	// BitsPerSecond unit (b/s)
	BitsPerSecond

	// MillissecondsPerSecond unit (ms/s)
	MillissecondsPerSecond

	// ReadsPerSecond unit (reads/s)
	ReadsPerSecond

	// WritesPerSecond unit (writes/s)
	WritesPerSecond

	// PacketsPerSecond unit (packets/s)
	PacketsPerSecond

	// ErrorsPerSecond unit (errors/s)
	ErrorsPerSecond
)

const (
	// StatusTestOkWithMetric corresponds to a well recevived metric
	StatusTestOkWithMetric int = iota

	// StatusTestOkWithoutMetric coresponds to a etablished communication
	// with the collector but a non received metric
	StatusTestOkWithoutMetric

	// StatusTestKO : impossible to established the communication with the collector
	StatusTestKO
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

type Collector interface {
	Gather() []MetricPoint
	Test() int
}
