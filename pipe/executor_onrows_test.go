package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

// TestExecutor_ExecuteDetailedOnRows_fullyFolded covers an app-source pipe
// that folds entirely into the pushed prefix (empty in-memory tail). The
// prefix's Columns and Stats.Truncated must survive into the final result —
// this is the regression covered by the "nil Columns on a fully-folded app
// pipe" bug: ExecuteDetailedOnRows used to only take bare rows, so a fully
// pushed pipe reported nil Columns and lost the app fetch's truncation flag.
func TestExecutor_ExecuteDetailedOnRows_fullyFolded(t *testing.T) {
	classic := &stubClassic{}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "app:app1/telemetry"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "sort", map[string]any{"by": []map[string]any{{"field": "ts", "dir": "asc"}}}),
			mustStage(t, "limit", map[string]any{"n": 5}),
		},
	}
	plan, err := PlanPipe(q, x.OpContext())
	if err != nil {
		t.Fatalf("PlanPipe: %v", err)
	}
	if len(plan.InMemoryOps) != 0 {
		t.Fatalf("expected fully-folded plan, got %d in-memory ops", len(plan.InMemoryOps))
	}

	prefix := &dsl.QueryResult{
		Rows: []dsl.Row{{"ts": 1}, {"ts": 2}},
		Columns: []dsl.ColumnInfo{
			{Name: "ts", Type: "int", Source: "raw"},
		},
		Stats: dsl.QueryStats{
			RowsScanned: 2,
			Sources:     []string{"app"},
			Truncated:   true,
		},
	}

	out, err := x.ExecuteDetailedOnRows(context.Background(), q, plan, prefix, "ws", "proj")
	if err != nil {
		t.Fatalf("ExecuteDetailedOnRows: %v", err)
	}
	if len(out.Result.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(out.Result.Rows))
	}
	if len(out.Result.Columns) != 1 || out.Result.Columns[0].Name != "ts" {
		t.Fatalf("columns = %+v, want the prefix's [ts] column to survive", out.Result.Columns)
	}
	if !out.Result.Stats.Truncated {
		t.Fatal("Stats.Truncated must propagate from the prefix when the tail is empty")
	}
	if out.Result.Stats.RowsScanned != 2 {
		t.Fatalf("RowsScanned = %d, want 2", out.Result.Stats.RowsScanned)
	}
}

// TestExecutor_ExecuteDetailedOnRows_withTail covers an app-source pipe with
// a non-empty in-memory tail: Truncated must still propagate from the
// prefix, but Columns are re-derived from the actual output rows (the tail
// may add/rename/drop fields, so the prefix's column list no longer applies).
func TestExecutor_ExecuteDetailedOnRows_withTail(t *testing.T) {
	classic := &stubClassic{}
	eval := &mockEval{results: map[string]any{"v + 1": 99.0}}
	x := NewExecutor(classic, &OpContext{Eval: eval}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "app:app1/telemetry"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "compute", map[string]any{"as": "bumped", "expr": "v + 1"}),
		},
	}
	plan, err := PlanPipe(q, x.OpContext())
	if err != nil {
		t.Fatalf("PlanPipe: %v", err)
	}
	if len(plan.InMemoryOps) == 0 {
		t.Fatal("expected a non-empty in-memory tail")
	}

	prefix := &dsl.QueryResult{
		Rows: []dsl.Row{{"v": 1.0}, {"v": 2.0}, {"v": 3.0}},
		Columns: []dsl.ColumnInfo{
			{Name: "v", Type: "float", Source: "raw"},
		},
		Stats: dsl.QueryStats{
			RowsScanned: 3,
			Sources:     []string{"app"},
			Truncated:   true,
		},
	}

	out, err := x.ExecuteDetailedOnRows(context.Background(), q, plan, prefix, "ws", "proj")
	if err != nil {
		t.Fatalf("ExecuteDetailedOnRows: %v", err)
	}
	if len(out.Result.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(out.Result.Rows))
	}
	for _, row := range out.Result.Rows {
		if row["bumped"] != 99.0 {
			t.Fatalf("compute did not run: %+v", row)
		}
	}
	if !out.Result.Stats.Truncated {
		t.Fatal("Stats.Truncated must propagate from the prefix even when a tail runs")
	}
	// Columns must be derived from the actual rows (v + bumped), not the
	// prefix's raw column list which doesn't know about the computed field.
	names := map[string]bool{}
	for _, c := range out.Result.Columns {
		names[c.Name] = true
	}
	if !names["bumped"] || !names["v"] {
		t.Fatalf("columns = %+v, want derived columns including v and bumped", out.Result.Columns)
	}
}

// TestExecutor_ExecuteDetailedOnRows_nilPrefix ensures a nil prefix result
// errors cleanly instead of panicking on prefix.Rows/prefix.Stats.
func TestExecutor_ExecuteDetailedOnRows_nilPrefix(t *testing.T) {
	classic := &stubClassic{}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "app:app1/telemetry"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "limit", map[string]any{"n": 5}),
		},
	}
	plan, err := PlanPipe(q, x.OpContext())
	if err != nil {
		t.Fatalf("PlanPipe: %v", err)
	}

	if _, err := x.ExecuteDetailedOnRows(context.Background(), q, plan, nil, "ws", "proj"); err == nil {
		t.Fatal("nil prefix must error, not panic")
	}
}
