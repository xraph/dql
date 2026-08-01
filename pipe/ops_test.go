package pipe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xraph/dql/dsl"
)

type mockEval struct {
	results map[string]any
}

func (m *mockEval) Eval(_ context.Context, expr string, row map[string]any) (any, error) {
	if v, ok := m.results[expr]; ok {
		return v, nil
	}
	return row[expr], nil
}

func stageJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestFilterOp_plainField(t *testing.T) {
	raw := stageJSON(t, map[string]any{
		"op":    "filter",
		"where": map[string]any{"field": "v", "op": ">", "value": 1},
	})
	op, err := filterFactory(raw, &OpContext{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	rows := []dsl.Row{{"v": 0}, {"v": 1}, {"v": 2}, {"v": 3}}
	out, err := op.Apply(context.Background(), rows)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(out), out)
	}
}

func TestFilterOp_expr_needsEval(t *testing.T) {
	raw := stageJSON(t, map[string]any{
		"op":    "filter",
		"where": map[string]any{"expr": "v > 1"},
	})
	_, err := filterFactory(raw, &OpContext{})
	if err == nil {
		t.Fatalf("expected error when eval is missing")
	}
}

func TestProjectOp_select(t *testing.T) {
	raw := stageJSON(t, map[string]any{
		"op":     "project",
		"select": []map[string]any{{"field": "a"}, {"field": "b", "as": "beta"}},
	})
	op, err := projectFactory(raw, nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"a": 1, "b": 2, "c": 3}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("rows: %d", len(out))
	}
	if out[0]["a"] != 1 || out[0]["beta"] != 2 {
		t.Fatalf("unexpected: %+v", out[0])
	}
	if _, ok := out[0]["c"]; ok {
		t.Fatalf("c should be dropped, got %+v", out[0])
	}
}

func TestProjectOp_drop(t *testing.T) {
	raw := stageJSON(t, map[string]any{
		"op":   "project",
		"drop": []string{"secret"},
	})
	op, err := projectFactory(raw, nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"id": 1, "secret": "s"}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, ok := out[0]["secret"]; ok {
		t.Fatalf("secret should be dropped")
	}
	if out[0]["id"] != 1 {
		t.Fatalf("id lost")
	}
}

func TestRenameOp(t *testing.T) {
	op, err := renameFactory(stageJSON(t, map[string]any{"op": "rename", "map": map[string]string{"a": "alpha"}}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, _ := op.Apply(context.Background(), []dsl.Row{{"a": 1}})
	if out[0]["alpha"] != 1 {
		t.Fatalf("rename failed: %+v", out[0])
	}
}

func TestComputeOp_needsEval(t *testing.T) {
	_, err := computeFactory(stageJSON(t, map[string]any{"op": "compute", "as": "x", "expr": "v*2"}), nil)
	if err == nil {
		t.Fatalf("expected error when eval missing")
	}
}

func TestComputeOp_ok(t *testing.T) {
	eval := &mockEval{results: map[string]any{"v*2": 20.0}}
	op, err := computeFactory(stageJSON(t, map[string]any{"op": "compute", "as": "doubled", "expr": "v*2"}), &OpContext{Eval: eval})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"v": 10}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out[0]["doubled"] != 20.0 {
		t.Fatalf("doubled wrong: %+v", out[0])
	}
}

func TestDistinctOp_byKeys(t *testing.T) {
	op, err := distinctFactory(stageJSON(t, map[string]any{"op": "distinct", "by": []string{"k"}}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, _ := op.Apply(context.Background(), []dsl.Row{{"k": "a", "v": 1}, {"k": "b"}, {"k": "a", "v": 2}})
	if len(out) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(out), out)
	}
}

func TestSortOp_desc(t *testing.T) {
	op, err := sortFactory(stageJSON(t, map[string]any{"op": "sort", "by": []map[string]any{{"field": "v", "dir": "desc"}}}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, _ := op.Apply(context.Background(), []dsl.Row{{"v": 1}, {"v": 3}, {"v": 2}})
	if out[0]["v"] != 3 || out[2]["v"] != 1 {
		t.Fatalf("sort wrong: %+v", out)
	}
}

func TestLimitSkipOp(t *testing.T) {
	limit, _ := limitFactory(stageJSON(t, map[string]any{"op": "limit", "n": 2}), nil)
	skip, _ := skipFactory(stageJSON(t, map[string]any{"op": "skip", "n": 1}), nil)
	rows := []dsl.Row{{"v": 1}, {"v": 2}, {"v": 3}, {"v": 4}}
	afterSkip, _ := skip.Apply(context.Background(), rows)
	out, _ := limit.Apply(context.Background(), afterSkip)
	if len(out) != 2 || out[0]["v"] != 2 || out[1]["v"] != 3 {
		t.Fatalf("limit/skip wrong: %+v", out)
	}
}

func TestAggregateOp_groupBy(t *testing.T) {
	gb, err := groupByFactory(stageJSON(t, map[string]any{"op": "groupBy", "keys": []string{"k"}}), nil)
	if err != nil {
		t.Fatalf("groupBy factory: %v", err)
	}
	agg, err := aggregateFactory(stageJSON(t, map[string]any{
		"op":   "aggregate",
		"aggs": []map[string]any{{"fn": "SUM", "field": "v", "as": "total"}},
	}), nil)
	if err != nil {
		t.Fatalf("aggregate factory: %v", err)
	}
	gbop := gb.(*groupByOp)
	aggOp := agg.(*aggregateOp).WithKeys(gbop.keys)

	rows := []dsl.Row{{"k": "a", "v": 1.0}, {"k": "b", "v": 2.0}, {"k": "a", "v": 3.0}}
	out, err := aggOp.Apply(context.Background(), rows)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 groups, got %d", len(out))
	}
	byK := map[string]float64{}
	for _, r := range out {
		byK[r["k"].(string)] = r["total"].(float64)
	}
	if byK["a"] != 4.0 || byK["b"] != 2.0 {
		t.Fatalf("sums wrong: %+v", byK)
	}
}

func TestAggregateOp_noGroupBy_collapsesToSingleRow(t *testing.T) {
	agg, _ := aggregateFactory(stageJSON(t, map[string]any{
		"op":   "aggregate",
		"aggs": []map[string]any{{"fn": "COUNT", "field": "*", "as": "n"}},
	}), nil)
	out, _ := agg.Apply(context.Background(), []dsl.Row{{}, {}, {}})
	if len(out) != 1 || out[0]["n"] != 3 {
		t.Fatalf("count wrong: %+v", out)
	}
}

func TestTapOp_passthroughRecordsCount(t *testing.T) {
	op, err := tapFactory(stageJSON(t, map[string]any{"op": "tap", "label": "dbg"}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	rows := []dsl.Row{{"v": 1}, {"v": 2}}
	out, err := op.Apply(context.Background(), rows)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("tap changed row count")
	}
	if tap, ok := op.(*tapOp); ok {
		if tap.Count() != 2 || tap.Label() != "dbg" {
			t.Fatalf("tap state wrong: %+v", tap)
		}
	}
}

func TestBuild_unknownOpErrors(t *testing.T) {
	_, err := Build(dsl.PipeStage{Op: "frobnicate"}, nil)
	if err == nil {
		t.Fatalf("expected error for unknown op")
	}
}
