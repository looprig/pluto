package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
	"github.com/looprig/inference"
	"github.com/looprig/mpqt/pkg/qual"
)

// heuristicQuality labels a token estimate produced without a real Counter:
// bytes-of-content / bytesPerToken. It is deliberately coarse and always
// says so, so a caller never mistakes it for a model-aware count.
const heuristicQuality = "heuristic"

// bytesPerToken is the divisor for the heuristic token estimate.
const bytesPerToken = 4

// Counter abstracts the llm context counter (design: llm-free root module).
// A real implementation lives in the CLI module, which alone may import an
// llm tokenizer; pricing only depends on this narrow interface. quality is a
// free-form, implementation-defined label (e.g. the tokenizer's name/version)
// surfaced to the caller via Plan.CounterQuality so an estimate's provenance
// is never silently lost.
type Counter interface {
	Count(ctx context.Context, req inference.Request) (tokens int, quality string, err error)
}

// Plan is the preflight cost plan printed before paid inference. TargetCalls
// and JudgeCalls are exact (derived from scenario/trial/evaluator counts,
// never estimated); InputTokens and OutputTokens are [expected, max]
// estimates in whole tokens; Expected and Max price those totals against the
// caller's Rates; CounterQuality names the token-counting method actually
// used ("heuristic" when counter was nil); Unknowns lists, in encounter
// order, every table or judge evaluator whose token contribution could not
// be estimated (missing template, no declared output cap) — never silently
// dropped.
type Plan struct {
	TargetCalls, JudgeCalls int
	InputTokens             [2]int // [expected, max]
	OutputTokens            [2]int // [expected, max]
	Expected, Max           Amount
	CounterQuality          string
	Unknowns                []string
}

// Preflight builds the plan for a set of runnable table plans: target calls
// are scenarios × trials per table, summed across plans; judge calls are
// scenarios × trials × (evaluators on that table whose Descriptor().Method
// is eval.MethodModel), summed across plans. Method is the signal used to
// identify a "judge" evaluator because it is the one thing eval.Evaluator
// exposes for this purpose (Method's own doc calls it out for "cost
// accounting"): every model judge built by eval/judge.New declares
// MethodModel, and every programmatic evaluator in eval/exact declares
// MethodProgrammatic; there is no exported marker or type assertion that
// distinguishes "judge" more directly without depending on the judge
// package's unexported concrete type.
//
// A non-runnable plan (Runnable=false) contributes nothing: qual.Plan leaves
// its Suite and Evaluators at their zero value, so it has no scenarios to
// count and is skipped explicitly here for clarity.
//
// templates supplies the inference.Request used to estimate token cost for
// one call, keyed by the identity of whatever makes that call: a table's own
// Table name for its target calls, and a judge evaluator's Descriptor().Name
// for its judge calls (the CLI, which constructs both the live target and
// each judge in Task 12, is positioned to supply both keys from the same
// templates it already built). A missing key means that portion of the plan
// cannot be estimated; Preflight records it in Unknowns and excludes it from
// the token totals rather than guessing.
//
// counter nil ⇒ every per-call input-token estimate falls back to a
// heuristic (content bytes / 4) and Plan.CounterQuality is "heuristic". A
// non-nil counter's own reported quality is used instead, and any error it
// returns aborts Preflight: a broken counter is not a number to route around
// silently before a paid run.
//
// The per-call output-token estimate (used for both the expected and max
// arms — no generation has happened yet to observe a real distribution) is
// read from the request's own declared cap: Override.MaxTokens, else
// Model.Sampling.MaxTokens, else Model.Limits.MaxOutputTokens. When none of
// those is set, the call's output contribution is recorded as an Unknown
// rather than invented. The per-call max *input* estimate additionally
// widens to Model.Limits.MaxInputTokens when that is a known, larger ceiling
// than the counted/heuristic estimate.
func Preflight(ctx context.Context, plans []qual.TablePlan, cfg eval.RunConfig, rates Rates, counter Counter, templates map[eval.Name]inference.Request) (Plan, error) {
	if err := cfg.Validate(); err != nil {
		return Plan{}, fmt.Errorf("pricing: Preflight: invalid RunConfig: %w", err)
	}
	trials := effectiveTrials(cfg)

	p := Plan{}
	if counter == nil {
		p.CounterQuality = heuristicQuality
	}

	var inExpected, inMax, outExpected, outMax int

	for _, plan := range plans {
		if !plan.Runnable {
			continue
		}
		scenarios := len(plan.Suite.Scenarios)
		if scenarios == 0 {
			continue
		}
		calls := scenarios * trials
		p.TargetCalls += calls

		judges := judgeDescriptors(plan.Evaluators)
		p.JudgeCalls += calls * len(judges)

		in, inMx, out, outMx, unknowns, err := p.estimateTable(ctx, plan, calls, judges, rates, counter, templates)
		if err != nil {
			return Plan{}, err
		}
		inExpected += in
		inMax += inMx
		outExpected += out
		outMax += outMx
		p.Unknowns = append(p.Unknowns, unknowns...)
	}

	p.InputTokens = [2]int{inExpected, inMax}
	p.OutputTokens = [2]int{outExpected, outMax}
	p.Expected = Cost(Usage{Input: inExpected, Output: outExpected, Complete: true}, rates)
	p.Max = Cost(Usage{Input: inMax, Output: outMax, Complete: true}, rates)

	return p, nil
}

