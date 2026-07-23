package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/looprig/eval"
	"github.com/looprig/inference"
	"github.com/looprig/inference/model"
	"github.com/looprig/mpqt/pkg/packfile"
	"github.com/looprig/mpqt/pkg/pricing"
	"github.com/looprig/mpqt/pkg/profile"
	"github.com/looprig/mpqt/pkg/qual"
	"github.com/looprig/mpqt/pkg/reportjson"
	"github.com/looprig/mpqt/pkg/run"
)

// cmdRun loads a manifest and profile, builds every named pack (surfacing
// packfile.ErrJudgeUnconfigured before any paid call when a pack needs a
// judge and --config was not given), runs the preflight cost estimate
// (unless skipped), executes against a live target per table, writes the
// canonical reportjson report, evaluates the profile, and exits 3 unless the
// resulting disposition meets --require.
func cmdRun(app App, args []string) int {
	fs := newFlagSet("run", "run --manifest FILE --profile FILE --packs DIR[,DIR...] [options]")
	manifestPath := fs.String("manifest", "", "manifest YAML for the target under test (required)")
	profilePath := fs.String("profile", "", "profile YAML to evaluate the run against (required)")
	var packDirs stringList
	fs.Var(&packDirs, "packs", "pack directory to run (repeatable, or comma-separated; required)")
	require := fs.String("require", string(profile.Qualified), "minimum disposition required for exit 0 (qualified|restricted|unverified|rejected)")
	configPath := fs.String("config", "", "judge LLM config YAML (required only if a pack uses a judge evaluator)")
	trials := fs.Int("trials", 0, "trials per scenario (0 = eval default of 1)")
	concurrency := fs.Int("concurrency", 0, "maximum concurrent samples (0 = sequential)")
	out := fs.String("out", "mpqt-report.json", "reportjson output path")
	pf := registerPricingFlags(fs)

	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}

	if *manifestPath == "" || *profilePath == "" || len(packDirs) == 0 {
		fmt.Fprintln(app.Stderr, "mpqt run: --manifest, --profile, and at least one --packs are required")
		fs.Usage()
		return ExitUsage
	}
	requireDisp := profile.Disposition(*require)
	if requireDisp.Rank() < 0 {
		fmt.Fprintf(app.Stderr, "mpqt run: --require %q is not a known disposition\n", *require)
		return ExitUsage
	}

	manifest, err := decodeManifestFile(*manifestPath)
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt run:", err)
		return ExitCommandFailure
	}
	prof, err := decodeProfileFile(*profilePath)
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt run:", err)
		return ExitCommandFailure
	}

	var judgeClient inference.Client
	var judgeModel model.Model
	if *configPath != "" {
		cfg, err := loadLLMConfig(*configPath)
		if err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run: load config:", err)
			return ExitCommandFailure
		}
		judgeModel = cfg.toModel()
		checkKeyPresence(app, app.Stdout, judgeModel.Provider)
		judgeClient, err = app.client(judgeModel)
		if err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run:", err)
			return ExitCommandFailure
		}
	}
	bc := packfile.BuildContext{JudgeClient: judgeClient, JudgeTemplate: inference.Request{Model: judgeModel}}

	type loadedPack struct {
		doc  *packfile.Document
		pack qual.Pack
	}
	loaded := make([]loadedPack, 0, len(packDirs))
	for _, dir := range packDirs {
		doc, err := packfile.LoadDir(dir)
		if err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run:", err)
			return ExitCommandFailure
		}
		pack, err := doc.Build(app.Registry, bc)
		if err != nil {
			if errors.Is(err, packfile.ErrJudgeUnconfigured) {
				fmt.Fprintf(app.Stderr, "mpqt run: %s: %v (supply --config with a judge llm block)\n", dir, err)
			} else {
				fmt.Fprintf(app.Stderr, "mpqt run: %s: %v\n", dir, err)
			}
			return ExitCommandFailure
		}
		loaded = append(loaded, loadedPack{doc: doc, pack: pack})
	}

	targetModel := run.ManifestModel(manifest)
	checkKeyPresence(app, app.Stdout, targetModel.Provider)
	targetClient, err := app.client(targetModel)
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt run:", err)
		return ExitCommandFailure
	}

	envs := map[eval.Name]*packfile.Environment{}
	packs := make([]qual.Pack, 0, len(loaded))
	var allPlans []qual.TablePlan
	templates := map[eval.Name]inference.Request{}
	for _, lp := range loaded {
		packs = append(packs, lp.pack)
		for _, tf := range lp.doc.Tables {
			envs[eval.Name(tf.Table)] = tf.Environment
		}
		plans, err := qual.Plan(lp.pack, manifest)
		if err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run:", err)
			return ExitCommandFailure
		}
		allPlans = append(allPlans, plans...)
		if err := addTemplates(templates, envs, plans, targetModel, judgeModel); err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run:", err)
			return ExitCommandFailure
		}
	}

	ctx := context.Background()
	cfg := eval.RunConfig{Trials: *trials, Concurrency: *concurrency}

	if !pf.skipCostEstimate {
		snap, err := loadSnapshot(ctx, app, pf.pricingSnapshot)
		if err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run: preflight:", err)
			return ExitCommandFailure
		}
		rates := ratesFor(snap, string(targetModel.Provider), targetModel.Name)
		counter, err := app.counter(targetModel)
		if err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run: preflight:", err)
			return ExitCommandFailure
		}
		plan, err := pricing.Preflight(ctx, allPlans, cfg, rates, counter, templates)
		if err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run: preflight:", err)
			return ExitCommandFailure
		}
		printPlan(app.Stdout, "run", plan)
		if ok, code := gatePreflight(pf, plan, app.Stdout); !ok {
			return code
		}
	}

	spec := run.Spec{
		Manifest: manifest,
		Packs:    packs,
		Config:   cfg,
		TargetForTable: func(plan qual.TablePlan) (eval.Target, error) {
			return run.BuildTarget(targetClient, manifest, envs[plan.Table], plan.Suite.Revision)
		},
	}

	res, err := run.Execute(ctx, spec)
	for _, sk := range res.Skipped {
		fmt.Fprintf(app.Stdout, "run: skipped %s/%s: missing %v\n", sk.Pack, sk.Table, sk.Missing)
	}
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt run: execute:", err)
		if len(res.Scorecard.Results) > 0 {
			if encoded, encErr := reportjson.Encode(res.Scorecard, nil); encErr == nil {
				if writeErr := os.WriteFile(*out, encoded, 0o600); writeErr == nil {
					fmt.Fprintf(app.Stdout, "run: wrote partial report to %s\n", *out)
				}
			}
		}
		return ExitCommandFailure
	}

	result, err := profile.Evaluate(res.Scorecard, prof)
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt run: evaluate profile:", err)
		return ExitCommandFailure
	}

	encoded, err := reportjson.Encode(res.Scorecard, &result)
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt run: encode report:", err)
		return ExitCommandFailure
	}
	if err := os.WriteFile(*out, encoded, 0o600); err != nil {
		fmt.Fprintln(app.Stderr, "mpqt run: write report:", err)
		return ExitCommandFailure
	}

	fmt.Fprintf(app.Stdout, "run: disposition=%s (require=%s) report=%s\n", result.Disposition, requireDisp, *out)

	if result.Disposition.Rank() < requireDisp.Rank() {
		return ExitGateFailed
	}
	return ExitOK
}

