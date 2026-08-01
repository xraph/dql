package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestLookup_leftJoin_nullsForUnmatched(t *testing.T) {
	right := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"id": "a", "name": "Alpha"},
		{"id": "b", "name": "Beta"},
	})}
	op, err := lookupFactory(stageJSON(t, map[string]any{
		"op":      "lookup",
		"dataset": "sites",
		"on":      map[string]any{"left": "site", "right": "id"},
		"select":  []string{"name"},
		"mode":    "left",
	}), &OpContext{Classic: right})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	ctx := withScope(context.Background(), "ws1", "")
	out, err := op.Apply(ctx, []dsl.Row{
		{"x": 1, "site": "a"},
		{"x": 2, "site": "z"}, // unmatched
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("left join should preserve unmatched rows, got %d", len(out))
	}
	if out[0]["name"] != "Alpha" {
		t.Fatalf("match 0 wrong: %+v", out[0])
	}
	if out[1]["name"] != nil && out[1]["name"] != "" {
		// unmatched left row should not carry a right-side value
		_, present := out[1]["name"]
		if present {
			t.Fatalf("unmatched row should not have right-side column: %+v", out[1])
		}
	}
}

func TestLookup_innerJoin_dropsUnmatched(t *testing.T) {
	right := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"id": "a", "name": "Alpha"},
	})}
	op, err := lookupFactory(stageJSON(t, map[string]any{
		"op":      "lookup",
		"dataset": "sites",
		"on":      map[string]any{"left": "site", "right": "id"},
		"mode":    "inner",
	}), &OpContext{Classic: right})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	ctx := withScope(context.Background(), "ws1", "")
	out, err := op.Apply(ctx, []dsl.Row{
		{"x": 1, "site": "a"},
		{"x": 2, "site": "z"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("inner join should drop unmatched rows, got %d", len(out))
	}
}

func TestLookup_asPrefix_namespacesColumns(t *testing.T) {
	right := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"id": "a", "name": "Alpha", "region": "us"},
	})}
	op, err := lookupFactory(stageJSON(t, map[string]any{
		"op":      "lookup",
		"dataset": "sites",
		"on":      map[string]any{"left": "site", "right": "id"},
		"as":      "site",
		"select":  []string{"name", "region"},
	}), &OpContext{Classic: right})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	ctx := withScope(context.Background(), "ws1", "")
	out, err := op.Apply(ctx, []dsl.Row{{"site": "a"}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out[0]["site_name"] != "Alpha" || out[0]["site_region"] != "us" {
		t.Fatalf("prefix not applied: %+v", out[0])
	}
}

func TestLookup_missingClassicErrorsAtBuild(t *testing.T) {
	_, err := lookupFactory(stageJSON(t, map[string]any{
		"op":      "lookup",
		"dataset": "x",
		"on":      map[string]any{"left": "a", "right": "b"},
	}), &OpContext{})
	if err == nil {
		t.Fatalf("expected error when Classic is nil")
	}
}

func TestLookup_missingScopeErrors(t *testing.T) {
	op, _ := lookupFactory(stageJSON(t, map[string]any{
		"op":      "lookup",
		"dataset": "x",
		"on":      map[string]any{"left": "a", "right": "b"},
	}), &OpContext{Classic: &stubClassic{result: &dsl.QueryResult{}}})
	_, err := op.Apply(context.Background(), []dsl.Row{{"a": 1}})
	if err == nil {
		t.Fatalf("expected error when workspace is missing from context")
	}
}
