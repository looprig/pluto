package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/looprig/eval"
	"github.com/looprig/mpqt/pkg/packfile"
	"github.com/looprig/mpqt/pkg/qual"
	fixtarget "github.com/looprig/mpqt/pkg/qual/target"
	"github.com/looprig/mpqt/pkg/run"
)

// digestLockfileName is pack.digest's fixed filename within a pack directory.
const digestLockfileName = "pack.digest"

// cmdValidate strictly loads every pack directory named on the command line
// (or, with none given, every directory under "." that contains a
// pack.yaml), runs Document.Lint, checks every table's environment against
// the portable-schema subset via Environment.Template, and verifies the
// pack's digest against its committed pack.digest lockfile when one is
// present. --execute additionally smoke-runs every script-backed scenario
// offline through pkg/run.Execute. Every dir is processed even after an
// earlier one fails, so a validate run always reports every problem it
// found, never just the first.
func cmdValidate(app App, args []string) int {
	fs := newFlagSet("validate", "validate [dir...] [--api-format FMT] [--execute]")
	apiFormat := fs.String("api-format", "", "check dialect projectability for this API format (v1: only \"\" is implemented; other values are accepted as a no-op with a note)")
	execute := fs.Bool("execute", false, "additionally smoke-run every script-backed table offline through pkg/run.Execute")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}

	if *apiFormat != "" {
		fmt.Fprintf(app.Stdout, "validate: --api-format %q: dialect projectability not yet implemented (only the default \"\" check runs)\n", *apiFormat)
	}

	dirs, err := packDirsFromArgs(fs.Args())
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt validate:", err)
		return ExitCommandFailure
	}
	if len(dirs) == 0 {
		fmt.Fprintln(app.Stderr, "mpqt validate: no pack directories found (looked for pack.yaml under .)")
		return ExitCommandFailure
	}

	failed := false
	for _, dir := range dirs {
		if !validateOne(app, dir, *execute) {
			failed = true
		}
	}

	if failed {
		return ExitCommandFailure
	}
	return ExitOK
}

// validateOne validates a single pack directory and reports its own failure,
// never aborting the caller's loop over the remaining directories.
func validateOne(app App, dir string, execute bool) bool {
	fmt.Fprintf(app.Stdout, "validate: %s\n", dir)
	ok := true

	doc, err := packfile.LoadDir(dir)
	if err != nil {
		fmt.Fprintf(app.Stdout, "  error: %v\n", err)
		return false
	}

	for _, finding := range doc.Lint() {
		fmt.Fprintf(app.Stdout, "  warning: %s\n", finding)
	}

	for _, tf := range doc.Tables {
		if _, err := tf.Environment.Template(); err != nil {
			fmt.Fprintf(app.Stdout, "  error: table %s: environment: %v\n", tf.Table, err)
			ok = false
		}
	}

	if !checkDigest(app, dir, doc) {
		ok = false
	}

	if execute {
		if err := executePack(context.Background(), app, dir, doc); err != nil {
			fmt.Fprintf(app.Stdout, "  error: --execute: %v\n", err)
			ok = false
		}
	}

	return ok
}

// checkDigest verifies dir's committed pack.digest lockfile against doc's own
// digest, when a lockfile is present. A freshly scaffolded pack (e.g. one
// `mpqt init` just wrote) has no lockfile yet -- that is reported as an
// informational note, never a failure, since init never writes one.
func checkDigest(app App, dir string, doc *packfile.Document) bool {
	// #nosec G304 -- dir is an operator-supplied (or self-discovered under
	// ".") pack directory validate already loaded via packfile.LoadDir;
	// reading its pack.digest lockfile crosses no new privilege boundary.
	lock, err := os.ReadFile(filepath.Join(dir, digestLockfileName))
	switch {
	case err == nil:
		if verr := packfile.VerifyDigest(doc, lock); verr != nil {
			fmt.Fprintf(app.Stdout, "  error: %v\n", verr)
			return false
		}
		return true
	case os.IsNotExist(err):
		fmt.Fprintln(app.Stdout, "  note: no pack.digest lockfile; skipping digest check")
		return true
	default:
		fmt.Fprintf(app.Stdout, "  error: read pack.digest: %v\n", err)
		return false
	}
}

// packDirsFromArgs resolves the pack directories validate should check: the
// positional arguments verbatim when any were given, else every directory
// discovered under "." that itself contains a pack.yaml.
func packDirsFromArgs(args []string) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}
	return discoverPackDirs(".")
}

