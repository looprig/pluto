package packfile

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// jsonFromNode recursively converts a decoded yaml.Node into canonical JSON:
// mappings become JSON objects with string keys (sorted, via
// encoding/json's map marshaling), sequences become JSON arrays, and scalars
// decode through yaml's own type resolution (string/bool/number/null).
// YAML aliases and non-string mapping keys are rejected: schema authors get
// one canonical, unambiguous JSON Schema out of arbitrary pack YAML, never a
// self-referential or non-portable one.
func jsonFromNode(n *yaml.Node) (json.RawMessage, error) {
	v, err := valueFromNode(n)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("packfile: encode schema node: %w", err)
	}
	return json.RawMessage(raw), nil
}

// valueFromNode converts one yaml.Node into a plain Go value suitable for
// json.Marshal: map[string]any for mappings, []any for sequences, and the
// scalar's own resolved type otherwise.
func valueFromNode(n *yaml.Node) (any, error) {
	if n == nil {
		return nil, nil
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return valueFromNode(n.Content[0])
	case yaml.AliasNode:
		return nil, &Error{Path: "schema", Reason: "yaml aliases are not supported in schema nodes"}
	case yaml.MappingNode:
		return mappingFromNode(n)
	case yaml.SequenceNode:
		seq := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := valueFromNode(c)
			if err != nil {
				return nil, err
			}
			seq = append(seq, v)
		}
		return seq, nil
	case yaml.ScalarNode:
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, &Error{Path: "schema", Reason: fmt.Sprintf("decode scalar: %v", err)}
		}
		return v, nil
	default:
		return nil, &Error{Path: "schema", Reason: fmt.Sprintf("unsupported yaml node kind %d", n.Kind)}
	}
}

func mappingFromNode(n *yaml.Node) (map[string]any, error) {
	m := make(map[string]any, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		keyNode, valNode := n.Content[i], n.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			return nil, &Error{Path: "schema", Reason: "mapping keys must be strings"}
		}
		val, err := valueFromNode(valNode)
		if err != nil {
			return nil, err
		}
		m[keyNode.Value] = val
	}
	return m, nil
}
