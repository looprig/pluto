package profile

import (
	"github.com/looprig/eval"
	"github.com/looprig/mpqt"
)

// Card is the read-only view of a scorecard that profile evaluation consumes.
// mpqt.Scorecard satisfies it once Task 12 adds FindingCount/SeverityCount;
// tests may substitute a fake.
type Card interface {
	Dimensions() ([]mpqt.DimensionScore, error)
	FindingCount(code eval.FindingCode) int
	SeverityCount(s eval.Severity) int
}

// Outcome classifies one requirement's result.
type Outcome string

const (
	Met       Outcome = "met"
	Violated  Outcome = "violated"
	Undecided Outcome = "undecided"
)

// RequirementResult is the per-clause evidence retained in the result.
type RequirementResult struct {
	Requirement Requirement
	Outcome     Outcome
}

// RestrictionResult records whether a restriction applies.
type RestrictionResult struct {
	Restriction Restriction
	Applied     bool
}

// Result is the derived disposition plus per-clause evidence. It contains no
// mutated scorecard data.
type Result struct {
	Profile      eval.Name
	Revision     eval.Revision
	Disposition  Disposition
	Requirements []RequirementResult
	Restrictions []RestrictionResult
}

// Evaluate derives a disposition. Precedence: any violated mandatory
// requirement yields Rejected; otherwise any undecided requirement yields
// Unverified; otherwise restrictions with unmet requirements yield Restricted;
// otherwise Qualified.
func Evaluate(card Card, p Profile) (Result, error) {
	if err := p.Validate(); err != nil {
		return Result{}, err
	}
	dims, err := card.Dimensions()
	if err != nil {
		return Result{}, err
	}
	byName := make(map[eval.Name]mpqt.DimensionScore, len(dims))
	for _, d := range dims {
		byName[d.Dimension] = d
	}

	res := Result{Profile: p.Name, Revision: p.Revision}
	violated, undecided := false, false
	for _, r := range p.Requirements {
		o := check(r, byName, card)
		res.Requirements = append(res.Requirements, RequirementResult{Requirement: r, Outcome: o})
		switch o {
		case Violated:
			violated = true
		case Undecided:
			undecided = true
		}
	}
	switch {
	case violated:
		res.Disposition = Rejected
	case undecided:
		res.Disposition = Unverified
	default:
		res.Disposition = Qualified
		for _, restr := range p.Restrictions {
			applied := check(restr.Requirement, byName, card) != Met
			res.Restrictions = append(res.Restrictions, RestrictionResult{Restriction: restr, Applied: applied})
			if applied {
				res.Disposition = Restricted
			}
		}
	}
	return res, nil
}

func check(r Requirement, dims map[eval.Name]mpqt.DimensionScore, card Card) Outcome {
	switch {
	case r.Dimension != "":
		d, ok := dims[r.Dimension]
		if !ok || d.Undecided {
			return Undecided
		}
		if r.MinCoverage != nil && d.Coverage < *r.MinCoverage {
			// Not enough verified evidence to decide: insufficient proof, not
			// demonstrated violation.
			return Undecided
		}
		if r.MinScore != nil && d.Score < *r.MinScore {
			return Violated
		}
		return Met
	case r.FindingCode != "":
		if card.FindingCount(r.FindingCode) > *r.MaxFindingCount {
			return Violated
		}
		return Met
	case r.Severity != "":
		if card.SeverityCount(r.Severity) > *r.MaxSeverityCount {
			return Violated
		}
		return Met
	}
	return Undecided
}
