// Package reportjson is MPQT's canonical, versioned JSON codec for a
// Scorecard plus an optional profile.Result. It embeds each table's raw
// eval.Report as the bytes produced by eval's own reportjson.Encode — already
// redacted and canonical — so this codec adds no additional lossiness of its
// own beyond what eval's codec already imposes. Decode is the untrusted
// deserialization boundary: unknown fields and an unknown version are
// rejected fail-closed, and the input is size-bounded before it is parsed.
package reportjson

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"

	"github.com/looprig/eval"
	evalreportjson "github.com/looprig/eval/reportjson"
	"github.com/looprig/mpqt/pkg/profile"
	"github.com/looprig/mpqt/pkg/qual"
)

// Version is the sole wire version this codec implements. It is read FIRST
// from every document; an unknown or missing version is rejected before the
// payload is trusted (fail-closed).
const Version = "mpqt-report/v1"

// MaxReportBytes bounds a document at the untrusted decode boundary, mirroring
// eval/reportjson.MaxReportBytes.
const MaxReportBytes = 64 << 20 // 64 MiB

// reasonMissingTableReport is added to the malformed-report reason vocabulary
// (beyond the set eval/reportjson uses) for a non-skipped table entry that
// carries no embedded report payload.
const reasonMissingTableReport = "missing table report payload"

// --- wire structs ------------------------------------------------------

type envelopeJSON struct {
	Version string          `json:"version"`
	Report  json.RawMessage `json:"report"`
}

type reportJSON struct {
	Manifest     manifestJSON       `json:"manifest"`
	Fingerprint  string             `json:"fingerprint"`
	Dimensions   []dimensionJSON    `json:"dimensions"`
	StatusRollup statusRollupJSON   `json:"status_rollup"`
	Tables       []tableEntryJSON   `json:"tables"`
	Profile      *profileResultJSON `json:"profile,omitempty"`
}

