package common

import (
	"math/rand"
	"time"
)

// JitterDelay return a number between value * [1-factor; 1+factor[
// If the valueSecond exceed max, max is used instead of valueSecond.
// factor should be less than 1
func JitterDelay(valueSecond float64, factor float64, maxSecond float64) time.Duration {
	scale := rand.Float64() * 2 * factor
	scale += 1 - factor
	if valueSecond > maxSecond {
		valueSecond = maxSecond
	}
	result := int(valueSecond * scale)
	return time.Duration(result) * time.Second
}
