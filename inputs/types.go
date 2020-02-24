package inputs

import (
	"glouton/types"
	"time"
)

// AnnotationAccumulator is a similar to an telegraf.Accumulator but allow to send metric with annocations
type AnnotationAccumulator interface {
	AddFieldsWithAnnotations(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time)
	AddError(err error)
}
