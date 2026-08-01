package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestDropNulls_anyMode(t *testing.T) {
	op, err := dropNullsFactory(stageJSON(t, map[string]any{
		"op":      "dropNulls",
		"columns": []string{"a", "b"},
	}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"a": 1, "b": 2},
		{"a": nil, "b": 2},
		{"a": 3, "b": nil},
		{"a": 4, "b": 5},
	})
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}
}

func TestFillNulls_value(t *testing.T) {
	op, _ := fillNullsFactory(stageJSON(t, map[string]any{
		"op":      "fillNulls",
		"columns": []string{"x"},
		"method":  "value",
		"value":   -1,
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"x": nil}, {"x": 5},
	})
	// JSON round-trips numeric literals as float64.
	if out[0]["x"] != float64(-1) || out[1]["x"] != 5 {
		t.Fatalf("fillValue wrong: %+v", out)
	}
}

func TestFillNulls_lastValue(t *testing.T) {
	op, _ := fillNullsFactory(stageJSON(t, map[string]any{
		"op":      "fillNulls",
		"columns": []string{"x"},
		"method":  "lastValue",
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"x": 10}, {"x": nil}, {"x": 20}, {"x": nil},
	})
	if out[1]["x"] != 10 {
		t.Fatalf("forward-fill row1: %+v", out[1])
	}
	if out[3]["x"] != 20 {
		t.Fatalf("forward-fill row3: %+v", out[3])
	}
}

func TestCast_intFromString(t *testing.T) {
	op, _ := castFactory(stageJSON(t, map[string]any{
		"op": "cast",
		"casts": []map[string]any{
			{"field": "x", "to": "int", "onError": "null"},
		},
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"x": "42"},
		{"x": "not-a-number"},
	})
	if out[0]["x"] != int64(42) {
		t.Fatalf("cast int wrong: %+v", out[0])
	}
	if out[1]["x"] != nil {
		t.Fatalf("onError=null should null the value: %+v", out[1])
	}
}

func TestDedupe_keepFirst(t *testing.T) {
	op, _ := dedupeFactory(stageJSON(t, map[string]any{
		"op": "dedupe",
		"by": []string{"id"},
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"id": 1, "v": "a"},
		{"id": 1, "v": "b"},
		{"id": 2, "v": "c"},
	})
	if len(out) != 2 || out[0]["v"] != "a" {
		t.Fatalf("dedupe first wrong: %+v", out)
	}
}

func TestDedupe_keepLast(t *testing.T) {
	op, _ := dedupeFactory(stageJSON(t, map[string]any{
		"op":   "dedupe",
		"by":   []string{"id"},
		"keep": "last",
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"id": 1, "v": "a"},
		{"id": 1, "v": "b"},
	})
	if out[0]["v"] != "b" {
		t.Fatalf("dedupe last wrong: %+v", out)
	}
}

func TestSample_n_deterministicWithSeed(t *testing.T) {
	make10 := func() []dsl.Row {
		out := make([]dsl.Row, 10)
		for i := range out {
			out[i] = dsl.Row{"i": i}
		}
		return out
	}
	op1, _ := sampleFactory(stageJSON(t, map[string]any{"op": "sample", "n": 3, "seed": 42}), nil)
	op2, _ := sampleFactory(stageJSON(t, map[string]any{"op": "sample", "n": 3, "seed": 42}), nil)
	a, _ := op1.Apply(context.Background(), make10())
	b, _ := op2.Apply(context.Background(), make10())
	if len(a) != 3 || len(b) != 3 {
		t.Fatalf("sample size wrong: %d %d", len(a), len(b))
	}
	for i := range a {
		if a[i]["i"] != b[i]["i"] {
			t.Fatalf("seeded sample is not deterministic: %+v vs %+v", a, b)
		}
	}
}

func TestAssert_rowFails(t *testing.T) {
	eval := &mockEval{results: map[string]any{"x > 0": false}}
	op, _ := assertFactory(stageJSON(t, map[string]any{
		"op":      "assert",
		"expr":    "x > 0",
		"message": "x must be positive",
	}), &OpContext{Eval: eval})
	_, err := op.Apply(context.Background(), []dsl.Row{{"x": -1}})
	if err == nil {
		t.Fatalf("expected assert to fail")
	}
}

func TestAssert_overallScope(t *testing.T) {
	eval := &mockEval{results: map[string]any{"count >= 1": true}}
	op, _ := assertFactory(stageJSON(t, map[string]any{
		"op":    "assert",
		"expr":  "count >= 1",
		"scope": "overall",
	}), &OpContext{Eval: eval})
	out, err := op.Apply(context.Background(), []dsl.Row{{}})
	if err != nil {
		t.Fatalf("overall assert should pass: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("rows passed through: %+v", out)
	}
}
