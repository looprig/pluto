package comparison_test

import (
	"context"
	"fmt"

	"github.com/looprig/pluto/pkg/codepacks/capability"
	plutocompare "github.com/looprig/pluto/pkg/compare"
	"github.com/looprig/pluto/pkg/qual"
	fixtarget "github.com/looprig/pluto/pkg/qual/target"
	"github.com/looprig/pluto/pkg/run"
)

// Example_compareAndReport compares like-for-like pack executions. Roles are
// part of each manifest, so swapping candidate and incumbent is rejected
// instead of silently reversing the interpretation of a regression.
func Example_compareAndReport() {
	pack := capability.V1()
	incumbent := execute(pack, manifest("incumbent-v1", qual.RoleIncumbent), conformingScripts())

	candidateScripts := conformingScripts()
	candidateScripts["if-001-exact-phrase"] = fixtarget.Script{Reply: "the beacon is out"}
	candidate := execute(pack, manifest("candidate-v2", qual.RoleCandidate), candidateScripts)

	comparison, err := plutocompare.Compare(candidate, incumbent)
	if err != nil {
		panic(err)
	}
	regressions := 0
	for _, table := range comparison.Tables {
		regressions += table.Regressed
	}

	fmt.Printf("candidate=%s incumbent=%s\n", comparison.Candidate.TargetID, comparison.Incumbent.TargetID)
	fmt.Printf("tables=%d regressions=%d unmatched=%d\n", len(comparison.Tables), regressions, len(comparison.UnmatchedTables))
	// Output:
	// candidate=candidate-v2 incumbent=incumbent-v1
	// tables=7 regressions=1 unmatched=0
}

func execute(pack qual.Pack, m qual.Manifest, scripts map[string]fixtarget.Script) qual.Scorecard {
	result, err := run.Execute(context.Background(), run.Spec{
		Manifest: m,
		Packs:    []qual.Pack{pack},
		Target:   fixtarget.NewScripted(m.TargetID, scripts),
	})
	if err != nil {
		panic(err)
	}
	return result.Scorecard
}

func manifest(targetID string, role qual.ModelRole) qual.Manifest {
	return qual.Manifest{
		TargetID:      targetID,
		Role:          role,
		Provider:      "fixture",
		Model:         "scripted",
		APIFormat:     "offline",
		BaseURL:       "https://example.invalid/v1",
		Revision:      "fixture-v1",
		EndpointClass: qual.EndpointRemote,
	}
}

func conformingScripts() map[string]fixtarget.Script {
	return map[string]fixtarget.Script{
		"if-001-exact-phrase":      {Reply: "the beacon is lit"},
		"if-002-forbidden-word":    {Reply: "The sky turned amber and rose."},
		"if-003-format-constraint": {Reply: "- apple\n- banana\n- cherry"},
		"if-004-priority-conflict": {Reply: "I will answer in English."},
		"ka-001-capital":           {Reply: "Canberra"},
		"ka-002-arithmetic":        {Reply: "391"},
		"ka-003-unit-conversion":   {Reply: "2500"},
	}
}