// estimateTable estimates the token contribution of one runnable table: its
// own target calls, plus one contribution per judge evaluator found on it.
// It returns the four running totals to add to the plan and any Unknown
// notes generated along the way; p is used only to track/update
// CounterQuality (the last quality seen from a real Counter call wins).
func (p *Plan) estimateTable(ctx context.Context, plan qual.TablePlan, calls int, judges []eval.Descriptor, rates Rates, counter Counter, templates map[eval.Name]inference.Request) (inExpected, inMax, outExpected, outMax int, unknowns []string, err error) {
	if req, ok := templates[plan.Table]; ok {
		in, inMx, out, outOK, quality, cerr := estimateCall(ctx, req, counter)
		if cerr != nil {
			return 0, 0, 0, 0, nil, fmt.Errorf("pricing: Preflight: table %s: counter: %w", plan.Table, cerr)
		}
		if counter != nil {
			p.CounterQuality = quality
		}
		inExpected += in * calls
		inMax += inMx * calls
		if outOK {
			outExpected += out * calls
			outMax += out * calls
		} else {
			unknowns = append(unknowns, fmt.Sprintf(
				"table %s: no declared output-token cap; output estimate for %d target call(s) excluded", plan.Table, calls))
		}
	} else {
		unknowns = append(unknowns, fmt.Sprintf(
			"table %s: no request template provided; token estimate for %d target call(s) excluded", plan.Table, calls))
	}

	for _, desc := range judges {
		req, ok := templates[desc.Name]
		if !ok {
			unknowns = append(unknowns, fmt.Sprintf(
				"judge %s (table %s): no request template provided; token estimate for %d judge call(s) excluded", desc.Name, plan.Table, calls))
			continue
		}
		in, inMx, out, outOK, quality, cerr := estimateCall(ctx, req, counter)
		if cerr != nil {
			return 0, 0, 0, 0, nil, fmt.Errorf("pricing: Preflight: judge %s: counter: %w", desc.Name, cerr)
		}
		if counter != nil {
			p.CounterQuality = quality
		}
		inExpected += in * calls
		inMax += inMx * calls
		if outOK {
			outExpected += out * calls
			outMax += out * calls
		} else {
			unknowns = append(unknowns, fmt.Sprintf(
				"judge %s (table %s): no declared output-token cap; output estimate for %d judge call(s) excluded", desc.Name, plan.Table, calls))
		}
	}

	return inExpected, inMax, outExpected, outMax, unknowns, nil
}

