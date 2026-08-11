package qualification_test

import (
	"context"
	"fmt"

	"github.com/looprig/pluto/pkg/codepacks/capability"
	"github.com/looprig/pluto/pkg/profile"
	"github.com/looprig/pluto/pkg/qual"
	fixtarget "github.com/looprig/pluto/pkg/qual/target"
	"github.com/looprig/pluto/pkg/reportjson"
	"github.com/looprig/pluto/pkg/run"
)

// Example_scriptedQualification runs a real Pluto pack without a provider or
// credentials. The scripted target is useful in tests for qualification
// policy, report handling, and pack composition. A live target can be supplied
// through run.Spec.TargetForTable by an application at its inference boundary.
func Example_scriptedQualification() {
	pack := capability.V1()
	card, err := run.Execute(context.Background(), run.Spec{
		Manifest: candidateManifest("candidate-v2"),
		Packs:    []qual.Pack{pack},
		Target:   fixtarget.NewScripted("candidate-v2", conformingScripts()),
	})
	if err != nil {
		panic(err)
	}

	minimumScore := 90.0
	minimumCoverage := 1.0
	policy := profile.Profile{
		Name:     "production-release",
		Revision: "v1",
		Requirements: []profile.Requirement{{
			Dimension:   "capability",
			MinScore:    &minimumScore,
			MinCoverage: &minimumCoverage,
		}},
	}
	decision, err := profile.Evaluate(card.Scorecard, policy)
	if err != nil {
		panic(err)
	}
	dimensions, err := card.Scorecard.Dimensions()
	if err != nil {
		panic(err)
	}

	report, err := reportjson.Encode(card.Scorecard, &decision)
	if err != nil {
		panic(err)
	}
	decoded, err := reportjson.Decode(report)
	if err != nil {
		panic(err)
	}

	fmt.Printf("score=%.0f coverage=%.0f%% disposition=%s\n", dimensions[0].Score, dimensions[0].Coverage*100, decision.Disposition)
	fmt.Printf("report=%s tables=%d\n", decoded.Version, len(decoded.Tables))
	// Output:
	// score=100 coverage=100% disposition=qualified
	// report=pluto-report/v1 tables=7
}

func candidateManifest(targetID string) qual.Manifest {
	return qual.Manifest{
		TargetID:      targetID,
		Role:          qual.RoleCandidate,
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
