//go:build qualification

// Package qualification's live test intentionally uses the distinct
// "qualification" build tag rather than this repo's house "integration"
// convention: it distinguishes a live-credentialed, cost-incurring example (a
// real paid call to OpenRouter) from the generic process-boundary integration
// tests that convention covers.
package qualification

import (
	"os"
	"testing"

	"github.com/looprig/eval"
	inferenceeval "github.com/looprig/eval/target/inference"
	"github.com/looprig/inference"
	inferauth "github.com/looprig/inference/auth"
	"github.com/looprig/inference/model"
	"github.com/looprig/llm/auto"
	"github.com/looprig/mpqt/packs/structuredoutput"
	"github.com/looprig/mpqt/pkg/mpqttest"
	"github.com/looprig/mpqt/pkg/qual"
)

func TestLiveQualification(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	m := model.CustomModel(
		"openrouter", model.APIFormatOpenAI,
		"https://openrouter.ai/api/v1", "openai/gpt-5.4-mini",
		model.WithStructuredOutput(),
	)
	client, err := auto.New(m, inferauth.APIKey(key))
	if err != nil {
		t.Fatalf("auto.New: %v", err)
	}
	target := inferenceeval.NewTarget(client, inference.Request{Model: m},
		inferenceeval.WithRevision(eval.Revision(structuredoutput.Revision)),
	)
	card := mpqttest.Run(t, mpqttest.RunSpec{
		Manifest: qual.Manifest{
			TargetID: "live-candidate", Role: qual.RoleCandidate,
			Provider: "openrouter", Model: "openai/gpt-5.4-mini",
			APIFormat: "openai", BaseURL: "https://openrouter.ai/api/v1",
			Revision:      eval.Revision(structuredoutput.Revision),
			EndpointClass: qual.EndpointRemote,
			Capabilities:  []qual.Capability{qual.CapabilityStructuredOutput},
		},
		Packs:  []qual.Pack{structuredoutput.V1()},
		Target: target,
	})
	roll, err := card.StatusRollup()
	if err != nil {
		t.Fatalf("StatusRollup: %v", err)
	}
	t.Logf("live status rollup: %+v", roll)
}
