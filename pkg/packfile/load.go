package packfile

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/looprig/eval"
	"github.com/looprig/eval/rubric"
	"github.com/looprig/mpqt/pkg/qual"
)

// packFileName is the fixed name of a pack's identity file within its
// directory.
const packFileName = "pack.yaml"

// Document is a loaded, structurally validated pack: raw file bytes retained
// for digesting, decoded files for building. It contains no evaluators and
// needs no clients -- `mpqt validate` stops here.
type Document struct {
	Dir    string
	Pack   PackFile
	Raw    map[string][]byte // filename -> bytes, pack.yaml included
	Tables []TableFile       // in pack.yaml order

	// unlisted holds the *.yaml files present in the directory at load time
	// but not referenced by pack.yaml's tables list, sorted for determinism.
	// Load ignores them entirely (they are never decoded); Lint reports them.
	unlisted []string
}

// Load reads and strictly decodes the pack rooted at dir within fsys:
// pack.yaml plus every table file it lists, in list order. A table file
// referenced by pack.yaml but absent from dir is a load error naming the
// file; a *.yaml file present in dir but not referenced by pack.yaml is
// silently ignored here (Lint reports it as a finding).
func Load(fsys fs.FS, dir string) (*Document, error) {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return nil, &Error{Path: dir, Reason: "invalid pack directory: " + err.Error(), Err: err}
	}

	packData, err := readBounded(sub, packFileName)
	if err != nil {
		return nil, &Error{Path: joinPath(dir, packFileName), Reason: "read pack.yaml: " + err.Error(), Err: err}
	}
	pf, err := DecodePack(bytes.NewReader(packData))
	if err != nil {
		return nil, &Error{Path: joinPath(dir, packFileName), Reason: err.Error(), Err: err}
	}

	raw := map[string][]byte{packFileName: packData}
	tables := make([]TableFile, 0, len(pf.Tables))
	listed := make(map[string]bool, len(pf.Tables))
	for _, name := range pf.Tables {
		if !fs.ValidPath(name) || name == packFileName {
			return nil, &Error{Path: joinPath(dir, name), Reason: "invalid table file reference"}
		}
		if listed[name] {
			return nil, &Error{Path: joinPath(dir, name), Reason: "duplicate table reference"}
		}
		listed[name] = true

		data, err := readBounded(sub, name)
		if err != nil {
			return nil, &Error{Path: joinPath(dir, name), Reason: "referenced table file not found: " + err.Error(), Err: err}
		}
		tf, err := DecodeTable(bytes.NewReader(data))
		if err != nil {
			return nil, &Error{Path: joinPath(dir, name), Reason: err.Error(), Err: err}
		}
		raw[name] = data
		tables = append(tables, tf)
	}

	unlisted, err := unlistedYAMLFiles(sub, listed)
	if err != nil {
		return nil, &Error{Path: dir, Reason: "list pack directory: " + err.Error(), Err: err}
	}

	return &Document{
		Dir:      dir,
		Pack:     pf,
		Raw:      raw,
		Tables:   tables,
		unlisted: unlisted,
	}, nil
}

// LoadDir loads the pack directory at path from the local filesystem. It is
// an os.DirFS wrapper for the CLI; unit tests use Load directly against
// testing/fstest.MapFS instead.
func LoadDir(path string) (*Document, error) {
	clean := filepath.Clean(path)
	doc, err := Load(os.DirFS(clean), ".")
	if err != nil {
		return nil, err
	}
	doc.Dir = clean
	return doc, nil
}

// Build assembles the qual.Pack: per table, scenarios come from
// ScenarioSpec.Scenario (default name "<pack>-<table>", revision from the
// table file) and evaluators come from the registry. Rubrics from every
// table's RubricSpecs are merged into bc.Rubrics first (a duplicate rubric
// name anywhere in the pack is an error), so a judge kind in one table may
// reference a rubric defined in another. The assembled qual.Pack is validated
// before it is returned, so packfile never hands qual a structurally invalid
// pack (this is also where pack-wide duplicate scenario IDs across tables are
// caught, by qual.Pack.Validate).
func (d *Document) Build(reg *Registry, bc BuildContext) (qual.Pack, error) {
	rubrics, err := d.mergedRubrics()
	if err != nil {
		return qual.Pack{}, err
	}
	buildBC := bc
	buildBC.Rubrics = rubrics

	tables := make([]qual.Table, 0, len(d.Tables))
	for _, tf := range d.Tables {
		tbl, err := d.buildTable(tf, reg, buildBC)
		if err != nil {
			return qual.Pack{}, err
		}
		tables = append(tables, tbl)
	}

	p := qual.Pack{
		Name:     eval.Name(d.Pack.Pack),
		Revision: eval.Revision(d.Pack.Revision),
		Tables:   tables,
	}
	if err := p.Validate(); err != nil {
		return qual.Pack{}, err
	}
	return p, nil
}

