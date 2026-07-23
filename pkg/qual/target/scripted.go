// Package target provides deterministic eval.Target fixtures for offline pack
// tests. A Scripted target answers each scenario from a canned script and
// emits typed eval evidence, always stamping the scenario's revision on the
// subject (eval.Sample.Validate requires the match).
package target

import (
	"context"
	"strconv"
	"time"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
)

// ToolCall is one scripted tool invocation surfaced as tool_operation
// evidence plus a tool operation span.
type ToolCall struct {
	Name    eval.Name
	ID      string
	IsError bool
}

// Structured marks the script as having produced schema-conformant structured
// output.
type Structured struct {
	SchemaName     eval.Name
	SchemaRevision eval.Revision
}

// StructuredErr marks the script as having produced a classified structured
// output failure.
type StructuredErr struct {
	Schema eval.Revision
	Reason eval.StructuredErrorReason
}

// Script is the canned behavior for one scenario ID.
type Script struct {
	Reply         string
	Duration      time.Duration // emitted as timing evidence when > 0
	ToolCalls     []ToolCall
	Structured    *Structured
	StructuredErr *StructuredErr
	Err           error // returned verbatim from Observe (target-stage failure)
}

// Scripted is a deterministic eval.Target. The zero value is unusable; use
// NewScripted.
type Scripted struct {
	name    string
	scripts map[string]Script
}

// NewScripted builds a Scripted target named name with one script per
// scenario ID.
func NewScripted(name string, scripts map[string]Script) *Scripted {
	return &Scripted{name: name, scripts: scripts}
}

// Name implements eval.Target.
func (s *Scripted) Name() string { return s.name }

// Observe implements eval.Target. The scenario is read-only per the Target
// contract; input messages are copied, never mutated.
func (s *Scripted) Observe(_ context.Context, sc eval.Scenario) (eval.Observation, error) {
	script, ok := s.scripts[sc.ID]
	if !ok {
		return eval.Observation{}, &UnscriptedScenarioError{}
	}
	if script.Err != nil {
		return eval.Observation{}, script.Err
	}

	conv := make(content.AgenticMessages, 0, len(sc.Input)+1)
	conv = append(conv, sc.Input...)
	blocks := []content.Block{&content.TextBlock{Text: script.Reply}}
	for _, tc := range script.ToolCalls {
		blocks = append(blocks, &content.ToolUseBlock{
			ID: tc.ID, Name: string(tc.Name), Input: []byte(`{}`),
		})
	}
	conv = append(conv, &content.AIMessage{Message: content.Message{
		Role: content.RoleAssistant, Blocks: blocks,
	}})

	var evidence []eval.Evidence
	var ops []eval.Operation
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, tc := range script.ToolCalls {
		id := eval.EvidenceID("tool_" + strconv.Itoa(i))
		evidence = append(evidence, eval.Evidence{
			ID:   id,
			Kind: eval.EvidenceToolOperation,
			ToolOperation: &eval.ToolOperationEvidence{
				ToolName: tc.Name, ToolUseID: tc.ID, IsError: tc.IsError,
			},
		})
		status := eval.OperationOK
		if tc.IsError {
			status = eval.OperationFailed
		}
		ops = append(ops, eval.Operation{
			ID:        "op_tool_" + strconv.Itoa(i),
			Kind:      eval.OperationTool,
			Status:    status,
			StartedAt: now,
			EndedAt:   now.Add(time.Millisecond),
			Evidence:  []eval.EvidenceRef{{Evidence: id}},
		})
	}
	if script.Duration > 0 {
		evidence = append(evidence, eval.Evidence{
			ID:   "timing",
			Kind: eval.EvidenceTiming,
			Timing: &eval.TimingEvidence{
				Label: "inference", Duration: script.Duration,
			},
		})
	}
	if script.Structured != nil {
		evidence = append(evidence, eval.Evidence{
			ID:   "structured_output",
			Kind: eval.EvidenceStructuredOutput,
			StructuredOutput: &eval.StructuredOutput{
				SchemaName:     script.Structured.SchemaName,
				SchemaRevision: script.Structured.SchemaRevision,
			},
		})
	}
	if script.StructuredErr != nil {
		evidence = append(evidence, eval.Evidence{
			ID:   "structured_error",
			Kind: eval.EvidenceStructuredError,
			StructuredError: &eval.StructuredOutputError{
				Schema: script.StructuredErr.Schema,
				Reason: script.StructuredErr.Reason,
			},
		})
	}

	return eval.Observation{
		Conversation: conv,
		Subject: eval.Subject{
			ID:       s.name,
			Kind:     eval.SubjectModel,
			Name:     eval.Name(s.name),
			Revision: sc.Revision,
		},
		Trace: eval.Trace{
			Operations: ops,
			Evidence:   evidence,
		},
		Expectation: sc.Expectation,
	}, nil
}

// UnscriptedScenarioError reports an Observe call for a scenario the fixture
// has no script for. Per convention the scenario ID is withheld.
type UnscriptedScenarioError struct{}

func (e *UnscriptedScenarioError) Error() string {
	return "fixture/target: no script for scenario"
}
