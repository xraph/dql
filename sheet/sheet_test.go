package sheet

import (
	"context"
	"strings"
	"testing"

	"github.com/xraph/dql/internal/rowops"
)

// --- Compile ---

func TestCompile_compilesEachExpressionExactlyOnce(t *testing.T) {
	c := newFakeCompiler()
	s, err := Compile(Config{Formulas: []Formula{
		{As: "profit", Expr: "revenue cost -"},
		{As: "total", Reduce: "profit sum"},
	}}, c)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if c.compiles != 2 {
		t.Errorf("compiled %d expressions, want 2 — one per formula, not one per row", c.compiles)
	}
	if got := s.Names(); len(got) != 2 || got[0] != "profit" || got[1] != "total" {
		t.Errorf("Names() = %v", got)
	}
}

func TestCompile_doesNotRecompilePerRow(t *testing.T) {
	c := newFakeCompiler()
	s, err := Compile(Config{Formulas: []Formula{{As: "double", Expr: "v 2 *"}}}, c)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	before := c.compiles

	rows := make([]rowops.Row, 500)
	for i := range rows {
		rows[i] = rowops.Row{"v": float64(i)}
	}
	if _, err := s.Apply(context.Background(), rows); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if c.compiles != before {
		t.Errorf("Apply compiled %d more expressions; evaluation must reuse the compiled form", c.compiles-before)
	}
}

func TestCompile_rejectsACycle(t *testing.T) {
	_, err := Compile(Config{Formulas: []Formula{
		{As: "a", Expr: "b 1 +"},
		{As: "b", Expr: "a 1 +"},
	}}, newFakeCompiler())
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("want a circular-dependency error, got %v", err)
	}
}

func TestCompile_rejectsAnUnknownPolicy(t *testing.T) {
	_, err := Compile(Config{
		Formulas: []Formula{{As: "a", Expr: "x"}},
		OnError:  "maybe",
	}, newFakeCompiler())
	if err == nil || !strings.Contains(err.Error(), "onError") {
		t.Fatalf("want an onError error, got %v", err)
	}
}

