// Command pluto is the runnable CLI binary. It is the composition root that
// wires pkg/cli.App's injected-dependency fields to real implementations
// backed by github.com/looprig/llm/auto -- the only place in the entire Pluto
// repo that imports github.com/looprig/llm. Everything under pkg/ stays
// llm-free by design, taking inference.Client/pricing.Counter as injected
// interfaces instead.
package main

import (
	"os"
	"strings"
	"time"

	"github.com/looprig/inference"
	"github.com/looprig/inference/auth"
	"github.com/looprig/inference/model"
	"github.com/looprig/llm/auto"
	"github.com/looprig/pluto/pkg/cli"
	"github.com/looprig/pluto/pkg/packfile"
	"github.com/looprig/pluto/pkg/pricing"
)

func main() {
	app := cli.App{
		Registry:   packfile.NewRegistry(),
		NewClient:  newClient,
		NewCounter: newCounter,
		LookupEnv:  os.LookupEnv,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Now:        time.Now,
	}
	os.Exit(cli.Main(os.Args[1:], app))
}

// keyFor reads the provider's conventional API key environment variable:
// the provider name upper-cased with "-" -> "_" plus "_API_KEY". This
// mirrors pkg/cli's own providerEnvVar convention (see cli.go's doc
// comment on checkKeyPresence), so the note it prints when a key is absent
// names the same variable this function actually reads.
func keyFor(provider model.ProviderName) auth.APIKey {
	env := strings.ToUpper(strings.ReplaceAll(string(provider), "-", "_")) + "_API_KEY"
	return auth.APIKey(os.Getenv(env))
}

func newClient(m model.Model) (inference.Client, error) {
	return auto.New(m, keyFor(m.Provider))
}

func newCounter(m model.Model) (pricing.Counter, error) {
	c, err := auto.NewCounter(m, keyFor(m.Provider))
	if err != nil {
		return nil, err
	}
	return counterAdapter{counter: c}, nil
}
