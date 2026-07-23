package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looprig/core/content"
	"github.com/looprig/inference"
	"github.com/looprig/inference/model"
	"github.com/looprig/inference/stream"
	"github.com/looprig/mpqt/pkg/cli"
	"github.com/looprig/mpqt/pkg/pricing"
)

// --- shared test fixtures used across every *_test.go file in this package ---

// testApp is a bytes.Buffer-backed cli.App plus convenience accessors for
// its captured Stdout/Stderr, so every command test can assert on exact
// output without touching the real terminal.
type testApp struct {
	cli.App
	Out, Err *bytes.Buffer
}

// newTestApp returns a testApp with buffers wired in and Registry/Now/
// LookupEnv left at their zero value: Main's own withDefaults must supply
// working defaults for every one of those (App{} zero value contract).
func newTestApp() *testApp {
	out, errW := &bytes.Buffer{}, &bytes.Buffer{}
	return &testApp{
		App: cli.App{Stdout: out, Stderr: errW},
		Out: out, Err: errW,
	}
}

// fakeClient is a minimal, canned inference.Client: Invoke returns a fixed
// *inference.Response (or a fixed error) and records every request it saw.
// Stream is never exercised by any command in this package and panics if
// called, exactly like pkg/run and pkg/gen's own test fixtures.
type fakeClient struct {
	resp  *inference.Response
	err   error
	calls []inference.Request
}

func (f *fakeClient) Invoke(_ context.Context, req inference.Request) (*inference.Response, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func (f *fakeClient) Stream(context.Context, inference.Request) (*stream.StreamReader[content.Chunk], error) {
	panic("fakeClient: Stream not implemented")
}

var _ inference.Client = (*fakeClient)(nil)

// assistantText builds a canned *inference.Response carrying a single
// assistant text block, mirroring pkg/gen's own test fixture shape.
func assistantText(text string) *inference.Response {
	return &inference.Response{
		Message: &content.AIMessage{Message: content.Message{
			Role:   content.RoleAssistant,
			Blocks: []content.Block{&content.TextBlock{Text: text}},
		}},
		FinishReason: stream.FinishReasonStop,
	}
}

// clientAppOption wires app.NewClient to always return client, regardless of
// the model it is asked to build one for.
func withClient(app *testApp, client inference.Client) {
	app.NewClient = func(model.Model) (inference.Client, error) { return client, nil }
}

// fakeCounter is a canned pricing.Counter: every call reports the same fixed
// token count and quality label.
type fakeCounter struct {
	tokens  int
	quality string
	err     error
}

func (c fakeCounter) Count(context.Context, inference.Request) (int, string, error) {
	return c.tokens, c.quality, c.err
}

var _ pricing.Counter = fakeCounter{}

func withCounter(app *testApp, c pricing.Counter) {
	app.NewCounter = func(model.Model) (pricing.Counter, error) { return c, nil }
}

// writeFile writes content to path, creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// --- a minimal, valid, ready-to-load pack fixture shared by validate/gen/run tests ---

const testPackYAML = "pack: testpack\nrevision: v1\ntables:\n  - t1.yaml\n"

// testTableYAML declares one table, no target capability requirements, a
// single required-text evaluator, and one scenario -- small enough to reason
// about in every test, real enough to Build and Execute.
const testTableYAML = `table: t1
revision: v1
dimension: capability
requires: []
environment:
  system: You are a helpful assistant.
evaluators:
  - kind: required-text
    substrings: ["ok"]
scenarios:
  - id: s1
    input:
      - role: user
        text: Say ok.
`

// writeTestPack writes testPackYAML/testTableYAML into dir and returns dir.
func writeTestPack(t *testing.T, dir string) string {
	t.Helper()
	writeFile(t, filepath.Join(dir, "pack.yaml"), testPackYAML)
	writeFile(t, filepath.Join(dir, "t1.yaml"), testTableYAML)
	return dir
}

const testManifestYAML = `target-id: candidate-1
role: candidate
provider: test
model: fake-model
api-format: openai
base-url: https://example.invalid/v1
revision: r1
endpoint-class: remote
capabilities: []
`

// testProfileYAML requires a perfect capability score: a conforming target
// (one whose replies satisfy every evaluator) is Qualified; a
// non-conforming one is Rejected.
const testProfileYAML = `name: enterprise
revision: v1
requirements:
  - dimension: capability
    min-score: 100
`

const testLLMConfigYAML = `llm:
  provider: test
  model: fake-model
`

// --- Main dispatch / exit-code tests ---

func TestMainNoArgsReturnsUsage(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	if code := cli.Main(nil, app.App); code != cli.ExitUsage {
		t.Fatalf("Main(nil) = %d, want %d", code, cli.ExitUsage)
	}
	if app.Err.Len() == 0 {
		t.Error("Main(nil): expected usage text on Stderr")
	}
}

func TestMainTopLevelHelp(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"-h", "--help", "help"} {
		app := newTestApp()
		if code := cli.Main([]string{flag}, app.App); code != cli.ExitOK {
			t.Fatalf("Main([%q]) = %d, want %d", flag, code, cli.ExitOK)
		}
		if !strings.Contains(app.Out.String(), "commands:") {
			t.Errorf("Main([%q]): Stdout missing usage body: %q", flag, app.Out.String())
		}
	}
}

