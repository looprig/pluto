package cli_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/looprig/pluto/pkg/cli"
	"github.com/looprig/pluto/pkg/packfile"
)

func TestSchemaPrintsPackfileSchema(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	if code := cli.Main([]string{"schema"}, app.App); code != cli.ExitOK {
		t.Fatalf("schema: code = %d, stderr = %s", code, app.Err.String())
	}

	want, err := packfile.Schema(packfile.NewRegistry())
	if err != nil {
		t.Fatalf("packfile.Schema: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(app.Out.Bytes()), bytes.TrimSpace(want)) {
		t.Errorf("schema output does not match packfile.Schema(NewRegistry())")
	}

	var doc map[string]any
	if err := json.Unmarshal(app.Out.Bytes(), &doc); err != nil {
		t.Fatalf("schema output is not valid JSON: %v", err)
	}
}

func TestSchemaRejectsExtraArgs(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	if code := cli.Main([]string{"schema", "unexpected"}, app.App); code != cli.ExitUsage {
		t.Fatalf("schema with extra args: code = %d, want %d", code, cli.ExitUsage)
	}
}

func TestSchemaHelp(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	if code := cli.Main([]string{"schema", "-h"}, app.App); code != cli.ExitOK {
		t.Fatalf("schema -h: code = %d", code)
	}
	if app.Out.Len() == 0 {
		t.Error("schema -h: expected usage text on Stdout")
	}
}
