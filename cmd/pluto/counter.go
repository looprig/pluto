package main

import (
	"context"
	"fmt"
	"math"

	"github.com/looprig/core/content"
	"github.com/looprig/inference"
	"github.com/looprig/inference/contextcount"
)

// counterAdapter satisfies pkg/pricing.Counter over a real
// contextcount.ContextCounter, translating ContextCount.InputTokens into
// Count's token result and CounterCapability() into its free-form quality
// label -- the only shape pricing (llm-free by design) is allowed to see.
type counterAdapter struct {
	counter contextcount.ContextCounter
}

// Count implements pricing.Counter.
func (c counterAdapter) Count(ctx context.Context, req inference.Request) (tokens int, quality string, err error) {
	count, err := c.counter.CountContext(ctx, req)
	if err != nil {
		return 0, "", err
	}
	return tokenCountToInt(count.InputTokens), qualityLabel(c.counter.CounterCapability()), nil
}

// qualityLabel names the counter's tokenizer revision and provenance so a
// pricing.Plan.CounterQuality value is never a bare, unlabeled number.
func qualityLabel(capability contextcount.CounterCapability) string {
	return fmt.Sprintf("%s/%s", capability.Provider, capability.TokenizerRev)
}

// tokenCountToInt safely narrows a content.TokenCount (uint64) to int,
// clamping to math.MaxInt instead of wrapping, mirroring
// pkg/pricing.tokenCountToInt.
func tokenCountToInt(tc content.TokenCount) int {
	if tc > content.TokenCount(math.MaxInt) {
		return math.MaxInt
	}
	return int(tc)
}
