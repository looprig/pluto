package mpqttest_test

import (
	"reflect"
	"testing"

	"github.com/looprig/eval"
	"github.com/looprig/mpqt/pkg/codepacks/structuredoutput"
	"github.com/looprig/mpqt/pkg/mpqttest"
	"github.com/looprig/mpqt/pkg/profile"
	"github.com/looprig/mpqt/pkg/qual"
	fixtarget "github.com/looprig/mpqt/pkg/qual/target"
)

func testManifest() qual.Manifest {
	return qual.Manifest{
		TargetID:      "offline-test",
		Role:          qual.RoleCandidate,
		Provider:      "test",
		Model:         "fake",
		APIFormat:     "openai",
		BaseURL:       "https://example.invalid/v1",
		Revision:      "r-fake",
		EndpointClass: qual.EndpointRemote,
		Capabilities:  []qual.Capability{qual.CapabilityStructuredOutput},
	}
}

func conformingScripts(pack qual.Pack) map[string]fixtarget.Script {
	scripts := map[string]fixtarget.Script{}
	for _, tbl := range pack.Tables {
		for _, sc := range tbl.Scenarios {
			scripts[sc.ID] = fixtarget.Script{
				Reply: "ok",
				Structured: &fixtarget.Structured{
					SchemaName:     "output",
					SchemaRevision: sc.Expectation.StructuredOutput.Schema,
				},
			}
		}
	}
	return scripts
}

func deviantScripts(pack qual.Pack) map[string]fixtarget.Script {
	scripts := map[string]fixtarget.Script{}
	for _, tbl := range pack.Tables {
		for _, sc := range tbl.Scenarios {
			scripts[sc.ID] = fixtarget.Script{
				Reply: "malformed",
				StructuredErr: &fixtarget.StructuredErr{
					Schema: sc.Expectation.StructuredOutput.Schema,
					Reason: eval.StructuredErrorSchemaMismatch,
				},
			}
		}
	}
	return scripts
}

func requirementProfile(minScore float64) profile.Profile {
	return profile.Profile{
		Name:     "test-profile",
		Revision: "1",
		Requirements: []profile.Requirement{
			{Dimension: "capability", MinScore: &minScore},
		},
	}
}

func TestRun_ConformingTargetQualifies(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	card := mpqttest.Run(t, mpqttest.RunSpec{
		Manifest: testManifest(),
		Packs:    []qual.Pack{pack},
		Target:   fixtarget.NewScripted("offline-test", conformingScripts(pack)),
	})
	mpqttest.RequireDisposition(t, card, requirementProfile(90), profile.Qualified)
}

func TestRun_DeviantTargetTripsMinScore(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	card := mpqttest.Run(t, mpqttest.RunSpec{
		Manifest: testManifest(),
		Packs:    []qual.Pack{pack},
		Target:   fixtarget.NewScripted("offline-test", deviantScripts(pack)),
	})

	result, err := profile.Evaluate(card, requirementProfile(100))
	if err != nil {
		t.Fatalf("profile.Evaluate: %v", err)
	}
	if result.Disposition == profile.Qualified {
		t.Errorf("Disposition = %s, want anything but Qualified for an all-failing deviant target", result.Disposition)
	}
	if result.Disposition != profile.Rejected {
		t.Errorf("Disposition = %s, want Rejected (MinScore:100 violated by an all-fail dimension)", result.Disposition)
	}
}

func TestRun_TrialsMultipliesSamples(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	const trials = 3
	card := mpqttest.Run(t, mpqttest.RunSpec{
		Manifest: testManifest(),
		Packs:    []qual.Pack{pack},
		Target:   fixtarget.NewScripted("offline-test", conformingScripts(pack)),
		Trials:   trials,
	})

	wantScenarios := 0
	for _, tbl := range pack.Tables {
		wantScenarios += len(tbl.Scenarios)
	}
	roll, err := card.StatusRollup()
	if err != nil {
		t.Fatalf("StatusRollup: %v", err)
	}
	if roll.Samples != trials*wantScenarios {
		t.Errorf("Samples = %d, want %d (trials=%d * scenarios=%d)", roll.Samples, trials*wantScenarios, trials, wantScenarios)
	}
}

func TestRun_SkippedTableRecordsMissingCapability(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	manifest := testManifest()
	manifest.Capabilities = nil // declares no capabilities: every table requiring
	// structured_output must be skipped, never silently run or dropped.

	// The target is never invoked on the skipped path, so an empty script map
	// is fine: a call to Observe for an unscripted scenario would itself fail
	// the test via UnscriptedScenarioError, which is exactly the guard that
	// proves the skip really did skip execution.
	card := mpqttest.Run(t, mpqttest.RunSpec{
		Manifest: manifest,
		Packs:    []qual.Pack{pack},
		Target:   fixtarget.NewScripted("offline-test", nil),
	})

	if len(card.Results) != 1 {
		t.Fatalf("Results = %+v, want exactly 1 table result", card.Results)
	}
	res := card.Results[0]
	if !res.Skipped {
		t.Fatalf("TableResult.Skipped = false, want true (manifest declares no capabilities)")
	}
	wantMissing := []qual.Capability{qual.CapabilityStructuredOutput}
	if !reflect.DeepEqual(res.Missing, wantMissing) {
		t.Errorf("Missing = %+v, want %+v", res.Missing, wantMissing)
	}
}
