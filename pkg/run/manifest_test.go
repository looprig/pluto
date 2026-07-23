package run_test

import (
	"strings"
	"testing"

	"github.com/looprig/eval"
	"github.com/looprig/inference/model"
	"github.com/looprig/mpqt/pkg/qual"
	"github.com/looprig/mpqt/pkg/run"
)

func TestManifestModel(t *testing.T) {
	t.Parallel()
	m := qual.Manifest{
		Provider:     "openrouter",
		Model:        "meta-llama/llama-3-70b",
		APIFormat:    "openai",
		BaseURL:      "https://openrouter.ai/api/v1",
		Capabilities: []qual.Capability{qual.CapabilityTools},
	}
	mm := run.ManifestModel(m)
	if mm.Provider != model.ProviderName("openrouter") ||
		mm.Name != "meta-llama/llama-3-70b" ||
		mm.APIFormat != model.APIFormat("openai") ||
		mm.BaseURL != m.BaseURL {
		t.Fatalf("model: %+v", mm)
	}
	if !mm.Caps.Tools {
		t.Error("Caps.Tools = false, want true (Capabilities: [tools])")
	}
	if mm.Caps.StructuredOutput || mm.Caps.AcceptsImages || mm.Caps.Thinking {
		t.Errorf("Caps = %+v, want only Tools set", mm.Caps)
	}
}

func TestManifestModelEveryCapability(t *testing.T) {
	t.Parallel()
	m := qual.Manifest{
		Provider:  "test",
		Model:     "fake",
		APIFormat: "openai",
		BaseURL:   "https://example.invalid/v1",
		Capabilities: []qual.Capability{
			qual.CapabilityTools, qual.CapabilityStructuredOutput,
			qual.CapabilityImages, qual.CapabilityThinking,
		},
	}
	mm := run.ManifestModel(m)
	want := model.Capabilities{Tools: true, StructuredOutput: true, AcceptsImages: true, Thinking: true}
	if mm.Caps != want {
		t.Errorf("Caps = %+v, want %+v", mm.Caps, want)
	}
}

func TestManifestModelEffort(t *testing.T) {
	t.Parallel()
	m := qual.Manifest{Provider: "test", Model: "fake", APIFormat: "openai", BaseURL: "https://example.invalid/v1", Effort: "high"}
	mm := run.ManifestModel(m)
	if mm.Sampling.Effort != model.EffortHigh {
		t.Errorf("Sampling.Effort = %s, want high", mm.Sampling.Effort)
	}
}

const validManifestYAML = `
target-id: candidate-1
role: candidate
provider: openrouter
model: meta-llama/llama-3-70b
api-format: openai
base-url: https://openrouter.ai/api/v1
revision: r1
endpoint-class: remote
capabilities:
  - structured_output
`

func TestManifestYAMLRoundTrip(t *testing.T) {
	t.Parallel()

	m, err := run.DecodeManifest(strings.NewReader(validManifestYAML))
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("decoded Manifest.Validate: %v", err)
	}
	if m.TargetID != "candidate-1" || m.Provider != "openrouter" || m.Model != "meta-llama/llama-3-70b" {
		t.Errorf("decoded manifest = %+v", m)
	}
	want := []qual.Capability{qual.CapabilityStructuredOutput}
	if len(m.Capabilities) != 1 || m.Capabilities[0] != want[0] {
		t.Errorf("Capabilities = %v, want %v", m.Capabilities, want)
	}

	t.Run("unknown field rejected", func(t *testing.T) {
		t.Parallel()
		doc := validManifestYAML + "unknown-field: surprise\n"
		if _, err := run.DecodeManifest(strings.NewReader(doc)); err == nil {
			t.Fatal("DecodeManifest: want error for an unknown field, got nil")
		}
	})

	t.Run("credential-looking field rejected", func(t *testing.T) {
		t.Parallel()
		// qual.Manifest is deliberately secret-free: it has no api-key
		// field, so this is rejected the same way any other unknown field
		// is -- strict decoding, not a special credential detector.
		doc := validManifestYAML + "api-key: sk-should-not-exist\n"
		if _, err := run.DecodeManifest(strings.NewReader(doc)); err == nil {
			t.Fatal("DecodeManifest: want error for an api-key field, got nil")
		}
	})

	t.Run("invalid manifest rejected after decode", func(t *testing.T) {
		t.Parallel()
		// A well-formed YAML document that decodes but describes a
		// structurally invalid manifest (empty provider) must still be
		// rejected by Manifest.Validate before DecodeManifest returns it.
		doc := strings.Replace(validManifestYAML, "provider: openrouter\n", "provider: \"\"\n", 1)
		if _, err := run.DecodeManifest(strings.NewReader(doc)); err == nil {
			t.Fatal("DecodeManifest: want error for an empty provider, got nil")
		}
	})
}

const validProfileYAML = `
name: example-profile
revision: "1"
requirements:
  - dimension: capability
    min-score: 90
restrictions:
  - description: reduced scope
    requirement:
      dimension: capability
      min-coverage: 0.5
`

func TestDecodeProfileYAMLRoundTrip(t *testing.T) {
	t.Parallel()

	p, err := run.DecodeProfile(strings.NewReader(validProfileYAML))
	if err != nil {
		t.Fatalf("DecodeProfile: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("decoded Profile.Validate: %v", err)
	}
	if p.Name != eval.Name("example-profile") || p.Revision != eval.Revision("1") {
		t.Errorf("decoded profile identity = %s@%s", p.Name, p.Revision)
	}
	if len(p.Requirements) != 1 || p.Requirements[0].MinScore == nil || *p.Requirements[0].MinScore != 90 {
		t.Errorf("Requirements = %+v", p.Requirements)
	}
	if len(p.Restrictions) != 1 || p.Restrictions[0].Description != "reduced scope" {
		t.Errorf("Restrictions = %+v", p.Restrictions)
	}

	t.Run("unknown field rejected", func(t *testing.T) {
		t.Parallel()
		doc := validProfileYAML + "unknown-field: surprise\n"
		if _, err := run.DecodeProfile(strings.NewReader(doc)); err == nil {
			t.Fatal("DecodeProfile: want error for an unknown field, got nil")
		}
	})

	t.Run("empty requirements rejected after decode", func(t *testing.T) {
		t.Parallel()
		doc := `
name: example-profile
revision: "1"
`
		if _, err := run.DecodeProfile(strings.NewReader(doc)); err == nil {
			t.Fatal("DecodeProfile: want error for zero requirements, got nil")
		}
	})
}