// addTemplates fills templates with one entry per runnable table's live
// target request (for pricing's target-call estimate) and one entry per
// judge evaluator found on a runnable table (for pricing's judge-call
// estimate), keyed exactly as pricing.Preflight documents: a table's own
// name, and a judge evaluator's Descriptor().Name.
func addTemplates(templates map[eval.Name]inference.Request, envs map[eval.Name]*packfile.Environment, plans []qual.TablePlan, targetModel, judgeModel model.Model) error {
	for _, plan := range plans {
		if !plan.Runnable {
			continue
		}
		req, err := envs[plan.Table].Template()
		if err != nil {
			return fmt.Errorf("table %s: environment: %w", plan.Table, err)
		}
		req.Model = targetModel
		templates[plan.Table] = req

		for _, ev := range plan.Evaluators {
			if ev == nil {
				continue
			}
			if d := ev.Descriptor(); d.Method != eval.MethodProgrammatic {
				if _, exists := templates[d.Name]; !exists {
					templates[d.Name] = inference.Request{Model: judgeModel}
				}
			}
		}
	}
	return nil
}

func decodeManifestFile(path string) (qual.Manifest, error) {
	f, err := os.Open(cleanPath(path))
	if err != nil {
		return qual.Manifest{}, fmt.Errorf("open manifest %s: %w", path, err)
	}
	defer f.Close()
	return run.DecodeManifest(f)
}

func decodeProfileFile(path string) (profile.Profile, error) {
	f, err := os.Open(cleanPath(path))
	if err != nil {
		return profile.Profile{}, fmt.Errorf("open profile %s: %w", path, err)
	}
	defer f.Close()
	return run.DecodeProfile(f)
}
