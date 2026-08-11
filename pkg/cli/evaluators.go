package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
)

// cmdEvaluators prints one row per registered evaluator kind: its name, the
// option names read from that kind's OptionsSchema properties, its evidence
// requirement, and its doc string -- the vocabulary a pack author needs
// (design: "pluto evaluators is where a pack author learns the vocabulary").
func cmdEvaluators(app App, args []string) int {
	fs := newFlagSet("evaluators", "evaluators")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintln(app.Stderr, "pluto evaluators: unexpected arguments")
		fs.Usage()
		return ExitUsage
	}

	w := tabwriter.NewWriter(app.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "KIND\tOPTIONS\tEVIDENCE\tDOC")
	for _, k := range app.Registry.Kinds() {
		opts, err := optionNames(k.OptionsSchema)
		if err != nil {
			fmt.Fprintln(app.Stderr, "pluto evaluators:", err)
			return ExitCommandFailure
		}
		optStr := "-"
		if len(opts) > 0 {
			optStr = strings.Join(opts, ",")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k.Name, optStr, k.Evidence, k.Doc)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(app.Stderr, "pluto evaluators:", err)
		return ExitCommandFailure
	}
	return ExitOK
}

// optionNames reads the top-level property names of a Kind's OptionsSchema
// JSON Schema fragment, sorted for deterministic output.
func optionNames(raw json.RawMessage) ([]string, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("decode evaluator options schema: %w", err)
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
