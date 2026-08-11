package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/looprig/eval"
	"github.com/looprig/inference"
	"github.com/looprig/inference/model"
	"github.com/looprig/pluto/pkg/gen"
	"github.com/looprig/pluto/pkg/packfile"
	"github.com/looprig/pluto/pkg/pricing"
	"github.com/looprig/pluto/pkg/qual"
)

// cmdGen loads generator config, runs the preflight cost estimate (unless
// skipped), and -- unless the estimate gates the call or --dry-run stops it
// first -- calls gen.Generate + gen.Append (or prints to stdout with
// --no-write; JSONL to stdout with --raw).
func cmdGen(app App, args []string) int {
	fs := newFlagSet("gen", "gen --pack DIR --table NAME -n N --config FILE [options]")
	packDir := fs.String("pack", "", "pack directory to generate into (required)")
	table := fs.String("table", "", "table name within the pack (required)")
	n := fs.Int("n", 5, "number of candidate scenarios to generate")
	focus := fs.String("focus", "", "optional free-text steer for seeded generation")
	intent := fs.String("intent", "", "bootstrap-mode intent text (required when the table has no existing scenarios)")
	configPath := fs.String("config", "", "generator LLM config YAML (required)")
	noWrite := fs.Bool("no-write", false, "print the appended YAML to stdout instead of writing the table file")
	rawOut := fs.Bool("raw", false, "additionally print accepted candidates as JSONL to stdout")
	dryRun := fs.Bool("dry-run", false, "preflight only: estimate cost and stop before the paid call")
	pf := registerPricingFlags(fs)
	rf := registerRateLimitFlags(fs)
	verbose := verboseFlag(fs)

	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}

	u := newUI(app.Stdout, app.LookupEnv, *verbose)

	// Set before resolving the client so the generator call is rate-limited.
	app.RateLimit = rf.config()

	if *packDir == "" || *table == "" || *configPath == "" {
		fmt.Fprintln(app.Stderr, "pluto gen: --pack, --table, and --config are required")
		fs.Usage()
		return ExitUsage
	}
	if *n < 1 {
		fmt.Fprintln(app.Stderr, "pluto gen: -n must be at least 1")
		return ExitUsage
	}

	doc, err := packfile.LoadDir(*packDir)
	if err != nil {
		fmt.Fprintln(app.Stderr, "pluto gen:", err)
		return ExitCommandFailure
	}
	tf, filename, err := tableAndFile(doc, *table)
	if err != nil {
		fmt.Fprintln(app.Stderr, "pluto gen:", err)
		return ExitCommandFailure
	}

	cfg, err := loadLLMConfig(*configPath)
	if err != nil {
		fmt.Fprintln(app.Stderr, "pluto gen: load config:", err)
		return ExitCommandFailure
	}
	genModel := cfg.toModel()

	u.title("gen", fmt.Sprintf("%s/%s · %s/%s · n=%d", doc.Pack.Pack, *table, genModel.Provider, genModel.Name, *n))
	noteMissingKey(u, app, genModel.Provider)
	client, err := app.client(genModel)
	if err != nil {
		fmt.Fprintln(app.Stderr, "pluto gen:", err)
		return ExitCommandFailure
	}

	ctx := context.Background()

	if !pf.skipCostEstimate {
		plan, err := buildGenPreflight(ctx, app, pf, doc.Pack.Pack, tf, genModel)
		if err != nil {
			fmt.Fprintln(app.Stderr, "pluto gen: preflight:", err)
			return ExitCommandFailure
		}
		renderPreflight(u, plan)
		if ok, code := gatePreflight(pf, plan, app.Stdout); !ok {
			return code
		}
		if *dryRun {
			return ExitOK
		}
	} else if *dryRun {
		fmt.Fprintln(app.Stdout, "gen: --dry-run with --skip-cost-estimate: nothing to estimate, stopping before the paid call")
		return ExitOK
	}

	result, err := gen.Generate(ctx, client, gen.Request{
		Doc: doc, Table: *table, N: *n, Focus: *focus, Intent: *intent, Model: genModel,
	})
	if err != nil {
		fmt.Fprintln(app.Stderr, "pluto gen:", err)
		return ExitCommandFailure
	}

	u.ok("%d candidate(s) → %d accepted, %d rejected",
		len(result.Accepted)+len(result.Rejected), len(result.Accepted), len(result.Rejected))
	for _, r := range result.Rejected {
		u.detail("rejected %s: %s", r.ID, r.Reason)
	}

	if *rawOut {
		if err := writeRawJSONL(app.Stdout, result.Accepted); err != nil {
			fmt.Fprintln(app.Stderr, "pluto gen: raw:", err)
			return ExitCommandFailure
		}
	}

	if len(result.Accepted) == 0 {
		return ExitOK
	}

	generatedBy := genModel.Name + "/" + app.Now().Format("2006-01-02")
	rawBytes, ok := doc.Raw[filename]
	if !ok {
		fmt.Fprintf(app.Stderr, "pluto gen: internal: no raw bytes for table file %s\n", filename)
		return ExitCommandFailure
	}
	appended, err := gen.Append(filename, rawBytes, result.Accepted, generatedBy)
	if err != nil {
		fmt.Fprintln(app.Stderr, "pluto gen: append:", err)
		return ExitCommandFailure
	}

	if *noWrite {
		if _, err := app.Stdout.Write(appended); err != nil {
			fmt.Fprintln(app.Stderr, "pluto gen:", err)
			return ExitCommandFailure
		}
		return ExitOK
	}

	outPath := filepath.Join(doc.Dir, filename)
	if err := os.WriteFile(outPath, appended, 0o600); err != nil {
		fmt.Fprintln(app.Stderr, "pluto gen: write:", err)
		return ExitCommandFailure
	}
	u.ok("appended %d scenario(s) → %s", len(result.Accepted), outPath)
	return ExitOK
}

