// Package cli implements every mpqt command against injected dependencies.
// It is llm-free; cmd/mpqt (the nested module) supplies the constructors
// (App.NewClient, App.NewCounter) that reach a real inference.Client and
// pricing.Counter. This package never imports github.com/looprig/llm and
// never constructs a client or counter itself -- it only ever calls the
// functions App carries.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/looprig/eval"
	"github.com/looprig/inference"
	"github.com/looprig/inference/model"
	"github.com/looprig/mpqt/pkg/packfile"
	"github.com/looprig/mpqt/pkg/pricing"
	"github.com/looprig/mpqt/pkg/profile"
	"github.com/looprig/mpqt/pkg/qual"
	"github.com/looprig/mpqt/pkg/ratelimit"
)

// Process exit codes, per Main's own doc comment: 0 ok; 1 command failure; 2
// usage; 3 disposition/comparison gate failed; 4 cost ceiling or
// pricing-completeness failure.
const (
	ExitOK             = 0
	ExitCommandFailure = 1
	ExitUsage          = 2
	ExitGateFailed     = 3
	ExitPricing        = 4
)

// App wires the environment-specific pieces. Every field has a working
// zero-cost default except the client constructors, which nil out LLM
// commands with a clear error: App{} is enough to run init/validate/schema/
// evaluators/compare (see withDefaults), but gen/run need NewClient (and,
// for a real token estimate rather than the heuristic fallback, NewCounter).
type App struct {
	Registry       *packfile.Registry
	NewClient      func(model.Model) (inference.Client, error) // nil => gen/run/judge unavailable
	NewCounter     func(model.Model) (pricing.Counter, error)  // nil => heuristic preflight
	LookupEnv      func(string) (string, bool)                 // for key presence checks (never values in output)
	Stdout, Stderr io.Writer
	Now            func() time.Time

	// RateLimit configures client-side rate limiting (RPM pacing, concurrency
	// cap, 429/5xx retry-with-backoff) applied to every live client this App
	// hands out. The zero value disables it (a pure passthrough). The paid
	// commands set it from their --max-rpm/--max-retries/--max-concurrent-
	// requests flags before resolving any client, so both the target and the
	// judge client route through the same limiter.
	RateLimit ratelimit.Config
}

// client resolves a live inference.Client for m via App.NewClient, wrapped in
// the App's rate limiter, or a clear, non-panicking error when no constructor
// was supplied. ratelimit.New is a no-op passthrough when RateLimit is unset,
// so an unconfigured App pays no wrapping cost.
func (a App) client(m model.Model) (inference.Client, error) {
	if a.NewClient == nil {
		return nil, errors.New("mpqt: no LLM client configured (App.NewClient is nil); this command needs cmd/mpqt or another composition root that supplies one")
	}
	c, err := a.NewClient(m)
	if err != nil {
		return nil, err
	}
	return ratelimit.New(c, a.RateLimit), nil
}

// counter resolves a pricing.Counter for m via App.NewCounter. A nil
// NewCounter is a legal configuration: it degrades every preflight estimate
// to pricing's own heuristic (recorded as such in Plan.CounterQuality), never
// an error.
func (a App) counter(m model.Model) (pricing.Counter, error) {
	if a.NewCounter == nil {
		return nil, nil
	}
	return a.NewCounter(m)
}

// verboseFlag registers the shared --verbose/-v flag that reveals ui.detail
// output (per-table pricing caveats, missing-key notes, the full skip list).
// Default off: a normal run prints only the useful summary.
func verboseFlag(fs *flag.FlagSet) *bool {
	v := fs.Bool("verbose", false, "show all detail instead of a concise summary")
	fs.BoolVar(v, "v", false, "shorthand for --verbose")
	return v
}

// noteMissingKey emits a verbose-only note when a provider's conventional API
// key env var is unset. It is detail, not a default line: a genuinely missing
// key for a key-required provider surfaces as a clear auth error at the first
// call, and a keyless provider (a local LM Studio endpoint) needs none.
func noteMissingKey(u *ui, app App, provider model.ProviderName) {
	if app.LookupEnv == nil || !u.verboseEnabled() {
		return
	}
	name := providerEnvVar(provider)
	if _, ok := app.LookupEnv(name); !ok {
		u.detail("%s is not set in the environment", name)
	}
}

