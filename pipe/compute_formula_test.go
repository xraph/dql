package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

type stubFormula struct {
	lastWS   string
	lastProj string
	lastAs   string
	lastExpr string
	lastRows []map[string]any
	out      []map[string]any
	err      error
}

func (s *stubFormula) ComputeOne(_ context.Context, ws, proj, as, expr string, rows []map[string]any) ([]map[string]any, error) {
	s.lastWS = ws
	s.lastProj = proj
	s.lastAs = as
	s.lastExpr = expr
	s.lastRows = rows
	if s.err != nil {
		return nil, s.err
	}
	return s.out, nil
}

func TestCompute_formulaKind_callsComputer(t *testing.T) {
	fm := &stubFormula{out: []map[string]any{{"v": 1, "tax": 0.1}}}
	op, err := computeFactory(stageJSON(t, map[string]any{
		"op":      "compute",
		"kind":    "formula",
		"as":      "tax",
		"formula": "v * 0.1",
	}), &OpContext{Formula: fm})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	ctx := withScope(context.Background(), "ws1", "proj1")
	out, err := op.Apply(ctx, []dsl.Row{{"v": 1}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if fm.lastWS != "ws1" || fm.lastProj != "proj1" {
		t.Fatalf("scope not threaded: ws=%q proj=%q", fm.lastWS, fm.lastProj)
	}
	if fm.lastAs != "tax" || fm.lastExpr != "v * 0.1" {
		t.Fatalf("formula args wrong: as=%q expr=%q", fm.lastAs, fm.lastExpr)
	}
	if out[0]["tax"] != 0.1 {
		t.Fatalf("output wrong: %+v", out[0])
	}
}

func TestCompute_formulaKind_missingFormula_factoryErrors(t *testing.T) {
	_, err := computeFactory(stageJSON(t, map[string]any{
		"op":   "compute",
		"kind": "formula",
		"as":   "tax",
	}), &OpContext{Formula: &stubFormula{}})
	if err == nil {
		t.Fatalf("expected error for missing formula")
	}
}

func TestCompute_formulaKind_missingComputer_factoryErrors(t *testing.T) {
	_, err := computeFactory(stageJSON(t, map[string]any{
		"op":      "compute",
		"kind":    "formula",
		"as":      "tax",
		"formula": "v",
	}), &OpContext{})
	if err == nil {
		t.Fatalf("expected error for missing FormulaComputer")
	}
}
