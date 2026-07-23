package compare

import (
	"testing"
	"time"

	"github.com/looprig/eval"
	"github.com/looprig/mpqt"
	"github.com/looprig/mpqt/internal/reporttest"
)

func candidateManifest() mpqt.Manifest {
	return mpqt.Manifest{
		TargetID:      "candidate",
		Role:          mpqt.RoleCandidate,
		Provider:      "acme",
		Model:         "acme-1",
		APIFormat:     "openai",
		BaseURL:       "https://example.invalid/v1",
		Revision:      "r-candidate",
		EndpointClass: mpqt.EndpointRemote,
	}
}

func incumbentManifest() mpqt.Manifest {
	return mpqt.Manifest{
		TargetID:      "incumbent",
		Role:          mpqt.RoleIncumbent,
		Provider:      "acme",
		Model:         "acme-0",
		APIFormat:     "openai",
		BaseURL:       "https://example.invalid/v1",
		Revision:      "r-incumbent",
		EndpointClass: mpqt.EndpointRemote,
	}
}

// reportRev builds a single-sample, single-evaluator report identical in shape
// to reporttest.Build but with a caller-chosen evaluator revision, so a
// cross-side revision bump (an incompatible case) can be constructed.
func reportRev(t *testing.T, rev eval.Revision, status eval.AssessmentStatus) eval.Report {
	t.Helper()
	desc := eval.Descriptor{Name: "ev", Revision: rev, Method: eval.MethodProgrammatic}
	started := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	var a eval.Assessment
	switch status {
	case eval.StatusPass:
		a = eval.Pass(desc)
	case eval.StatusFail:
		a = eval.Fail(desc, eval.Finding{Code: "quality_shortfall", Severity: eval.SeverityMedium})
	default:
		t.Fatalf("reportRev: unsupported status %s", status)
	}
	r := eval.Report{
		ID:        "suite@rev",
		Suite:     "rev",
		Target:    "r1",
		StartedAt: started,
		EndedAt:   started.Add(time.Second),
		Samples: []eval.SampleReport{{
			ScenarioID: "s",
			TrialIndex: 0,
			Observation: eval.Observation{Subject: eval.Subject{
				ID: "t", Kind: eval.SubjectModel, Name: "t", Revision: "r1",
			}},
			Assessments: []eval.Assessment{a},
		}},
		Summary: eval.Summary{Samples: 1, Assessments: map[eval.AssessmentStatus]int{status: 1}},
		Provenance: eval.Provenance{
			Suite: "rev", Target: "r1",
			Evaluators: []eval.EvaluatorRevision{{Name: "ev", Revision: rev}},
		},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("fixture report invalid: %v", err)
	}
	return r
}

// buildScorecards assembles a matched candidate/incumbent scorecard pair
// covering: a table present on both sides that regresses (baseline pass,
// candidate fail), one that improves (baseline fail, candidate pass), one
// that is unchanged (both pass), one that is incompatible (evaluator revision
// bump between sides), a table present only on the candidate side, a table
// present only on the incumbent side, and a table present on both sides' key
// but skipped by the candidate.
func buildScorecards(t *testing.T) (mpqt.Scorecard, mpqt.Scorecard) {
	t.Helper()

	candidate := mpqt.Scorecard{
		Manifest: candidateManifest(),
		Results: []mpqt.TableResult{
			{Pack: "p", Table: "t-regress", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusFail)},
			{Pack: "p", Table: "t-improve", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusPass)},
			{Pack: "p", Table: "t-same", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusPass)},
			{Pack: "p", Table: "t-incompat", Dimension: "capability",
				Report: reportRev(t, "2", eval.StatusPass)},
			{Pack: "p", Table: "only-candidate", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusPass)},
			{Pack: "p", Table: "t-skip", Dimension: "capability",
				Skipped: true, Missing: []mpqt.Capability{mpqt.CapabilityTools}},
		},
	}
	incumbent := mpqt.Scorecard{
		Manifest: incumbentManifest(),
		Results: []mpqt.TableResult{
			{Pack: "p", Table: "t-regress", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusPass)},
			{Pack: "p", Table: "t-improve", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusFail)},
			{Pack: "p", Table: "t-same", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusPass)},
			{Pack: "p", Table: "t-incompat", Dimension: "capability",
				Report: reportRev(t, "1", eval.StatusPass)},
			{Pack: "p", Table: "only-incumbent", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusPass)},
			{Pack: "p", Table: "t-skip", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusPass)},
		},
	}
	return candidate, incumbent
}