// counterForPreflight resolves a pricing counter for m, degrading to the
// byte-heuristic (a nil Counter, which pricing.Preflight records as
// CounterQuality "heuristic") with a note when no exact token counter can be
// built for the provider/format. A preflight cost estimate is best-effort and
// must never abort a run: a provider without an exact context counter (e.g. a
// local LM Studio endpoint) still runs, it just gets a lower-quality estimate.
// A nil NewCounter (heuristic by configuration) is silent; only a genuine
// construction error prints the note.
func (a App) counterForPreflight(m model.Model, w io.Writer) pricing.Counter {
	c, err := a.counter(m)
	if err != nil {
		fmt.Fprintf(w, "note: exact token counter unavailable for provider %q (%v); cost estimate falls back to a byte heuristic\n", m.Provider, err)
		return nil
	}
	return c
}

// withDefaults fills every field of App that has a working zero-cost
// default, so App{} (or a partially populated App) is enough to run every
// command that doesn't need a real LLM client.
func withDefaults(app App) App {
	if app.Registry == nil {
		app.Registry = packfile.NewRegistry()
	}
	if app.Stdout == nil {
		app.Stdout = os.Stdout
	}
	if app.Stderr == nil {
		app.Stderr = os.Stderr
	}
	if app.Now == nil {
		app.Now = time.Now
	}
	if app.LookupEnv == nil {
		app.LookupEnv = os.LookupEnv
	}
	return app
}

// Main parses args and dispatches. Returns the process exit code:
// 0 ok; 1 command failure; 2 usage; 3 disposition/comparison gate failed;
// 4 cost ceiling or pricing-completeness failure.
func Main(args []string, app App) int {
	app = withDefaults(app)

	if len(args) == 0 {
		printTopUsage(app.Stderr)
		return ExitUsage
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "-h", "--help", "-help", "help":
		printTopUsage(app.Stdout)
		return ExitOK
	case "init":
		return cmdInit(app, rest)
	case "validate":
		return cmdValidate(app, rest)
	case "schema":
		return cmdSchema(app, rest)
	case "evaluators":
		return cmdEvaluators(app, rest)
	case "gen":
		return cmdGen(app, rest)
	case "run":
		return cmdRun(app, rest)
	case "compare":
		return cmdCompare(app, rest)
	default:
		fmt.Fprintf(app.Stderr, "mpqt: unknown command %q\n", cmd)
		printTopUsage(app.Stderr)
		return ExitUsage
	}
}

func printTopUsage(w io.Writer) {
	fmt.Fprint(w, `usage: mpqt <command> [flags]

commands:
  init <name> [dir]   scaffold a custom pack directory
  validate [dir...]   strict load + lint + digest check (+ optional --execute)
  schema              print the pack file JSON Schema
  evaluators          list evaluator kinds, options, and evidence requirements
  gen                 generate candidate scenarios for one table
  run                 execute packs against a live target and gate on a profile
  compare             gate a candidate report against an incumbent report

Run 'mpqt <command> -h' for command-specific flags.
`)
}

// --- shared flag-parsing helpers, used by every subcommand ---

// newFlagSet builds the one flag.FlagSet a subcommand parses its own flags
// with. Usage is fixed at construction so both the explicit -h path and a
// flag.Parse error print the same synopsis to whichever writer parseFlags
// pointed fs.Output() at.
func newFlagSet(name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: mpqt %s\n", synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// hasHelpFlag reports whether args contains an explicit help request. It is
// checked before flag.Parse so a bare "-h" never has to survive strict flag
// parsing to be recognized.
func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "-help" {
			return true
		}
	}
	return false
}

// parseFlags parses args with fs. An explicit help request prints fs's usage
// to app.Stdout and returns (0, false): the design's "-h prints usage to
// Stdout" rule. Any other parse failure prints to app.Stderr (flag's own
// error plus fs.Usage) and returns (ExitUsage, false). ok is true only when
// the caller should proceed to run the command with its own parsed flags.
func parseFlags(app App, fs *flag.FlagSet, args []string) (code int, ok bool) {
	if hasHelpFlag(args) {
		fs.SetOutput(app.Stdout)
		fs.Usage()
		return ExitOK, false
	}
	fs.SetOutput(app.Stderr)
	if err := fs.Parse(args); err != nil {
		return ExitUsage, false
	}
	return 0, true
}

// stringList is a repeatable flag.Value: each occurrence of the flag (or a
// comma-separated value) appends one or more entries.
type stringList []string

func (s *stringList) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(*s, ",")
}

func (s *stringList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			*s = append(*s, part)
		}
	}
	return nil
}

// --- generator/judge LLM config, shared by gen and run ---

