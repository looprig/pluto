package profile

import (
	"testing"

	"github.com/looprig/eval"
	"github.com/looprig/mpqt/pkg/qual"
)

func card(dims ...qual.DimensionScore) fakeCard {
	return fakeCard{dims: dims}
}

type fakeCard struct {
	dims     []qual.DimensionScore
	findings map[eval.FindingCode]int
	severity map[eval.Severity]int
}

func (f fakeCard) Dimensions() ([]qual.DimensionScore, error) { return f.dims, nil }
func (f fakeCard) FindingCount(code eval.FindingCode) int     { return f.findings[code] }
func (f fakeCard) SeverityCount(s eval.Severity) int          { return f.severity[s] }

func capDim(score, coverage float64) qual.DimensionScore {
	return qual.DimensionScore{
		Dimension: "capability", Score: score, Coverage: coverage,
		Verdicts: 10, Assessments: 10,
	}
}

func validProfile() Profile {
	minScore := 80.0
	return Profile{
		Name:     "production-agent",
		Revision: "2026-07-22",
		Requirements: []Requirement{
			{Dimension: "capability", MinScore: &minScore},
		},
	}
}

func TestProfileValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*Profile)
		wantErr bool
	}{
		{name: "valid", mutate: func(p *Profile) {}},
		{name: "empty name", mutate: func(p *Profile) { p.Name = "" }, wantErr: true},
		{name: "empty revision", mutate: func(p *Profile) { p.Revision = "" }, wantErr: true},
		{name: "no requirements", mutate: func(p *Profile) { p.Requirements = nil }, wantErr: true},
		{name: "requirement without subject", mutate: func(p *Profile) {
			p.Requirements = []Requirement{{}}
		}, wantErr: true},
		{name: "score out of range", mutate: func(p *Profile) {
			bad := 101.0
			p.Requirements = []Requirement{{Dimension: "capability", MinScore: &bad}}
		}, wantErr: true},
		{name: "negative finding cap", mutate: func(p *Profile) {
			n := -1
			p.Requirements = []Requirement{{FindingCode: "x", MaxFindingCount: &n}}
		}, wantErr: true},
		{name: "coverage out of range", mutate: func(p *Profile) {
			bad := 1.5
			p.Requirements = []Requirement{{Dimension: "capability", MinCoverage: &bad}}
		}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := validProfile()
			tt.mutate(&p)
			err := p.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEvaluateDispositions(t *testing.T) {
	t.Parallel()
	minScore := 80.0
	minCov := 0.9
	zero := 0

	tests := []struct {
		name    string
		card    fakeCard
		profile Profile
		want    Disposition
	}{
		{
			name: "all met is qualified",
			card: card(capDim(90, 0.95)),
			profile: Profile{Name: "p", Revision: "1", Requirements: []Requirement{
				{Dimension: "capability", MinScore: &minScore, MinCoverage: &minCov},
			}},
			want: Qualified,
		},
		{
			name: "score below floor is rejected",
			card: card(capDim(70, 0.95)),
			profile: Profile{Name: "p", Revision: "1", Requirements: []Requirement{
				{Dimension: "capability", MinScore: &minScore},
			}},
			want: Rejected,
		},
		{
			name: "missing dimension is unverified",
			card: card(capDim(90, 0.95)),
			profile: Profile{Name: "p", Revision: "1", Requirements: []Requirement{
				{Dimension: "safety", MinScore: &minScore},
			}},
			want: Unverified,
		},
		{
			name: "undecided dimension is unverified",
			card: card(qual.DimensionScore{Dimension: "capability", Undecided: true}),
			profile: Profile{Name: "p", Revision: "1", Requirements: []Requirement{
				{Dimension: "capability", MinScore: &minScore},
			}},
			want: Unverified,
		},
		{
			name: "coverage below floor is unverified not rejected",
			card: card(capDim(90, 0.5)),
			profile: Profile{Name: "p", Revision: "1", Requirements: []Requirement{
				{Dimension: "capability", MinScore: &minScore, MinCoverage: &minCov},
			}},
			want: Unverified,
		},
		{
			name: "violation outranks missing evidence",
			card: card(capDim(70, 0.95)),
			profile: Profile{Name: "p", Revision: "1", Requirements: []Requirement{
				{Dimension: "capability", MinScore: &minScore},
				{Dimension: "safety", MinScore: &minScore},
			}},
			want: Rejected,
		},
		{
			name: "critical finding zero tolerance rejected",
			card: fakeCard{
				dims:     []qual.DimensionScore{capDim(90, 0.95)},
				severity: map[eval.Severity]int{eval.SeverityCritical: 1},
			},
			profile: Profile{Name: "p", Revision: "1", Requirements: []Requirement{
				{Severity: eval.SeverityCritical, MaxSeverityCount: &zero},
			}},
			want: Rejected,
		},
		{
			name: "restriction downgrades qualified",
			card: card(capDim(90, 0.95), qual.DimensionScore{
				Dimension: "safety", Score: 60, Coverage: 1, Verdicts: 10, Assessments: 10,
			}),
			profile: Profile{Name: "p", Revision: "1",
				Requirements: []Requirement{
					{Dimension: "capability", MinScore: &minScore},
				},
				Restrictions: []Restriction{{
					Description: "no unattended tool use",
					Requirement: Requirement{Dimension: "safety", MinScore: &minScore},
				}},
			},
			want: Restricted,
		},
		{
			name: "restriction ignored when mandatory violated",
			card: card(capDim(70, 0.95)),
			profile: Profile{Name: "p", Revision: "1",
				Requirements: []Requirement{
					{Dimension: "capability", MinScore: &minScore},
				},
				Restrictions: []Restriction{{
					Description: "irrelevant",
					Requirement: Requirement{Dimension: "capability", MinScore: &minScore},
				}},
			},
			want: Rejected,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := Evaluate(tt.card, tt.profile)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if res.Disposition != tt.want {
				t.Errorf("Disposition = %s, want %s", res.Disposition, tt.want)
			}
			if len(res.Requirements) != len(tt.profile.Requirements) {
				t.Errorf("requirement evidence entries = %d, want %d",
					len(res.Requirements), len(tt.profile.Requirements))
			}
		})
	}

	if _, err := Evaluate(card(capDim(90, 1)), Profile{}); err == nil {
		t.Error("Evaluate() with invalid profile should error")
	}
}

func TestDispositionRank(t *testing.T) {
	t.Parallel()
	tests := []struct {
		d    Disposition
		want int
	}{
		{Rejected, 0},
		{Unverified, 1},
		{Restricted, 2},
		{Qualified, 3},
		{Disposition("not-a-real-disposition"), -1},
		{Disposition(""), -1},
	}
	for _, tt := range tests {
		t.Run(string(tt.d), func(t *testing.T) {
			t.Parallel()
			if got := tt.d.Rank(); got != tt.want {
				t.Errorf("Disposition(%q).Rank() = %d, want %d", tt.d, got, tt.want)
			}
		})
	}
}

// TestDispositionRankPairwiseOrdering exercises every pairwise comparison
// across the full worst-to-best ladder, not just the two extremes: the
// reviews specifically flagged Restricted vs Unverified as an under-tested
// boundary, since it sits in the middle of the ladder rather than at an edge.
func TestDispositionRankPairwiseOrdering(t *testing.T) {
	t.Parallel()
	ladder := []Disposition{Rejected, Unverified, Restricted, Qualified}
	for i, worse := range ladder {
		for j, better := range ladder {
			worse, better := worse, better
			switch {
			case i < j:
				t.Run(string(worse)+"_less_than_"+string(better), func(t *testing.T) {
					t.Parallel()
					if !(worse.Rank() < better.Rank()) {
						t.Errorf("%s.Rank()=%d, want strictly less than %s.Rank()=%d", worse, worse.Rank(), better, better.Rank())
					}
				})
			case i == j:
				t.Run(string(worse)+"_equal_to_itself", func(t *testing.T) {
					t.Parallel()
					if worse.Rank() != better.Rank() {
						t.Errorf("%s.Rank()=%d, want equal to itself", worse, worse.Rank())
					}
				})
			default:
				t.Run(string(worse)+"_greater_than_"+string(better), func(t *testing.T) {
					t.Parallel()
					if !(worse.Rank() > better.Rank()) {
						t.Errorf("%s.Rank()=%d, want strictly greater than %s.Rank()=%d", worse, worse.Rank(), better, better.Rank())
					}
				})
			}
		}
	}
}
