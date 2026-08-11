package main

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/looprig/core/content"
	"github.com/looprig/inference"
	"github.com/looprig/inference/contextcount"
)

// fakeCounter is a minimal contextcount.ContextCounter test double.
type fakeCounter struct {
	count contextcount.ContextCount
	cap   contextcount.CounterCapability
	err   error
}

func (f fakeCounter) CountContext(context.Context, inference.Request) (contextcount.ContextCount, error) {
	return f.count, f.err
}

func (f fakeCounter) CounterCapability() contextcount.CounterCapability { return f.cap }

func TestCounterAdapterCount(t *testing.T) {
	tests := []struct {
		name        string
		counter     fakeCounter
		wantTokens  int
		wantQuality string
		wantErr     bool
	}{
		{
			name: "happy path maps tokens and quality",
			counter: fakeCounter{
				count: contextcount.ContextCount{InputTokens: 123},
				cap:   contextcount.CounterCapability{Provider: "google", TokenizerRev: "gemini-2.0"},
			},
			wantTokens:  123,
			wantQuality: "google/gemini-2.0",
		},
		{
			name: "zero tokens",
			counter: fakeCounter{
				count: contextcount.ContextCount{InputTokens: 0},
				cap:   contextcount.CounterCapability{Provider: "google", TokenizerRev: "gemini-2.0"},
			},
			wantTokens:  0,
			wantQuality: "google/gemini-2.0",
		},
		{
			name: "counter error propagates",
			counter: fakeCounter{
				err: errors.New("boom"),
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			adapter := counterAdapter{counter: tt.counter}
			tokens, quality, err := adapter.Count(context.Background(), inference.Request{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Count() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if tokens != tt.wantTokens {
				t.Errorf("Count() tokens = %d, want %d", tokens, tt.wantTokens)
			}
			if quality != tt.wantQuality {
				t.Errorf("Count() quality = %q, want %q", quality, tt.wantQuality)
			}
		})
	}
}

func TestTokenCountToInt(t *testing.T) {
	tests := []struct {
		name string
		in   uint64
		want int
	}{
		{name: "zero", in: 0, want: 0},
		{name: "typical", in: 4096, want: 4096},
		{name: "clamps above MaxInt", in: math.MaxUint64, want: math.MaxInt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tokenCountToInt(content.TokenCount(tt.in))
			if got != tt.want {
				t.Errorf("tokenCountToInt(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
