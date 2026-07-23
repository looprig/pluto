package packfile

import (
	"errors"
	"strings"
	"testing"
)

// TestErrorUnwrap verifies that *Error preserves the underlying cause it
// wraps, so a caller can errors.Is/errors.As through it to the original
// sentinel or typed error rather than only string-matching Reason.
func TestErrorUnwrap(t *testing.T) {
	t.Run("wrapped sentinel is reachable via errors.Is", func(t *testing.T) {
		wrapped := wrapPathErr("evaluators/judge", ErrJudgeUnconfigured)

		if !errors.Is(wrapped, ErrJudgeUnconfigured) {
			t.Fatalf("errors.Is could not reach ErrJudgeUnconfigured through %v", wrapped)
		}

		var pe *Error
		if !errors.As(wrapped, &pe) {
			t.Fatalf("errors.As could not reach *Error through %v", wrapped)
		}
		if pe.Err != ErrJudgeUnconfigured {
			t.Fatalf("Error.Err = %v, want ErrJudgeUnconfigured", pe.Err)
		}
	})

	t.Run("yaml decode error is reachable through DecodeTable's *Error", func(t *testing.T) {
		_, err := DecodeTable(strings.NewReader("table: [this is not closed\n"))
		if err == nil {
			t.Fatal("malformed YAML accepted")
		}

		var pe *Error
		if !errors.As(err, &pe) {
			t.Fatalf("want *Error, got %T: %v", err, err)
		}
		if pe.Err == nil {
			t.Fatalf("Error.Err is nil, want the underlying yaml decode error preserved")
		}
		if errors.Unwrap(err) != pe.Err {
			t.Fatalf("errors.Unwrap(err) = %v, want pe.Err (%v)", errors.Unwrap(err), pe.Err)
		}
	})

	t.Run("plain-string Error has no fabricated cause", func(t *testing.T) {
		err := &Error{Path: "evaluators", Reason: "missing kind"}
		if err.Unwrap() != nil {
			t.Fatalf("Unwrap() = %v, want nil for a string-only *Error", err.Unwrap())
		}
	})
}
