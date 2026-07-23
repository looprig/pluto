package mpqt

import (
	"strings"
	"testing"
)

func validManifest() Manifest {
	return Manifest{
		TargetID:      "candidate",
		Role:          RoleCandidate,
		Provider:      "openrouter",
		Model:         "openai/gpt-5.4",
		APIFormat:     "openai",
		BaseURL:       "https://openrouter.ai/api/v1",
		Effort:        "high",
		Revision:      "gpt-5.4@2026-07-01",
		EndpointClass: EndpointRemote,
		Capabilities:  []Capability{CapabilityTools, CapabilityStructuredOutput},
	}
}

func TestManifestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*Manifest)
		wantErr bool
	}{
		{name: "valid", mutate: func(m *Manifest) {}},
		{name: "empty target id", mutate: func(m *Manifest) { m.TargetID = "" }, wantErr: true},
		{name: "oversized target id", mutate: func(m *Manifest) { m.TargetID = strings.Repeat("a", MaxManifestStringBytes+1) }, wantErr: true},
		{name: "unknown role", mutate: func(m *Manifest) { m.Role = "referee" }, wantErr: true},
		{name: "zero role", mutate: func(m *Manifest) { m.Role = "" }, wantErr: true},
		{name: "empty provider", mutate: func(m *Manifest) { m.Provider = "" }, wantErr: true},
		{name: "empty model", mutate: func(m *Manifest) { m.Model = "" }, wantErr: true},
		{name: "empty api format", mutate: func(m *Manifest) { m.APIFormat = "" }, wantErr: true},
		{name: "http non-loopback base url", mutate: func(m *Manifest) { m.BaseURL = "http://example.com/v1" }, wantErr: true},
		{name: "http loopback base url ok", mutate: func(m *Manifest) { m.BaseURL = "http://127.0.0.1:8080/v1" }},
		{name: "empty base url rejected", mutate: func(m *Manifest) { m.BaseURL = "" }, wantErr: true},
		{name: "userinfo in url rejected", mutate: func(m *Manifest) { m.BaseURL = "https://user:pass@host/v1" }, wantErr: true},
		{name: "query in url rejected", mutate: func(m *Manifest) { m.BaseURL = "https://host/v1?key=abc" }, wantErr: true},
		{name: "empty revision", mutate: func(m *Manifest) { m.Revision = "" }, wantErr: true},
		{name: "unknown endpoint class", mutate: func(m *Manifest) { m.EndpointClass = "cloud" }, wantErr: true},
		{name: "zero endpoint class", mutate: func(m *Manifest) { m.EndpointClass = "" }, wantErr: true},
		{name: "unknown capability", mutate: func(m *Manifest) { m.Capabilities = []Capability{"telepathy"} }, wantErr: true},
		{name: "duplicate capability", mutate: func(m *Manifest) { m.Capabilities = []Capability{CapabilityTools, CapabilityTools} }, wantErr: true},
		{name: "no capabilities ok", mutate: func(m *Manifest) { m.Capabilities = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := validManifest()
			tt.mutate(&m)
			err := m.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManifestFingerprint(t *testing.T) {
	t.Parallel()
	a := validManifest()
	fpA1, err := a.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint() error = %v", err)
	}
	fpA2, err := a.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint() second call error = %v", err)
	}
	if fpA1 != fpA2 {
		t.Errorf("fingerprint not deterministic: %q vs %q", fpA1, fpA2)
	}
	if !strings.HasPrefix(fpA1, "sha256:") {
		t.Errorf("fingerprint %q lacks sha256: prefix", fpA1)
	}
	b := validManifest()
	b.Effort = "low"
	fpB, err := b.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint() error = %v", err)
	}
	if fpB == fpA1 {
		t.Error("different manifests produced identical fingerprints")
	}
	invalid := validManifest()
	invalid.TargetID = ""
	if _, err := invalid.Fingerprint(); err == nil {
		t.Error("Fingerprint() on invalid manifest should error")
	}
}
