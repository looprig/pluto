// Package profile derives an organization's qualification disposition from an
// MPQT scorecard. Profiles are policy data: evaluation is a pure function that
// never mutates raw results and never calls a model or target.
package profile

import (
	"github.com/looprig/eval"
	"github.com/looprig/mpqt/pkg/qual"
)

// Disposition is the derived release-policy outcome. There is no valid zero
// value.
type Disposition string

const (
	Qualified  Disposition = "qualified"
	Restricted Disposition = "restricted"
	Rejected   Disposition = "rejected"
	Unverified Disposition = "unverified"
)

// Requirement is one testable policy clause. Exactly one subject must be set:
// either Dimension (with MinScore and/or MinCoverage) or FindingCode (with
// MaxFindingCount) or Severity (with MaxSeverityCount). Nil bounds are
// "no constraint of that kind".
type Requirement struct {
	Dimension   eval.Name
	MinScore    *float64 // [0,100]
	MinCoverage *float64 // [0,1]

	FindingCode     eval.FindingCode
	MaxFindingCount *int

	Severity         eval.Severity
	MaxSeverityCount *int
}

// Validate rejects a requirement with no subject, more than one subject, a
// subject without any bound, or an out-of-range bound.
func (r Requirement) Validate() error {
	subjects := 0
	if r.Dimension != "" {
		subjects++
		if r.MinScore == nil && r.MinCoverage == nil {
			return &qual.ValidationError{Field: "Requirement", Reason: "dimension subject needs MinScore or MinCoverage"}
		}
	}
	if r.FindingCode != "" {
		subjects++
		if r.MaxFindingCount == nil {
			return &qual.ValidationError{Field: "Requirement", Reason: "finding subject needs MaxFindingCount"}
		}
	}
	if r.Severity != "" {
		subjects++
		if err := r.Severity.Validate(); err != nil {
			return err
		}
		if r.MaxSeverityCount == nil {
			return &qual.ValidationError{Field: "Requirement", Reason: "severity subject needs MaxSeverityCount"}
		}
	}
	if subjects != 1 {
		return &qual.ValidationError{Field: "Requirement", Reason: "exactly one subject required"}
	}
	if r.MinScore != nil && (*r.MinScore < 0 || *r.MinScore > 100) {
		return &qual.ValidationError{Field: "Requirement.MinScore", Reason: "must be within [0,100]"}
	}
	if r.MinCoverage != nil && (*r.MinCoverage < 0 || *r.MinCoverage > 1) {
		return &qual.ValidationError{Field: "Requirement.MinCoverage", Reason: "must be within [0,1]"}
	}
	if r.MaxFindingCount != nil && *r.MaxFindingCount < 0 {
		return &qual.ValidationError{Field: "Requirement.MaxFindingCount", Reason: "must not be negative"}
	}
	if r.MaxSeverityCount != nil && *r.MaxSeverityCount < 0 {
		return &qual.ValidationError{Field: "Requirement.MaxSeverityCount", Reason: "must not be negative"}
	}
	return nil
}

// Restriction is a non-mandatory clause: when its requirement is not met, the
// disposition downgrades from qualified to restricted and the description
// names the reduced deployment scope.
type Restriction struct {
	Description string
	Requirement Requirement
}

// Profile is a named, versioned set of mandatory requirements and optional
// restrictions.
type Profile struct {
	Name         eval.Name
	Revision     eval.Revision
	Requirements []Requirement
	Restrictions []Restriction
}

// Validate checks profile identity, at least one mandatory requirement, and
// every clause.
func (p Profile) Validate() error {
	if err := p.Name.Validate(); err != nil {
		return err
	}
	if err := p.Revision.Validate(); err != nil {
		return err
	}
	if len(p.Requirements) == 0 {
		return &qual.ValidationError{Field: "Profile.Requirements", Reason: "must not be empty"}
	}
	for _, r := range p.Requirements {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	for _, r := range p.Restrictions {
		if r.Description == "" {
			return &qual.ValidationError{Field: "Restriction.Description", Reason: "must not be empty"}
		}
		if err := r.Requirement.Validate(); err != nil {
			return err
		}
	}
	return nil
}
