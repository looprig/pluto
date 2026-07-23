package gen_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/looprig/mpqt/pkg/gen"
	"github.com/looprig/mpqt/pkg/packfile"
)

// fixtureTable is a hand-authored table file carrying a comment header (the
// kind of provenance/rationale comment the design doc calls out as the
// reason YAML was chosen over JSONL) plus one existing scenario.
const fixtureTable = `# yaml-language-server: $schema=../../pkg/packfile/schema.json
# tool-use/discipline: hand-curated seed scenarios, do not delete tu-101
table: discipline
revision: v1
dimension: capability
requires: [tools]
evaluators:
  - kind: forbidden-tool
    tool: bash
scenarios:
  - id: tu-101-no-tool-needed
    input:
      - role: user
        text: What is 2+2?
    expect:
      forbidden-actions: [bash]
    labels: {category: tool-use}
`

func TestAppendPreservesCommentsAndAddsGeneratedByLabel(t *testing.T) {
	t.Parallel()

	specs := []packfile.ScenarioSpec{
		{
			ID:    "tu-200-generated",
			Input: []packfile.MessageSpec{{Role: "user", Text: "Please list the files in /tmp."}},
		},
		{
			ID:     "tu-201-generated",
			Input:  []packfile.MessageSpec{{Role: "user", Text: "What's the weather?"}},
			Labels: map[string]string{"category": "tool-use"},
		},
	}

	out, err := gen.Append("tool-use/discipline.yaml", []byte(fixtureTable), specs, "claude-sonnet-5/2026-07-23")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	outStr := string(out)
	if !strings.Contains(outStr, "hand-curated seed scenarios, do not delete tu-101") {
		t.Fatalf("Append() dropped the comment header; got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "$schema=../../pkg/packfile/schema.json") {
		t.Fatalf("Append() dropped the schema comment; got:\n%s", outStr)
	}

	// tu-200-generated has an empty Name and a nil Expect; the appended YAML
	// must not carry zero-value boilerplate for either (no hand-authored
	// scenario in this corpus has a `name:` or `expect:` key when unset).
	if strings.Contains(outStr, `name: ""`) {
		t.Errorf("Append() output carries a zero-value name key; got:\n%s", outStr)
	}
	if strings.Contains(outStr, "expect: null") {
		t.Errorf("Append() output carries a zero-value expect key; got:\n%s", outStr)
	}

	tf, err := packfile.DecodeTable(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("DecodeTable(Append() output): %v\noutput:\n%s", err, outStr)
	}

	if len(tf.Scenarios) != 3 {
		t.Fatalf("tf.Scenarios has %d entries, want 3 (1 existing + 2 appended); got %+v", len(tf.Scenarios), tf.Scenarios)
	}
	if tf.Scenarios[0].ID != "tu-101-no-tool-needed" {
		t.Fatalf("existing scenario was reordered or lost: %+v", tf.Scenarios[0])
	}

	byID := make(map[string]packfile.ScenarioSpec, len(tf.Scenarios))
	for _, sc := range tf.Scenarios {
		byID[sc.ID] = sc
	}

	for _, wantID := range []string{"tu-200-generated", "tu-201-generated"} {
		sc, ok := byID[wantID]
		if !ok {
			t.Fatalf("appended scenario %q missing after round trip", wantID)
		}
		if sc.Labels["generated-by"] != "claude-sonnet-5/2026-07-23" {
			t.Errorf("scenario %q labels[generated-by] = %q, want %q", wantID, sc.Labels["generated-by"], "claude-sonnet-5/2026-07-23")
		}
	}

	// tu-201-generated already carried a "category" label; Append must merge
	// generated-by in, not clobber the existing label.
	if got := byID["tu-201-generated"].Labels["category"]; got != "tool-use" {
		t.Errorf("scenario tu-201-generated labels[category] = %q, want %q (existing label must survive the merge)", got, "tool-use")
	}

	// tu-101 (untouched) must be byte-identical in substance: same expect and
	// label content as before Append ran.
	original := byID["tu-101-no-tool-needed"]
	if len(original.Expect.ForbiddenActions) != 1 || original.Expect.ForbiddenActions[0] != "bash" {
		t.Errorf("existing scenario's expect block was altered: %+v", original.Expect)
	}
	if original.Labels["category"] != "tool-use" {
		t.Errorf("existing scenario's labels were altered: %+v", original.Labels)
	}
}

func TestAppendOnTableWithNoExistingScenarios(t *testing.T) {
	t.Parallel()

	const noScenariosTable = `table: billing
revision: v1
dimension: custom
`
	specs := []packfile.ScenarioSpec{
		{ID: "bill-001", Input: []packfile.MessageSpec{{Role: "user", Text: "Why was I charged twice?"}}},
	}

	out, err := gen.Append("my-assistant/billing.yaml", []byte(noScenariosTable), specs, "claude-sonnet-5/2026-07-23")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	tf, err := packfile.DecodeTable(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("DecodeTable(Append() output): %v\noutput:\n%s", err, out)
	}
	if len(tf.Scenarios) != 1 || tf.Scenarios[0].ID != "bill-001" {
		t.Fatalf("tf.Scenarios = %+v, want exactly bill-001", tf.Scenarios)
	}
	if tf.Scenarios[0].Labels["generated-by"] != "claude-sonnet-5/2026-07-23" {
		t.Errorf("generated-by label missing: %+v", tf.Scenarios[0].Labels)
	}
}

func TestAppendWithNoSpecsIsANoop(t *testing.T) {
	t.Parallel()

	out, err := gen.Append("tool-use/discipline.yaml", []byte(fixtureTable), nil, "claude-sonnet-5/2026-07-23")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	tf, err := packfile.DecodeTable(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("DecodeTable(Append() output): %v", err)
	}
	if len(tf.Scenarios) != 1 {
		t.Fatalf("tf.Scenarios = %+v, want unchanged (1 entry)", tf.Scenarios)
	}
}

func TestAppendRejectsMalformedTableFile(t *testing.T) {
	t.Parallel()

	specs := []packfile.ScenarioSpec{{ID: "x", Input: []packfile.MessageSpec{{Role: "user", Text: "hi"}}}}
	if _, err := gen.Append("bad.yaml", []byte("not: [valid"), specs, "m/d"); err == nil {
		t.Fatal("malformed YAML accepted")
	}
	if _, err := gen.Append("scalar.yaml", []byte("just-a-scalar\n"), specs, "m/d"); err == nil {
		t.Fatal("non-mapping root accepted")
	}
}
