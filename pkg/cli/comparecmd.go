package cli

import (
	"fmt"
	"os"

	"github.com/looprig/mpqt/pkg/compare"
	"github.com/looprig/mpqt/pkg/qual"
	"github.com/looprig/mpqt/pkg/reportjson"
)

// cmdCompare decodes two reportjson.Encode-produced files and rolls their
// candidate-vs-incumbent diff up through pkg/compare, exiting 3 whenever any
// matched table carries a regression.
func cmdCompare(app App, args []string) int {
	fs := newFlagSet("compare", "compare --candidate FILE --incumbent FILE")
	candidatePath := fs.String("candidate", "", "candidate reportjson file (required)")
	incumbentPath := fs.String("incumbent", "", "incumbent reportjson file (required)")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}

	if *candidatePath == "" || *incumbentPath == "" {
		fmt.Fprintln(app.Stderr, "mpqt compare: --candidate and --incumbent are required")
		fs.Usage()
		return ExitUsage
	}

	candidate, err := decodeReport(*candidatePath)
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt compare:", err)
		return ExitCommandFailure
	}
	incumbent, err := decodeReport(*incumbentPath)
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt compare:", err)
		return ExitCommandFailure
	}

	cmp, err := compare.Compare(scorecardFromDecoded(candidate), scorecardFromDecoded(incumbent))
	if err != nil {
		fmt.Fprintln(app.Stderr, "mpqt compare:", err)
		return ExitCommandFailure
	}

	regressions := 0
	for _, tc := range cmp.Tables {
		fmt.Fprintf(app.Stdout, "compare: %s/%s regressed=%d improved=%d unchanged=%d incompatible=%d\n",
			tc.Pack, tc.Table, tc.Regressed, tc.Improved, tc.Unchanged, tc.Incompatible)
		regressions += tc.Regressed
	}
	for _, u := range cmp.UnmatchedTables {
		fmt.Fprintf(app.Stdout, "compare: unmatched %s/%s (%s)\n", u.Pack, u.Table, u.Side)
	}
	fmt.Fprintf(app.Stdout, "compare: total regressions=%d\n", regressions)

	if regressions > 0 {
		return ExitGateFailed
	}
	return ExitOK
}

func decodeReport(path string) (reportjson.Decoded, error) {
	data, err := os.ReadFile(cleanPath(path))
	if err != nil {
		return reportjson.Decoded{}, fmt.Errorf("read report %s: %w", path, err)
	}
	dec, err := reportjson.Decode(data)
	if err != nil {
		return reportjson.Decoded{}, fmt.Errorf("decode report %s: %w", path, err)
	}
	return dec, nil
}

// scorecardFromDecoded projects a decoded report back into the qual.Scorecard
// shape pkg/compare consumes; DecodedTable mirrors qual.TableResult
// field-for-field.
func scorecardFromDecoded(d reportjson.Decoded) qual.Scorecard {
	results := make([]qual.TableResult, len(d.Tables))
	for i, t := range d.Tables {
		results[i] = qual.TableResult{
			Pack: t.Pack, Table: t.Table, Dimension: t.Dimension,
			Skipped: t.Skipped, Missing: t.Missing, Report: t.Report,
		}
	}
	return qual.Scorecard{Manifest: d.Manifest, Results: results}
}
