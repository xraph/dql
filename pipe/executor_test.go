package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

type stubClassic struct {
	result *dsl.QueryResult
	err    error
	last   *dsl.QueryDSL
}

func (s *stubClassic) Execute(_ context.Context, q *dsl.QueryDSL, _, _ string) (*dsl.QueryResult, error) {
	s.last = q
	return s.result, s.err
}

func TestExecutor_fullyPushed_bypassesInMemoryOps(t *testing.T) {
	classic := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{{"ts": 1}, {"ts": 2}})}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "sort", map[string]any{"by": []map[string]any{{"field": "ts", "dir": "asc"}}}),
			mustStage(t, "limit", map[string]any{"n": 5}),
		},
	}
	res, err := x.Execute(context.Background(), q, "ws", "proj")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows: %d", len(res.Rows))
	}
	if classic.last.Mode != "" {
		t.Fatalf("classic called with mode %q, want empty", classic.last.Mode)
	}
	if classic.last.Limit == nil || *classic.last.Limit != 5 {
		t.Fatalf("limit not folded into classic call: %+v", classic.last.Limit)
	}
}

func TestExecutor_inMemoryTail_appliedAfterClassic(t *testing.T) {
	classic := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"v": 1.0}, {"v": 2.0}, {"v": 3.0},
	})}
	eval := &mockEval{results: map[string]any{"v + 1": 99.0}}
	x := NewExecutor(classic, &OpContext{Eval: eval}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "compute", map[string]any{"as": "bumped", "expr": "v + 1"}),
		},
	}
	res, err := x.Execute(context.Background(), q, "ws", "proj")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("rows: %d", len(res.Rows))
	}
	for _, row := range res.Rows {
		if row["bumped"] != 99.0 {
			t.Fatalf("compute did not run: %+v", row)
		}
	}
}

func TestExecutor_maxRows_guard(t *testing.T) {
	rows := make([]dsl.Row, 5)
	for i := range rows {
		rows[i] = dsl.Row{"v": i}
	}
	classic := &stubClassic{result: dsl.NewQueryResult(rows)}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{MaxRows: 3})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{mustStage(t, "tap", map[string]any{})},
	}
	_, err := x.Execute(context.Background(), q, "ws", "proj")
	if err == nil {
		t.Fatalf("expected error for exceeding MaxRows")
	}
}

func TestExecutor_maxStages_guard(t *testing.T) {
	classic := &stubClassic{result: dsl.NewQueryResult(nil)}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{MaxStages: 1})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "tap", map[string]any{}),
			mustStage(t, "tap", map[string]any{}),
		},
	}
	_, err := x.Execute(context.Background(), q, "ws", "proj")
	if err == nil {
		t.Fatalf("expected error for exceeding MaxStages")
	}
}

// TestExecutor_collectsPipeStageStats verifies that each in-memory tail stage
// surfaces a PipeStageStat carrying its op name, optional id, optional label
// (tap), and rowsIn/rowsOut counts.
func TestExecutor_collectsPipeStageStats(t *testing.T) {
	classic := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"v": 1},
		{"v": 2},
		{"v": 3},
	})}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "tap", map[string]any{"id": "before", "label": "before-filter"}),
			mustStage(t, "filter", map[string]any{"where": map[string]any{"field": "v", "op": ">", "value": 1}}),
			mustStage(t, "tap", map[string]any{"label": "after-filter"}),
		},
	}
	res, err := x.ExecuteDetailed(context.Background(), q, "ws", "proj")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	stats := res.Result.Stats.Pipe
	if len(stats) != 3 {
		t.Fatalf("want 3 stage stats, got %d: %+v", len(stats), stats)
	}
	// Stage 0: tap with id and label, sees all 3 rows.
	if stats[0].Op != "tap" || stats[0].ID != "before" || stats[0].Label != "before-filter" {
		t.Fatalf("stage 0 mismatch: %+v", stats[0])
	}
	if stats[0].RowsIn != 3 || stats[0].RowsOut != 3 {
		t.Fatalf("stage 0 counts: in=%d out=%d, want 3/3", stats[0].RowsIn, stats[0].RowsOut)
	}
	// Stage 1: filter drops one row (v=1).
	if stats[1].Op != "filter" {
		t.Fatalf("stage 1 op: got %q want filter", stats[1].Op)
	}
	if stats[1].RowsIn != 3 || stats[1].RowsOut != 2 {
		t.Fatalf("stage 1 counts: in=%d out=%d, want 3/2", stats[1].RowsIn, stats[1].RowsOut)
	}
	// Stage 2: tap with label only (no id), sees the filtered 2 rows.
	if stats[2].Op != "tap" || stats[2].ID != "" || stats[2].Label != "after-filter" {
		t.Fatalf("stage 2 mismatch: %+v", stats[2])
	}
	if stats[2].RowsIn != 2 || stats[2].RowsOut != 2 {
		t.Fatalf("stage 2 counts: in=%d out=%d, want 2/2", stats[2].RowsIn, stats[2].RowsOut)
	}
}

