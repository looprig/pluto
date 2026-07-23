package reportjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/looprig/eval"
	"github.com/looprig/mpqt"
	"github.com/looprig/mpqt/internal/reporttest"
	"github.com/looprig/mpqt/profile"
)

func testManifest() mpqt.Manifest {
	return mpqt.Manifest{
		TargetID:      "t",
		Role:          mpqt.RoleCandidate,
		Provider:      "acme",
		Model:         "acme-1",
		APIFormat:     "openai",
		BaseURL:       "https://example.invalid/v1",
		Revision:      "r1",
		EndpointClass: mpqt.EndpointRemote,
		Capabilities:  []mpqt.Capability{mpqt.CapabilityStructuredOutput},
	}
}

func testCard(t *testing.T) mpqt.Scorecard {
	t.Helper()
	return mpqt.Scorecard{
		Manifest: testManifest(),
		Results: []mpqt.TableResult{
			{Pack: "p", Table: "t1", Dimension: "capability",
				Report: reporttest.Build(t, eval.StatusPass, eval.StatusFail)},
			{Pack: "p", Table: "t2", Dimension: "capability",
				Skipped: true, Missing: []mpqt.Capability{mpqt.CapabilityTools}},
		},
	}
}

func testProfileResult(t *testing.T, card mpqt.Scorecard) *profile.Result {
	t.Helper()
	minScore := 0.0
	p := profile.Profile{
		Name:     "p",
		Revision: "1",
		Requirements: []profile.Requirement{
			{Dimension: "capability", MinScore: &minScore},
		},
	}
	res, err := profile.Evaluate(card, p)
	if err != nil {
		t.Fatalf("profile.Evaluate: %v", err)
	}
	return &res
}

func TestEncode_Deterministic(t *testing.T) {
	t.Parallel()
	card := testCard(t)
	result := testProfileResult(t, card)

	a, err := Encode(card, result)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	b, err := Encode(card, result)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Error("Encode() is not deterministic: two calls on the same input produced different bytes")
	}
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	t.Parallel()
	card := testCard(t)
	result := testProfileResult(t, card)

	encoded, err := Encode(card, result)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if decoded.Version != Version {
		t.Errorf("Version = %q, want %q", decoded.Version, Version)
	}
	if !reflect.DeepEqual(decoded.Manifest, card.Manifest) {
		t.Errorf("Manifest = %+v, want %+v", decoded.Manifest, card.Manifest)
	}
	wantFingerprint, err := card.Manifest.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if decoded.Fingerprint != wantFingerprint {
		t.Errorf("Fingerprint = %q, want %q", decoded.Fingerprint, wantFingerprint)
	}
	wantDims, err := card.Dimensions()
	if err != nil {
		t.Fatalf("Dimensions: %v", err)
	}
	if len(decoded.Dimensions) != len(wantDims) {
		t.Fatalf("Dimensions = %+v, want %+v", decoded.Dimensions, wantDims)
	}
	for i := range wantDims {
		if decoded.Dimensions[i] != wantDims[i] {
			t.Errorf("Dimensions[%d] = %+v, want %+v", i, decoded.Dimensions[i], wantDims[i])
		}
	}
	wantRoll, err := card.StatusRollup()
	if err != nil {
		t.Fatalf("StatusRollup: %v", err)
	}
	if decoded.StatusRollup.Samples != wantRoll.Samples || decoded.StatusRollup.TargetErrors != wantRoll.TargetErrors {
		t.Errorf("StatusRollup = %+v, want %+v", decoded.StatusRollup, wantRoll)
	}
	for st, n := range wantRoll.ByStatus {
		if decoded.StatusRollup.ByStatus[st] != n {
			t.Errorf("StatusRollup.ByStatus[%s] = %d, want %d", st, decoded.StatusRollup.ByStatus[st], n)
		}
	}

	if len(decoded.Tables) != len(card.Results) {
		t.Fatalf("Tables = %+v, want %d entries", decoded.Tables, len(card.Results))
	}
	byKey := map[string]DecodedTable{}
	for _, tbl := range decoded.Tables {
		byKey[string(tbl.Pack)+"/"+string(tbl.Table)] = tbl
	}
	for _, res := range card.Results {
		got, ok := byKey[string(res.Pack)+"/"+string(res.Table)]
		if !ok {
			t.Fatalf("decoded tables missing %s/%s", res.Pack, res.Table)
		}
		if got.Dimension != res.Dimension || got.Skipped != res.Skipped {
			t.Errorf("table %s/%s = %+v, want Dimension=%s Skipped=%v", res.Pack, res.Table, got, res.Dimension, res.Skipped)
		}
		if !res.Skipped {
			// The embedded eval report is decoded via eval's OWN reportjson.Decode,
			// which is REDACTED BY DESIGN: Observation is the zero value and
			// Finding.Message is empty. Assert the redaction, not recovery.
			for _, sample := range got.Report.Samples {
				if !reflect.DeepEqual(sample.Observation, eval.Observation{}) {
					t.Errorf("decoded sample Observation = %+v, want zero value (redacted)", sample.Observation)
				}
				for _, a := range sample.Assessments {
					for _, f := range a.Findings {
						if f.Message != "" {
							t.Errorf("decoded finding Message = %q, want empty (redacted)", f.Message)
						}
					}
				}
			}
		}
	}

	if decoded.Profile == nil {
		t.Fatal("Profile = nil, want the encoded profile.Result")
	}
	if decoded.Profile.Disposition != result.Disposition {
		t.Errorf("Profile.Disposition = %s, want %s", decoded.Profile.Disposition, result.Disposition)
	}

	// Re-encoding the decoded projection reproduces the same bytes: every field
	// the wire form carries (manifest, dimensions, status rollup, table
	// identity, and the eval report's own safe fields) survives one full
	// encode/decode cycle exactly. Only Observation and Finding.Message do not
	// (asserted above): eval's reportjson never serializes them in the first
	// place, so re-encoding the redacted projection is a true fixed point.
	reconstructed := mpqt.Scorecard{Manifest: decoded.Manifest}
	for _, tbl := range decoded.Tables {
		reconstructed.Results = append(reconstructed.Results, mpqt.TableResult{
			Pack: tbl.Pack, Table: tbl.Table, Dimension: tbl.Dimension,
			Skipped: tbl.Skipped, Missing: tbl.Missing, Report: tbl.Report,
		})
	}
	reEncoded, err := Encode(reconstructed, decoded.Profile)
	if err != nil {
		t.Fatalf("re-Encode() error = %v", err)
	}
	if !bytes.Equal(encoded, reEncoded) {
		t.Error("re-Encode(Decode(Encode(x))) != Encode(x): round trip is not a fixed point")
	}
}

