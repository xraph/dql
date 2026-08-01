package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

// TestExecuteDetailed_primedAfterCallApp verifies that the executor captures
// the post-callApp snapshot so live replay can skip the external call.
func TestExecuteDetailed_primedAfterCallApp(t *testing.T) {
	// Classic prefix returns two rows.
	classic := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"id": 1}, {"id": 2},
	})}
	// App enriches each row with "app": true.
	app := &stubAppCaller{response: map[string]any{
		"rows": []any{
			map[string]any{"id": 1, "app": true},
			map[string]any{"id": 2, "app": true},
		},
	}}
	eval := &mockEval{results: map[string]any{"id + 10": 11}}
	x := NewExecutor(classic, &OpContext{AppCaller: app, Eval: eval}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "callApp", map[string]any{"appId": "enrich"}),
			mustStage(t, "compute", map[string]any{"as": "bumped", "expr": "id + 10"}),
		},
	}
	res, err := x.ExecuteDetailed(context.Background(), q, "ws1", "proj1")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Result.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(res.Result.Rows))
	}
	if res.PrimedAt != 1 {
		t.Fatalf("primedAt should be 1 (after callApp), got %d", res.PrimedAt)
	}
	// Primed rows should have "app" set but NOT "bumped".
	if res.PrimedRows[0]["app"] != true {
		t.Fatalf("primed rows should carry callApp output: %+v", res.PrimedRows[0])
	}
	if _, ok := res.PrimedRows[0]["bumped"]; ok {
		t.Fatalf("primed rows should not have downstream compute result: %+v", res.PrimedRows[0])
	}
}

// TestExecuteFromStage_doesNotCallApp verifies that replay resumes from the
// primed snapshot without re-invoking the app.
func TestExecuteFromStage_doesNotCallApp(t *testing.T) {
	classic := &stubClassic{}
	app := &stubAppCaller{response: map[string]any{"rows": []any{}}}
	eval := &mockEval{results: map[string]any{"id + 10": 99.0}}
	x := NewExecutor(classic, &OpContext{AppCaller: app, Eval: eval}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "callApp", map[string]any{"appId": "enrich"}),
			mustStage(t, "compute", map[string]any{"as": "bumped", "expr": "id + 10"}),
		},
	}
	plan, err := PlanPipe(q, x.OpContext())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Prime with 2 rows that already carry the "app" enrichment.
	primed := []dsl.Row{
		{"id": 1, "app": true},
		{"id": 2, "app": true},
	}
	beforeCalls := app.calls
	res, err := x.ExecuteFromStage(context.Background(), plan, 1, primed, nil, "ws1", "proj1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if app.calls != beforeCalls {
		t.Fatalf("replay should not invoke callApp, but it was called %d times", app.calls-beforeCalls)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("want 2 rows after replay, got %d", len(res.Rows))
	}
	// The compute ran — "bumped" should be present now.
	if res.Rows[0]["bumped"] != 99.0 {
		t.Fatalf("replay did not run the safe tail: %+v", res.Rows[0])
	}
}

// TestExecuteFromStage_skipsUnsafeStagesInRange ensures that even if the
// primed index is 0 (before any side-effect op) the replay skips the unsafe
// ops rather than invoking them.
func TestExecuteFromStage_skipsUnsafeStagesInRange(t *testing.T) {
	classic := &stubClassic{}
	app := &stubAppCaller{response: map[string]any{"rows": []any{}}}
	eval := &mockEval{results: map[string]any{"id + 1": 42.0}}
	x := NewExecutor(classic, &OpContext{AppCaller: app, Eval: eval}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "compute", map[string]any{"as": "v", "expr": "id + 1"}),
			mustStage(t, "callApp", map[string]any{"appId": "enrich"}),
		},
	}
	plan, err := PlanPipe(q, x.OpContext())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	primed := []dsl.Row{{"id": 1}, {"id": 2}}
	appCallsBefore := app.calls
	_, err = x.ExecuteFromStage(context.Background(), plan, 0, primed, nil, "ws1", "proj1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if app.calls != appCallsBefore {
		t.Fatalf("callApp should be skipped during replay")
	}
}

// TestExecuteDetailed_purePipe_primedAtZero verifies that a pipe without any
// side-effecting op has primedAt=0 and primedRows equal to the post-prefix rows.
func TestExecuteDetailed_purePipe_primedAtZero(t *testing.T) {
	classic := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{{"v": 1}, {"v": 2}})}
	eval := &mockEval{results: map[string]any{"v + 1": 99.0}}
	x := NewExecutor(classic, &OpContext{Eval: eval}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{mustStage(t, "compute", map[string]any{"as": "b", "expr": "v + 1"})},
	}
	res, err := x.ExecuteDetailed(context.Background(), q, "ws", "proj")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.PrimedAt != 0 {
		t.Fatalf("pure pipe should have primedAt=0, got %d", res.PrimedAt)
	}
	if len(res.PrimedRows) != 2 {
		t.Fatalf("primed rows should include all post-prefix rows, got %d", len(res.PrimedRows))
	}
}

// TestExecuteFromStage_outOfRange_errors guards against bad primedAt values.
func TestExecuteFromStage_outOfRange_errors(t *testing.T) {
	x := NewExecutor(&stubClassic{}, nil, ExecutorConfig{})
	plan := &PipePlan{InMemoryOps: []Operator{&tapOp{}}}
	_, err := x.ExecuteFromStage(context.Background(), plan, 5, nil, nil, "ws", "proj")
	if err == nil {
		t.Fatalf("expected out-of-range error")
	}
}
