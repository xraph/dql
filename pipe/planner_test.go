package pipe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xraph/dql/dsl"
)

func mustStage(t *testing.T, op string, cfg map[string]any) dsl.PipeStage {
	t.Helper()
	full := map[string]any{"op": op}
	for k, v := range cfg {
		full[k] = v
	}
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	stage := dsl.PipeStage{Op: op, Config: raw}
	// Mirror UnmarshalJSON so tests building stages directly still get id/from.
	if v, ok := full["id"].(string); ok {
		stage.ID = v
	}
	if v, ok := full["from"].(string); ok {
		stage.From = v
	}
	return stage
}

func TestPipePlanner_pushesFilterSortLimit(t *testing.T) {
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "filter", map[string]any{"where": map[string]any{"field": "level", "op": "==", "value": "ERROR"}}),
			mustStage(t, "sort", map[string]any{"by": []map[string]any{{"field": "ts", "dir": "desc"}}}),
			mustStage(t, "limit", map[string]any{"n": 10}),
		},
	}
	plan, err := PlanPipe(q, nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.InMemoryOps) != 0 {
		t.Fatalf("expected fully-pushed plan, got in-mem ops %+v", plan.InMemoryStages)
	}
	if plan.PushedDSL.Where == nil || plan.PushedDSL.Where.Field != "level" {
		t.Fatalf("filter not pushed: %+v", plan.PushedDSL.Where)
	}
	if len(plan.PushedDSL.OrderBy) != 1 || plan.PushedDSL.OrderBy[0].Field != "ts" {
		t.Fatalf("sort not pushed: %+v", plan.PushedDSL.OrderBy)
	}
	if plan.PushedDSL.Limit == nil || *plan.PushedDSL.Limit != 10 {
		t.Fatalf("limit not pushed: %+v", plan.PushedDSL.Limit)
	}
}

func TestPipePlanner_breaksAtFirstNonPushable(t *testing.T) {
	eval := &mockEval{results: map[string]any{"v*2": 2.0}}
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "filter", map[string]any{"where": map[string]any{"field": "v", "op": ">", "value": 0}}),
			mustStage(t, "compute", map[string]any{"as": "doubled", "expr": "v*2"}),
			mustStage(t, "sort", map[string]any{"by": []map[string]any{{"field": "doubled", "dir": "asc"}}}),
		},
	}
	plan, err := PlanPipe(q, &OpContext{Eval: eval})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.PushedStages) != 1 || plan.PushedStages[0] != "filter" {
		t.Fatalf("pushed prefix wrong: %+v", plan.PushedStages)
	}
	if len(plan.InMemoryOps) != 2 {
		t.Fatalf("expected 2 in-mem ops, got %d", len(plan.InMemoryOps))
	}
	if plan.InMemoryStages[0] != "compute" || plan.InMemoryStages[1] != "sort" {
		t.Fatalf("tail wrong: %+v", plan.InMemoryStages)
	}
}

func TestPipePlanner_groupByAggregate_pushable(t *testing.T) {
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "groupBy", map[string]any{"keys": []string{"host"}}),
			mustStage(t, "aggregate", map[string]any{"aggs": []map[string]any{{"fn": "COUNT", "field": "*", "as": "n"}}}),
		},
	}
	plan, err := PlanPipe(q, nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.InMemoryOps) != 0 {
		t.Fatalf("expected full push, got %+v", plan.InMemoryStages)
	}
	if len(plan.PushedDSL.GroupBy) != 1 || plan.PushedDSL.GroupBy[0] != "host" {
		t.Fatalf("group keys wrong: %+v", plan.PushedDSL.GroupBy)
	}
	if len(plan.PushedDSL.Aggregate) != 1 || plan.PushedDSL.Aggregate[0].Fn != "COUNT" {
		t.Fatalf("aggs wrong: %+v", plan.PushedDSL.Aggregate)
	}
}

func TestPipePlanner_exprFilterBreaksPrefix(t *testing.T) {
	eval := &mockEval{}
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "filter", map[string]any{"where": map[string]any{"expr": "value > 0"}}),
			mustStage(t, "sort", map[string]any{"by": []map[string]any{{"field": "ts", "dir": "desc"}}}),
		},
	}
	plan, err := PlanPipe(q, &OpContext{Eval: eval})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.PushedStages) != 0 {
		t.Fatalf("expr filter should break prefix: %+v", plan.PushedStages)
	}
	if len(plan.InMemoryOps) != 2 {
		t.Fatalf("expected both ops in-memory, got %d", len(plan.InMemoryOps))
	}
}

