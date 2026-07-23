// Package compare rolls up an eval/compare diff between a candidate and an
// incumbent MPQT scorecard, aligned by (Pack, Table). It performs no trial
// pairing of its own: github.com/looprig/eval/compare already classifies each
// (scenario, evaluator) case, retaining every per-trial result; this package
// only aligns tables across the two scorecards and rolls the retained case
// classification up per table.
package compare

import (
	"sort"

	"github.com/looprig/eval"
	evalcompare "github.com/looprig/eval/compare"
	"github.com/looprig/mpqt/pkg/qual"
)

// Side names why a table failed to align between the two scorecards.
type Side string

const (
	// SideCandidateOnly: the table appears in the candidate scorecard but not
	// the incumbent's.
	SideCandidateOnly Side = "candidate-only"
	// SideIncumbentOnly: the table appears in the incumbent scorecard but not
	// the candidate's.
	SideIncumbentOnly Side = "incumbent-only"
	// SideSkipped: the table's (Pack, Table) key is present on both sides but
	// at least one side skipped it (missing capability), so there is no
	// executed report to compare.
	SideSkipped Side = "skipped"
)

// Validate reports whether s is a known Side.
func (s Side) Validate() error {
	switch s {
	case SideCandidateOnly, SideIncumbentOnly, SideSkipped:
		return nil
	}
	return &qual.ValidationError{Field: "Side", Reason: "unknown value"}
}

// UnmatchedTable is a (Pack, Table) that could not be compared, with the
// reason it was excluded. It is never silently dropped from a Comparison.
type UnmatchedTable struct {
	Pack, Table eval.Name
	Side        Side
}

// TableComparison is one matched (Pack, Table) pair's rolled-up diff. Result
// is the full github.com/looprig/eval/compare output, retained intact.
// Regressed, Improved, Unchanged, and Incompatible are per-case counts over
// Result.Cases; see classifyCounts for the exact mapping from CaseClass (and,
// for Regressed/Improved, the per-side trial outcome) to these buckets. A
// case that is CaseAdded, CaseRemoved, or a CaseChanged/CaseFailed/
// CaseErrored/CaseUnverified case that doesn't fit the Regressed/Improved
// definition below is not tallied into any of the four counts; it remains
// visible in Result.Cases.
type TableComparison struct {
	Pack, Table, Dimension eval.Name
	Result                 evalcompare.Comparison
	Regressed              int
	Improved               int
	Unchanged              int
	Incompatible           int
}

// Comparison is the full candidate-vs-incumbent diff: matched tables plus
// every table that could not be matched.
type Comparison struct {
	Candidate       qual.Manifest
	Incumbent       qual.Manifest
	Tables          []TableComparison
	UnmatchedTables []UnmatchedTable
}

// RoleMismatchError reports that a manifest's role did not match the role
// required of its position in the comparison (candidate or incumbent).
type RoleMismatchError struct {
	Field string
	Role  qual.ModelRole
	Want  qual.ModelRole
}

func (e *RoleMismatchError) Error() string {
	return "compare: " + e.Field + " has role " + string(e.Role) + ", want " + string(e.Want)
}

type tableKey struct {
	Pack, Table eval.Name
}

// Compare validates both manifests (structurally, and that candidate carries
// qual.RoleCandidate and incumbent carries qual.RoleIncumbent) before any
// comparison work happens, then aligns tables by (Pack, Table) key. A table
// present on only one side, or skipped on either side, surfaces in
// UnmatchedTables rather than being dropped. Every remaining matched pair is
// diffed with evalcompare.Compare (baseline=incumbent, candidate=candidate)
// and rolled up per table.
func Compare(candidate, incumbent qual.Scorecard) (Comparison, error) {
	if err := candidate.Manifest.Validate(); err != nil {
		return Comparison{}, err
	}
	if err := incumbent.Manifest.Validate(); err != nil {
		return Comparison{}, err
	}
	if candidate.Manifest.Role != qual.RoleCandidate {
		return Comparison{}, &RoleMismatchError{Field: "candidate.Manifest.Role", Role: candidate.Manifest.Role, Want: qual.RoleCandidate}
	}
	if incumbent.Manifest.Role != qual.RoleIncumbent {
		return Comparison{}, &RoleMismatchError{Field: "incumbent.Manifest.Role", Role: incumbent.Manifest.Role, Want: qual.RoleIncumbent}
	}

	candByKey := indexResults(candidate.Results)
	incByKey := indexResults(incumbent.Results)
	keys := unionTableKeys(candByKey, incByKey)

	out := Comparison{Candidate: candidate.Manifest, Incumbent: incumbent.Manifest}
	for _, k := range keys {
		c, cok := candByKey[k]
		i, iok := incByKey[k]
		switch {
		case !cok:
			out.UnmatchedTables = append(out.UnmatchedTables, UnmatchedTable{Pack: k.Pack, Table: k.Table, Side: SideIncumbentOnly})
			continue
		case !iok:
			out.UnmatchedTables = append(out.UnmatchedTables, UnmatchedTable{Pack: k.Pack, Table: k.Table, Side: SideCandidateOnly})
			continue
		case c.Skipped || i.Skipped:
			out.UnmatchedTables = append(out.UnmatchedTables, UnmatchedTable{Pack: k.Pack, Table: k.Table, Side: SideSkipped})
			continue
		}
		tc, err := compareTable(c, i)
		if err != nil {
			return Comparison{}, err
		}
		out.Tables = append(out.Tables, tc)
	}

	sort.Slice(out.Tables, func(a, b int) bool {
		if out.Tables[a].Pack != out.Tables[b].Pack {
			return out.Tables[a].Pack < out.Tables[b].Pack
		}
		return out.Tables[a].Table < out.Tables[b].Table
	})
	sort.Slice(out.UnmatchedTables, func(a, b int) bool {
		if out.UnmatchedTables[a].Pack != out.UnmatchedTables[b].Pack {
			return out.UnmatchedTables[a].Pack < out.UnmatchedTables[b].Pack
		}
		return out.UnmatchedTables[a].Table < out.UnmatchedTables[b].Table
	})
	return out, nil
}