func TestMainUnknownCommand(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	if code := cli.Main([]string{"bogus"}, app.App); code != cli.ExitUsage {
		t.Fatalf("Main([bogus]) = %d, want %d", code, cli.ExitUsage)
	}
	if !strings.Contains(app.Err.String(), "bogus") {
		t.Errorf("Stderr = %q, want it to name the unknown command", app.Err.String())
	}
}

// TestAppZeroValueRunsNonLLMCommands proves App{} (only Stdout/Stderr set,
// everything else left at its zero value) is enough to run every command
// that doesn't need a real LLM client.
func TestAppZeroValueRunsNonLLMCommands(t *testing.T) {
	t.Parallel()

	t.Run("schema", func(t *testing.T) {
		t.Parallel()
		app := newTestApp()
		if code := cli.Main([]string{"schema"}, app.App); code != cli.ExitOK {
			t.Fatalf("schema: code = %d, stderr = %s", code, app.Err.String())
		}
		if !strings.Contains(app.Out.String(), `"pack.yaml"`) && !strings.Contains(app.Out.String(), "mpqt pack file") {
			t.Errorf("schema: Stdout doesn't look like a JSON Schema: %s", app.Out.String())
		}
	})

	t.Run("evaluators", func(t *testing.T) {
		t.Parallel()
		app := newTestApp()
		if code := cli.Main([]string{"evaluators"}, app.App); code != cli.ExitOK {
			t.Fatalf("evaluators: code = %d, stderr = %s", code, app.Err.String())
		}
		if !strings.Contains(app.Out.String(), "required-text") {
			t.Errorf("evaluators: Stdout missing a known kind: %s", app.Out.String())
		}
	})

	t.Run("init", func(t *testing.T) {
		t.Parallel()
		app := newTestApp()
		dir := t.TempDir()
		if code := cli.Main([]string{"init", "my-assistant", dir}, app.App); code != cli.ExitOK {
			t.Fatalf("init: code = %d, stderr = %s", code, app.Err.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "my-assistant", "pack.yaml")); err != nil {
			t.Errorf("init: pack.yaml not written: %v", err)
		}
	})

	t.Run("validate", func(t *testing.T) {
		t.Parallel()
		app := newTestApp()
		dir := writeTestPack(t, t.TempDir())
		if code := cli.Main([]string{"validate", dir}, app.App); code != cli.ExitOK {
			t.Fatalf("validate: code = %d, stdout = %s stderr = %s", code, app.Out.String(), app.Err.String())
		}
	})

	t.Run("compare bad flags is a usage error not a panic", func(t *testing.T) {
		t.Parallel()
		app := newTestApp()
		if code := cli.Main([]string{"compare"}, app.App); code != cli.ExitUsage {
			t.Fatalf("compare: code = %d, want %d", code, cli.ExitUsage)
		}
	})
}

