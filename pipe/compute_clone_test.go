package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

// mutatingFormula simulates the formula manager's in-place mutation:
// it adds the result column to each row map it receives.
type mutatingFormula struct{}

func (mutatingFormula) ComputeOne(_ context.Context, _, _, as, _ string, rows []map[string]any) ([]map[string]any, error) {
	for _, r := range rows {
		r[as] = "mutated"
	}
	return rows, nil
}

func TestCompute_formula_doesNotMutateInputRows(t *testing.T) {
	op, err := computeFactory(stageJSON(t, map[string]any{
		"op":      "compute",
		"kind":    "formula",
		"as":      "tag",
		"formula": "noop",
	}), &OpContext{Formula: mutatingFormula{}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{{"id": 1}, {"id": 2}}
	ctx := withScope(context.Background(), "ws", "")
	out, err := op.Apply(ctx, in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Input rows must not have gained the "tag" column.
	for i, r := range in {
		if _, leaked := r["tag"]; leaked {
			t.Fatalf("input row %d mutated: %+v", i, r)
		}
	}
	// Output rows must have it.
	for i, r := range out {
		if r["tag"] != "mutated" {
			t.Fatalf("output row %d missing tag: %+v", i, r)
		}
	}
}