// discoverPackDirs walks root and returns every directory (at any depth,
// root itself included) that contains a pack.yaml file, sorted for
// deterministic output. Hidden directories (dotfiles like .git) are never
// descended into.
func discoverPackDirs(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if _, statErr := os.Stat(filepath.Join(path, "pack.yaml")); statErr == nil {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs, err
}

// --- --execute: smoke-run script-backed tables offline ---

// executePack builds dir's pack (real eval.Evaluators, no judge client --
// --execute never spends a paid call), merges every table's script section
// into one Scripted target, and runs it through pkg/run.Execute against a
// synthetic manifest that declares every capability the pack requires (so
// capability gating never gets in the way of a pure smoke test). It reports
// whatever Execute returned even when Execute itself errors (ground rule:
// Execute may return partial, still-usable results alongside an error).
func executePack(ctx context.Context, app App, dir string, doc *packfile.Document) error {
	pack, err := doc.Build(app.Registry, packfile.BuildContext{})
	if err != nil {
		return fmt.Errorf("build pack: %w", err)
	}
	scripts, err := mergedScripts(doc)
	if err != nil {
		return err
	}
	manifest := executeManifest(unionCapabilities(pack))
	target := fixtarget.NewScripted(dir, scripts)

	res, err := run.Execute(ctx, run.Spec{Manifest: manifest, Packs: []qual.Pack{pack}, Target: target})
	fmt.Fprintf(app.Stdout, "  --execute: %d table(s) executed, %d skipped\n", len(res.Reports), len(res.Skipped))
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	return nil
}

// scriptFromSpec converts a packfile.ScriptSpec (the strictly-decoded YAML
// DTO) into the qual/target.Script the scripted fixture actually runs on.
// packfile itself never performs this conversion (Document.Build never
// touches TableFile.Script -- it is consumed only here, at the CLI's
// --execute boundary).
func scriptFromSpec(spec packfile.ScriptSpec) (fixtarget.Script, error) {
	var dur time.Duration
	if spec.Duration != "" {
		d, err := time.ParseDuration(spec.Duration)
		if err != nil {
			return fixtarget.Script{}, fmt.Errorf("invalid duration %q: %w", spec.Duration, err)
		}
		dur = d
	}

	toolCalls := make([]fixtarget.ToolCall, 0, len(spec.ToolCalls))
	for _, tc := range spec.ToolCalls {
		toolCalls = append(toolCalls, fixtarget.ToolCall{Name: eval.Name(tc.Name), ID: tc.ID, IsError: tc.IsError})
	}

	var structured *fixtarget.Structured
	if spec.Structured != nil {
		structured = &fixtarget.Structured{
			SchemaName:     eval.Name(spec.Structured.SchemaName),
			SchemaRevision: eval.Revision(spec.Structured.SchemaRevision),
		}
	}

	var structuredErr *fixtarget.StructuredErr
	if spec.StructuredErr != nil {
		structuredErr = &fixtarget.StructuredErr{
			Schema: eval.Revision(spec.StructuredErr.Schema),
			Reason: eval.StructuredErrorReason(spec.StructuredErr.Reason),
		}
	}

	return fixtarget.Script{
		Reply:         spec.Reply,
		Duration:      dur,
		ToolCalls:     toolCalls,
		Structured:    structured,
		StructuredErr: structuredErr,
	}, nil
}

// mergedScripts collects every table's Script map into one, keyed by
// scenario ID -- safe because qual.Pack.Validate (run inside Document.Build)
// already enforces pack-wide scenario ID uniqueness.
func mergedScripts(doc *packfile.Document) (map[string]fixtarget.Script, error) {
	out := map[string]fixtarget.Script{}
	for _, tf := range doc.Tables {
		for id, spec := range tf.Script {
			sc, err := scriptFromSpec(spec)
			if err != nil {
				return nil, fmt.Errorf("table %s script %s: %w", tf.Table, id, err)
			}
			out[id] = sc
		}
	}
	return out, nil
}

// unionCapabilities returns every capability required by any table in pack,
// deduplicated, so a synthetic --execute manifest can declare them all and
// never skip a table for a capability reason during a pure offline smoke
// test.
func unionCapabilities(pack qual.Pack) []qual.Capability {
	seen := map[qual.Capability]bool{}
	var out []qual.Capability
	for _, tbl := range pack.Tables {
		for _, c := range tbl.Requires {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// executeManifest is the synthetic, secret-free manifest --execute runs
// against: an offline fixture, not a real target, so its identity fields are
// fixed constants rather than anything operator-supplied.
func executeManifest(caps []qual.Capability) qual.Manifest {
	return qual.Manifest{
		TargetID:      "validate-execute",
		Role:          qual.RoleCandidate,
		Provider:      "validate",
		Model:         "scripted-fixture",
		APIFormat:     "none",
		BaseURL:       "https://validate.invalid",
		Revision:      "validate-execute",
		EndpointClass: qual.EndpointLocal,
		Capabilities:  caps,
	}
}
