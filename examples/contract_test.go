package examples_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const offlineExamplesCommand = "GOWORK=off go test -race ./examples/..."

type examplesManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	Repository    string `json:"repository"`
	ProofSources  []struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Path   string `json:"path"`
		Symbol string `json:"symbol"`
	} `json:"proofSources"`
	Examples []struct {
		ID             string            `json:"id"`
		Ecosystem      string            `json:"ecosystem"`
		Owner          string            `json:"owner"`
		SourcePath     string            `json:"sourcePath"`
		Availability   string            `json:"availability"`
		Versions       map[string]string `json:"versions"`
		OfflineCommand string            `json:"offlineCommand"`
		Assertion      string            `json:"assertion"`
		WorkflowPath   string            `json:"workflowPath"`
		JobID          string            `json:"jobId"`
		Cleanup        string            `json:"cleanup"`
		LiveGate       json.RawMessage   `json:"liveGate"`
		ProofIDs       []string          `json:"proofIds"`
	} `json:"examples"`
}

func TestDocsExamplesArtifacts(t *testing.T) {
	repositoryRoot := filepath.Clean("..")
	manifestData, err := os.ReadFile(filepath.Join(repositoryRoot, "testdata/docs/examples.json"))
	if err != nil {
		t.Fatalf("read examples manifest: %v", err)
	}

	var manifest examplesManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode examples manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 || manifest.Repository != "pluto" {
		t.Fatalf("manifest identity = schema %d repository %q", manifest.SchemaVersion, manifest.Repository)
	}

	expectedProofs := map[string]struct {
		proofType string
		path      string
		symbol    string
	}{
		"example-pluto-qualification-fixture": {"executable-fixture", "examples/qualification/example_test.go", "Example_scriptedQualification"},
		"example-pluto-comparison-fixture":    {"executable-fixture", "examples/comparison/example_test.go", "Example_compareAndReport"},
		"example-pluto-artifacts-contract":    {"test", "examples/contract_test.go", "TestDocsExamplesArtifacts"},
		"example-pluto-run-source":            {"source", "pkg/run/run.go", "Execute"},
		"example-pluto-profile-source":        {"source", "pkg/profile/evaluate.go", "Evaluate"},
		"example-pluto-compare-source":        {"source", "pkg/compare/compare.go", "Compare"},
		"example-pluto-report-source":         {"source", "pkg/reportjson/codec.go", "Encode"},
	}
	proofs := make(map[string]bool, len(manifest.ProofSources))
	allowedTypes := map[string]bool{"executable-fixture": true, "test": true, "source": true}
	for _, proof := range manifest.ProofSources {
		expected, ok := expectedProofs[proof.ID]
		if !ok {
			t.Errorf("unexpected proof source ID %q", proof.ID)
		} else if proof.Type != expected.proofType || proof.Path != expected.path || proof.Symbol != expected.symbol {
			t.Errorf("proof source %q = type %q path %q symbol %q, want type %q path %q symbol %q", proof.ID, proof.Type, proof.Path, proof.Symbol, expected.proofType, expected.path, expected.symbol)
		}
		if !allowedTypes[proof.Type] {
			t.Errorf("proof source %q has non-canonical type %q", proof.ID, proof.Type)
		}
		if proofs[proof.ID] {
			t.Errorf("duplicate proof source ID %q", proof.ID)
		}
		proofs[proof.ID] = true
		if strings.Contains(proof.Path, "#") {
			t.Errorf("proof source %q path contains a symbol fragment: %q", proof.ID, proof.Path)
		}
		if _, err := os.Stat(filepath.Join(repositoryRoot, proof.Path)); err != nil {
			t.Errorf("proof source %q does not resolve locally: %v", proof.ID, err)
		}
	}
	if len(manifest.ProofSources) != len(expectedProofs) {
		t.Errorf("proof source count = %d, want %d", len(manifest.ProofSources), len(expectedProofs))
	}

	wants := []struct {
		id, sourcePath, assertion, cleanup string
		proofIDs                           []string
	}{
		{
			id: "example-pluto-scripted-qualification", sourcePath: "examples/qualification/example_test.go",
			assertion: "A credential-free scripted target runs the core capability pack, produces a 100-point score with full coverage, satisfies a minimum-score profile, and round-trips the canonical Pluto report envelope.",
			cleanup:   "No cleanup required; qualification, profile evaluation, and report encoding use in-memory fixtures only.",
			proofIDs:  []string{"example-pluto-qualification-fixture", "example-pluto-artifacts-contract", "example-pluto-run-source", "example-pluto-profile-source", "example-pluto-report-source"},
		},
		{
			id: "example-pluto-candidate-comparison", sourcePath: "examples/comparison/example_test.go",
			assertion: "Two credential-free scripted runs compare the same pack as incumbent and candidate, retaining the table-level regression and the complete comparison result.",
			cleanup:   "No cleanup required; both runs and their comparison remain in memory and contact no provider.",
			proofIDs:  []string{"example-pluto-comparison-fixture", "example-pluto-artifacts-contract", "example-pluto-run-source", "example-pluto-compare-source"},
		},
	}
	if len(manifest.Examples) != len(wants) {
		t.Fatalf("manifest examples = %d, want %d", len(manifest.Examples), len(wants))
	}
	for i, want := range wants {
		example := manifest.Examples[i]
		if example.ID != want.id || example.SourcePath != want.sourcePath {
			t.Errorf("example %d identity = %q / %q", i, example.ID, example.SourcePath)
		}
		if example.Ecosystem != "go" || example.Owner != "pluto" || example.Availability != "source-workspace" {
			t.Errorf("example %q classification is incorrect: %#v", example.ID, example)
		}
		if !reflect.DeepEqual(example.Versions, map[string]string{"github.com/looprig/pluto": "source-workspace"}) {
			t.Errorf("example %q versions = %#v", example.ID, example.Versions)
		}
		if example.OfflineCommand != offlineExamplesCommand || example.Assertion != want.assertion || example.Cleanup != want.cleanup {
			t.Errorf("example %q command/assertion/cleanup mismatch", example.ID)
		}
		if example.WorkflowPath != ".github/workflows/docs-examples.yml" || example.JobID != "docs-examples" {
			t.Errorf("example %q workflow metadata = %q / %q", example.ID, example.WorkflowPath, example.JobID)
		}
		if string(example.LiveGate) != "null" {
			t.Errorf("example %q liveGate = %s, want null", example.ID, example.LiveGate)
		}
		if !reflect.DeepEqual(example.ProofIDs, want.proofIDs) {
			t.Errorf("example %q proofIds = %v, want %v", example.ID, example.ProofIDs, want.proofIDs)
		}
		for _, proofID := range example.ProofIDs {
			if !proofs[proofID] {
				t.Errorf("example %q references unknown proof %q", example.ID, proofID)
			}
		}
	}

	workflow, err := os.ReadFile(filepath.Join(repositoryRoot, ".github/workflows/docs-examples.yml"))
	if err != nil {
		t.Fatalf("read docs examples workflow: %v", err)
	}
	for _, literal := range []string{
		"docs-examples:",
		offlineExamplesCommand,
		"GOWORK=off make test build packs",
		"GOWORK=off go test -race ./...",
		"cd cmd/pluto && GOWORK=off go test -race ./...",
	} {
		if !strings.Contains(string(workflow), literal) {
			t.Errorf("workflow does not contain %q", literal)
		}
	}
}
