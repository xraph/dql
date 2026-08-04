package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/sheet"
)

func TestSheetOp_computesInDependencyOrder(t *testing.T) {
	raw := json.RawMessage(`{"formulas":[
		{"as":"margin","expr":"profit revenue /"},
		{"as":"profit","expr":"revenue cost -"}
	]}`)
	op, err := Build(dsl.PipeStage{Op: "sheet", Config: raw}, &OpContext{ExprCompiler: testCompiler{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"revenue": 100.0, "cost": 60.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0]["profit"] != 40.0 || out[0]["margin"] != 0.4 {
		t.Errorf("got %+v", out[0])
	}
}

func TestSheetOp_reduceLandsInEveryRow(t *testing.T) {
	// A pipe stage returns rows, so a sheet-wide scalar has to reach the
	// caller as a column: every row carries the same value.
	raw := json.RawMessage(`{"formulas":[{"as":"total","reduce":"v sum"}]}`)
	op, err := Build(dsl.PipeStage{Op: "sheet", Config: raw}, &OpContext{ExprCompiler: testCompiler{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"v": 1.0}, {"v": 2.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for i, row := range out {
		if row["total"] != 3.0 {
			t.Errorf("row %d total = %v, want 3", i, row["total"])
		}
	}
}

func TestSheetOp_needsAnExprCompiler(t *testing.T) {
	raw := json.RawMessage(`{"formulas":[{"as":"a","expr":"x"}]}`)
	_, err := Build(dsl.PipeStage{Op: "sheet", Config: raw}, &OpContext{})
	if err == nil || !strings.Contains(err.Error(), "exprCompiler") {
		t.Fatalf("want a requirement error naming exprCompiler, got %v", err)
	}
}

func TestSheetOp_isReportedAsMissingWithoutACompiler(t *testing.T) {
	missing := MissingRequirements(&OpContext{})
	got, ok := missing["sheet"]
	if !ok {
		t.Fatal("sheet must be reported unavailable when no compiler is wired")
	}
	if len(got) != 1 || got[0] != ReqExprCompiler {
		t.Errorf("sheet requirements = %v", got)
	}
}

func TestSheetOp_isAvailableWithACompiler(t *testing.T) {
	if _, missing := MissingRequirements(&OpContext{ExprCompiler: testCompiler{}})["sheet"]; missing {
		t.Error("sheet must be available once a compiler is wired")
	}
}

func TestSheetOp_isInTheCatalog(t *testing.T) {
	meta, ok := CatalogIndex()["sheet"]
	if !ok {
		t.Fatal("sheet is registered but missing from Catalog()")
	}
	if len(meta.Requires) != 1 || meta.Requires[0] != ReqExprCompiler {
		t.Errorf("catalog Requires = %v", meta.Requires)
	}
}

func TestSheetOp_isLiveSafe(t *testing.T) {
	raw := json.RawMessage(`{"formulas":[{"as":"a","expr":"x 1 +"}]}`)
	op, err := Build(dsl.PipeStage{Op: "sheet", Config: raw}, &OpContext{ExprCompiler: testCompiler{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !op.IsLiveSafe() {
		t.Error("a sheet is pure; it must be live-safe")
	}
}

func TestSheetOp_validationFailsAtBuildNotAtApply(t *testing.T) {
	raw := json.RawMessage(`{"formulas":[
		{"as":"a","expr":"b 1 +"},
		{"as":"b","expr":"a 1 +"}
	]}`)
	_, err := Build(dsl.PipeStage{Op: "sheet", Config: raw}, &OpContext{ExprCompiler: testCompiler{}})
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("a cycle must be rejected while the stage is being built, got %v", err)
	}
}

func TestSheetOp_validateStagesRejectsABadSheet(t *testing.T) {
	stages := []dsl.PipeStage{{
		Op:     "sheet",
		Config: json.RawMessage(`{"formulas":[{"as":"a","expr":"x","reduce":"x sum"}]}`),
	}}
	err := ValidateStages(stages, &OpContext{ExprCompiler: testCompiler{}})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("want a validation error about the two keys, got %v", err)
	}
}

func TestSheetOp_onErrorNullKeepsGoing(t *testing.T) {
	raw := json.RawMessage(`{"onError":"null","formulas":[{"as":"r","expr":"a b /"}]}`)
	op, err := Build(dsl.PipeStage{Op: "sheet", Config: raw}, &OpContext{ExprCompiler: testCompiler{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{
		{"a": 1.0, "b": 0.0},
		{"a": 6.0, "b": 2.0},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0]["r"] != nil || out[1]["r"] != 3.0 {
		t.Errorf("got %v, %v", out[0]["r"], out[1]["r"])
	}
}

// --- test compiler ---
//
// A whitespace-separated postfix grammar, enough to exercise the operator
// without wiring a real expression language. Kept here rather than exported
// from the sheet package so no toy grammar ships in production code.

type testCompiler struct{}

func (testCompiler) FreeIdentifiers(expr string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.Fields(expr) {
		if isTestOperator(tok) {
			continue
		}
		if _, err := strconv.ParseFloat(tok, 64); err == nil {
			continue
		}
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out, nil
}

func (testCompiler) Compile(expr string) (sheet.CompiledExpr, error) {
	toks := strings.Fields(expr)
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	return &testExpr{toks: toks}, nil
}

func isTestOperator(tok string) bool {
	switch tok {
	case "+", "-", "*", "/", "sum", "count":
		return true
	}
	return false
}

type testExpr struct{ toks []string }

func (e *testExpr) Eval(_ context.Context, args map[string]any) (any, error) {
	var stack []any
	for _, tok := range e.toks {
		switch tok {
		case "+", "-", "*", "/":
			if len(stack) < 2 {
				return nil, fmt.Errorf("stack underflow at %q", tok)
			}
			b, a := stack[len(stack)-1], stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			af, aok := a.(float64)
			bf, bok := b.(float64)
			if !aok || !bok {
				return nil, fmt.Errorf("non-numeric operand for %q", tok)
			}
			switch tok {
			case "+":
				stack = append(stack, af+bf)
			case "-":
				stack = append(stack, af-bf)
			case "*":
				stack = append(stack, af*bf)
			case "/":
				if bf == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				stack = append(stack, af/bf)
			}
		case "sum", "count":
			if len(stack) < 1 {
				return nil, fmt.Errorf("stack underflow at %q", tok)
			}
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			vals, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("%s: want a column, got %T", tok, v)
			}
			var acc float64
			var n int64
			for _, x := range vals {
				if x == nil {
					continue
				}
				n++
				if f, ok := x.(float64); ok {
					acc += f
				}
			}
			if tok == "count" {
				stack = append(stack, n)
				continue
			}
			if n == 0 {
				stack = append(stack, nil)
				continue
			}
			stack = append(stack, acc)
		default:
			if f, err := strconv.ParseFloat(tok, 64); err == nil {
				stack = append(stack, f)
				continue
			}
			stack = append(stack, args[tok])
		}
	}
	if len(stack) != 1 {
		return nil, fmt.Errorf("expression left %d values on the stack", len(stack))
	}
	return stack[0], nil
}
