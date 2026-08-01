package exec

import (
	"context"
	"strings"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/scope"
)

// TestEngine_pipeDisabled_executeRejects pins fix #1: WithPipeEnabled(false)
// (translated to EngineConfig.PipeDisabled=true) must cause Execute to
// reject pipe-mode queries with a clear error.
func TestEngine_pipeDisabled_executeRejects(t *testing.T) {
	eng := NewEngine(nil, &fakeResolver{}, nil, EngineConfig{PipeDisabled: true, ScopeFor: func(_, _ string) scope.Scope { return scope.Scope{} }})
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
	}
	_, err := eng.Execute(context.Background(), q, "ws", "")
	if err == nil {
		t.Fatalf("expected error when PipeDisabled=true")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("error should mention disabled: %v", err)
	}
}

func TestEngine_pipeDisabled_explainRejects(t *testing.T) {
	eng := NewEngine(nil, &fakeResolver{}, nil, EngineConfig{PipeDisabled: true, ScopeFor: func(_, _ string) scope.Scope { return scope.Scope{} }})
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
	}
	_, err := eng.Explain(context.Background(), q, "ws", "")
	if err == nil {
		t.Fatalf("expected error when PipeDisabled=true")
	}
}

// fakeResolver returns minimal dataset info so the engine setup succeeds.
type fakeResolver struct{}

func (fakeResolver) ResolveDataset(_ context.Context, _, _, name string) (*dsl.DatasetInfo, error) {
	return &dsl.DatasetInfo{Name: name, TableName: "ds_" + name}, nil
}