// LLMConfig is the generator/judge configuration file shape: non-secret
// provider/model identity only. The API key comes only from the provider's
// own environment variable (never this file, never a CLI flag) -- see
// checkKeyPresence.
type LLMConfig struct {
	LLM struct {
		Provider  string `yaml:"provider"`
		Model     string `yaml:"model"`
		APIFormat string `yaml:"api-format"`
		BaseURL   string `yaml:"base-url"`
	} `yaml:"llm"`
}

// toModel converts the config into the model.Model identity gen/run construct
// a client or counter from. Named toModel (not model) to avoid shadowing the
// imported model package it constructs a value from.
func (c LLMConfig) toModel() model.Model {
	return model.Model{
		Provider:  model.ProviderName(c.LLM.Provider),
		APIFormat: model.APIFormat(c.LLM.APIFormat),
		BaseURL:   c.LLM.BaseURL,
		Name:      c.LLM.Model,
	}
}

// loadLLMConfig strictly decodes path (via packfile.StrictDecode, reusing
// packfile's decode logic rather than importing gopkg.in/yaml.v3 here)
// into an LLMConfig.
func loadLLMConfig(path string) (LLMConfig, error) {
	f, err := os.Open(cleanPath(path))
	if err != nil {
		return LLMConfig{}, fmt.Errorf("cli: open llm config %s: %w", path, err)
	}
	defer f.Close()

	var cfg LLMConfig
	if err := packfile.StrictDecode(f, &cfg); err != nil {
		return LLMConfig{}, fmt.Errorf("cli: decode llm config %s: %w", path, err)
	}
	return cfg, nil
}

// --- provider API key presence (never value) ---

// providerEnvVar mirrors cmd/mpqt's own naming convention (Task 12 Step 4):
// the provider name upper-cased with "-" -> "_" plus "_API_KEY".
func providerEnvVar(provider model.ProviderName) string {
	return strings.ToUpper(strings.ReplaceAll(string(provider), "-", "_")) + "_API_KEY"
}

// --- preflight pricing flags, shared by gen and run ---

// pricingFlags is the common preflight flag set both gen and run register.
type pricingFlags struct {
	skipCostEstimate bool
	maxCostUSD       float64 // -1 = no ceiling configured
	pricingSnapshot  string
	requirePriced    bool
}

func registerPricingFlags(fs *flag.FlagSet) *pricingFlags {
	pf := &pricingFlags{maxCostUSD: -1}
	fs.BoolVar(&pf.skipCostEstimate, "skip-cost-estimate", false,
		"skip the preflight token/cost estimate entirely and proceed directly to the paid call")
	fs.Float64Var(&pf.maxCostUSD, "max-estimated-cost-usd", -1,
		"abort before any paid call if the estimated maximum cost exceeds this ceiling (USD)")
	fs.StringVar(&pf.pricingSnapshot, "pricing-snapshot", "",
		"a models.dev price snapshot: a local file path, or an https URL to fetch")
	fs.BoolVar(&pf.requirePriced, "require-priced", false,
		"abort before any paid call unless the estimate is fully priced (no unknown rate/dimension)")
	return pf
}

// defaultMaxRetries is the out-of-the-box retry count for paid commands.
// Retrying transient rate-limit/server failures is pure benefit, so it is on
// by default; --max-retries 0 turns it off.
const defaultMaxRetries = 4

// rateLimitFlags holds the client-side rate-limit knobs shared by run and gen.
type rateLimitFlags struct {
	maxRPM        int
	maxConcurrent int
	maxRetries    int
}

func registerRateLimitFlags(fs *flag.FlagSet) *rateLimitFlags {
	rf := &rateLimitFlags{}
	fs.IntVar(&rf.maxRPM, "max-rpm", 0,
		"cap requests per minute to the provider, spaced evenly (0 = unlimited); applies to target and judge calls")
	fs.IntVar(&rf.maxConcurrent, "max-concurrent-requests", 0,
		"cap simultaneous in-flight provider requests (0 = unlimited)")
	fs.IntVar(&rf.maxRetries, "max-retries", defaultMaxRetries,
		"retry a rate-limited (HTTP 429) or transient 5xx/network failure this many times, with exponential backoff (0 = no retry)")
	return rf
}

// config projects the flags onto a ratelimit.Config. A negative --max-retries
// is clamped to 0 (disabled) rather than rejected, so the flag can never make
// a paid command abort before it starts.
func (rf *rateLimitFlags) config() ratelimit.Config {
	retries := rf.maxRetries
	if retries < 0 {
		retries = 0
	}
	return ratelimit.Config{
		MaxRPM:        rf.maxRPM,
		MaxConcurrent: rf.maxConcurrent,
		MaxRetries:    retries,
	}
}