func TestPipePlanner_emptyPipeErrors(t *testing.T) {
	q := &dsl.QueryDSL{Mode: "pipe", From: dsl.FromClause{Dataset: "events"}}
	if _, err := PlanPipe(q, nil); err == nil {
		t.Fatalf("expected error for empty pipe")
	}
}

// TestPipePlanner_referencedIDBreaksPrefix verifies that a stage whose id is
// consumed by a later `from:` cannot be folded into the pushable prefix —
// the executor must capture its rows so the consumer can read them.
func TestPipePlanner_referencedIDBreaksPrefix(t *testing.T) {
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "filter", map[string]any{
				"id":    "errs",
				"where": map[string]any{"field": "level", "op": "==", "value": "ERROR"},
			}),
			mustStage(t, "sort", map[string]any{
				"from": "errs",
				"by":   []map[string]any{{"field": "ts", "dir": "desc"}},
			}),
		},
	}
	plan, err := PlanPipe(q, nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.PushedStages) != 0 {
		t.Fatalf("referenced id should keep filter in-memory; pushed=%v", plan.PushedStages)
	}
	if len(plan.InMemoryOps) != 2 {
		t.Fatalf("expected both ops in-memory, got %d", len(plan.InMemoryOps))
	}
	if plan.InMemoryIDs[0] != "errs" {
		t.Fatalf("InMemoryIDs[0] = %q, want %q", plan.InMemoryIDs[0], "errs")
	}
	if plan.InMemoryFroms[1] != "errs" {
		t.Fatalf("InMemoryFroms[1] = %q, want %q", plan.InMemoryFroms[1], "errs")
	}
}

// TestPipePlanner_unknownFromErrors makes sure dangling stage references are
// caught at plan time rather than blowing up at row time.
func TestPipePlanner_unknownFromErrors(t *testing.T) {
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "filter", map[string]any{
				"from":  "missing",
				"where": map[string]any{"field": "v", "op": ">", "value": 0},
			}),
		},
	}
	if _, err := PlanPipe(q, nil); err == nil {
		t.Fatalf("expected error for unknown stage id")
	}
}

// TestPipePlanner_duplicateIDErrors guards uniqueness of stage ids.
func TestPipePlanner_duplicateIDErrors(t *testing.T) {
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "filter", map[string]any{"id": "a", "where": map[string]any{"field": "v", "op": ">", "value": 0}}),
			mustStage(t, "filter", map[string]any{"id": "a", "where": map[string]any{"field": "v", "op": "<", "value": 10}}),
		},
	}
	if _, err := PlanPipe(q, nil); err == nil {
		t.Fatalf("expected error for duplicate stage id")
	}
}

// TestExecutor_fromReferenceWiresInput end-to-end checks that the executor
// feeds a stage with `from:` from the captured upstream output rather than
// the previous step's rows.
func TestExecutor_fromReferenceWiresInput(t *testing.T) {
	classic := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"id": 1, "level": "ERROR", "n": 5},
		{"id": 2, "level": "INFO", "n": 9},
		{"id": 3, "level": "ERROR", "n": 1},
	})}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "filter", map[string]any{
				"id":    "errs",
				"where": map[string]any{"field": "level", "op": "==", "value": "ERROR"},
			}),
			// Without the explicit `from`, this would consume the previous
			// stage's output (the same errs rows). The point of the test is
			// that `from: "errs"` selects errs explicitly even though the
			// previous step is `errs` itself — exercising the lookup path.
			mustStage(t, "sort", map[string]any{
				"from": "errs",
				"by":   []map[string]any{{"field": "n", "dir": "desc"}},
			}),
		},
	}
	res, err := x.Execute(context.Background(), q, "ws", "proj")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 ERROR rows, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["n"] != 5 || res.Rows[1]["n"] != 1 {
		t.Fatalf("sort by n desc failed: %+v", res.Rows)
	}
}