func TestCompile_requiresACompiler(t *testing.T) {
	_, err := Compile(Config{Formulas: []Formula{{As: "a", Expr: "x"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "expression compiler") {
		t.Fatalf("want a missing-compiler error, got %v", err)
	}
}

func TestCompile_surfacesAParseErrorAgainstItsFormula(t *testing.T) {
	_, err := Compile(Config{Formulas: []Formula{{As: "bad", Expr: "   "}}}, newFakeCompiler())
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("a parse failure must name the formula it came from, got %v", err)
	}
}

// --- Apply ---

func TestApply_computesColumnsInDependencyOrder(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		// Declared out of order on purpose.
		{As: "margin", Expr: "profit revenue /"},
		{As: "profit", Expr: "revenue cost -"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	res, err := s.Apply(context.Background(), []rowops.Row{
		{"revenue": 100.0, "cost": 60.0},
		{"revenue": 200.0, "cost": 150.0},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Rows[0]["profit"] != 40.0 || res.Rows[0]["margin"] != 0.4 {
		t.Errorf("row 0: profit=%v margin=%v", res.Rows[0]["profit"], res.Rows[0]["margin"])
	}
	if res.Rows[1]["profit"] != 50.0 || res.Rows[1]["margin"] != 0.25 {
		t.Errorf("row 1: profit=%v margin=%v", res.Rows[1]["profit"], res.Rows[1]["margin"])
	}
}

func TestApply_reduceSeesTheWholeColumnIncludingComputedOnes(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "profit", Expr: "revenue cost -"},
		{As: "total_profit", Reduce: "profit sum"},
		{As: "share", Expr: "profit total_profit /"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	res, err := s.Apply(context.Background(), []rowops.Row{
		{"revenue": 100.0, "cost": 60.0},  // profit 40
		{"revenue": 200.0, "cost": 140.0}, // profit 60
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Scalars["total_profit"] != 100.0 {
		t.Fatalf("total_profit = %v, want 100", res.Scalars["total_profit"])
	}
	if res.Rows[0]["share"] != 0.4 || res.Rows[1]["share"] != 0.6 {
		t.Errorf("share: %v, %v", res.Rows[0]["share"], res.Rows[1]["share"])
	}
	// A reduce is a sheet-wide scalar; the engine does not write it into rows.
	if _, present := res.Rows[0]["total_profit"]; present {
		t.Error("a reduce must not be written into rows by the engine")
	}
}

func TestApply_unknownReferenceIsAnError(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "x", Expr: "revenu 1 +"}, // typo
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = s.Apply(context.Background(), []rowops.Row{{"revenue": 1.0}})
	if err == nil || !strings.Contains(err.Error(), "revenu") {
		t.Fatalf("want an error naming the unresolved identifier, got %v", err)
	}
}

func TestApply_sparseRowsStillDefineTheColumn(t *testing.T) {
	// A column absent from row 0 but present later is still a column of the
	// sheet: reference checking unions the keys of every row, so this must not
	// be reported as an unresolved reference. Under the null policy the row
	// that lacks the key simply yields null.
	s, err := Compile(Config{
		Formulas: []Formula{{As: "out", Expr: "late 1 +"}},
		OnError:  "null",
	}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := s.Apply(context.Background(), []rowops.Row{
		{"other": 1.0},
		{"late": 5.0},
	})
	if err != nil {
		t.Fatalf("a key present in a later row must count as a column: %v", err)
	}
	if res.Rows[0]["out"] != nil {
		t.Errorf("row missing the column should yield null, got %v", res.Rows[0]["out"])
	}
	if res.Rows[1]["out"] != 6.0 {
		t.Errorf("row 1 = %v, want 6", res.Rows[1]["out"])
	}
}

func TestApply_failPolicyAbortsOnFirstError(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "ratio", Expr: "a b /"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = s.Apply(context.Background(), []rowops.Row{
		{"a": 1.0, "b": 1.0},
		{"a": 1.0, "b": 0.0}, // division by zero
	})
	if err == nil || !strings.Contains(err.Error(), "ratio") {
		t.Fatalf("want an abort naming the formula, got %v", err)
	}
}

func TestApply_nullPolicyContinuesAndRecords(t *testing.T) {
	s, err := Compile(Config{
		Formulas: []Formula{{As: "ratio", Expr: "a b /"}},
		OnError:  "null",
	}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := s.Apply(context.Background(), []rowops.Row{
		{"a": 1.0, "b": 0.0}, // fails
		{"a": 6.0, "b": 2.0}, // fine
	})
	if err != nil {
		t.Fatalf("Apply must not abort under the null policy: %v", err)
	}
	if res.Rows[0]["ratio"] != nil {
		t.Errorf("failing cell must be null, got %v", res.Rows[0]["ratio"])
	}
	if res.Rows[1]["ratio"] != 3.0 {
		t.Errorf("row 1 = %v, want 3", res.Rows[1]["ratio"])
	}
	if res.ErrorCount != 1 || len(res.Errors) != 1 || res.Errors[0].Row != 0 {
		t.Errorf("errors = %+v count=%d", res.Errors, res.ErrorCount)
	}
}

func TestApply_nullPolicyCapsWhatItRetainsButNotWhatItCounts(t *testing.T) {
	s, err := Compile(Config{
		Formulas: []Formula{{As: "ratio", Expr: "a b /"}},
		OnError:  "null",
	}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	rows := make([]rowops.Row, MaxRecordedErrors+50)
	for i := range rows {
		rows[i] = rowops.Row{"a": 1.0, "b": 0.0} // every row fails
	}
	res, err := s.Apply(context.Background(), rows)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Errors) != MaxRecordedErrors {
		t.Errorf("retained %d errors, want the cap of %d", len(res.Errors), MaxRecordedErrors)
	}
	if res.ErrorCount != len(rows) {
		t.Errorf("ErrorCount = %d, want the true total %d", res.ErrorCount, len(rows))
	}
}

func TestApply_emptyInputStillEvaluatesReduces(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "n", Reduce: "revenue count"},
		{As: "total", Reduce: "revenue sum"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := s.Apply(context.Background(), nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Scalars["n"] != int64(0) {
		t.Errorf("count over no rows = %v, want 0", res.Scalars["n"])
	}
	if res.Scalars["total"] != nil {
		t.Errorf("sum over no rows = %v, want nil (SQL returns NULL)", res.Scalars["total"])
	}
}

func TestApply_honoursCancellation(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{{As: "out", Expr: "v 1 +"}}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Apply(ctx, []rowops.Row{{"v": 1.0}}); err == nil {
		t.Fatal("a cancelled context must stop the sheet")
	}
}