// gatePreflight applies pf's ceiling/completeness rules to plan, printing an
// explanation to w and returning ok=false with the exit code the caller must
// return immediately whenever a rule is violated. Fail secure: an
// explicitly-requested guarantee (a ceiling, --require-priced) that cannot be
// proven because the estimate is incomplete is treated as a gate failure,
// never a silent pass.
func gatePreflight(pf *pricingFlags, plan pricing.Plan, w io.Writer) (ok bool, code int) {
	if pf.requirePriced && (!plan.Expected.Known || !plan.Max.Known) {
		fmt.Fprintf(w, "mpqt: --require-priced set but the cost estimate is incomplete (%s)\n", incompleteReason(plan))
		return false, ExitPricing
	}
	if pf.maxCostUSD >= 0 {
		if !plan.Max.Known {
			fmt.Fprintf(w, "mpqt: --max-estimated-cost-usd=%.4f set but the maximum cost estimate is unknown (%s)\n", pf.maxCostUSD, incompleteReason(plan))
			return false, ExitPricing
		}
		if plan.Max.USD > pf.maxCostUSD {
			fmt.Fprintf(w, "mpqt: estimated maximum cost $%.4f exceeds ceiling $%.4f\n", plan.Max.USD, pf.maxCostUSD)
			return false, ExitPricing
		}
	}
	return true, ExitOK
}

func incompleteReason(plan pricing.Plan) string {
	if !plan.Expected.Known && plan.Expected.Reason != "" {
		return plan.Expected.Reason
	}
	if !plan.Max.Known && plan.Max.Reason != "" {
		return plan.Max.Reason
	}
	return "unknown"
}

// renderPreflight prints the concise, styled preflight summary. The per-item
// pricing caveats (plan.Unknowns) are verbose-only: by default it prints just
// their count, so the summary stays a couple of lines instead of one per table.
func renderPreflight(u *ui, plan pricing.Plan) {
	u.step("preflight · %d target + %d judge call(s) · %s", plan.TargetCalls, plan.JudgeCalls, costSummary(plan))
	u.detail("input tokens ~%d–%d, output ~%d–%d, via %s counter",
		plan.InputTokens[0], plan.InputTokens[1], plan.OutputTokens[0], plan.OutputTokens[1], plan.CounterQuality)
	if n := len(plan.Unknowns); n > 0 {
		if u.verboseEnabled() {
			for _, un := range plan.Unknowns {
				u.detail("%s", un)
			}
		} else {
			u.info("%d pricing caveat(s) hidden — pass --verbose for detail", n)
		}
	}
}

// costSummary renders a compact cost estimate for the preflight line.
func costSummary(plan pricing.Plan) string {
	if !plan.Expected.Known && !plan.Max.Known {
		return "cost unknown"
	}
	return "est cost " + formatAmount(plan.Expected) + "–" + formatAmount(plan.Max)
}

// tableFailure names one failed scenario for the report's failures list.
type tableFailure struct{ table, scenario string }

// renderRunReport prints the final report once a run completes: a one-line
// totals summary, per-dimension scores, a concise failures list (full detail
// under --verbose), and the disposition badge with the report path.
func renderRunReport(u *ui, card qual.Scorecard, result profile.Result, require profile.Disposition, reportPath string, dur time.Duration) {
	var tables, scenarios, passed, failed, errs int
	var failures []tableFailure
	for _, tr := range card.Results {
		if tr.Skipped {
			continue
		}
		tables++
		s := tr.Report.Summary
		scenarios += s.Samples
		passed += s.Assessments[eval.StatusPass]
		failed += s.Assessments[eval.StatusFail]
		errs += s.Assessments[eval.StatusError] + s.TargetErrors
		for _, sm := range tr.Report.Samples {
			if sampleFailed(sm) {
				failures = append(failures, tableFailure{
					table:    string(tr.Pack) + "/" + string(tr.Table),
					scenario: sm.ScenarioID,
				})
			}
		}
	}

	u.blank()
	totals := fmt.Sprintf("%d tables · %d scenarios · %s · %s · %d errors",
		tables, scenarios,
		u.paint(ansiGreen, fmt.Sprintf("%d passed", passed)),
		u.paint(ansiRed, fmt.Sprintf("%d failed", failed)), errs)
	if failed > 0 || errs > 0 {
		u.fail("%s", totals)
	} else {
		u.ok("%s", totals)
	}
	u.info("time %s   ·   cost — (pricing not wired for this provider)", elapsed(dur))

	if dims, err := card.Dimensions(); err == nil && len(dims) > 0 {
		u.blank()
		u.line("", "", "%s", u.paint(ansiBold, "Dimensions"))
		for _, d := range dims {
			if d.Undecided {
				u.info("%-22s   undecided  (coverage %.0f%%)", d.Dimension, d.Coverage*100)
			} else {
				u.info("%-22s %6.1f     (coverage %.0f%%)", d.Dimension, d.Score, d.Coverage*100)
			}
		}
	}

	renderFailures(u, failures)

	u.blank()
	label, color := dispositionStyle(result.Disposition)
	summary := fmt.Sprintf("%s   required: %s   ·   report → %s", u.badge(color, label), require, reportPath)
	if result.Disposition.Rank() >= require.Rank() {
		u.ok("%s", summary)
	} else {
		u.fail("%s", summary)
	}
}

