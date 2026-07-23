package packfile

import (
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/looprig/eval"
)

// toolUseFS returns a minimal, valid one-table pack directory: the same
// fixture the plan's Step 1 example uses.
func toolUseFS() fstest.MapFS {
	return fstest.MapFS{
		"tool-use/pack.yaml":       {Data: []byte("pack: tool-use\nrevision: v1\ntables:\n  - discipline.yaml\n")},
		"tool-use/discipline.yaml": {Data: []byte(minimalTable)},
	}
}

func TestLoadPackFromFS(t *testing.T) {
	fsys := toolUseFS()
	doc, err := Load(fsys, "tool-use")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.Pack.Pack != "tool-use" || len(doc.Tables) != 1 {
		t.Fatalf("doc: %+v", doc)
	}
	if len(doc.Raw) != 2 || doc.Raw["pack.yaml"] == nil || doc.Raw["discipline.yaml"] == nil {
		t.Fatalf("raw: %+v", doc.Raw)
	}

	p, err := doc.Build(NewRegistry(), BuildContext{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("qual validate: %v", err)
	}
	if p.Name != eval.Name("tool-use") || p.Tables[0].Dimension != eval.Name("capability") {
		t.Fatalf("pack: %+v", p)
	}
}

func TestLoadDirWrapsOSFS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/pack.yaml", "pack: tool-use\nrevision: v1\ntables:\n  - discipline.yaml\n")
	writeFile(t, dir+"/discipline.yaml", minimalTable)

	doc, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if doc.Pack.Pack != "tool-use" || len(doc.Tables) != 1 {
		t.Fatalf("doc: %+v", doc)
	}
	if doc.Dir != dir {
		t.Fatalf("Dir = %q, want %q", doc.Dir, dir)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadRejectsUnlistedTableReference(t *testing.T) {
	fsys := fstest.MapFS{
		"tool-use/pack.yaml": {Data: []byte("pack: tool-use\nrevision: v1\ntables:\n  - missing.yaml\n")},
	}
	_, err := Load(fsys, "tool-use")
	if err == nil {
		t.Fatal("unlisted table reference accepted")
	}
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if !strings.Contains(pe.Path, "missing.yaml") {
		t.Fatalf("error should name the missing file, got %+v", pe)
	}
}

func TestLoadRejectsDuplicateTableReference(t *testing.T) {
	fsys := fstest.MapFS{
		"tool-use/pack.yaml":       {Data: []byte("pack: tool-use\nrevision: v1\ntables:\n  - discipline.yaml\n  - discipline.yaml\n")},
		"tool-use/discipline.yaml": {Data: []byte(minimalTable)},
	}
	_, err := Load(fsys, "tool-use")
	if err == nil {
		t.Fatal("duplicate table reference accepted")
	}
}

func TestLoadIgnoresUnlistedFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"tool-use/pack.yaml":       {Data: []byte("pack: tool-use\nrevision: v1\ntables:\n  - discipline.yaml\n")},
		"tool-use/discipline.yaml": {Data: []byte(minimalTable)},
		"tool-use/stray.yaml":      {Data: []byte("bogus: true\n")},
	}
	doc, err := Load(fsys, "tool-use")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(doc.Tables) != 1 {
		t.Fatalf("doc.Tables = %+v, want 1 table", doc.Tables)
	}
	if _, ok := doc.Raw["stray.yaml"]; ok {
		t.Fatal("stray.yaml should not be loaded into Raw")
	}

	findings := doc.Lint()
	found := false
	for _, f := range findings {
		if strings.Contains(f, "stray.yaml") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Lint() = %v, want a finding naming stray.yaml", findings)
	}
}

const dupTableOne = `
table: table-one
revision: v1
dimension: capability
evaluators:
  - kind: forbidden-tool
    tool: bash
scenarios:
  - id: dup-1
    input:
      - role: user
        text: hi
`

const dupTableTwo = `
table: table-two
revision: v1
dimension: capability
evaluators:
  - kind: forbidden-tool
    tool: bash
scenarios:
  - id: dup-1
    input:
      - role: user
        text: hi
`

func TestLoadRejectsDuplicateScenarioIDsAcrossTables(t *testing.T) {
	fsys := fstest.MapFS{
		"dup-pack/pack.yaml":      {Data: []byte("pack: dup-pack\nrevision: v1\ntables:\n  - table-one.yaml\n  - table-two.yaml\n")},
		"dup-pack/table-one.yaml": {Data: []byte(dupTableOne)},
		"dup-pack/table-two.yaml": {Data: []byte(dupTableTwo)},
	}
	doc, err := Load(fsys, "dup-pack")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = doc.Build(NewRegistry(), BuildContext{})
	if err == nil {
		t.Fatal("duplicate scenario ID across tables accepted")
	}
	if !strings.Contains(err.Error(), "duplicate scenario ID") {
		t.Fatalf("error should report duplicate scenario ID, got %v", err)
	}
}

const seamToolCallsNoEvaluator = `
table: seam-tools
revision: v1
dimension: capability
evaluators:
  - kind: forbidden-tool
    tool: bash
scenarios:
  - id: seam-1
    input:
      - role: user
        text: hi
    expect:
      expected-tool-calls:
        - tool: search
          min: 1
`

const seamStructuredNoEvaluator = `
table: seam-structured
revision: v1
dimension: capability
evaluators:
  - kind: forbidden-tool
    tool: bash
scenarios:
  - id: seam-2
    input:
      - role: user
        text: hi
    expect:
      structured-output:
        schema: order-v1
`

const seamCovered = `
table: seam-covered
revision: v1
dimension: capability
evaluators:
  - kind: required-tool
    tool: search
  - kind: schema-result
scenarios:
  - id: seam-3
    input:
      - role: user
        text: hi
    expect:
      expected-tool-calls:
        - tool: search
          min: 1
      structured-output:
        schema: order-v1
`

func TestLintReportsExpectedToolCallsWithoutToolEvaluator(t *testing.T) {
	fsys := fstest.MapFS{
		"seam/pack.yaml":  {Data: []byte("pack: seam\nrevision: v1\ntables:\n  - table.yaml\n")},
		"seam/table.yaml": {Data: []byte(seamToolCallsNoEvaluator)},
	}
	doc, err := Load(fsys, "seam")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	findings := doc.Lint()
	found := false
	for _, f := range findings {
		if strings.Contains(f, "seam-1") && strings.Contains(f, "expected-tool-calls") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Lint() = %v, want expected-tool-calls seam warning", findings)
	}
}

func TestLintReportsStructuredOutputWithoutSchemaEvaluator(t *testing.T) {
	fsys := fstest.MapFS{
		"seam/pack.yaml":  {Data: []byte("pack: seam\nrevision: v1\ntables:\n  - table.yaml\n")},
		"seam/table.yaml": {Data: []byte(seamStructuredNoEvaluator)},
	}
	doc, err := Load(fsys, "seam")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	findings := doc.Lint()
	found := false
	for _, f := range findings {
		if strings.Contains(f, "seam-2") && strings.Contains(f, "structured-output") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Lint() = %v, want structured-output seam warning", findings)
	}
}

func TestLintNoSeamWarningWhenCovered(t *testing.T) {
	fsys := fstest.MapFS{
		"seam/pack.yaml":  {Data: []byte("pack: seam\nrevision: v1\ntables:\n  - table.yaml\n")},
		"seam/table.yaml": {Data: []byte(seamCovered)},
	}
	doc, err := Load(fsys, "seam")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if findings := doc.Lint(); len(findings) != 0 {
		t.Fatalf("Lint() = %v, want no findings when evaluators cover the expect seam", findings)
	}
}
