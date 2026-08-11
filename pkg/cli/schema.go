package cli

import (
	"fmt"

	"github.com/looprig/pluto/pkg/packfile"
)

// cmdSchema prints packfile.Schema(app.Registry): the JSON Schema describing
// both pack.yaml and a table file, generated from the registry so it always
// reflects the running binary's actual set of evaluator kinds.
func cmdSchema(app App, args []string) int {
	fs := newFlagSet("schema", "schema")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintln(app.Stderr, "pluto schema: unexpected arguments")
		fs.Usage()
		return ExitUsage
	}

	data, err := packfile.Schema(app.Registry)
	if err != nil {
		fmt.Fprintln(app.Stderr, "pluto schema:", err)
		return ExitCommandFailure
	}
	if _, err := app.Stdout.Write(data); err != nil {
		return ExitCommandFailure
	}
	return ExitOK
}
