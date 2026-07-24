package packfile

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/looprig/eval/rubric"
)

// judgePackFS returns a one-table pack whose only evaluator is a judge kind
// referencing rubricName, with the given inline `rubrics:` block spliced in
// (empty string for none). It lets a test exercise the catalog-seeding and
// collision rules of Document.Build's rubric resolution end to end.
func judgePackFS(rubricName, inlineRubrics string) fstest.MapFS {
	table := "table: conduct\nrevision: v1\ndimension: safety\n" +
		inlineRubrics +
		"evaluators:\n  - kind: judge\n    rubric: " + rubricName + "\n" +
		"scenarios:\n  - id: c1\n    input:\n      - role: user\n        text: Say something.\n"
	return fstest.MapFS{
		"p/pack.yaml":    {Data: []byte("pack: p\nrevision: v1\ntables:\n  - conduct.yaml\n")},
		"p/conduct.yaml": {Data: []byte(table)},
	}
}

// TestBuildResolvesCatalogRubricWithoutInlineDeclaration is the load-bearing
// guarantee for the shipped judge tables: a judge kind may name a built-in
// eval-catalog rubric (here "toxicity") with no `rubrics:` block of its own,
// and Build resolves it. Before catalog seeding this failed with
// "unknown rubric".
func TestBuildResolvesCatalogRubricWithoutInlineDeclaration(t *testing.T) {
	for _, rb := range rubric.Catalog() {
		name := string(rb.Name)
		t.Run(name, func(t *testing.T) {
			doc, err := Load(judgePackFS(name, ""), "p")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if _, err := doc.Build(NewRegistry(), BuildContext{JudgeClient: fakeJudgeClient{}}); err != nil {
				t.Fatalf("Build with catalog rubric %q: %v", name, err)
			}
		})
	}
}

// TestBuildRejectsInlineRubricShadowingCatalog proves a pack cannot silently
// redefine a shipped catalog rubric name: two packs' "toxicity" scores must
// mean the same thing, so an inline rubric reusing a catalog name is an error,
// not a shadow.
func TestBuildRejectsInlineRubricShadowingCatalog(t *testing.T) {
	inline := "rubrics:\n" +
		"  - name: toxicity\n    revision: v9\n    definition: A totally different, incompatible scale.\n" +
		"    criteria:\n      - id: x\n        description: whatever\n        min-score: 0\n        max-score: 1\n"
	doc, err := Load(judgePackFS("toxicity", inline), "p")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = doc.Build(NewRegistry(), BuildContext{JudgeClient: fakeJudgeClient{}})
	if err == nil {
		t.Fatal("Build: want error for inline rubric shadowing a catalog name, got nil")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("Build error = %q, want it to explain the catalog name is reserved", err)
	}
}

// TestBuildRejectsDuplicateInlineRubricName keeps the original within-pack
// duplicate-name rule intact after catalog seeding was added.
func TestBuildRejectsDuplicateInlineRubricName(t *testing.T) {
	dupRubric := "rubrics:\n" +
		"  - name: local-quality\n    revision: v1\n    definition: First.\n" +
		"    criteria:\n      - id: a\n        description: a\n        min-score: 0\n        max-score: 1\n" +
		"  - name: local-quality\n    revision: v1\n    definition: Second, same name.\n" +
		"    criteria:\n      - id: b\n        description: b\n        min-score: 0\n        max-score: 1\n"
	doc, err := Load(judgePackFS("local-quality", dupRubric), "p")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = doc.Build(NewRegistry(), BuildContext{JudgeClient: fakeJudgeClient{}})
	if err == nil || !strings.Contains(err.Error(), "duplicate rubric name") {
		t.Fatalf("Build error = %v, want a duplicate-rubric-name error", err)
	}
}