// TestExecutor_omitPipeStats verifies the OmitPipeStats opt-out: stats are
// collected and surfaced by default, suppressed when the flag is set.
func TestExecutor_omitPipeStats(t *testing.T) {
	classic := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"v": 1}, {"v": 2}, {"v": 3},
	})}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{})

	build := func(omit bool) *dsl.QueryDSL {
		return &dsl.QueryDSL{
			Mode:          "pipe",
			From:          dsl.FromClause{Dataset: "events"},
			OmitPipeStats: omit,
			Pipe: []dsl.PipeStage{
				mustStage(t, "tap", map[string]any{"label": "a"}),
				mustStage(t, "filter", map[string]any{"where": map[string]any{"field": "v", "op": ">", "value": 1}}),
			},
		}
	}

	// Default — stats present.
	res, err := x.Execute(context.Background(), build(false), "ws", "proj")
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if len(res.Stats.Pipe) != 2 {
		t.Fatalf("default should include 2 stage stats, got %d", len(res.Stats.Pipe))
	}

	// Opt out — stats absent.
	res, err = x.Execute(context.Background(), build(true), "ws", "proj")
	if err != nil {
		t.Fatalf("omit: %v", err)
	}
	if res.Stats.Pipe != nil {
		t.Fatalf("OmitPipeStats=true should suppress stats, got %+v", res.Stats.Pipe)
	}
}

// TestExecutor_pipeSample_capped asserts the per-stage Sample is capped at
// PipeStageStatSampleSize even when stages produce many more rows.
func TestExecutor_pipeSample_capped(t *testing.T) {
	rows := make([]dsl.Row, 20)
	for i := range rows {
		rows[i] = dsl.Row{"i": i}
	}
	classic := &stubClassic{result: dsl.NewQueryResult(rows)}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "tap", map[string]any{"label": "all"}),
		},
	}
	res, err := x.Execute(context.Background(), q, "ws", "proj")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Stats.Pipe) != 1 {
		t.Fatalf("expected 1 stage stat, got %d", len(res.Stats.Pipe))
	}
	got := len(res.Stats.Pipe[0].Sample)
	if got != dsl.PipeStageStatSampleSize {
		t.Fatalf("sample size: got %d, want %d", got, dsl.PipeStageStatSampleSize)
	}
	if res.Stats.Pipe[0].Sample[0]["i"] != 0 {
		t.Fatalf("sample should be head-of-slice; first row i: got %v want 0", res.Stats.Pipe[0].Sample[0]["i"])
	}
}

// TestExecutor_pipeSample_empty asserts a stage that produces zero rows
// emits a nil Sample so the JSON tag's omitempty drops the field.
func TestExecutor_pipeSample_empty(t *testing.T) {
	classic := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{{"v": 1}})}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			// `tap` is non-pushable, so the subsequent filter stays in-memory
			// (otherwise it'd fold into the SQL prefix and not surface as a
			// stage stat at all).
			mustStage(t, "tap", map[string]any{}),
			// Drops every row — v=1 fails the v > 1 predicate.
			mustStage(t, "filter", map[string]any{"where": map[string]any{"field": "v", "op": ">", "value": 1}}),
		},
	}
	res, err := x.Execute(context.Background(), q, "ws", "proj")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Stats.Pipe) != 2 {
		t.Fatalf("expected 2 stage stats, got %d", len(res.Stats.Pipe))
	}
	filterStat := res.Stats.Pipe[1]
	if filterStat.Op != "filter" || filterStat.RowsOut != 0 {
		t.Fatalf("filter stage should have produced 0 rows: %+v", filterStat)
	}
	if filterStat.Sample != nil {
		t.Fatalf("sample for zero-output stage should be nil, got %+v", filterStat.Sample)
	}
}

// TestExecutor_pipeSample_isolated asserts mutating the sample after
// execution does not affect the rows returned to the caller.
func TestExecutor_pipeSample_isolated(t *testing.T) {
	classic := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"v": 1}, {"v": 2},
	})}
	x := NewExecutor(classic, &OpContext{}, ExecutorConfig{})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{mustStage(t, "tap", map[string]any{})},
	}
	res, err := x.Execute(context.Background(), q, "ws", "proj")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Mutate the captured sample in place.
	res.Stats.Pipe[0].Sample[0]["v"] = "POISONED"

	// The pipeline's actual rows should still hold the original value.
	if res.Rows[0]["v"] != 1 {
		t.Fatalf("rows leaked: got %v want 1 (sample mutation should not affect the row stream)", res.Rows[0]["v"])
	}
}
