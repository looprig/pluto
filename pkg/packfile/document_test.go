package packfile

import (
	"strings"
	"testing"
)

const minimalTable = `
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

func TestDecodeTableFile(t *testing.T) {
	tf, err := DecodeTable(strings.NewReader(minimalTable))
	if err != nil {
		t.Fatalf("DecodeTable: %v", err)
	}
	if tf.Table != "discipline" || tf.Revision != "v1" || tf.Dimension != "capability" {
		t.Fatalf("identity mismatch: %+v", tf)
	}
	if len(tf.Requires) != 1 || tf.Requires[0] != "tools" {
		t.Fatalf("requires: %+v", tf.Requires)
	}
	if len(tf.Evaluators) != 1 || tf.Evaluators[0].Kind != "forbidden-tool" {
		t.Fatalf("evaluators: %+v", tf.Evaluators)
	}
	sc := tf.Scenarios[0]
	if sc.ID != "tu-101-no-tool-needed" || sc.Input[0].Role != "user" || sc.Input[0].Text != "What is 2+2?" {
		t.Fatalf("scenario: %+v", sc)
	}
	if sc.Expect == nil || len(sc.Expect.ForbiddenActions) != 1 || sc.Expect.ForbiddenActions[0] != "bash" {
		t.Fatalf("expect: %+v", sc.Expect)
	}
	if sc.Labels["category"] != "tool-use" {
		t.Fatalf("labels: %+v", sc.Labels)
	}
}

func TestDecodeTableRejectsUnknownField(t *testing.T) {
	_, err := DecodeTable(strings.NewReader("table: t\nrevision: v1\ndimension: d\nbogus: 1\n"))
	if err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestDecodeTableRejectsOversizedInput(t *testing.T) {
	big := "table: t\n# " + strings.Repeat("x", MaxFileBytes) + "\n"
	if _, err := DecodeTable(strings.NewReader(big)); err == nil {
		t.Fatal("oversized input accepted")
	}
}
