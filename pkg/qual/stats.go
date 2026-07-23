package qual

import (
	"math"
	"sort"
)

// StatSummary is a deterministic numeric rollup. Quantile uses linear
// interpolation between closest ranks (the "linear" / type-7 method: the
// quantile q over n sorted values is taken at index q*(n-1), interpolating
// between neighbors). Variance is the unbiased sample variance (n-1 divisor),
// 0 for a single observation.
type StatSummary struct {
	Count    int
	Mean     float64
	Median   float64
	Quantile float64
	Min      float64
	Max      float64
	Variance float64
}

// Summarize computes a StatSummary over values at quantile q in [0,1]. The
// input is copied, never mutated. Empty input and non-finite values are
// rejected: statistics over unknowns would silently launder missing data.
func Summarize(values []float64, q float64) (StatSummary, error) {
	if len(values) == 0 {
		return StatSummary{}, &ValidationError{Field: "Summarize.values", Reason: "must not be empty"}
	}
	if q < 0 || q > 1 || math.IsNaN(q) {
		return StatSummary{}, &ValidationError{Field: "Summarize.q", Reason: "must be within [0,1]"}
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	var sum float64
	for _, v := range sorted {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return StatSummary{}, &ValidationError{Field: "Summarize.values", Reason: "non-finite value"}
		}
		sum += v
	}
	sort.Float64s(sorted)
	n := len(sorted)
	mean := sum / float64(n)
	variance := 0.0
	if n > 1 {
		var ss float64
		for _, v := range sorted {
			d := v - mean
			ss += d * d
		}
		variance = ss / float64(n-1)
	}
	return StatSummary{
		Count:    n,
		Mean:     mean,
		Median:   interpolate(sorted, 0.5),
		Quantile: interpolate(sorted, q),
		Min:      sorted[0],
		Max:      sorted[n-1],
		Variance: variance,
	}, nil
}

func interpolate(sorted []float64, q float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
