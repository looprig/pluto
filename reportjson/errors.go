package reportjson

import (
	"strconv"
	"unicode/utf8"
)

// This file declares the concrete, classifiable error types the codec returns.
// Every distinct failure mode is a typed struct so callers classify with
// errors.As, never by matching strings. Diagnostics carry only safe locators —
// a fixed reason vocabulary and safe integers — and never the offending report
// bytes.

// maxVersionTokenBytes bounds how much of an unknown version token may appear
// in a diagnostic. A token longer than this is withheld entirely.
const maxVersionTokenBytes = 64

// Fixed vocabulary for MalformedReportError.Reason.
const (
	reasonInvalidJSON  = "invalid JSON"
	reasonInvalidUTF8  = "invalid UTF-8"
	reasonTrailingData = "trailing data after report"
	reasonEmptyReport  = "missing report payload"
	reasonUnknownField = "unknown field"
)

// UnknownVersionError reports that a document's version discriminator was
// missing or names a wire version this codec does not implement. The version
// token may originate from untrusted input, so it is bounded and withheld
// when hostile; Version is either a short, valid token or "" when redacted.
type UnknownVersionError struct {
	Version string
}

func (e *UnknownVersionError) Error() string {
	if e.Version == "" {
		return "mpqt/reportjson: unknown or missing report version"
	}
	return "mpqt/reportjson: unknown report version " + strconv.Quote(e.Version)
}

// ReportTooLargeError reports that a document exceeded MaxReportBytes. Only
// safe integers are carried; no content is embedded.
type ReportTooLargeError struct {
	Size int
	Max  int
}

func (e *ReportTooLargeError) Error() string {
	return "mpqt/reportjson: report of " + strconv.Itoa(e.Size) +
		" bytes exceeds max " + strconv.Itoa(e.Max)
}

// MalformedReportError reports that the bytes were not exactly one well-formed
// mpqt-report/v1 document. Reason is drawn only from the fixed vocabulary
// above, so no untrusted content leaks.
type MalformedReportError struct {
	Reason string
}

func (e *MalformedReportError) Error() string {
	return "mpqt/reportjson: malformed report: " + e.Reason
}

// InvalidReportError reports that a decoded document was well-formed JSON but
// a reconstructed part failed domain validation. Cause is exposed via Unwrap
// so callers can classify it further (for example as an *mpqt.ValidationError).
type InvalidReportError struct {
	Cause error
}

func (e *InvalidReportError) Error() string {
	return "mpqt/reportjson: invalid report: " + e.Cause.Error()
}

func (e *InvalidReportError) Unwrap() error { return e.Cause }

// EncodeError reports that a scorecard could not be serialized. Cause is
// exposed via Unwrap.
type EncodeError struct {
	Cause error
}

func (e *EncodeError) Error() string { return "mpqt/reportjson: cannot encode report" }

func (e *EncodeError) Unwrap() error { return e.Cause }

// safeVersionToken returns a bounded, safe rendering of an unknown version
// token for a diagnostic, or "" when the token is oversized or not valid
// UTF-8.
func safeVersionToken(v string) string {
	if v == "" || len(v) > maxVersionTokenBytes || !utf8.ValidString(v) {
		return ""
	}
	return v
}
