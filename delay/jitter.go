package delay

import (
	"math"
	"math/rand"
	"time"
)

func Min(durations ...time.Duration) time.Duration {
	var result time.Duration

	if len(durations) > 0 {
		result = durations[0]
	}

	for _, v := range durations {
		if result > v {
			result = v
		}
	}

	return result
}

// Exponential return an exponential delay. N should be the number of successive iteration/errors (counting from 1).
// The value retuned is "base * powerFactor ^ n". The value is capped at max.
// powerFactor should be > 1 (or the delay will be smaller and smaller).
// Exponential works at seconds resolution. Base should be > of few seconds.
func Exponential(base time.Duration, powerFactor float64, n int, max time.Duration) time.Duration {
	n--
	if n < 0 {
		n = 0
	}

	baseSeconds := base.Seconds()
	seconds := baseSeconds * math.Pow(powerFactor, float64(n))

	return Min(max, time.Duration(seconds)*time.Second)
}

// JitterDelay return a number between value * [1-factor; 1+factor[
// If the valueSecond exceed max, max is used instead of valueSecond.
// factor should be less than 1.
func JitterDelay(baseDelay time.Duration, factor float64) time.Duration {
	valueSecond := baseDelay.Seconds()
	scale := rand.Float64() * 2 * factor //nolint:gosec
	scale += 1 - factor

	result := int(valueSecond * scale)

	return time.Duration(result) * time.Second
}
