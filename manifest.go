package mpqt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"sort"
	"unicode/utf8"

	"github.com/looprig/eval"
)

// MaxManifestStringBytes bounds every free-form manifest string, in bytes.
const MaxManifestStringBytes = 256

// ModelRole distinguishes the candidate under qualification from the incumbent
// it is compared against. There is no valid zero value.
type ModelRole string

const (
	RoleCandidate ModelRole = "candidate"
	RoleIncumbent ModelRole = "incumbent"
)

// Validate reports whether r is a known role.
func (r ModelRole) Validate() error {
	switch r {
	case RoleCandidate, RoleIncumbent:
		return nil
	}
	return &ValidationError{Field: "ModelRole", Reason: "unknown value"}
}

// EndpointClass records where the target executes, which bounds what MPQT can
// claim to have observed. There is no valid zero value.
type EndpointClass string

const (
	// EndpointRemote is hosted inference: only requests, responses, tool calls,
	// usage, latency, and errors are observable.
	EndpointRemote EndpointClass = "remote"
	// EndpointLocal is a local inference server on this host.
	EndpointLocal EndpointClass = "local"
	// EndpointProcess is a foreign process executed under sandbox control.
	EndpointProcess EndpointClass = "process"
)

// Validate reports whether c is a known endpoint class.
func (c EndpointClass) Validate() error {
	switch c {
	case EndpointRemote, EndpointLocal, EndpointProcess:
		return nil
	}
	return &ValidationError{Field: "EndpointClass", Reason: "unknown value"}
}

// Capability names a target feature a pack table may require. There is no
// valid zero value.
type Capability string

const (
	CapabilityTools            Capability = "tools"
	CapabilityStructuredOutput Capability = "structured_output"
	CapabilityImages           Capability = "images"
	CapabilityThinking         Capability = "thinking"
)

// Validate reports whether c is a known capability.
func (c Capability) Validate() error {
	switch c {
	case CapabilityTools, CapabilityStructuredOutput, CapabilityImages, CapabilityThinking:
		return nil
	}
	return &ValidationError{Field: "Capability", Reason: "unknown value"}
}

// Manifest is the secret-free identity of one model configuration under test.
// It deliberately has no credential field: authentication is resolved outside
// MPQT and never becomes part of a report. Fingerprint gives the manifest a
// stable reproducibility identity.
type Manifest struct {
	TargetID      string
	Role          ModelRole
	Provider      string
	Model         string
	APIFormat     string
	BaseURL       string
	Effort        string
	Revision      eval.Revision
	EndpointClass EndpointClass
	Capabilities  []Capability
}

// Validate checks structural validity: required bounded strings, a known role
// and endpoint class, an https-or-loopback base URL with no userinfo or query,
// and unique known capabilities.
func (m Manifest) Validate() error {
	for _, f := range []struct{ field, v string }{
		{"TargetID", m.TargetID},
		{"Provider", m.Provider},
		{"Model", m.Model},
		{"APIFormat", m.APIFormat},
	} {
		if err := boundedString(f.field, f.v); err != nil {
			return err
		}
	}
	if err := m.Role.Validate(); err != nil {
		return err
	}
	if err := m.EndpointClass.Validate(); err != nil {
		return err
	}
	if m.Effort != "" {
		if err := boundedString("Effort", m.Effort); err != nil {
			return err
		}
	}
	if err := m.Revision.Validate(); err != nil {
		return err
	}
	if err := validateBaseURL(m.BaseURL); err != nil {
		return err
	}
	seen := make(map[Capability]struct{}, len(m.Capabilities))
	for _, c := range m.Capabilities {
		if err := c.Validate(); err != nil {
			return err
		}
		if _, dup := seen[c]; dup {
			return &ValidationError{Field: "Capabilities", Reason: "duplicate value"}
		}
		seen[c] = struct{}{}
	}
	return nil
}

func boundedString(field, v string) error {
	if v == "" {
		return &ValidationError{Field: field, Reason: "must not be empty"}
	}
	if len(v) > MaxManifestStringBytes {
		return &ValidationError{Field: field, Reason: "exceeds byte bound"}
	}
	if !utf8.ValidString(v) {
		return &ValidationError{Field: field, Reason: "not valid UTF-8"}
	}
	return nil
}

func validateBaseURL(raw string) error {
	if raw == "" {
		return &ValidationError{Field: "BaseURL", Reason: "must not be empty"}
	}
	if len(raw) > MaxManifestStringBytes {
		return &ValidationError{Field: "BaseURL", Reason: "exceeds byte bound"}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return &ValidationError{Field: "BaseURL", Reason: "not a valid URL"}
	}
	if u.User != nil {
		return &ValidationError{Field: "BaseURL", Reason: "must not carry userinfo"}
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return &ValidationError{Field: "BaseURL", Reason: "must not carry query or fragment"}
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return &ValidationError{Field: "BaseURL", Reason: "http allowed only for loopback"}
	}
	return &ValidationError{Field: "BaseURL", Reason: "unsupported scheme"}
}

// Fingerprint returns a deterministic sha256 identity over the manifest's
// canonical JSON form. It validates first so an ill-formed manifest can never
// acquire an identity. Capabilities is order-independent (Validate only
// rejects duplicates, not reordering), so the hash input sorts a copy of
// Capabilities before marshaling; the receiver and any caller-owned slice are
// left untouched.
func (m Manifest) Fingerprint() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	canonical := m
	if len(m.Capabilities) > 0 {
		sorted := make([]Capability, len(m.Capabilities))
		copy(sorted, m.Capabilities)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		canonical.Capabilities = sorted
	}
	// A fixed struct with deterministic field order under encoding/json.
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", &ValidationError{Field: "Manifest", Reason: "not encodable"}
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ValidationError reports a structurally invalid MPQT value. Following eval's
// convention, it names the field and reason but never echoes the offending
// value.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return "mpqt: invalid " + e.Field + ": " + e.Reason
}
