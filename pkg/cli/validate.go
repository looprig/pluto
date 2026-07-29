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
	fs := newFlagSet("validate", "validate [dir...] [--api-format FMT] [--execute] [--write-digests]")
	apiFormat := fs.String("api-format", "", "check dialect projectability for this API format (v1: only \"\" is implemented; other values are accepted as a no-op with a note)")
	execute := fs.Bool("execute", false, "additionally smoke-run every script-backed table offline through pkg/run.Execute")
	writeDigests := fs.Bool("write-digests", false, "(re)write each pack's pack.digest lockfile from its current contents and revision, instead of verifying it")
	verbose := verboseFlag(fs)
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}

	u := newUI(app.Stdout, app.LookupEnv, *verbose)

	dirs, err := packDirsFromArgs(fs.Args())
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt validate:", err)
		return ExitCommandFailure
	}
	if len(dirs) == 0 {
		fmt.Fprintln(app.Stderr, "mpqt validate: no pack directories found (looked for pack.yaml under .)")
		return ExitCommandFailure
	}

	u.title("validate", fmt.Sprintf("%d pack(s)", len(dirs)))

	if *apiFormat != "" {
		u.info("--api-format %q: dialect projectability not yet implemented (only the default \"\" check runs)", *apiFormat)
	}

	failed := false
	for _, dir := range dirs {
		if !validateOne(u, app, dir, *execute, *writeDigests) {
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
func validateOne(u *ui, app App, dir string, execute, writeDigests bool) bool {
	u.step("%s", dir)
	ok := true

	doc, err := packfile.LoadDir(dir)
	if err != nil {
		u.fail("%v", err)
		return false
	}

	for _, finding := range doc.Lint() {
		u.warn("%s", finding)
	}

	for _, tf := range doc.Tables {
		if _, err := tf.Environment.Template(); err != nil {
			u.fail("table %s: environment: %v", tf.Table, err)
			ok = false
		}
	}

	if writeDigests {
		if !writeDigest(u, dir, doc) {
			ok = false
		}
	} else if !checkDigest(u, dir, doc) {
		ok = false
	}

	if execute {
		if err := executePack(context.Background(), u, app, dir, doc); err != nil {
			u.fail("--execute: %v", err)
			ok = false
		}
	}

	if ok {
		u.ok("%s clean", dir)
	}

	return ok
}

// checkDigest verifies dir's committed pack.digest lockfile against doc's own
// digest, when a lockfile is present. A freshly scaffolded pack (e.g. one
// `mpqt init` just wrote) has no lockfile yet -- that is reported as an
// informational note, never a failure, since init never writes one.
func checkDigest(u *ui, dir string, doc *packfile.Document) bool {
	// #nosec G304 -- dir is an operator-supplied (or self-discovered under
	// ".") pack directory validate already loaded via packfile.LoadDir;
	// reading its pack.digest lockfile crosses no new privilege boundary.
	lock, err := os.ReadFile(filepath.Join(dir, digestLockfileName))
	switch {
	case err == nil:
		if verr := packfile.VerifyDigest(doc, lock); verr != nil {
			u.fail("%v", verr)
			return false
		}
		return true
	case os.IsNotExist(err):
		u.info("no pack.digest lockfile; skipping digest check")
		return true
	default:
		u.fail("read pack.digest: %v", err)
		return false
	}
}

// writeDigest (re)writes dir's pack.digest lockfile from doc's current
// contents and revision. It is the maintainer-side counterpart to checkDigest:
// after a deliberate scenario/evaluator change and revision bump, `mpqt
// validate --write-digests packs/...` regenerates every lockfile so the
// committed digests track the corpus. Writing is reported so a CI run that
// accidentally passes the flag is visible in its log.
func writeDigest(u *ui, dir string, doc *packfile.Document) bool {
	lockPath := filepath.Join(dir, digestLockfileName)
	// #nosec G306 -- a pack.digest lockfile is non-secret provenance meant to
	// be committed and world-readable; 0o644 matches the repo's other tracked
	// files.
	if err := os.WriteFile(lockPath, packfile.DigestLockfile(doc), 0o644); err != nil {
		u.fail("write pack.digest: %v", err)
		return false
	}
	u.ok("wrote %s", lockPath)
	return true
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
func executePack(ctx context.Context, u *ui, app App, dir string, doc *packfile.Document) error {
	offline, judged := splitJudgeTables(doc)
	if len(offline.Tables) == 0 {
		u.info("--execute: 0 table(s) executed, %d skipped (judge)", judged)
		return nil
	}

	pack, err := offline.Build(app.Registry, packfile.BuildContext{})
	if err != nil {
		return fmt.Errorf("build pack: %w", err)
	}
	scripts, err := mergedScripts(offline)
	if err != nil {
		return err
	}
	manifest := executeManifest(unionCapabilities(pack))
	target := fixtarget.NewScripted(dir, scripts)

	// Same live viewport as `mpqt run`, so the offline smoke run shows each
	// table completing (animated on a terminal, plain lines off one).
	vp := newViewport(app.Stdout, app.LookupEnv, len(offline.Tables))
	res, err := run.Execute(ctx, run.Spec{
		Manifest: manifest, Packs: []qual.Pack{pack}, Target: target,
		Progress: func(plan qual.TablePlan) {
			if plan.Runnable {
				vp.start(string(plan.Pack)+"/"+string(plan.Table), len(plan.Suite.Scenarios))
			}
		},
		OnResult: func(plan qual.TablePlan, rep eval.Report) {
			vp.finish(string(plan.Pack)+"/"+string(plan.Table),
				rep.Summary.Assessments[eval.StatusPass], rep.Summary.Assessments[eval.StatusFail])
		},
	})
	vp.close()
	u.info("--execute: %d table(s) executed, %d skipped, %d skipped (judge)",
		len(res.Reports), len(res.Skipped), judged)
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	return nil
}

// splitJudgeTables returns a shallow Document copy holding only the pack's
// offline-runnable (non-judge) tables, plus the count of judge tables left
// out. A judge evaluator cannot be built without a judge client and cannot be
// scored from a networkless scripted fixture, so --execute skips those tables
// visibly rather than failing the whole pack. The copy shares Dir/Pack/Raw
// with doc (untouched); only the Tables slice is filtered, which is all
// Document.Build and mergedScripts read.
func splitJudgeTables(doc *packfile.Document) (offline *packfile.Document, judged int) {
	kept := make([]packfile.TableFile, 0, len(doc.Tables))
	for _, tf := range doc.Tables {
		if tf.UsesJudge() {
			judged++
			continue
		}
		kept = append(kept, tf)
	}
	cp := *doc
	cp.Tables = kept
	return &cp, judged
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