func TestCompare_TableAlignmentAndRollup(t *testing.T) {
	t.Parallel()
	candidate, incumbent := buildScorecards(t)

	got, err := Compare(candidate, incumbent)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}

	if got.Candidate.TargetID != "candidate" {
		t.Errorf("Comparison.Candidate = %+v, want TargetID candidate", got.Candidate)
	}
	if got.Incumbent.TargetID != "incumbent" {
		t.Errorf("Comparison.Incumbent = %+v, want TargetID incumbent", got.Incumbent)
	}

	// Unmatched tables: candidate-only, incumbent-only, and skipped, never
	// silently dropped.
	wantUnmatched := map[string]Side{
		"only-candidate": SideCandidateOnly,
		"only-incumbent": SideIncumbentOnly,
		"t-skip":         SideSkipped,
	}
	if len(got.UnmatchedTables) != len(wantUnmatched) {
		t.Fatalf("UnmatchedTables = %+v, want %d entries", got.UnmatchedTables, len(wantUnmatched))
	}
	for _, u := range got.UnmatchedTables {
		want, ok := wantUnmatched[string(u.Table)]
		if !ok {
			t.Errorf("unexpected unmatched table %q", u.Table)
			continue
		}
		if u.Side != want {
			t.Errorf("UnmatchedTable[%s].Side = %s, want %s", u.Table, u.Side, want)
		}
	}

	// Matched tables: t-regress, t-improve, t-same, t-incompat.
	byTable := map[eval.Name]TableComparison{}
	for _, tc := range got.Tables {
		byTable[tc.Table] = tc
	}
	if len(byTable) != 4 {
		t.Fatalf("Tables = %+v, want 4 matched tables", got.Tables)
	}

	if tc := byTable["t-regress"]; tc.Regressed != 1 || tc.Improved != 0 || tc.Unchanged != 0 || tc.Incompatible != 0 {
		t.Errorf("t-regress counts = %+v, want Regressed=1 only", tc)
	}
	if tc := byTable["t-improve"]; tc.Improved != 1 || tc.Regressed != 0 || tc.Unchanged != 0 || tc.Incompatible != 0 {
		t.Errorf("t-improve counts = %+v, want Improved=1 only", tc)
	}
	if tc := byTable["t-same"]; tc.Unchanged != 1 || tc.Regressed != 0 || tc.Improved != 0 || tc.Incompatible != 0 {
		t.Errorf("t-same counts = %+v, want Unchanged=1 only", tc)
	}
	if tc := byTable["t-incompat"]; tc.Incompatible != 1 || tc.Regressed != 0 || tc.Improved != 0 || tc.Unchanged != 0 {
		t.Errorf("t-incompat counts = %+v, want Incompatible=1 only", tc)
	}

	// The full eval/compare output is retained, not discarded.
	if len(byTable["t-regress"].Result.Cases) == 0 {
		t.Error("TableComparison.Result.Cases is empty, want retained case detail")
	}
}

func TestCompare_RoleValidation(t *testing.T) {
	t.Parallel()
	candidate, incumbent := buildScorecards(t)

	tests := []struct {
		name      string
		candidate mpqt.Scorecard
		incumbent mpqt.Scorecard
	}{
		{
			name:      "both candidate",
			candidate: withRole(candidate, mpqt.RoleCandidate),
			incumbent: withRole(incumbent, mpqt.RoleCandidate),
		},
		{
			name:      "swapped roles",
			candidate: withRole(candidate, mpqt.RoleIncumbent),
			incumbent: withRole(incumbent, mpqt.RoleCandidate),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Compare(tt.candidate, tt.incumbent); err == nil {
				t.Fatal("Compare() error = nil, want role validation error")
			}
		})
	}
}

func withRole(sc mpqt.Scorecard, role mpqt.ModelRole) mpqt.Scorecard {
	sc.Manifest.Role = role
	return sc
}
