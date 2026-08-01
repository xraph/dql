package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

// scriptedEval lets a test return per-expression boolean predicates against
// a row. Lookup uses the row's "id" field to vary behaviour.
type scriptedEval struct {
	predicate func(expr string, row map[string]any) any
}

func (s *scriptedEval) Eval(_ context.Context, expr string, row map[string]any) (any, error) {
	return s.predicate(expr, row), nil
}

func TestBranch_routesByPredicate(t *testing.T) {
	eval := &scriptedEval{predicate: func(_ string, row map[string]any) any {
		return row["err"] == true
	}}
	stage := stageJSON(t, map[string]any{
		"op":   "branch",
		"when": "err",
		"then": []map[string]any{
			{"op": "compute", "as": "tag", "expr": "1"},
		},
		"else": []map[string]any{
			{"op": "compute", "as": "tag", "expr": "0"},
		},
	})
	op, err := branchFactory(stage, &OpContext{Eval: &mockEval{
		results: map[string]any{"1": "errored", "0": "ok"},
	}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	// Inject scripted eval after factory built sub-ops with the mockEval.
	op.(*branchOp).eval = eval

	in := []dsl.Row{
		{"id": 1, "err": true},
		{"id": 2, "err": false},
		{"id": 3, "err": true},
	}
	out, err := op.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("rows: %d", len(out))
	}
	// Then bucket emits two errored rows first, else bucket emits one ok row.
	errCount, okCount := 0, 0
	for _, r := range out {
		if r["tag"] == "errored" {
			errCount++
		}
		if r["tag"] == "ok" {
			okCount++
		}
	}
	if errCount != 2 || okCount != 1 {
		t.Fatalf("bucket counts wrong: err=%d ok=%d full=%+v", errCount, okCount, out)
	}
}

func TestBranch_isLiveSafe_propagates(t *testing.T) {
	// then contains a callApp → branch must be unsafe.
	app := &stubAppCaller{}
	stage := stageJSON(t, map[string]any{
		"op":   "branch",
		"when": "x",
		"then": []map[string]any{
			{"op": "callApp", "appId": "slack"},
		},
	})
	op, err := branchFactory(stage, &OpContext{
		Eval:      &mockEval{},
		AppCaller: app,
	})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if op.IsLiveSafe() {
		t.Fatalf("branch with callApp child must not be live-safe")
	}
}

func TestBranch_pureChildren_isSafe(t *testing.T) {
	stage := stageJSON(t, map[string]any{
		"op":   "branch",
		"when": "x",
		"then": []map[string]any{{"op": "limit", "n": 5}},
		"else": []map[string]any{{"op": "limit", "n": 1}},
	})
	op, err := branchFactory(stage, &OpContext{Eval: &mockEval{}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if !op.IsLiveSafe() {
		t.Fatalf("branch with pure children should be live-safe")
	}
}

func TestBranch_missingThen_factoryErrors(t *testing.T) {
	stage := stageJSON(t, map[string]any{"op": "branch", "when": "x"})
	_, err := branchFactory(stage, &OpContext{Eval: &mockEval{}})
	if err == nil {
		t.Fatalf("expected error for missing then")
	}
}
