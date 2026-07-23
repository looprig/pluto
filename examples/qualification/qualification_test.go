// Package qualification demonstrates a full MPQT qualification run end to
// end: plan a pack against a manifest, execute it (offline here, live in
// live_test.go), and gate on the derived disposition.
package qualification

import (
	"testing"

	"github.com/looprig/mpqt"
	fixtarget "github.com/looprig/mpqt/fixture/target"
	"github.com/looprig/mpqt/mpqttest"
	"github.com/looprig/mpqt/packs/structuredoutput"
	"github.com/looprig/mpqt/profile"
)

func TestOfflineQualification(t *testing.T) {
	pack := structuredoutput.V1()
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
	card := mpqttest.Run(t, mpqttest.RunSpec{
		Manifest: mpqt.Manifest{
			TargetID: "offline-example", Role: mpqt.RoleCandidate,
			Provider: "test", Model: "fake", APIFormat: "openai",
			BaseURL: "https://example.invalid/v1", Revision: "r-fake",
			EndpointClass: mpqt.EndpointRemote,
			Capabilities:  []mpqt.Capability{mpqt.CapabilityStructuredOutput},
		},
		Packs:  []mpqt.Pack{pack},
		Target: fixtarget.NewScripted("offline-example", scripts),
	})
	minScore := 90.0
	mpqttest.RequireDisposition(t, card, profile.Profile{
		Name: "example", Revision: "1",
		Requirements: []profile.Requirement{
			{Dimension: "capability", MinScore: &minScore},
		},
	}, profile.Qualified)
}
