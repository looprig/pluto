package mpqt

import (
	"testing"
	"time"

	"github.com/looprig/eval"
)

// reportWith builds a minimal valid eval.Report carrying one assessment per
// listed status for evaluator "ev" revision "1" on scenario "s".
func reportWith(t *testing.T, statuses ...eval.AssessmentStatus) eval.Report {
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

func TestScorecardDimensions(t *testing.T) {
	t.Parallel()
	sc := Scorecard{
		Manifest: validManifest(),
		Results: []TableResult{
			{
				Pack: "p", Table: "t1", Dimension: "capability",
				Report: reportWith(t, eval.StatusPass, eval.StatusPass, eval.StatusFail),
			},
			{
				Pack: "p", Table: "t2", Dimension: "capability",
				Report: reportWith(t, eval.StatusPass, eval.StatusUnverified),
			},
			{
				Pack: "p", Table: "t3", Dimension: "safety",
				Report: reportWith(t, eval.StatusError, eval.StatusSkipped),
			},
			{
				Pack: "p", Table: "t4", Dimension: "safety",
				Skipped: true, Missing: []Capability{CapabilityTools},
			},
		},
	}
	scores, err := sc.Dimensions()
	if err != nil {
		t.Fatalf("Dimensions() error = %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("Dimensions() returned %d entries, want 2", len(scores))
	}
	byName := map[eval.Name]DimensionScore{}
	for _, d := range scores {
		byName[d.Dimension] = d
	}
	cap := byName["capability"]
	// verdicts: pass, pass, fail, pass => 3/4 => 75. unverified excluded from
	// score, counted against coverage: 4 verdicts / 5 assessments = 0.8.
	if cap.Score != 75 {
		t.Errorf("capability Score = %v, want 75", cap.Score)
	}
	if cap.Coverage != 0.8 {
		t.Errorf("capability Coverage = %v, want 0.8", cap.Coverage)
	}
	safety := byName["safety"]
	if !safety.Undecided {
		t.Error("safety with zero verdicts must be Undecided")
	}
	if safety.Score != 0 {
		t.Errorf("undecided Score = %v, want 0", safety.Score)
	}
	if safety.SkippedTables != 1 {
		t.Errorf("safety SkippedTables = %d, want 1", safety.SkippedTables)
	}

	empty := Scorecard{Manifest: validManifest()}
	if _, err := empty.Dimensions(); err == nil {
		t.Error("Dimensions() with no results should error")
	}
}

func TestScorecardStatusRollup(t *testing.T) {
	t.Parallel()
	sc := Scorecard{
		Manifest: validManifest(),
		Results: []TableResult{
			{Pack: "p", Table: "t1", Dimension: "capability",
				Report: reportWith(t, eval.StatusPass, eval.StatusFail, eval.StatusError)},
		},
	}
	roll, err := sc.StatusRollup()
	if err != nil {
		t.Fatalf("StatusRollup() error = %v", err)
	}
	if roll.Samples != 3 {
		t.Errorf("Samples = %d, want 3", roll.Samples)
	}
	want := map[eval.AssessmentStatus]int{
		eval.StatusPass: 1, eval.StatusFail: 1, eval.StatusError: 1,
	}
	for st, n := range want {
		if roll.ByStatus[st] != n {
			t.Errorf("ByStatus[%s] = %d, want %d", st, roll.ByStatus[st], n)
		}
	}
}