func TestEncode_NilProfileResultOmitted(t *testing.T) {
	t.Parallel()
	card := testCard(t)

	encoded, err := Encode(card, nil)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.Profile != nil {
		t.Errorf("Profile = %+v, want nil", decoded.Profile)
	}
}

func TestEncode_InvalidManifestRejected(t *testing.T) {
	t.Parallel()
	card := testCard(t)
	card.Manifest.TargetID = "" // invalid: required field

	if _, err := Encode(card, nil); err == nil {
		t.Fatal("Encode() error = nil, want a manifest validation error")
	} else {
		var ve *mpqt.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("Encode() error = %v (%T), want *mpqt.ValidationError", err, err)
		}
	}
}

func TestDecode_UnknownVersion(t *testing.T) {
	t.Parallel()
	env := map[string]json.RawMessage{
		"version": json.RawMessage(`"mpqt-report/v99"`),
		"report":  json.RawMessage(`{}`),
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	_, err = Decode(data)
	var uv *UnknownVersionError
	if !errors.As(err, &uv) {
		t.Fatalf("Decode() error = %v (%T), want *UnknownVersionError", err, err)
	}
}

func TestDecode_UnknownField(t *testing.T) {
	t.Parallel()
	card := testCard(t)
	encoded, err := Encode(card, nil)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	raw["bogus_field"] = json.RawMessage(`true`)
	tampered, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	if _, err := Decode(tampered); err == nil {
		t.Fatal("Decode() error = nil, want unknown-field rejection")
	}
}

func TestDecode_OversizedInput(t *testing.T) {
	t.Parallel()
	oversized := make([]byte, MaxReportBytes+1)
	_, err := Decode(oversized)
	var tooLarge *ReportTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("Decode() error = %v (%T), want *ReportTooLargeError", err, err)
	}
}
