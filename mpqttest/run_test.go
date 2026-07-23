package mpqttest_test

import (
	"testing"

	"github.com/looprig/eval"
	"github.com/looprig/mpqt"
	fixtarget "github.com/looprig/mpqt/fixture/target"
	"github.com/looprig/mpqt/mpqttest"
	"github.com/looprig/mpqt/packs/structuredoutput"
	"github.com/looprig/mpqt/profile"
)

func testManifest() mpqt.Manifest {
	return mpqt.Manifest{
		TargetID:      "offline-test",
		Role:          mpqt.RoleCandidate,
		Provider:      "test",
		Model:         "fake",
		APIFormat:     "openai",
		BaseURL:       "https://example.invalid/v1",
		Revision:      "r-fake",
		EndpointClass: mpqt.EndpointRemote,
		Capabilities:  []mpqt.Capability{mpqt.CapabilityStructuredOutput},
	}
}

func conformingScripts(pack mpqt.Pack) map[string]fixtarget.Script {
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

func deviantScripts(pack mpqt.Pack) map[string]fixtarget.Script {
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
		Packs:    []mpqt.Pack{pack},
		Target:   fixtarget.NewScripted("offline-test", conformingScripts(pack)),
	})
	mpqttest.RequireDisposition(t, card, requirementProfile(90), profile.Qualified)
}

func TestRun_DeviantTargetTripsMinScore(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	card := mpqttest.Run(t, mpqttest.RunSpec{
		Manifest: testManifest(),
		Packs:    []mpqt.Pack{pack},
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
		Packs:    []mpqt.Pack{pack},
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
