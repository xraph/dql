package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestFlatten_basicArray(t *testing.T) {
	op, err := flattenFactory(stageJSON(t, map[string]any{
		"op":    "flatten",
		"field": "tags",
	}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{
		{"id": 1, "tags": []any{"a", "b"}},
		{"id": 2, "tags": []any{"c"}},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 output rows, got %d", len(out))
	}
	if out[0]["tags"] != "a" || out[0]["id"] != 1 {
		t.Fatalf("first output wrong: %+v", out[0])
	}
}

func TestFlatten_asKeepsOriginal(t *testing.T) {
	op, _ := flattenFactory(stageJSON(t, map[string]any{
		"op":    "flatten",
		"field": "tags",
		"as":    "tag",
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{{"id": 1, "tags": []any{"a", "b"}}})
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}
	if out[0]["tag"] != "a" {
		t.Fatalf("as column not populated: %+v", out[0])
	}
	// Original field preserved under its name when As is set.
	if _, ok := out[0]["tags"].([]any); !ok {
		t.Fatalf("original tags array should be preserved when as is set: %+v", out[0])
	}
}

func TestFlatten_indexAs(t *testing.T) {
	op, _ := flattenFactory(stageJSON(t, map[string]any{
		"op":      "flatten",
		"field":   "items",
		"indexAs": "idx",
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{{"items": []any{"x", "y", "z"}}})
	if len(out) != 3 {
		t.Fatalf("rows: %d", len(out))
	}
	if out[0]["idx"] != 0 || out[2]["idx"] != 2 {
		t.Fatalf("idx wrong: %+v", out)
	}
}

func TestFlatten_emptyArrayDropsRowByDefault(t *testing.T) {
	op, _ := flattenFactory(stageJSON(t, map[string]any{"op": "flatten", "field": "tags"}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"id": 1, "tags": []any{}},
		{"id": 2, "tags": []any{"ok"}},
	})
	if len(out) != 1 {
		t.Fatalf("empty array should drop the row by default: %+v", out)
	}
	if out[0]["id"] != 2 {
		t.Fatalf("kept the wrong row: %+v", out)
	}
}

func TestFlatten_preserveEmpty(t *testing.T) {
	op, _ := flattenFactory(stageJSON(t, map[string]any{
		"op":            "flatten",
		"field":         "tags",
		"preserveEmpty": true,
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"id": 1, "tags": []any{}},
	})
	if len(out) != 1 {
		t.Fatalf("preserveEmpty should keep rows with empty arrays: %+v", out)
	}
}

func TestFlatten_scalarField_dropsRow(t *testing.T) {
	op, _ := flattenFactory(stageJSON(t, map[string]any{"op": "flatten", "field": "x"}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{{"x": 42}})
	if len(out) != 0 {
		t.Fatalf("non-array value should not be flattened: %+v", out)
	}
}

func TestFlatten_missingField_factoryErrors(t *testing.T) {
	_, err := flattenFactory(stageJSON(t, map[string]any{"op": "flatten"}), nil)
	if err == nil {
		t.Fatalf("expected error for missing field")
	}
}