func indexResults(results []qual.TableResult) map[tableKey]qual.TableResult {
	out := make(map[tableKey]qual.TableResult, len(results))
	for _, r := range results {
		out[tableKey{Pack: r.Pack, Table: r.Table}] = r
	}
	return out
}

func unionTableKeys(a, b map[tableKey]qual.TableResult) []tableKey {
	seen := make(map[tableKey]struct{}, len(a)+len(b))
	keys := make([]tableKey, 0, len(a)+len(b))
	for k := range a {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	for k := range b {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Pack != keys[j].Pack {
			return keys[i].Pack < keys[j].Pack
		}
		return keys[i].Table < keys[j].Table
	})
	return keys
}

// compareTable diffs one matched (Pack, Table) pair. incumbent is the
// baseline; candidate is the candidate, matching evalcompare.Compare's
// (baseline, candidate) argument order.
func compareTable(candidate, incumbent qual.TableResult) (TableComparison, error) {
	result, err := evalcompare.Compare(incumbent.Report, candidate.Report)
	if err != nil {
		return TableComparison{}, err
	}
	tc := TableComparison{
		Pack: candidate.Pack, Table: candidate.Table, Dimension: candidate.Dimension,
		Result: result,
	}
	for _, cc := range result.Cases {
		tallyCase(&tc, cc)
	}
	return tc, nil
}

// statusRank orders eval.AssessmentStatus from least to most serious, mirroring
// the unexported `outcome` ranking in github.com/looprig/eval/compare
// (compare.go: "outcome ranks a side's assessments into a single
// representative status using the precedence error > unverified > fail >
// pass > skipped"). CaseComparison does not expose that ranking, so it is
// reproduced here to derive a side's overall pass/fail outcome from its
// retained per-trial TrialResult slice.
var statusRank = map[eval.AssessmentStatus]int{
	eval.StatusError:      5,
	eval.StatusUnverified: 4,
	eval.StatusFail:       3,
	eval.StatusPass:       2,
	eval.StatusSkipped:    1,
}

// trialOutcome reduces a side's per-trial results to one representative
// status using statusRank, exactly as eval/compare's classify does when
// deciding a case's class.
func trialOutcome(trials []evalcompare.TrialResult) eval.AssessmentStatus {
	best := eval.AssessmentStatus("")
	bestRank := 0
	for _, tr := range trials {
		if r := statusRank[tr.Status]; r > bestRank {
			bestRank = r
			best = tr.Status
		}
	}
	return best
}

// tallyCase folds one case's classification into a table's rollup counts.
//
// The mapping was verified against github.com/looprig/eval/compare's
// classify() (compare/compare.go): CaseClass alone tells you whether the
// CANDIDATE side is currently error/unverified/failed/passed, but not whether
// the BASELINE was previously passing — that requires inspecting the
// retained per-trial Baseline/Candidate slices. The chosen mapping:
//
//   - Regressed: the candidate case classified as CaseErrored, CaseUnverified,
//     or CaseFailed (candidate no longer passes) AND the baseline's own
//     trial outcome was StatusPass. A pre-existing baseline failure is not a
//     fresh regression.
//   - Improved: the candidate case classified as CaseChanged (which, per
//     classify, only happens once the candidate side's outcome is
//     StatusPass) AND the baseline's trial outcome was NOT StatusPass. A
//     CaseChanged case where both sides already passed (distribution moved
//     but the verdict didn't) is a real diff but not a pass/fail improvement,
//     so it is deliberately left untallied here — it remains visible via
//     Result.Cases.
//   - Unchanged: CaseUnchanged, verbatim.
//   - Incompatible: CaseIncompatible, verbatim.
//
// CaseAdded and CaseRemoved (no counterpart on one side) are not tallied into
// any of the four counts.
func tallyCase(tc *TableComparison, cc evalcompare.CaseComparison) {
	switch cc.Class {
	case evalcompare.CaseIncompatible:
		tc.Incompatible++
	case evalcompare.CaseUnchanged:
		tc.Unchanged++
	case evalcompare.CaseErrored, evalcompare.CaseUnverified, evalcompare.CaseFailed:
		if trialOutcome(cc.Baseline) == eval.StatusPass {
			tc.Regressed++
		}
	case evalcompare.CaseChanged:
		if trialOutcome(cc.Baseline) != eval.StatusPass {
			tc.Improved++
		}
	}
}