// mergedRubrics builds the pack-wide rubric.Rubric map from every table's
// RubricSpecs, rejecting a rubric name that appears in more than one table.
func (d *Document) mergedRubrics() (map[string]rubric.Rubric, error) {
	merged := make(map[string]rubric.Rubric)
	for _, tf := range d.Tables {
		for _, rs := range tf.Rubrics {
			rb, err := rs.Rubric()
			if err != nil {
				return nil, err
			}
			name := string(rb.Name)
			if _, dup := merged[name]; dup {
				return nil, &Error{Path: "rubrics/" + name, Reason: "duplicate rubric name across pack"}
			}
			merged[name] = rb
		}
	}
	return merged, nil
}

// buildTable converts one strictly-decoded TableFile into a qual.Table.
func (d *Document) buildTable(tf TableFile, reg *Registry, bc BuildContext) (qual.Table, error) {
	defaultName := d.Pack.Pack + "-" + tf.Table

	scenarios := make([]eval.Scenario, 0, len(tf.Scenarios))
	for _, ss := range tf.Scenarios {
		sc, err := ss.Scenario(defaultName, tf.Revision)
		if err != nil {
			return qual.Table{}, err
		}
		scenarios = append(scenarios, sc)
	}

	evaluators := make([]eval.Evaluator, 0, len(tf.Evaluators))
	for _, es := range tf.Evaluators {
		ev, err := reg.Build(es, bc)
		if err != nil {
			return qual.Table{}, err
		}
		evaluators = append(evaluators, ev)
	}

	requires := make([]qual.Capability, 0, len(tf.Requires))
	for _, r := range tf.Requires {
		requires = append(requires, qual.Capability(r))
	}

	return qual.Table{
		Name:       eval.Name(tf.Table),
		Revision:   eval.Revision(tf.Revision),
		Dimension:  eval.Name(tf.Dimension),
		Requires:   requires,
		Scenarios:  scenarios,
		Evaluators: evaluators,
	}, nil
}

// seamSchemaKind is the evaluator kind Lint treats as enforcing
// structured-output.
const seamSchemaKind = "schema-result"

// Lint returns non-fatal findings: unlisted *.yaml files in the directory,
// expect/evaluator seam warnings -- a scenario with expected-tool-calls
// but no required-tool/tool-error-rate kind in the table, or a
// structured-output expect without a schema-result kind -- and a table's
// unconsumed run: block. Findings are diagnostics for pack authors, never
// load or build errors.
func (d *Document) Lint() []string {
	var findings []string
	for _, name := range d.unlisted {
		findings = append(findings, fmt.Sprintf(
			"%s: unlisted file %q is not referenced by pack.yaml's tables list and will be ignored",
			d.Dir, name))
	}

	for _, tf := range d.Tables {
		if tf.Run.isSet() {
			findings = append(findings, fmt.Sprintf(
				"table %q: run: block is decoded but not yet consumed by mpqt run (per-table trials/concurrency/timeouts have no effect; use the CLI's global --trials/--concurrency flags instead)",
				tf.Table))
		}

		kinds := make(map[string]bool, len(tf.Evaluators))
		for _, ev := range tf.Evaluators {
			kinds[ev.Kind] = true
		}
		hasToolEval := kinds["required-tool"] || kinds["tool-error-rate"]
		hasSchemaEval := kinds[seamSchemaKind]

		for _, sc := range tf.Scenarios {
			if sc.Expect == nil {
				continue
			}
			if len(sc.Expect.ExpectedToolCalls) > 0 && !hasToolEval {
				findings = append(findings, fmt.Sprintf(
					"table %q scenario %q: expect.expected-tool-calls is declared but no required-tool or tool-error-rate evaluator enforces it",
					tf.Table, sc.ID))
			}
			if sc.Expect.StructuredOutput != nil && !hasSchemaEval {
				findings = append(findings, fmt.Sprintf(
					"table %q scenario %q: expect.structured-output is declared but no schema-result evaluator enforces it",
					tf.Table, sc.ID))
			}
		}
	}
	return findings
}

// readBounded reads name from fsys, enforcing MaxFileBytes via the same
// boundedReadAll primitive strictDecode uses for the decode step itself.
func readBounded(fsys fs.FS, name string) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return boundedReadAll(f, MaxFileBytes)
}

// unlistedYAMLFiles returns the *.yaml/*.yml files in fsys's root that are
// not in listed, sorted for deterministic Lint output.
func unlistedYAMLFiles(fsys fs.FS, listed map[string]bool) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == packFileName || listed[name] {
			continue
		}
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// joinPath renders a dir/file path for diagnostics; dir is an fs.FS-style
// slash path (possibly "."), so this deliberately avoids filepath.Join's
// OS-specific separator handling.
func joinPath(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}
