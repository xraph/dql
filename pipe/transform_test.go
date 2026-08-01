package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestTransform_multipleComputes(t *testing.T) {
	eval := &mockEval{results: map[string]any{
		"a + b":   30.0,
		"a * b":   200.0,
		"total/2": 15.0,
	}}
	op, err := transformFactory(stageJSON(t, map[string]any{
		"op": "transform",
		"compute": []map[string]any{
			{"as": "total", "expr": "a + b"},
			{"as": "prod", "expr": "a * b"},
			{"as": "half", "expr": "total/2"},
		},
	}), &OpContext{Eval: eval})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"a": 10.0, "b": 20.0}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out[0]["total"] != 30.0 {
		t.Fatalf("total: %+v", out[0])
	}
	if out[0]["prod"] != 200.0 {
		t.Fatalf("prod: %+v", out[0])
	}
	if out[0]["half"] != 15.0 {
		t.Fatalf("half should reference earlier `total`: %+v", out[0])
	}
	// Original columns preserved.
	if out[0]["a"] != 10.0 {
		t.Fatalf("original column lost: %+v", out[0])
	}
}

func TestTransform_fromIsColumnCopy(t *testing.T) {
	op, err := transformFactory(stageJSON(t, map[string]any{
		"op": "transform",
		"compute": []map[string]any{
			{"as": "id", "from": "_id"},
		},
	}), nil) // no eval needed for from-only
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"_id": "abc"}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out[0]["id"] != "abc" {
		t.Fatalf("from copy failed: %+v", out[0])
	}
}

func TestTransform_replaceDropsExtraColumns(t *testing.T) {
	eval := &mockEval{results: map[string]any{"a + b": 30.0}}
	op, _ := transformFactory(stageJSON(t, map[string]any{
		"op":      "transform",
		"replace": true,
		"compute": []map[string]any{
			{"as": "id", "from": "id"},
			{"as": "total", "expr": "a + b"},
		},
	}), &OpContext{Eval: eval})
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"id": 1, "a": 10.0, "b": 20.0, "noisy": "drop me"},
	})
	if _, has := out[0]["a"]; has {
		t.Fatalf("replace=true should drop unproduced columns: %+v", out[0])
	}
	if _, has := out[0]["noisy"]; has {
		t.Fatalf("replace=true should drop unproduced columns: %+v", out[0])
	}
	if out[0]["total"] != 30.0 {
		t.Fatalf("produced column missing: %+v", out[0])
	}
}

func TestTransform_dropList(t *testing.T) {
	op, _ := transformFactory(stageJSON(t, map[string]any{
		"op":   "transform",
		"drop": []string{"secret"},
		"compute": []map[string]any{
			{"as": "id", "from": "id"},
		},
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"id": 1, "secret": "shh", "ok": "kept"},
	})
	if _, has := out[0]["secret"]; has {
		t.Fatalf("drop should remove secret: %+v", out[0])
	}
	if out[0]["ok"] != "kept" {
		t.Fatalf("non-replace mode preserves untouched columns: %+v", out[0])
	}
}

func TestTransform_missingEval_factoryErrorsForExprEntry(t *testing.T) {
	_, err := transformFactory(stageJSON(t, map[string]any{
		"op": "transform",
		"compute": []map[string]any{
			{"as": "x", "expr": "1 + 1"},
		},
	}), &OpContext{})
	if err == nil {
		t.Fatalf("expected error when eval missing for expr entry")
	}
}

func TestTransform_fromOnlyDoesNotNeedEval(t *testing.T) {
	op, err := transformFactory(stageJSON(t, map[string]any{
		"op": "transform",
		"compute": []map[string]any{
			{"as": "id", "from": "_id"},
		},
	}), nil)
	if err != nil {
		t.Fatalf("from-only should not need eval: %v", err)
	}
	if op == nil {
		t.Fatalf("op nil")
	}
}

func TestTransform_exprAndFromMutuallyExclusive(t *testing.T) {
	_, err := transformFactory(stageJSON(t, map[string]any{
		"op": "transform",
		"compute": []map[string]any{
			{"as": "x", "expr": "1", "from": "y"},
		},
	}), &OpContext{Eval: &mockEval{}})
	if err == nil {
		t.Fatalf("expected error for both expr and from")
	}
}

func TestTransform_emptyCompute_factoryErrors(t *testing.T) {
	_, err := transformFactory(stageJSON(t, map[string]any{
		"op": "transform", "compute": []any{},
	}), nil)
	if err == nil {
		t.Fatalf("expected error for empty compute")
	}
}
