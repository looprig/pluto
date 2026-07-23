package qual

import (
	"math"
	"testing"
)

func TestStats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      []float64
		q       float64
		mean    float64
		median  float64
		quant   float64
		variant float64
		wantErr bool
	}{
		{name: "single", in: []float64{4}, q: 0.5, mean: 4, median: 4, quant: 4, variant: 0},
		{name: "pair", in: []float64{1, 3}, q: 0.5, mean: 2, median: 2, quant: 2, variant: 2},
		{name: "quartile interpolated", in: []float64{1, 2, 3, 4}, q: 0.25, mean: 2.5, median: 2.5, quant: 1.75, variant: 5.0 / 3.0},
		{name: "unsorted input", in: []float64{4, 1, 3, 2}, q: 0.25, mean: 2.5, median: 2.5, quant: 1.75, variant: 5.0 / 3.0},
		{name: "q zero is min", in: []float64{5, 1, 9}, q: 0, mean: 5, median: 5, quant: 1, variant: 16},
		{name: "q one is max", in: []float64{5, 1, 9}, q: 1, mean: 5, median: 5, quant: 9, variant: 16},
		{name: "empty", in: nil, q: 0.5, wantErr: true},
		{name: "nan input", in: []float64{1, math.NaN()}, q: 0.5, wantErr: true},
		{name: "inf input", in: []float64{1, math.Inf(1)}, q: 0.5, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := Summarize(tt.in, tt.q)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Summarize() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			const eps = 1e-9
			for _, chk := range []struct {
				label     string
				got, want float64
			}{
				{"mean", s.Mean, tt.mean},
				{"median", s.Median, tt.median},
				{"quantile", s.Quantile, tt.quant},
				{"variance", s.Variance, tt.variant},
			} {
				if math.Abs(chk.got-chk.want) > eps {
					t.Errorf("%s = %v, want %v", chk.label, chk.got, chk.want)
				}
			}
		})
	}

	if _, err := Summarize([]float64{1, 2}, -0.1); err == nil {
		t.Error("Summarize() with q < 0 should error")
	}
	if _, err := Summarize([]float64{1, 2}, 1.1); err == nil {
		t.Error("Summarize() with q > 1 should error")
	}
	in := []float64{3, 1, 2}
	if _, err := Summarize(in, 0.5); err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Error("Summarize() mutated its input slice")
	}
}
