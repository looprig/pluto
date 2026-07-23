package run

import (
	"io"

	"github.com/looprig/eval"
	"github.com/looprig/inference/model"
	"github.com/looprig/mpqt/pkg/packfile"
	"github.com/looprig/mpqt/pkg/profile"
	"github.com/looprig/mpqt/pkg/qual"
)

// ManifestModel maps a secret-free qual.Manifest onto the inference module's
// model.Model descriptor used to build a live target: Provider, Model (as
// Name), APIFormat, and BaseURL carry across directly, Effort becomes the
// model's default Sampling.Effort, and each qual.Capability sets the
// matching model.Capabilities field (Tools, StructuredOutput, Images ->
// AcceptsImages, Thinking -> Thinking). ManifestModel assumes m is already
// valid (m.Capabilities holds only known values); BuildTarget validates m
// first, so an unknown capability is rejected there rather than silently
// dropped here.
func ManifestModel(m qual.Manifest) model.Model {
	mm := model.Model{
		Provider:  model.ProviderName(m.Provider),
		APIFormat: model.APIFormat(m.APIFormat),
		BaseURL:   m.BaseURL,
		Name:      m.Model,
	}
	mm.Sampling.Effort = model.Effort(m.Effort)
	for _, c := range m.Capabilities {
		switch c {
		case qual.CapabilityTools:
			mm.Caps.Tools = true
		case qual.CapabilityStructuredOutput:
			mm.Caps.StructuredOutput = true
		case qual.CapabilityImages:
			mm.Caps.AcceptsImages = true
		case qual.CapabilityThinking:
			mm.Caps.Thinking = true
		}
	}
	return mm
}

// manifestFile is the hand-authored YAML mirror of qual.Manifest. Field
// names are kebab-case to match the rest of the packfile YAML corpus;
// mapping to qual.Manifest happens in DecodeManifest, and qual.Manifest
// itself deliberately carries no credential field, so a field like
// "api-key" has nowhere to decode into and is rejected by StrictDecode's
// KnownFields(true) the same way any other unknown field is.
type manifestFile struct {
	TargetID      string   `yaml:"target-id"`
	Role          string   `yaml:"role"`
	Provider      string   `yaml:"provider"`
	Model         string   `yaml:"model"`
	APIFormat     string   `yaml:"api-format"`
	BaseURL       string   `yaml:"base-url"`
	Effort        string   `yaml:"effort"`
	Revision      string   `yaml:"revision"`
	EndpointClass string   `yaml:"endpoint-class"`
	Capabilities  []string `yaml:"capabilities"`
}

// DecodeManifest strictly decodes r (via packfile.StrictDecode, so an
// unknown field is rejected the same way the rest of the packfile YAML
// corpus rejects one) into a qual.Manifest and validates it before
// returning. This package never imports gopkg.in/yaml.v3 itself, reusing
// packfile's strict-decode logic instead; pkg/gen imports yaml.v3 directly
// for an unrelated concern (encode-side yaml.Node surgery).
func DecodeManifest(r io.Reader) (qual.Manifest, error) {
	var mf manifestFile
	if err := packfile.StrictDecode(r, &mf); err != nil {
		return qual.Manifest{}, err
	}
	caps := make([]qual.Capability, 0, len(mf.Capabilities))
	for _, c := range mf.Capabilities {
		caps = append(caps, qual.Capability(c))
	}
	m := qual.Manifest{
		TargetID:      mf.TargetID,
		Role:          qual.ModelRole(mf.Role),
		Provider:      mf.Provider,
		Model:         mf.Model,
		APIFormat:     mf.APIFormat,
		BaseURL:       mf.BaseURL,
		Effort:        mf.Effort,
		Revision:      eval.Revision(mf.Revision),
		EndpointClass: qual.EndpointClass(mf.EndpointClass),
		Capabilities:  caps,
	}
	if err := m.Validate(); err != nil {
		return qual.Manifest{}, err
	}
	return m, nil
}

// requirementFile is the YAML mirror of profile.Requirement.
type requirementFile struct {
	Dimension   string   `yaml:"dimension"`
	MinScore    *float64 `yaml:"min-score"`
	MinCoverage *float64 `yaml:"min-coverage"`

	FindingCode     string `yaml:"finding-code"`
	MaxFindingCount *int   `yaml:"max-finding-count"`

	Severity         string `yaml:"severity"`
	MaxSeverityCount *int   `yaml:"max-severity-count"`
}

func (rf requirementFile) requirement() profile.Requirement {
	return profile.Requirement{
		Dimension:        eval.Name(rf.Dimension),
		MinScore:         rf.MinScore,
		MinCoverage:      rf.MinCoverage,
		FindingCode:      eval.FindingCode(rf.FindingCode),
		MaxFindingCount:  rf.MaxFindingCount,
		Severity:         eval.Severity(rf.Severity),
		MaxSeverityCount: rf.MaxSeverityCount,
	}
}

// restrictionFile is the YAML mirror of profile.Restriction.
type restrictionFile struct {
	Description string          `yaml:"description"`
	Requirement requirementFile `yaml:"requirement"`
}

// profileFile is the hand-authored YAML mirror of profile.Profile.
type profileFile struct {
	Name         string            `yaml:"name"`
	Revision     string            `yaml:"revision"`
	Requirements []requirementFile `yaml:"requirements"`
	Restrictions []restrictionFile `yaml:"restrictions"`
}

// DecodeProfile strictly decodes r (via packfile.StrictDecode) into a
// profile.Profile and validates it before returning.
func DecodeProfile(r io.Reader) (profile.Profile, error) {
	var pf profileFile
	if err := packfile.StrictDecode(r, &pf); err != nil {
		return profile.Profile{}, err
	}
	reqs := make([]profile.Requirement, 0, len(pf.Requirements))
	for _, rf := range pf.Requirements {
		reqs = append(reqs, rf.requirement())
	}
	restrictions := make([]profile.Restriction, 0, len(pf.Restrictions))
	for _, rsf := range pf.Restrictions {
		restrictions = append(restrictions, profile.Restriction{
			Description: rsf.Description,
			Requirement: rsf.Requirement.requirement(),
		})
	}
	p := profile.Profile{
		Name:         eval.Name(pf.Name),
		Revision:     eval.Revision(pf.Revision),
		Requirements: reqs,
		Restrictions: restrictions,
	}
	if err := p.Validate(); err != nil {
		return profile.Profile{}, err
	}
	return p, nil
}