// judgeDescriptors returns the descriptors of every non-nil evaluator in
// evaluators whose Method is eval.MethodModel — this table's judge calls.
func judgeDescriptors(evaluators []eval.Evaluator) []eval.Descriptor {
	var out []eval.Descriptor
	for _, ev := range evaluators {
		if ev == nil {
			continue
		}
		if desc := ev.Descriptor(); desc.Method == eval.MethodModel {
			out = append(out, desc)
		}
	}
	return out
}

// effectiveTrials returns cfg's effective per-scenario trial count, applying
// the same one-trial default eval.RunConfig itself applies internally
// (RunConfig.Trials's own doc: "A value of 0 means one trial"). cfg.Validate
// has already rejected a negative value by the time this runs.
func effectiveTrials(cfg eval.RunConfig) int {
	if cfg.Trials <= 0 {
		return 1
	}
	return cfg.Trials
}

// estimateCall estimates one call's token cost from req: inputExpected is
// counter's count when counter is non-nil, else the heuristic byte/4
// estimate; inputMax widens to Model.Limits.MaxInputTokens when that is a
// known, larger ceiling. output/outputKnown is the request's own declared
// output cap (see Preflight's doc for the lookup order); outputKnown is
// false when nothing is declared, and the caller must not invent a number.
func estimateCall(ctx context.Context, req inference.Request, counter Counter) (inputExpected, inputMax, output int, outputKnown bool, quality string, err error) {
	if counter != nil {
		tokens, q, cerr := counter.Count(ctx, req)
		if cerr != nil {
			return 0, 0, 0, false, "", cerr
		}
		inputExpected = tokens
		quality = q
	} else {
		inputExpected = heuristicTokens(req)
		quality = heuristicQuality
	}

	inputMax = inputExpected
	if lim := tokenCountToInt(req.Model.Limits.MaxInputTokens); lim > inputMax {
		inputMax = lim
	}

	output, outputKnown = outputCap(req)
	return inputExpected, inputMax, output, outputKnown, quality, nil
}

// outputCap reads req's own declared output-token ceiling: a per-call
// Override.MaxTokens, else the model's default Sampling.MaxTokens, else the
// model's advertised Limits.MaxOutputTokens. ok is false when none of those
// is set.
func outputCap(req inference.Request) (cap int, ok bool) {
	if req.Override != nil && req.Override.MaxTokens != nil {
		return *req.Override.MaxTokens, true
	}
	if req.Model.Sampling.MaxTokens != nil {
		return *req.Model.Sampling.MaxTokens, true
	}
	if req.Model.Limits.MaxOutputTokens > 0 {
		return tokenCountToInt(req.Model.Limits.MaxOutputTokens), true
	}
	return 0, false
}

// tokenCountToInt safely narrows a content.TokenCount (uint64) to int,
// clamping to math.MaxInt instead of wrapping when the declared count
// exceeds what an int can represent on this platform. A model that
// advertises an absurd context limit is not this package's concern to
// reject; it is enough that the estimate never becomes silently negative.
func tokenCountToInt(tc content.TokenCount) int {
	if tc > content.TokenCount(math.MaxInt) {
		return math.MaxInt
	}
	return int(tc)
}

// heuristicTokens estimates req's input token count as (JSON-encoded bytes
// of its system prompt and message thread) / bytesPerToken. It deliberately
// marshals only System and Messages, not the full Request (Model metadata,
// tool schemas, and so on are not "content" in the sense this heuristic
// approximates). A marshal failure yields 0, never a panic or a fabricated
// count — heuristicQuality already tells the caller this number is coarse.
func heuristicTokens(req inference.Request) int {
	payload := struct {
		System   string                  `json:"system"`
		Messages content.AgenticMessages `json:"messages"`
	}{System: req.System, Messages: req.Messages}
	data, err := json.Marshal(payload)
	if err != nil {
		return 0
	}
	return len(data) / bytesPerToken
}
