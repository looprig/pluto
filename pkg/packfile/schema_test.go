package packfile

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestSchemaMatchesCommittedFile(t *testing.T) {
	generated, err := Schema(NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(generated), bytes.TrimSpace(committed)) {
		t.Fatal("schema.json is stale: run `go generate ./pkg/packfile` and commit")
	}
}

func TestSchemaIsValidJSONAndCoversEvaluatorKinds(t *testing.T) {
	raw, err := Schema(NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	for _, k := range NewRegistry().Kinds() {
		if !bytes.Contains(raw, []byte(`"`+k.Name+`"`)) {
			t.Fatalf("kind %s missing", k.Name)
		}
	}
}
