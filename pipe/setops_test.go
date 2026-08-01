package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestIntersect_byKey(t *testing.T) {
	stage := stageJSON(t, map[string]any{
		"op": "intersect",
		"sources": []map[string]any{
			{"pipe": []map[string]any{{"op": "tap"}}},
			{"pipe": []map[string]any{{"op": "filter", "where": map[string]any{"field": "id", "op": ">", "value": 1}}}},
		},
		"by": []string{"id"},
	})
	op, err := intersectFactory(stage, &OpContext{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{
		{"id": 1, "v": "a"},
		{"id": 2, "v": "b"},
		{"id": 3, "v": "c"},
	}
	out, _ := op.Apply(context.Background(), in)
	// First sub keeps all 3 rows; second sub drops id=1.
	// Intersect on `id` → rows 2,3 from first sub.
	if len(out) != 2 {
		t.Fatalf("intersect rows wrong: %+v", out)
	}
}

func TestExcept_byKey(t *testing.T) {
	stage := stageJSON(t, map[string]any{
		"op":   "except",
		"left": map[string]any{"pipe": []map[string]any{{"op": "tap"}}},
		"right": map[string]any{"pipe": []map[string]any{
			{"op": "filter", "where": map[string]any{"field": "id", "op": "==", "value": 2}},
		}},
		"by": []string{"id"},
	})
	op, err := exceptFactory(stage, &OpContext{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{{"id": 1}, {"id": 2}, {"id": 3}}
	out, _ := op.Apply(context.Background(), in)
	if len(out) != 2 {
		t.Fatalf("except rows wrong: %+v", out)
	}
	for _, row := range out {
		if row["id"] == 2 {
			t.Fatalf("right's row leaked into except output: %+v", row)
		}
	}
}

func TestCrossJoin_cartesian(t *testing.T) {
	right := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"x": 1}, {"x": 2},
	})}
	op, err := crossJoinFactory(stageJSON(t, map[string]any{
		"op":      "crossJoin",
		"dataset": "rhs",
	}), &OpContext{Classic: right})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	ctx := withScope(context.Background(), "ws1", "")
	out, _ := op.Apply(ctx, []dsl.Row{{"a": "x"}, {"a": "y"}})
	if len(out) != 4 {
		t.Fatalf("cartesian product should be 2×2=4: %+v", out)
	}
}
