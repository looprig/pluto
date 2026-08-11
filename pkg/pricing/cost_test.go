package pricing_test

import (
	"strings"
	"testing"

	"github.com/looprig/pluto/pkg/pricing"
)

func f64(v float64) *float64 { return &v }

func TestCost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		usage      pricing.Usage
		rates      pricing.Rates
		wantKnown  bool
		wantUSD    float64
		wantReason string // substring check only when non-empty
	}{
		{
			name:      "happy path: input and output priced, no reasoning rate",
			usage:     pricing.Usage{Input: 1_000_000, Output: 500_000, Complete: true},
			rates:     pricing.Rates{Input: f64(3), Output: f64(15)},
			wantKnown: true,
			wantUSD:   3 + 7.5,
		},
		{
			name: "distinct reasoning rate prices output-reasoning at output rate and reasoning at reasoning rate",
			usage: pricing.Usage{
				Input: 0, Output: 1_000_000, Reasoning: 400_000, Complete: true,
			},
			rates: pricing.Rates{
				Input: f64(3), Output: f64(15), Reasoning: f64(60),
			},
			wantKnown: true,
			// (1_000_000 - 400_000) * 15/1e6 + 400_000 * 60/1e6 = 9 + 24 = 33
			wantUSD: 9 + 24,
		},
		{
			name:       "reasoning exceeds output: invariant violation is unknown, never a silent correction",
			usage:      pricing.Usage{Output: 100, Reasoning: 101, Complete: true},
			rates:      pricing.Rates{Input: f64(1), Output: f64(1), Reasoning: f64(1)},
			wantKnown:  false,
			wantReason: "reasoning",
		},
		{
			name:       "usage incomplete: provider reported nothing",
			usage:      pricing.Usage{Input: 100, Output: 100, Complete: false},
			rates:      pricing.Rates{Input: f64(1), Output: f64(1)},
			wantKnown:  false,
			wantReason: "incomplete",
		},
		{
			name:       "negative usage is unknown, never priced",
			usage:      pricing.Usage{Input: -1, Complete: true},
			rates:      pricing.Rates{Input: f64(1)},
			wantKnown:  false,
			wantReason: "negative",
		},
		{
			name:       "nil rate with nonzero usage is unknown, never priced at zero or another rate",
			usage:      pricing.Usage{Input: 1000, Output: 0, Complete: true},
			rates:      pricing.Rates{Input: nil, Output: f64(15)},
			wantKnown:  false,
			wantReason: "input",
		},
		{
			name:      "explicit zero rate with nonzero usage is known and contributes zero cost",
			usage:     pricing.Usage{Input: 1_000_000, Output: 0, Complete: true},
			rates:     pricing.Rates{Input: f64(0), Output: f64(15)},
			wantKnown: true,
			wantUSD:   0,
		},
		{
			name:      "zero usage on an unpriced dimension is known: nothing was consumed",
			usage:     pricing.Usage{Input: 0, Output: 1_000_000, Complete: true},
			rates:     pricing.Rates{Input: nil, Output: f64(15)},
			wantKnown: true,
			wantUSD:   15,
		},
		{
			name:       "cache-read priced, cache-write not: unknown only when cache-write usage is nonzero",
			usage:      pricing.Usage{CacheRead: 1_000_000, CacheWrite: 1_000_000, Complete: true},
			rates:      pricing.Rates{Input: f64(0), Output: f64(0), CacheRead: f64(10), CacheWrite: nil},
			wantKnown:  false,
			wantReason: "cache-write",
		},
		{
			name:      "cache dimensions priced correctly when both rates present",
			usage:     pricing.Usage{CacheRead: 1_000_000, CacheWrite: 2_000_000, Complete: true},
			rates:     pricing.Rates{Input: f64(0), Output: f64(0), CacheRead: f64(0.3), CacheWrite: f64(3.75)},
			wantKnown: true,
			wantUSD:   0.3 + 7.5,
		},
		{
			name:      "all-zero usage is known and free even against an empty Rates",
			usage:     pricing.Usage{Complete: true},
			rates:     pricing.Rates{},
			wantKnown: true,
			wantUSD:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pricing.Cost(tt.usage, tt.rates)
			if got.Known != tt.wantKnown {
				t.Fatalf("Cost().Known = %v, want %v (reason=%q)", got.Known, tt.wantKnown, got.Reason)
			}
			if !tt.wantKnown {
				if got.Reason == "" {
					t.Error("Known=false must carry a non-empty Reason")
				}
				if tt.wantReason != "" && !strings.Contains(got.Reason, tt.wantReason) {
					t.Errorf("Reason = %q, want substring %q", got.Reason, tt.wantReason)
				}
				return
			}
			if got.Reason != "" {
				t.Errorf("Known=true must not carry a Reason, got %q", got.Reason)
			}
			if diff := got.USD - tt.wantUSD; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("Cost().USD = %v, want %v", got.USD, tt.wantUSD)
			}
		})
	}
}
