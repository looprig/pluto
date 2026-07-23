package packfile

import "fmt"

// Error is the typed failure for every packfile boundary rejection.
type Error struct {
	Path   string // "<pack>/<file>:<yaml path>" when known
	Reason string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return "packfile: " + e.Reason
	}
	return fmt.Sprintf("packfile: %s: %s", e.Path, e.Reason)
}
