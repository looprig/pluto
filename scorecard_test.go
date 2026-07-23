package mpqt

import (
	"testing"

	"github.com/looprig/eval"
	"github.com/looprig/mpqt/internal/reporttest"
)

func TestScorecardDimensions(t *testing.T) {
	t.Parallel()
	sc := Scorecard{
		Manifest: validManifest(),
		Results: []TableResult{
			{
				Pack: "p", Table: "t1", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusPass, eval.StatusPass, eval.StatusFail),
			},
			{
				Pack: "p", Table: "t2", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusPass, eval.StatusUnverified),
			},
			{
				Pack: "p", Table: "t3", Dimension: "safety",
				Report: reporttest.Build(t, eval.StatusError, eval.StatusSkipped),
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
				Report: reporttest.Build(t, eval.StatusPass, eval.StatusFail, eval.StatusError)},
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

func TestScorecardFindingAndSeverityCount(t *testing.T) {
	t.Parallel()
	// reporttest.Build's Fail assessments always carry finding code
	// "quality_shortfall" at SeverityMedium. Three failing tables (one of
	// them skipped) should count the two executed failures only.
	sc := Scorecard{
		Manifest: validManifest(),
		Results: []TableResult{
			{Pack: "p", Table: "t1", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusFail, eval.StatusPass)},
			{Pack: "p", Table: "t2", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusFail)},
			{Pack: "p", Table: "t3", Dimension: "capability",
				Skipped: true, Missing: []Capability{CapabilityTools},
				Report: reporttest.Build(t, eval.StatusFail)},
		},
	}
	if got := sc.FindingCount("quality_shortfall"); got != 2 {
		t.Errorf("FindingCount(quality_shortfall) = %d, want 2", got)
	}
	if got := sc.FindingCount("unknown_code"); got != 0 {
		t.Errorf("FindingCount(unknown_code) = %d, want 0", got)
	}
	if got := sc.SeverityCount(eval.SeverityMedium); got != 2 {
		t.Errorf("SeverityCount(medium) = %d, want 2", got)
	}
	if got := sc.SeverityCount(eval.SeverityCritical); got != 0 {
		t.Errorf("SeverityCount(critical) = %d, want 0", got)
	}
}