// TestGenWithNilClientReturnsClearError proves a nil App.NewClient produces a
// clear, non-panicking error (never a nil-pointer dereference) rather than
// silently doing nothing.
func TestGenWithNilClientReturnsClearError(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := writeTestPack(t, t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "gen.yaml")
	writeFile(t, cfgPath, testLLMConfigYAML)

	code := cli.Main([]string{
		"gen", "--pack", dir, "--table", "t1", "-n", "1",
		"--config", cfgPath, "--skip-cost-estimate", "--no-write",
	}, app.App)
	if code != cli.ExitCommandFailure {
		t.Fatalf("gen with nil NewClient: code = %d, want %d; stderr=%s", code, cli.ExitCommandFailure, app.Err.String())
	}
	if app.Err.Len() == 0 {
		t.Error("gen with nil NewClient: expected a clear error on Stderr")
	}
}

// TestRunWithNilClientReturnsClearError mirrors the gen case for run.
func TestRunWithNilClientReturnsClearError(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := t.TempDir()
	writeTestPack(t, dir)
	manifestPath := filepath.Join(dir, "manifest.yaml")
	profilePath := filepath.Join(dir, "profile.yaml")
	writeFile(t, manifestPath, testManifestYAML)
	writeFile(t, profilePath, testProfileYAML)

	code := cli.Main([]string{
		"run", "--manifest", manifestPath, "--profile", profilePath, "--packs", dir,
		"--skip-cost-estimate", "--out", filepath.Join(dir, "report.json"),
	}, app.App)
	if code != cli.ExitCommandFailure {
		t.Fatalf("run with nil NewClient: code = %d, want %d; stderr=%s", code, cli.ExitCommandFailure, app.Err.String())
	}
	if app.Err.Len() == 0 {
		t.Error("run with nil NewClient: expected a clear error on Stderr")
	}
}

// TestSecretsNeverEchoed drives gen and run with an App.LookupEnv that
// returns a canary "secret" for every name, and asserts the literal canary
// value never appears anywhere in Stdout or Stderr across a whole battery of
// commands -- a real grep over captured output, not a code-inspection
// argument.
func TestSecretsNeverEchoed(t *testing.T) {
	t.Parallel()
	const canary = "sk-test-canary-12345"

	run := func(t *testing.T, args []string) *testApp {
		app := newTestApp()
		app.LookupEnv = func(string) (string, bool) { return canary, true }
		cli.Main(args, app.App)
		return app
	}

	dir := t.TempDir()
	writeTestPack(t, dir)
	cfgPath := filepath.Join(dir, "gen.yaml")
	writeFile(t, cfgPath, testLLMConfigYAML)
	manifestPath := filepath.Join(dir, "manifest.yaml")
	profilePath := filepath.Join(dir, "profile.yaml")
	writeFile(t, manifestPath, testManifestYAML)
	writeFile(t, profilePath, testProfileYAML)

	cases := []struct {
		name string
		args []string
	}{
		{"schema", []string{"schema"}},
		{"evaluators", []string{"evaluators"}},
		{"validate", []string{"validate", dir}},
		{
			"gen-no-client", []string{
				"gen", "--pack", dir, "--table", "t1", "-n", "1", "--config", cfgPath,
				"--skip-cost-estimate", "--no-write",
			},
		},
		{
			"run-no-client", []string{
				"run", "--manifest", manifestPath, "--profile", profilePath, "--packs", dir,
				"--skip-cost-estimate", "--out", filepath.Join(dir, "report.json"),
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			app := run(t, tt.args)
			if strings.Contains(app.Out.String(), canary) {
				t.Errorf("%s: Stdout leaked the canary secret: %s", tt.name, app.Out.String())
			}
			if strings.Contains(app.Err.String(), canary) {
				t.Errorf("%s: Stderr leaked the canary secret: %s", tt.name, app.Err.String())
			}
		})
	}
}