// maxListedFailures bounds the failures list in the default report; the rest
// are summarized as a count (every failure is always in the JSON report).
const maxListedFailures = 15

// renderFailures lists the failed scenarios, capped unless --verbose.
func renderFailures(u *ui, failures []tableFailure) {
	if len(failures) == 0 {
		return
	}
	u.blank()
	u.line("", "", "%s %s", u.paint(ansiBold, "Failures"),
		u.paint(ansiDim, fmt.Sprintf("(%d — see the report for assertion detail)", len(failures))))
	limit := len(failures)
	if !u.verboseEnabled() && limit > maxListedFailures {
		limit = maxListedFailures
	}
	for _, f := range failures[:limit] {
		u.fail("%-38s %s", f.table, u.paint(ansiDim, f.scenario))
	}
	if limit < len(failures) {
		u.info("…and %d more — pass --verbose to list them all", len(failures)-limit)
	}
}

// sampleFailed reports whether a sample is a failure: a target error, or any
// failing assessment.
func sampleFailed(sm eval.SampleReport) bool {
	if sm.TargetErr != nil {
		return true
	}
	for _, a := range sm.Assessments {
		if a.Status == eval.StatusFail {
			return true
		}
	}
	return false
}

// dispositionStyle maps a disposition onto its badge label and color.
func dispositionStyle(d profile.Disposition) (label, color string) {
	switch d {
	case profile.Qualified:
		return "QUALIFIED", ansiGreen
	case profile.Restricted:
		return "RESTRICTED", ansiYellow
	case profile.Unverified:
		return "UNVERIFIED", ansiYellow
	case profile.Rejected:
		return "REJECTED", ansiRed
	default:
		return string(d), ""
	}
}

func formatAmount(a pricing.Amount) string {
	if !a.Known {
		return "unknown (" + a.Reason + ")"
	}
	return fmt.Sprintf("$%.4f", a.USD)
}

// isURL reports whether s looks like an http(s) URL rather than a local
// file path.
func isURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// cleanPath sanitizes an operator-supplied file path before it is opened.
func cleanPath(p string) string {
	return filepath.Clean(p)
}

// loadSnapshot resolves a pricing snapshot from a --pricing-snapshot flag
// value: empty yields the zero Snapshot (every rate lookup then misses,
// which ratesFor turns into an honest all-unknown pricing.Rates); an
// http(s) URL is fetched with pricing.FetchSnapshot (already
// redirect-safe); anything else is read as a local file and parsed with
// pricing.ParseSnapshot.
func loadSnapshot(ctx context.Context, app App, path string) (pricing.Snapshot, error) {
	if path == "" {
		return pricing.Snapshot{}, nil
	}
	if isURL(path) {
		return pricing.FetchSnapshot(ctx, nil, path)
	}
	clean := cleanPath(path)
	// #nosec G304 -- path is an operator-supplied --pricing-snapshot value;
	// reading a file the operator names on their own filesystem is this
	// command's whole job, not a privilege-boundary crossing.
	data, err := os.ReadFile(clean)
	if err != nil {
		return pricing.Snapshot{}, fmt.Errorf("cli: read pricing snapshot %s: %w", path, err)
	}
	return pricing.ParseSnapshot(data, "file://"+clean, app.Now())
}

// ratesFor looks up snap's row for provider/modelName. A miss (including a
// zero Snapshot with a nil Rows map) yields the zero pricing.Rates: every
// dimension nil, i.e. honestly unknown rather than free.
func ratesFor(snap pricing.Snapshot, provider, modelName string) pricing.Rates {
	if snap.Rows == nil {
		return pricing.Rates{}
	}
	if r, ok := snap.Rows[provider+"/"+modelName]; ok {
		return r
	}
	return pricing.Rates{}
}
