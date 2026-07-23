package pricing

// This file declares Usage (one call's normalized token usage) and Amount (a
// cost subtotal honest about what it does not know), and the Cost function
// that prices the one against the other. Cost never invents a number: a
// nonzero usage dimension with no matching rate makes the whole Amount
// Known=false with a Reason, rather than silently pricing it at zero or
// falling back to a different dimension's rate.

// Usage is one call's normalized token usage. Reasoning is a subset of
// Output (a design invariant: reasoning tokens are billed as output tokens
// that happen to be reasoning, never as an addition to it). Complete is
// false when the provider did not report usage for the call at all; Cost
// treats that as wholly unknown rather than guessing zero.
type Usage struct {
	Input, Output, Reasoning, CacheRead, CacheWrite int
	Complete                                        bool
}

// Amount is a cost subtotal that is honest about unknowns. Known is false
// when some usage dimension had no matching rate, the reasoning-subset
// invariant was violated, or the usage itself was incomplete; Reason then
// explains why, and USD is not a meaningful value.
type Amount struct {
	USD    float64
	Known  bool
	Reason string
}

// unknown builds an Amount with Known=false and the given reason.
func unknown(reason string) Amount {
	return Amount{Reason: reason}
}

// Cost prices u against r: (Output-Reasoning)×output_rate + Reasoning×
// reasoning_rate when r.Reasoning is set, else Output×output_rate once. It
// never double-counts reasoning tokens as both output and reasoning.
//
// Cost returns Known=false, never a fabricated number, when: u.Complete is
// false (the provider reported no usage at all); u.Reasoning exceeds
// u.Output (the reasoning-subset invariant is violated — this package has
// no error return here, so a violation surfaces as an unknown Amount, not a
// silent correction); any usage dimension carries a negative count; or any
// dimension with nonzero usage has a nil rate. A dimension with zero usage
// never blocks the calculation, whether or not it is priced — nothing was
// consumed, so there is nothing to be unsure about. A dimension explicitly
// priced at zero (a non-nil rate pointing at 0.0) is Known=true and
// contributes zero cost; that is a real (free) price, not a missing one.
func Cost(u Usage, r Rates) Amount {
	if !u.Complete {
		return unknown("usage incomplete: provider did not report token usage")
	}
	if u.Input < 0 || u.Output < 0 || u.Reasoning < 0 || u.CacheRead < 0 || u.CacheWrite < 0 {
		return unknown("usage invalid: negative token count")
	}
	if u.Reasoning > u.Output {
		return unknown("usage invalid: reasoning tokens exceed output tokens (reasoning must be a subset of output)")
	}

	var total float64

	amt, ok := priceDimension(u.Input, r.Input)
	if !ok {
		return unknown("no rate for input tokens")
	}
	total += amt

	if r.Reasoning != nil {
		outAmt, ok := priceDimension(u.Output-u.Reasoning, r.Output)
		if !ok {
			return unknown("no rate for output tokens")
		}
		reasonAmt, ok := priceDimension(u.Reasoning, r.Reasoning)
		if !ok {
			return unknown("no rate for reasoning tokens")
		}
		total += outAmt + reasonAmt
	} else {
		outAmt, ok := priceDimension(u.Output, r.Output)
		if !ok {
			return unknown("no rate for output tokens")
		}
		total += outAmt
	}

	amt, ok = priceDimension(u.CacheRead, r.CacheRead)
	if !ok {
		return unknown("no rate for cache-read tokens")
	}
	total += amt

	amt, ok = priceDimension(u.CacheWrite, r.CacheWrite)
	if !ok {
		return unknown("no rate for cache-write tokens")
	}
	total += amt

	return Amount{USD: total, Known: true}
}

// priceDimension prices count tokens (never negative; callers guard that)
// against rate, USD per million tokens. A zero count always prices at zero
// and ok=true regardless of rate: nothing was consumed, so an absent rate
// for that dimension is irrelevant, not unknown. A nonzero count with a nil
// rate returns ok=false: the caller must treat the whole Amount as unknown
// rather than dropping the dimension or substituting another rate.
func priceDimension(count int, rate *float64) (float64, bool) {
	if count == 0 {
		return 0, true
	}
	if rate == nil {
		return 0, false
	}
	return float64(count) * (*rate) / 1_000_000, true
}
