//go:build qualification

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
	"github.com/looprig/mpqt"
	"github.com/looprig/mpqt/mpqttest"
	"github.com/looprig/mpqt/packs/structuredoutput"
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
		Manifest: mpqt.Manifest{
			TargetID: "live-candidate", Role: mpqt.RoleCandidate,
			Provider: "openrouter", Model: "openai/gpt-5.4-mini",
			APIFormat: "openai", BaseURL: "https://openrouter.ai/api/v1",
			Revision:      eval.Revision(structuredoutput.Revision),
			EndpointClass: mpqt.EndpointRemote,
			Capabilities:  []mpqt.Capability{mpqt.CapabilityStructuredOutput},
		},
		Packs:  []mpqt.Pack{structuredoutput.V1()},
		Target: target,
	})
	roll, err := card.StatusRollup()
	if err != nil {
		t.Fatalf("StatusRollup: %v", err)
	}
	t.Logf("live status rollup: %+v", roll)
}
