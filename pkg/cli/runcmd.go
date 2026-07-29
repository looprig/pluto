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
	concurrency := fs.Int("concurrency", 0, "run up to N tables in parallel (0/1 = sequential); provider load stays bounded by --max-concurrent-requests")
	out := fs.String("out", "mpqt-report.json", "reportjson output path")
	pf := registerPricingFlags(fs)
	rf := registerRateLimitFlags(fs)
	verbose := verboseFlag(fs)

	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}

	u := newUI(app.Stdout, app.LookupEnv, *verbose)

	// Set before resolving any client so both the target and judge clients
	// route through the same rate limiter.
	app.RateLimit = rf.config()

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

	u.title("run", fmt.Sprintf("%s · %s/%s · %d pack(s)",
		manifest.TargetID, manifest.Provider, manifest.Model, len(packDirs)))

	var judgeClient inference.Client
	var judgeModel model.Model
	if *configPath != "" {
		cfg, err := loadLLMConfig(*configPath)
		if err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run: load config:", err)
			return ExitCommandFailure
		}
		judgeModel = cfg.toModel()
		noteMissingKey(u, app, judgeModel.Provider)
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
	noteMissingKey(u, app, targetModel.Provider)
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
	cfg := eval.RunConfig{Trials: *trials}

	if !pf.skipCostEstimate {
		snap, err := loadSnapshot(ctx, app, pf.pricingSnapshot)
		if err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run: preflight:", err)
			return ExitCommandFailure
		}
		rates := ratesFor(snap, string(targetModel.Provider), targetModel.Name)
		counter := app.counterForPreflight(targetModel, u.detailW())
		plan, err := pricing.Preflight(ctx, allPlans, cfg, rates, counter, templates)
		if err != nil {
			fmt.Fprintln(app.Stderr, "mpqt run: preflight:", err)
			return ExitCommandFailure
		}
		renderPreflight(u, plan)
		if ok, code := gatePreflight(pf, plan, app.Stdout); !ok {
			return code
		}
	}

	runnable := 0
	for _, p := range allPlans {
		if p.Runnable {
			runnable++
		}
	}
	// Live pnpm-style viewport: a spinning running row over the last few
	// completed rows plus a progress footer, redrawn in place on a terminal
	// (plain append-only lines off one). Progress starts a table's row;
	// OnResult scrolls it into place with its pass/fail tally.
	vp := newViewport(app.Stdout, app.LookupEnv, runnable)
	runStart := app.Now()

	spec := run.Spec{
		Manifest:         manifest,
		Packs:            packs,
		Config:           cfg,
		TableConcurrency: *concurrency,
		TargetForTable: func(plan qual.TablePlan) (eval.Target, error) {
			return run.BuildTarget(targetClient, manifest, envs[plan.Table], plan.Suite.Revision)
		},
		Progress: func(plan qual.TablePlan) {
			if plan.Runnable {
				vp.start(string(plan.Pack)+"/"+string(plan.Table), len(plan.Suite.Scenarios))
			}
		},
		OnResult: func(plan qual.TablePlan, rep eval.Report) {
			vp.finish(string(plan.Pack)+"/"+string(plan.Table),
				rep.Summary.Assessments[eval.StatusPass], rep.Summary.Assessments[eval.StatusFail])
		},
	}

	res, err := run.Execute(ctx, spec)
	vp.close()
	if n := len(res.Skipped); n > 0 {
		u.info("%d table(s) skipped (missing required capabilities)", n)
		for _, sk := range res.Skipped {
			u.detail("skipped %s/%s — missing %v", sk.Pack, sk.Table, sk.Missing)
		}
	}
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt run: execute:", err)
		if len(res.Scorecard.Results) > 0 {
			if encoded, encErr := reportjson.Encode(res.Scorecard, nil); encErr == nil {
				if writeErr := os.WriteFile(*out, encoded, 0o600); writeErr == nil {
					u.warn("wrote partial report → %s", *out)
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

	renderRunReport(u, res.Scorecard, result, requireDisp, *out, app.Now().Sub(runStart))

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