type manifestJSON struct {
	TargetID      string   `json:"target_id"`
	Role          string   `json:"role"`
	Provider      string   `json:"provider"`
	Model         string   `json:"model"`
	APIFormat     string   `json:"api_format"`
	BaseURL       string   `json:"base_url"`
	Effort        string   `json:"effort,omitempty"`
	Revision      string   `json:"revision"`
	EndpointClass string   `json:"endpoint_class"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

type dimensionJSON struct {
	Dimension     string  `json:"dimension"`
	Score         float64 `json:"score"`
	Coverage      float64 `json:"coverage"`
	Verdicts      int     `json:"verdicts"`
	Assessments   int     `json:"assessments"`
	SkippedTables int     `json:"skipped_tables"`
	Undecided     bool    `json:"undecided"`
}

type statusRollupJSON struct {
	Samples      int            `json:"samples"`
	TargetErrors int            `json:"target_errors"`
	ByStatus     map[string]int `json:"by_status,omitempty"`
}

type tableEntryJSON struct {
	Pack      string          `json:"pack"`
	Table     string          `json:"table"`
	Dimension string          `json:"dimension"`
	Skipped   bool            `json:"skipped"`
	Missing   []string        `json:"missing,omitempty"`
	Report    json.RawMessage `json:"report,omitempty"`
}

type requirementJSON struct {
	Dimension        string   `json:"dimension,omitempty"`
	MinScore         *float64 `json:"min_score,omitempty"`
	MinCoverage      *float64 `json:"min_coverage,omitempty"`
	FindingCode      string   `json:"finding_code,omitempty"`
	MaxFindingCount  *int     `json:"max_finding_count,omitempty"`
	Severity         string   `json:"severity,omitempty"`
	MaxSeverityCount *int     `json:"max_severity_count,omitempty"`
}

type requirementResultJSON struct {
	Requirement requirementJSON `json:"requirement"`
	Outcome     string          `json:"outcome"`
}

type restrictionJSON struct {
	Description string          `json:"description"`
	Requirement requirementJSON `json:"requirement"`
}

type restrictionResultJSON struct {
	Restriction restrictionJSON `json:"restriction"`
	Applied     bool            `json:"applied"`
}

type profileResultJSON struct {
	Profile      string                  `json:"profile"`
	Revision     string                  `json:"revision"`
	Disposition  string                  `json:"disposition"`
	Requirements []requirementResultJSON `json:"requirements,omitempty"`
	Restrictions []restrictionResultJSON `json:"restrictions,omitempty"`
}

// --- Decoded result type -------------------------------------------------

// Decoded is the strict reconstruction of one encoded document.
type Decoded struct {
	Version      string
	Manifest     qual.Manifest
	Fingerprint  string
	Dimensions   []qual.DimensionScore
	StatusRollup qual.StatusRollup
	Tables       []DecodedTable
	Profile      *profile.Result
}

// DecodedTable is one table entry's reconstruction. Report is the zero
// eval.Report when Skipped is true; otherwise it is the REDACTED projection
// eval's own reportjson.Decode returns (zero Observation, empty Finding
// messages) — see eval/reportjson's package doc.
type DecodedTable struct {
	Pack, Table, Dimension eval.Name
	Skipped                bool
	Missing                []qual.Capability
	Report                 eval.Report
}

// --- encode --------------------------------------------------------------

// Encode serializes card and the optional profile result to the canonical
// mpqt-report/v1 wire form. It validates card.Manifest first, so a
// structurally invalid manifest is rejected before any encoding work happens,
// then computes the dimension scores and status rollup (each of which itself
// fails on an empty Scorecard), then embeds each non-skipped table's raw
// report as the bytes eval's own reportjson.Encode produces. result may be
// nil; the wire form then omits the profile entry entirely. Encode is
// deterministic: the same card and result always produce identical bytes.
func Encode(card qual.Scorecard, result *profile.Result) ([]byte, error) {
	if err := card.Manifest.Validate(); err != nil {
		return nil, err
	}
	fingerprint, err := card.Manifest.Fingerprint()
	if err != nil {
		return nil, err
	}
	dims, err := card.Dimensions()
	if err != nil {
		return nil, err
	}
	rollup, err := card.StatusRollup()
	if err != nil {
		return nil, err
	}

	rj := reportJSON{
		Manifest:     projectManifest(card.Manifest),
		Fingerprint:  fingerprint,
		Dimensions:   projectDimensions(dims),
		StatusRollup: projectStatusRollup(rollup),
	}
	tables, err := projectTables(card.Results)
	if err != nil {
		return nil, err
	}
	rj.Tables = tables
	if result != nil {
		rj.Profile = projectProfileResult(*result)
	}

	payload, err := json.Marshal(rj)
	if err != nil {
		return nil, &EncodeError{Cause: err}
	}
	out, err := json.Marshal(envelopeJSON{Version: Version, Report: payload})
	if err != nil {
		return nil, &EncodeError{Cause: err}
	}
	if len(out) > MaxReportBytes {
		return nil, &ReportTooLargeError{Size: len(out), Max: MaxReportBytes}
	}
	return out, nil
}

func projectManifest(m qual.Manifest) manifestJSON {
	caps := make([]string, len(m.Capabilities))
	for i, c := range m.Capabilities {
		caps[i] = string(c)
	}
	return manifestJSON{
		TargetID:      m.TargetID,
		Role:          string(m.Role),
		Provider:      m.Provider,
		Model:         m.Model,
		APIFormat:     m.APIFormat,
		BaseURL:       m.BaseURL,
		Effort:        m.Effort,
		Revision:      string(m.Revision),
		EndpointClass: string(m.EndpointClass),
		Capabilities:  caps,
	}
}

func projectDimensions(dims []qual.DimensionScore) []dimensionJSON {
	out := make([]dimensionJSON, len(dims))
	for i, d := range dims {
		out[i] = dimensionJSON{
			Dimension: string(d.Dimension), Score: d.Score, Coverage: d.Coverage,
			Verdicts: d.Verdicts, Assessments: d.Assessments,
			SkippedTables: d.SkippedTables, Undecided: d.Undecided,
		}
	}
	return out
}

func projectStatusRollup(r qual.StatusRollup) statusRollupJSON {
	var byStatus map[string]int
	if len(r.ByStatus) > 0 {
		byStatus = make(map[string]int, len(r.ByStatus))
		for st, n := range r.ByStatus {
			byStatus[string(st)] = n
		}
	}
	return statusRollupJSON{Samples: r.Samples, TargetErrors: r.TargetErrors, ByStatus: byStatus}
}

func projectTables(results []qual.TableResult) ([]tableEntryJSON, error) {
	out := make([]tableEntryJSON, len(results))
	for i, res := range results {
		missing := make([]string, len(res.Missing))
		for j, c := range res.Missing {
			missing[j] = string(c)
		}
		te := tableEntryJSON{
			Pack: string(res.Pack), Table: string(res.Table), Dimension: string(res.Dimension),
			Skipped: res.Skipped, Missing: missing,
		}
		if !res.Skipped {
			reportBytes, err := evalreportjson.Encode(res.Report)
			if err != nil {
				return nil, &EncodeError{Cause: err}
			}
			te.Report = reportBytes
		}
		out[i] = te
	}
	return out, nil
}

func projectRequirement(r profile.Requirement) requirementJSON {
	return requirementJSON{
		Dimension: string(r.Dimension), MinScore: r.MinScore, MinCoverage: r.MinCoverage,
		FindingCode: string(r.FindingCode), MaxFindingCount: r.MaxFindingCount,
		Severity: string(r.Severity), MaxSeverityCount: r.MaxSeverityCount,
	}
}

func projectProfileResult(r profile.Result) *profileResultJSON {
	reqs := make([]requirementResultJSON, len(r.Requirements))
	for i, rr := range r.Requirements {
		reqs[i] = requirementResultJSON{Requirement: projectRequirement(rr.Requirement), Outcome: string(rr.Outcome)}
	}
	restrs := make([]restrictionResultJSON, len(r.Restrictions))
	for i, rr := range r.Restrictions {
		restrs[i] = restrictionResultJSON{
			Restriction: restrictionJSON{Description: rr.Restriction.Description, Requirement: projectRequirement(rr.Restriction.Requirement)},
			Applied:     rr.Applied,
		}
	}
	return &profileResultJSON{
		Profile: string(r.Profile), Revision: string(r.Revision), Disposition: string(r.Disposition),
		Requirements: reqs, Restrictions: restrs,
	}
}

// --- decode --------------------------------------------------------------

// Decode reads an mpqt-report/v1 document. It is the untrusted boundary:
// enforced in order, the size bound, valid UTF-8, exactly one JSON value (no
// trailing data), strict envelope decoding (no unknown fields), a known
// version, strict payload decoding (no unknown fields), and finally domain
// reconstruction — the manifest is re-validated, and each non-skipped table's
// embedded report is decoded via eval's OWN reportjson.Decode, which yields
// the redacted projection (zero Observation, empty Finding.Message).
func Decode(data []byte) (Decoded, error) {
	var zero Decoded

	if len(data) > MaxReportBytes {
		return zero, &ReportTooLargeError{Size: len(data), Max: MaxReportBytes}
	}
	if !utf8.Valid(data) {
		return zero, &MalformedReportError{Reason: reasonInvalidUTF8}
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var env envelopeJSON
	if err := dec.Decode(&env); err != nil {
		if isUnknownField(err) {
			return zero, &MalformedReportError{Reason: reasonUnknownField}
		}
		return zero, &MalformedReportError{Reason: reasonInvalidJSON}
	}
	if dec.More() {
		return zero, &MalformedReportError{Reason: reasonTrailingData}
	}

	if env.Version != Version {
		return zero, &UnknownVersionError{Version: safeVersionToken(env.Version)}
	}
	if len(env.Report) == 0 {
		return zero, &MalformedReportError{Reason: reasonEmptyReport}
	}

	pdec := json.NewDecoder(bytes.NewReader(env.Report))
	pdec.DisallowUnknownFields()
	var rj reportJSON
	if err := pdec.Decode(&rj); err != nil {
		if isUnknownField(err) {
			return zero, &MalformedReportError{Reason: reasonUnknownField}
		}
		return zero, &MalformedReportError{Reason: reasonInvalidJSON}
	}
	if pdec.More() {
		return zero, &MalformedReportError{Reason: reasonTrailingData}
	}

	return reconstruct(rj)
}

func reconstruct(rj reportJSON) (Decoded, error) {
	manifest, err := reconstructManifest(rj.Manifest)
	if err != nil {
		return Decoded{}, err
	}
	tables, err := reconstructTables(rj.Tables)
	if err != nil {
		return Decoded{}, err
	}
	var profileResult *profile.Result
	if rj.Profile != nil {
		pr, err := reconstructProfileResult(*rj.Profile)
		if err != nil {
			return Decoded{}, err
		}
		profileResult = &pr
	}
	return Decoded{
		Version:      Version,
		Manifest:     manifest,
		Fingerprint:  rj.Fingerprint,
		Dimensions:   reconstructDimensions(rj.Dimensions),
		StatusRollup: reconstructStatusRollup(rj.StatusRollup),
		Tables:       tables,
		Profile:      profileResult,
	}, nil
}

func reconstructManifest(mj manifestJSON) (qual.Manifest, error) {
	caps := make([]qual.Capability, len(mj.Capabilities))
	for i, c := range mj.Capabilities {
		caps[i] = qual.Capability(c)
	}
	m := qual.Manifest{
		TargetID: mj.TargetID, Role: qual.ModelRole(mj.Role), Provider: mj.Provider,
		Model: mj.Model, APIFormat: mj.APIFormat, BaseURL: mj.BaseURL, Effort: mj.Effort,
		Revision: eval.Revision(mj.Revision), EndpointClass: qual.EndpointClass(mj.EndpointClass),
		Capabilities: caps,
	}
	if err := m.Validate(); err != nil {
		return qual.Manifest{}, &InvalidReportError{Cause: err}
	}
	return m, nil
}

func reconstructDimensions(djs []dimensionJSON) []qual.DimensionScore {
	out := make([]qual.DimensionScore, len(djs))
	for i, d := range djs {
		out[i] = qual.DimensionScore{
			Dimension: eval.Name(d.Dimension), Score: d.Score, Coverage: d.Coverage,
			Verdicts: d.Verdicts, Assessments: d.Assessments,
			SkippedTables: d.SkippedTables, Undecided: d.Undecided,
		}
	}
	return out
}

func reconstructStatusRollup(rj statusRollupJSON) qual.StatusRollup {
	var byStatus map[eval.AssessmentStatus]int
	if len(rj.ByStatus) > 0 {
		byStatus = make(map[eval.AssessmentStatus]int, len(rj.ByStatus))
		for st, n := range rj.ByStatus {
			byStatus[eval.AssessmentStatus(st)] = n
		}
	}
	return qual.StatusRollup{Samples: rj.Samples, TargetErrors: rj.TargetErrors, ByStatus: byStatus}
}

func reconstructTables(tjs []tableEntryJSON) ([]DecodedTable, error) {
	out := make([]DecodedTable, len(tjs))
	for i, tj := range tjs {
		missing := make([]qual.Capability, len(tj.Missing))
		for j, c := range tj.Missing {
			mc := qual.Capability(c)
			if err := mc.Validate(); err != nil {
				return nil, &InvalidReportError{Cause: err}
			}
			missing[j] = mc
		}
		dt := DecodedTable{
			Pack: eval.Name(tj.Pack), Table: eval.Name(tj.Table), Dimension: eval.Name(tj.Dimension),
			Skipped: tj.Skipped, Missing: missing,
		}
		if !tj.Skipped {
			if len(tj.Report) == 0 {
				return nil, &MalformedReportError{Reason: reasonMissingTableReport}
			}
			report, err := evalreportjson.Decode(tj.Report)
			if err != nil {
				return nil, &InvalidReportError{Cause: err}
			}
			dt.Report = report
		}
		out[i] = dt
	}
	return out, nil
}

func reconstructRequirement(rj requirementJSON) profile.Requirement {
	return profile.Requirement{
		Dimension: eval.Name(rj.Dimension), MinScore: rj.MinScore, MinCoverage: rj.MinCoverage,
		FindingCode: eval.FindingCode(rj.FindingCode), MaxFindingCount: rj.MaxFindingCount,
		Severity: eval.Severity(rj.Severity), MaxSeverityCount: rj.MaxSeverityCount,
	}
}

func reconstructProfileResult(pj profileResultJSON) (profile.Result, error) {
	reqs := make([]profile.RequirementResult, len(pj.Requirements))
	for i, rr := range pj.Requirements {
		reqs[i] = profile.RequirementResult{Requirement: reconstructRequirement(rr.Requirement), Outcome: profile.Outcome(rr.Outcome)}
	}
	restrs := make([]profile.RestrictionResult, len(pj.Restrictions))
	for i, rr := range pj.Restrictions {
		restrs[i] = profile.RestrictionResult{
			Restriction: profile.Restriction{Description: rr.Restriction.Description, Requirement: reconstructRequirement(rr.Restriction.Requirement)},
			Applied:     rr.Applied,
		}
	}
	return profile.Result{
		Profile: eval.Name(pj.Profile), Revision: eval.Revision(pj.Revision),
		Disposition: profile.Disposition(pj.Disposition), Requirements: reqs, Restrictions: restrs,
	}, nil
}

// isUnknownField reports whether a json decode error was an unknown-field
// rejection from DisallowUnknownFields. encoding/json signals this only via
// the error string, so this is the one place a string match is unavoidable;
// it classifies the codec's OWN decoder output, never untrusted content.
func isUnknownField(err error) bool {
	return err != nil && bytes.Contains([]byte(err.Error()), []byte("unknown field"))
}
