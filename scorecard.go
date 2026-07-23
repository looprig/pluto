package mpqt

import (
	"sort"

	"github.com/looprig/eval"
)

// TableResult is one table's outcome: either a full eval.Report or a
// capability skip retained from preflight. The raw report is preserved intact
// behind every rollup.
type TableResult struct {
	Pack      eval.Name
	Table     eval.Name
	Dimension eval.Name
	Skipped   bool
	Missing   []Capability
	Report    eval.Report
}

// Scorecard is the objective result of one MPQT run for one manifest. It
// carries no policy: dispositions are derived later by a profile.
type Scorecard struct {
	Manifest Manifest
	Results  []TableResult
}

// DimensionScore is a bounded [0,100] quality score with separately reported
// coverage. Score is the mean over verdict-bearing assessments only (pass=1,
// fail=0). Unverified, error, and skipped assessments contribute no quality
// value; they reduce Coverage (verdicts / non-skipped assessments). A
// dimension with zero verdicts is Undecided, never a silent zero or a silent
// pass.
type DimensionScore struct {
	Dimension     eval.Name
	Score         float64
	Coverage      float64
	Verdicts      int
	Assessments   int
	SkippedTables int
	Undecided     bool
}

// Dimensions rolls every table result up by dimension, in dimension name
// order. It fails on an empty scorecard: no evidence is not a score.
func (s Scorecard) Dimensions() ([]DimensionScore, error) {
	if len(s.Results) == 0 {
		return nil, &ValidationError{Field: "Scorecard.Results", Reason: "must not be empty"}
	}
	acc := map[eval.Name]*DimensionScore{}
	var passes = map[eval.Name]int{}
	for _, res := range s.Results {
		d := acc[res.Dimension]
		if d == nil {
			d = &DimensionScore{Dimension: res.Dimension}
			acc[res.Dimension] = d
		}
		if res.Skipped {
			d.SkippedTables++
			continue
		}
		for _, sample := range res.Report.Samples {
			for _, a := range sample.Assessments {
				switch a.Status {
				case eval.StatusPass:
					d.Verdicts++
					d.Assessments++
					passes[res.Dimension]++
				case eval.StatusFail:
					d.Verdicts++
					d.Assessments++
				case eval.StatusUnverified, eval.StatusError:
					d.Assessments++
				case eval.StatusSkipped:
					// deliberate non-execution: excluded from coverage denominator
				}
			}
		}
	}
	out := make([]DimensionScore, 0, len(acc))
	for _, d := range acc {
		if d.Verdicts == 0 {
			d.Undecided = true
		} else {
			d.Score = 100 * float64(passes[d.Dimension]) / float64(d.Verdicts)
		}
		if d.Assessments > 0 {
			d.Coverage = float64(d.Verdicts) / float64(d.Assessments)
		}
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dimension < out[j].Dimension })
	return out, nil
}

// StatusRollup aggregates raw assessment status counts and sample counts over
// every executed table. Status counts are diagnostics, never quality values.
type StatusRollup struct {
	Samples      int
	TargetErrors int
	ByStatus     map[eval.AssessmentStatus]int
}

// StatusRollup computes the report-wide status rollup.
func (s Scorecard) StatusRollup() (StatusRollup, error) {
	if len(s.Results) == 0 {
		return StatusRollup{}, &ValidationError{Field: "Scorecard.Results", Reason: "must not be empty"}
	}
	roll := StatusRollup{ByStatus: map[eval.AssessmentStatus]int{}}
	for _, res := range s.Results {
		if res.Skipped {
			continue
		}
		roll.Samples += res.Report.Summary.Samples
		roll.TargetErrors += res.Report.Summary.TargetErrors
		for st, n := range res.Report.Summary.Assessments {
			roll.ByStatus[st] += n
		}
	}
	return roll, nil
}
