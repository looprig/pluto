package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/looprig/pluto/pkg/packfile"
)

// cmdInit scaffolds a custom pack directory: <dir>/<name>/pack.yaml,
// <name>/example.yaml (a minimal, commented, already-valid template table),
// and <name>/schema.json (a local copy of packfile.Schema, since the
// corpus's relative $schema path only resolves inside this repo). Every
// written file carries the "# yaml-language-server: $schema=schema.json"
// header so editor IntelliSense works in any repo, per the design's
// "Custom packs" scaffold.
func cmdInit(app App, args []string) int {
	fs := newFlagSet("init", "init <name> [dir]")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}

	pos := fs.Args()
	if len(pos) < 1 || len(pos) > 2 {
		fmt.Fprintln(app.Stderr, "pluto init: expected <name> [dir]")
		fs.Usage()
		return ExitUsage
	}
	name := pos[0]
	dir := "."
	if len(pos) == 2 {
		dir = pos[1]
	}

	if err := validateComponent("name", name); err != nil {
		fmt.Fprintln(app.Stderr, "pluto init:", err)
		return ExitUsage
	}

	base := filepath.Join(filepath.Clean(dir), name)
	if err := os.MkdirAll(base, 0o750); err != nil {
		fmt.Fprintln(app.Stderr, "pluto init:", err)
		return ExitCommandFailure
	}

	schemaData, err := packfile.Schema(app.Registry)
	if err != nil {
		fmt.Fprintln(app.Stderr, "pluto init: schema:", err)
		return ExitCommandFailure
	}

	if err := os.WriteFile(filepath.Join(base, "pack.yaml"), []byte(initPackYAML(name)), 0o600); err != nil {
		fmt.Fprintln(app.Stderr, "pluto init:", err)
		return ExitCommandFailure
	}
	if err := os.WriteFile(filepath.Join(base, "example.yaml"), []byte(initExampleYAML(name)), 0o600); err != nil {
		fmt.Fprintln(app.Stderr, "pluto init:", err)
		return ExitCommandFailure
	}
	if err := os.WriteFile(filepath.Join(base, "schema.json"), schemaData, 0o600); err != nil {
		fmt.Fprintln(app.Stderr, "pluto init:", err)
		return ExitCommandFailure
	}

	fmt.Fprintf(app.Stdout, "init: wrote %s/pack.yaml, %s/example.yaml, %s/schema.json\n", base, base, base)
	return ExitOK
}

// validateComponent rejects an empty name or one that is not a single path
// component (no path separators, not "." or ".."), before it is ever joined
// into a filesystem path -- the "sanitize before use" / "stay within the
// expected root" rule for any user-supplied path fragment.
func validateComponent(field, v string) error {
	if v == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if v != filepath.Base(v) || v == "." || v == ".." {
		return fmt.Errorf("%s must be a single path component (no path separators), got %q", field, v)
	}
	return nil
}

// yamlQuoted double-quotes s for safe interpolation into a hand-assembled
// YAML scalar, regardless of what characters a caller-supplied pack name
// contains.
func yamlQuoted(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(s) + `"`
}

func initPackYAML(name string) string {
	return "# yaml-language-server: $schema=schema.json\n" +
		"pack: " + yamlQuoted(name) + "\n" +
		"revision: v1\n" +
		"tables:\n" +
		"  - example.yaml\n"
}

// initExampleYAML is a minimal, already-valid table file: it loads, lints
// clean, and builds against the built-in registry as-is (the init+validate
// round trip), while every field a real pack author must customize carries a
// TODO comment. dimension defaults to the pack name itself, per the design's
// "Custom packs default to their own dimension".
func initExampleYAML(name string) string {
	return "# yaml-language-server: $schema=schema.json\n" +
		"#\n" +
		"# Pluto custom pack template. Run `pluto evaluators` for the full list of\n" +
		"# evaluator kinds and `pluto schema` for the JSON Schema (also copied next to\n" +
		"# this file as schema.json for editor IntelliSense).\n" +
		"table: example\n" +
		"revision: v1\n" +
		"dimension: " + yamlQuoted(name) + "  # defaults to the pack name; customize to gate on it independently\n" +
		"requires: []  # target capabilities this table needs, e.g. [tools, structured_output]\n" +
		"\n" +
		"environment:\n" +
		"  # TODO: paste your assistant's real system prompt below, verbatim.\n" +
		"  system: |\n" +
		"    You are a helpful assistant.\n" +
		"  # TODO: list the tools your assistant actually offers, verbatim, so the\n" +
		"  # scenarios below can reference real tool names and schemas.\n" +
		"  # tools:\n" +
		"  #   - name: search\n" +
		"  #     description: Look something up\n" +
		"  #     schema:\n" +
		"  #       type: object\n" +
		"  #       properties: {query: {type: string}}\n" +
		"  #       required: [query]\n" +
		"\n" +
		"evaluators:\n" +
		"  # TODO: describe what to evaluate. required-text is a placeholder --\n" +
		"  # replace it (or add more) with the evaluator kinds that actually enforce\n" +
		"  # your table's expectations; see `pluto evaluators`.\n" +
		"  - kind: required-text\n" +
		"    substrings: [\"TODO: a substring every correct answer must contain\"]\n" +
		"\n" +
		"scenarios:\n" +
		"  - id: example-001\n" +
		"    input:\n" +
		"      - role: user\n" +
		"        text: \"TODO: a representative task for your assistant\"\n" +
		"    expect:\n" +
		"      required-facts: [\"TODO: what a correct answer must establish\"]\n" +
		"    labels: {category: example}\n"
}
