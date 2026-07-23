package packfile

import "fmt"

// Error is the typed failure for every packfile boundary rejection.
type Error struct {
	Path   string // "<pack>/<file>:<yaml path>" when known
	Reason string
	Err    error // optional underlying cause; nil when Reason is not derived from one
}

func (e *Error) Error() string {
	if e.Path == "" {
		return "packfile: " + e.Reason
	}
	return fmt.Sprintf("packfile: %s: %s", e.Path, e.Reason)
}

// Unwrap exposes the underlying cause (if any), so errors.Is/errors.As can
// reach the original error (a yaml syntax error, os.PathError, io.EOF,
// ErrJudgeUnconfigured, etc.) through a *Error rather than only string-
// matching against Reason.
func (e *Error) Unwrap() error {
	return e.Err
}

// wrapPathErr wraps err in a *Error naming path, unless err is already a
// *Error (in which case it is returned as-is to avoid double-wrapping). The
// original err is retained as Err so it remains reachable via errors.Is/As.
func wrapPathErr(path string, err error) error {
	if _, ok := err.(*Error); ok {
		return err
	}
	return &Error{Path: path, Reason: err.Error(), Err: err}
}
