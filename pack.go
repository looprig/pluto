package mpqt

import (
	"github.com/looprig/eval"
)

// Table is MPQT's unit of execution: a named, versioned scenario family that
// shares one evaluator set and contributes to one score dimension. A runnable
// table expands to exactly one eval.Suite; MPQT never adds a second runner.
type Table struct {
	Name       eval.Name
	Revision   eval.Revision
	Dimension  eval.Name
	Requires   []Capability
	Scenarios  []eval.Scenario
	Evaluators []eval.Evaluator
}

// Validate checks the table in isolation. Cross-table rules (duplicate
// scenario IDs) belong to Pack.Validate.
func (t Table) Validate() error {
	if err := t.Name.Validate(); err != nil {
		return err
	}
	if err := t.Revision.Validate(); err != nil {
		return err
	}
	if err := t.Dimension.Validate(); err != nil {
		return err
	}
	seen := make(map[Capability]struct{}, len(t.Requires))
	for _, c := range t.Requires {
		if err := c.Validate(); err != nil {
			return err
		}
		if _, dup := seen[c]; dup {
			return &ValidationError{Field: "Table.Requires", Reason: "duplicate capability"}
		}
		seen[c] = struct{}{}
	}
	if len(t.Scenarios) == 0 {
		return &ValidationError{Field: "Table.Scenarios", Reason: "must not be empty"}
	}
	for _, sc := range t.Scenarios {
		if err := sc.Validate(); err != nil {
			return err
		}
	}
	if len(t.Evaluators) == 0 {
		return &ValidationError{Field: "Table.Evaluators", Reason: "must not be empty"}
	}
	for _, ev := range t.Evaluators {
		if ev == nil {
			return &ValidationError{Field: "Table.Evaluators", Reason: "nil evaluator"}
		}
		if err := ev.Descriptor().Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Suite expands the table into the eval.Suite that eval.Run executes.
func (t Table) Suite() eval.Suite {
	return eval.Suite{
		Name:      t.Name,
		Revision:  t.Revision,
		Scenarios: t.Scenarios,
	}
}

// Pack is a versioned set of tables. Scenario IDs are unique across the whole
// pack so results remain unambiguous when tables are rolled up.
type Pack struct {
	Name     eval.Name
	Revision eval.Revision
	Tables   []Table
}

// Validate checks pack identity, per-table validity, unique table names, and
// pack-wide scenario ID uniqueness.
func (p Pack) Validate() error {
	if err := p.Name.Validate(); err != nil {
		return err
	}
	if err := p.Revision.Validate(); err != nil {
		return err
	}
	if len(p.Tables) == 0 {
		return &ValidationError{Field: "Pack.Tables", Reason: "must not be empty"}
	}
	names := make(map[eval.Name]struct{}, len(p.Tables))
	ids := make(map[string]struct{})
	for _, tbl := range p.Tables {
		if err := tbl.Validate(); err != nil {
			return err
		}
		if _, dup := names[tbl.Name]; dup {
			return &ValidationError{Field: "Pack.Tables", Reason: "duplicate table name"}
		}
		names[tbl.Name] = struct{}{}
		for _, sc := range tbl.Scenarios {
			if _, dup := ids[sc.ID]; dup {
				return &ValidationError{Field: "Pack.Tables", Reason: "duplicate scenario ID"}
			}
			ids[sc.ID] = struct{}{}
		}
	}
	return nil
}

// TablePlan is the preflight result for one table against one manifest. A
// non-runnable plan retains the table identity and the missing capabilities so
// the scorecard can report skipped coverage instead of silently dropping work.
type TablePlan struct {
	Pack       eval.Name
	Table      eval.Name
	Dimension  eval.Name
	Runnable   bool
	Missing    []Capability
	Suite      eval.Suite
	Evaluators []eval.Evaluator
}

// Plan validates the pack and manifest, then produces one TablePlan per table
// in pack order. It performs no execution and no I/O.
func Plan(p Pack, m Manifest) ([]TablePlan, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	have := make(map[Capability]struct{}, len(m.Capabilities))
	for _, c := range m.Capabilities {
		have[c] = struct{}{}
	}
	plans := make([]TablePlan, 0, len(p.Tables))
	for _, tbl := range p.Tables {
		var missing []Capability
		for _, req := range tbl.Requires {
			if _, ok := have[req]; !ok {
				missing = append(missing, req)
			}
		}
		pl := TablePlan{
			Pack:      p.Name,
			Table:     tbl.Name,
			Dimension: tbl.Dimension,
			Runnable:  len(missing) == 0,
			Missing:   missing,
		}
		if pl.Runnable {
			pl.Suite = tbl.Suite()
			pl.Evaluators = tbl.Evaluators
		}
		plans = append(plans, pl)
	}
	return plans, nil
}
