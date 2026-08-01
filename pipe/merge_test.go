package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestMerge_concatenatesSubPipes(t *testing.T) {
	stage := stageJSON(t, map[string]any{
		"op": "merge",
		"sources": []map[string]any{
			{"pipe": []map[string]any{{"op": "limit", "n": 1}}},
			{"pipe": []map[string]any{{"op": "limit", "n": 2}}},
		},
	})
	op, err := mergeFactory(stage, &OpContext{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{{"id": 1}, {"id": 2}, {"id": 3}}
	out, err := op.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 3 { // 1 + 2
		t.Fatalf("merge total wrong: %d", len(out))
	}
}

func TestMerge_isolatesSubPipeMutations(t *testing.T) {
	// First sub renames id → key. Second sub keeps id intact. Output should
	// contain rows from both — proving mutations in sub 1 didn't leak into
	// sub 2's input.
	stage := stageJSON(t, map[string]any{
		"op": "merge",
		"sources": []map[string]any{
			{"pipe": []map[string]any{{"op": "rename", "map": map[string]string{"id": "key"}}}},
			{"pipe": []map[string]any{{"op": "tap"}}},
		},
	})
	op, err := mergeFactory(stage, &OpContext{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{{"id": 1}}
	out, err := op.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("rows: %d", len(out))
	}
	if _, ok := out[0]["key"]; !ok {
		t.Fatalf("first sub-pipe should have produced 'key': %+v", out[0])
	}
	if _, ok := out[1]["id"]; !ok {
		t.Fatalf("second sub-pipe should have kept 'id': %+v", out[1])
	}
}

func TestMerge_emptySources_factoryErrors(t *testing.T) {
	stage := stageJSON(t, map[string]any{"op": "merge", "sources": []any{}})
	_, err := mergeFactory(stage, &OpContext{})
	if err == nil {
		t.Fatalf("expected error for empty sources")
	}
}
