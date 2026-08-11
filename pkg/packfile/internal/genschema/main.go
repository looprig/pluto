// Command genschema regenerates pkg/packfile/schema.json from the evaluator
// registry. Run via `go generate ./pkg/packfile` (see schema.go's directive);
// it is a generator only and is never imported by any non-generated code.
package main

import (
	"log"
	"os"

	"github.com/looprig/pluto/pkg/packfile"
)

// out is schema.json's path relative to the working directory `go generate`
// sets when it runs this program's //go:generate directive: the directory
// containing schema.go, i.e. pkg/packfile itself.
const out = "schema.json"

func main() {
	raw, err := packfile.Schema(packfile.NewRegistry())
	if err != nil {
		log.Fatalf("genschema: %v", err)
	}
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		log.Fatalf("genschema: write %s: %v", out, err)
	}
}