// tableAndFile locates the TableFile named table within doc, plus the raw
// filename it was loaded from (doc.Tables and doc.Pack.Tables share the same
// order -- Document.Load builds both slices in one pass over pf.Tables).
func tableAndFile(doc *packfile.Document, table string) (packfile.TableFile, string, error) {
	for i, tf := range doc.Tables {
		if tf.Table == table {
			return tf, doc.Pack.Tables[i], nil
		}
	}
	return packfile.TableFile{}, "", fmt.Errorf("table %q not found in pack %q", table, doc.Pack.Pack)
}

// buildGenPreflight estimates the cost of the one structured-output call
// gen.Generate will make: a single-scenario, single-trial synthetic
// qual.TablePlan (one call), templated from the table's own environment (the
// same tool schemas and system prompt gen's real prompt draws on) merged
// with genModel. It is a best-effort approximation of gen's actual prompt,
// not a byte-for-byte reconstruction of it -- pkg/gen exposes no
// build-prompt-without-invoking entry point to reuse instead.
func buildGenPreflight(ctx context.Context, app App, pf *pricingFlags, packName string, tf packfile.TableFile, m model.Model) (pricing.Plan, error) {
	req, err := tf.Environment.Template()
	if err != nil {
		return pricing.Plan{}, fmt.Errorf("environment template: %w", err)
	}
	req.Model = m

	tableName := eval.Name(tf.Table)
	plan := qual.TablePlan{
		Pack:      eval.Name(packName),
		Table:     tableName,
		Dimension: eval.Name(tf.Dimension),
		Runnable:  true,
		Suite: eval.Suite{
			Name:     tableName,
			Revision: eval.Revision(tf.Revision),
			Scenarios: []eval.Scenario{
				{ID: "gen-call", Name: tableName, Revision: eval.Revision(tf.Revision)},
			},
		},
	}

	snap, err := loadSnapshot(ctx, app, pf.pricingSnapshot)
	if err != nil {
		return pricing.Plan{}, err
	}
	rates := ratesFor(snap, string(m.Provider), m.Name)

	counter := app.counterForPreflight(m, app.Stdout)

	return pricing.Preflight(ctx, []qual.TablePlan{plan}, eval.RunConfig{}, rates, counter,
		map[eval.Name]inference.Request{tableName: req})
}

// genRawScenario is the JSONL wire shape --raw prints: a plain projection of
// packfile.ScenarioSpec (which carries only yaml tags) with json tags for a
// clean pipeline format.
type genRawScenario struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name,omitempty"`
	Input  []packfile.MessageSpec `json:"input"`
	Expect *packfile.ExpectSpec   `json:"expect,omitempty"`
	Labels map[string]string      `json:"labels,omitempty"`
}

func writeRawJSONL(w io.Writer, specs []packfile.ScenarioSpec) error {
	enc := json.NewEncoder(w)
	for _, s := range specs {
		row := genRawScenario{ID: s.ID, Name: s.Name, Input: s.Input, Expect: s.Expect, Labels: s.Labels}
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	return nil
}
