// Package reporttest builds minimal, valid eval.Report fixtures shared by
// MPQT's own test suites. It is test-only support code, never imported by
// production packages.
package reporttest

import (
	"testing"
	"time"

	"github.com/looprig/eval"
)

// Build builds a minimal valid eval.Report carrying one assessment per listed
// status for evaluator "ev" revision "1" on scenario "s", subject "t"/"r1".
func Build(t *testing.T, statuses ...eval.AssessmentStatus) eval.Report {
	t.Helper()
	desc := eval.Descriptor{Name: "ev", Revision: "1", Method: eval.MethodProgrammatic}
	started := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	samples := make([]eval.SampleReport, 0, len(statuses))
	provEvals := []eval.EvaluatorRevision{{Name: "ev", Revision: "1"}}
	summary := eval.Summary{Assessments: map[eval.AssessmentStatus]int{}}
	for i, st := range statuses {
		var a eval.Assessment
		switch st {
		case eval.StatusPass:
			a = eval.Pass(desc)
		case eval.StatusFail:
			a = eval.Fail(desc, eval.Finding{Code: "quality_shortfall", Severity: eval.SeverityMedium})
		case eval.StatusUnverified:
			a = eval.Unverified(desc)
		case eval.StatusError:
			a = eval.Errored(desc)
		case eval.StatusSkipped:
			a = eval.Skipped(desc)
		}
		samples = append(samples, eval.SampleReport{
			ScenarioID: "s",
			TrialIndex: i,
			Observation: eval.Observation{Subject: eval.Subject{
				ID: "t", Kind: eval.SubjectModel, Name: "t", Revision: "r1",
			}},
			Assessments: []eval.Assessment{a},
		})
		summary.Samples++
		summary.Assessments[st]++
	}
	r := eval.Report{
		ID:        "suite@rev",
		Suite:     "rev",
		Target:    "r1",
		StartedAt: started,
		EndedAt:   started.Add(time.Second),
		Samples:   samples,
		Summary:   summary,
		Provenance: eval.Provenance{
			Suite: "rev", Target: "r1", Evaluators: provEvals,
		},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("fixture report invalid: %v", err)
	}
	return r
}
