package gen

import (
	"bytes"
	"fmt"

	"github.com/looprig/pluto/pkg/packfile"
	"gopkg.in/yaml.v3"
)

// scenariosKey is the table file's top-level scenarios sequence key.
const scenariosKey = "scenarios"

// generatedByLabel is the provenance label Append stamps onto every appended
// scenario.
const generatedByLabel = "generated-by"

// yamlIndent matches the 2-space indent used throughout the hand-authored
// pack YAML corpus (see docs/2026-07-23-phase2-packfiles-generation-cli-design.md).
const yamlIndent = 2

// Append inserts specs into tableFile's scenarios sequence via yaml.Node
// surgery: it parses tableFile into a yaml.Node tree, appends one new
// sequence item per spec to the existing "scenarios" node (creating an empty
// one if the table has none yet), and re-encodes the whole tree. Every other
// node in the tree -- including comments -- is left untouched, so this is
// NOT a strict-decode-then-re-encode round trip (which would lose them).
//
// Each appended scenario gains a {generated-by: generatedBy} label, merged
// into any labels already present on the spec (overriding a prior
// "generated-by" value rather than duplicating the key). generatedBy is a
// caller-supplied "<model>/<date>" string; Append never reads the clock or a
// model catalogue itself. path is used only for diagnostics.
func Append(path string, tableFile []byte, specs []packfile.ScenarioSpec, generatedBy string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(tableFile, &root); err != nil {
		return nil, fmt.Errorf("gen: append %s: parse: %w", path, err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, fmt.Errorf("gen: append %s: empty document", path)
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("gen: append %s: table file root is not a mapping", path)
	}

	seq, err := scenariosSequence(mapping)
	if err != nil {
		return nil, fmt.Errorf("gen: append %s: %w", path, err)
	}

	for _, spec := range specs {
		node, err := scenarioNode(spec, generatedBy)
		if err != nil {
			return nil, fmt.Errorf("gen: append %s: %w", path, err)
		}
		seq.Content = append(seq.Content, node)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("gen: append %s: encode: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("gen: append %s: encode: %w", path, err)
	}
	return buf.Bytes(), nil
}

// scenariosSequence returns the "scenarios" sequence node within mapping,
// appending an empty one as the mapping's last key if the table has none yet.
func scenariosSequence(mapping *yaml.Node) (*yaml.Node, error) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == scenariosKey {
			val := mapping.Content[i+1]
			if val.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("%s is not a sequence", scenariosKey)
			}
			return val, nil
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: scenariosKey}
	seqNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	mapping.Content = append(mapping.Content, keyNode, seqNode)
	return seqNode, nil
}

// appendScenario mirrors packfile.ScenarioSpec's fields for encoding only,
// with omitempty added on the fields that are legitimately optional (Name
// defaults to "<pack>-<table>" when empty, Expect and Labels may be unset).
// ScenarioSpec's own yaml tags have no omitempty (needed for strict-decode
// round-tripping elsewhere), so marshaling it directly would write every
// unset field out as noisy zero-value YAML (e.g. `name: ""`, `expect: null`)
// that no hand-authored scenario carries; this local DTO-shaped type keeps
// that fix confined to pkg/gen's append path rather than touching the
// shared packfile.ScenarioSpec type.
type appendScenario struct {
	ID     string                 `yaml:"id"`
	Name   string                 `yaml:"name,omitempty"`
	Input  []packfile.MessageSpec `yaml:"input"`
	Expect *packfile.ExpectSpec   `yaml:"expect,omitempty"`
	Labels map[string]string      `yaml:"labels,omitempty"`
}

// scenarioNode renders spec (with generatedBy merged into its labels) as a
// yaml.Node mapping matching the scenarios sequence's item shape: it converts
// spec to the local appendScenario encoding DTO (yaml.Marshal) rather than
// hand-building the node, then re-parses that YAML into a node so it can be
// spliced into the surrounding tree.
func scenarioNode(spec packfile.ScenarioSpec, generatedBy string) (*yaml.Node, error) {
	labels := make(map[string]string, len(spec.Labels)+1)
	for k, v := range spec.Labels {
		labels[k] = v
	}
	labels[generatedByLabel] = generatedBy

	merged := appendScenario{
		ID:     spec.ID,
		Name:   spec.Name,
		Input:  spec.Input,
		Expect: spec.Expect,
		Labels: labels,
	}

	data, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal scenario %q: %w", spec.ID, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("reparse scenario %q: %w", spec.ID, err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("empty scenario node for %q", spec.ID)
	}
	return doc.Content[0], nil
}
