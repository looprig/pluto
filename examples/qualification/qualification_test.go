// Package qualification demonstrates a full MPQT qualification run end to
// end: plan a pack against a manifest, execute it (offline here, live in
// live_test.go), and gate on the derived disposition.
package qualification

import (
	"testing"

	"github.com/looprig/mpqt/pkg/codepacks/structuredoutput"
	"github.com/looprig/mpqt/pkg/mpqttest"
	"github.com/looprig/mpqt/pkg/profile"
	"github.com/looprig/mpqt/pkg/qual"
	fixtarget "github.com/looprig/mpqt/pkg/qual/target"
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
		Manifest: qual.Manifest{
			TargetID: "offline-example", Role: qual.RoleCandidate,
			Provider: "test", Model: "fake", APIFormat: "openai",
			BaseURL: "https://example.invalid/v1", Revision: "r-fake",
			EndpointClass: qual.EndpointRemote,
			Capabilities:  []qual.Capability{qual.CapabilityStructuredOutput},
		},
		Packs:  []qual.Pack{pack},
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
