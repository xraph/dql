package sheet

import (
	"context"
	"math"
	"testing"

	"github.com/xraph/dql/internal/rowops"
)

func floatCol(vals []float64, nullAt ...int) Column {
	nulls := NewBitmap(len(vals))
	for _, i := range nullAt {
		nulls.Set(i)
	}
	return NewFloatColumn(vals, nulls)
}

func TestKernels_matchSQLOnNullsAndEmptyInput(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		col  Column
		want any
	}{
		{"sum", "sum", floatCol([]float64{1, 2, 3}), 6.0},
		{"sum skips nulls", "sum", floatCol([]float64{1, 0, 3}, 1), 4.0},
		{"sum of nothing is null", "sum", floatCol(nil), nil},
		{"avg", "avg", floatCol([]float64{2, 4}), 3.0},
		{"avg skips nulls", "avg", floatCol([]float64{2, 0, 4}, 1), 3.0},
		{"avg of nothing is null", "avg", floatCol(nil), nil},
		{"min", "min", floatCol([]float64{5, 2, 9}), 2.0},
		{"max", "max", floatCol([]float64{5, 2, 9}), 9.0},
		{"min of nothing is null", "min", floatCol(nil), nil},
		{"max of nothing is null", "max", floatCol(nil), nil},
		{"count skips nulls", "count", floatCol([]float64{1, 0, 3}, 1), int64(2)},
		{"count of nothing is zero", "count", floatCol(nil), int64(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, ok := LookupReduce(tt.fn)
			if !ok {
				t.Fatalf("no kernel for %q", tt.fn)
			}
			got, err := k.Reduce(tt.col)
			if err != nil {
				t.Fatalf("Reduce: %v", err)
			}
			if got != tt.want {
				t.Errorf("%s = %v (%T), want %v (%T)", tt.fn, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestKernels_negativeValuesDoNotConfuseMinMax(t *testing.T) {
	// A kernel seeded with a zero accumulator instead of the first value would
	// report 0 here.
	for fn, want := range map[string]float64{"min": -9, "max": -2} {
		k, _ := LookupReduce(fn)
		got, err := k.Reduce(floatCol([]float64{-5, -2, -9}))
		if err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		if got != want {
			t.Errorf("%s = %v, want %v", fn, got, want)
		}
	}
}

func TestKernels_declareTheirPushdownName(t *testing.T) {
	for _, name := range []string{"count", "sum", "avg", "min", "max"} {
		k, ok := LookupReduce(name)
		if !ok {
			t.Fatalf("no kernel for %q", name)
		}
		if k.PushdownName() != name {
			t.Errorf("%s: PushdownName = %q, want %q", name, k.PushdownName(), name)
		}
	}
}

func TestKernels_handleAnAnyBackedColumn(t *testing.T) {
	k, _ := LookupReduce("sum")
	got, err := k.Reduce(NewAnyColumn([]any{1.0, nil, "not a number", 2.0}))
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if got != 3.0 {
		t.Errorf("sum over mixed values = %v, want 3", got)
	}
}

func TestKernels_countIncludesNonNumericValues(t *testing.T) {
	k, _ := LookupReduce("count")
	got, err := k.Reduce(NewAnyColumn([]any{"a", nil, "b"}))
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if got != int64(2) {
		t.Errorf("count of a text column = %v, want 2", got)
	}
}

func TestKernels_sumOfInfinitiesStaysInfinite(t *testing.T) {
	k, _ := LookupReduce("sum")
	got, err := k.Reduce(floatCol([]float64{math.Inf(1), 1}))
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if f, _ := got.(float64); !math.IsInf(f, 1) {
		t.Errorf("got %v, want +Inf", got)
	}
}

// --- Kernel / compiler equivalence ---
//
// Every kernel must agree with the compiler-evaluated form. This is the guard
// that keeps the native path an optimisation rather than a second semantics;
// without it the two could drift on exactly the cases above.

func TestReduce_kernelAgreesWithTheCompiler(t *testing.T) {
	fixtures := map[string][]rowops.Row{
		"mixed":       {{"v": 1.0}, {"v": nil}, {"v": 4.0}, {"v": 10.0}},
		"all null":    {{"v": nil}, {"v": nil}},
		"single":      {{"v": 7.0}},
		"negatives":   {{"v": -5.0}, {"v": -2.0}, {"v": -9.0}},
		"empty":       {},
		"non-numeric": {{"v": "text"}, {"v": 3.0}},
	}
	for _, fn := range []string{"sum", "avg", "min", "max", "count"} {
		for name, rows := range fixtures {
			t.Run(fn+"/"+name, func(t *testing.T) {
				viaKernel := runReduce(t, "v "+fn, rows, true)
				viaCompiler := runReduce(t, "v "+fn, rows, false)
				if viaKernel != viaCompiler {
					t.Errorf("kernel = %v (%T), compiler = %v (%T)",
						viaKernel, viaKernel, viaCompiler, viaCompiler)
				}
			})
		}
	}
}

func runReduce(t *testing.T, src string, rows []rowops.Row, kernels bool) any {
	t.Helper()
	s, err := Compile(Config{Formulas: []Formula{{As: "out", Reduce: src}}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	s.disableKernels = !kernels
	res, err := s.Apply(context.Background(), rows)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res.Scalars["out"]
}

func TestReduce_kernelMatchesBothSpellings(t *testing.T) {
	// The postfix form the toy grammar uses, and the prefix form a real
	// expression language would use.
	for _, src := range []string{"v sum", "sum(v)"} {
		s, err := Compile(Config{Formulas: []Formula{{As: "out", Reduce: src}}}, newFakeCompiler())
		if err != nil {
			t.Fatalf("Compile(%q): %v", src, err)
		}
		if _, col, ok := s.kernelFor(s.order[0]); !ok || col != "v" {
			t.Errorf("%q did not select a kernel (col=%q ok=%v)", src, col, ok)
		}
	}
}

func TestReduce_compoundExpressionsBypassKernels(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "out", Reduce: "v sum 2 /"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, _, ok := s.kernelFor(s.order[0]); ok {
		t.Fatal("a compound reduce must not match a kernel")
	}
	res, err := s.Apply(context.Background(), []rowops.Row{{"v": 4.0}, {"v": 6.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Scalars["out"] != 5.0 {
		t.Errorf("got %v, want 5", res.Scalars["out"])
	}
}

func TestReduce_multiColumnReducesBypassKernels(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "out", Reduce: "a sum b sum +"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, _, ok := s.kernelFor(s.order[0]); ok {
		t.Fatal("a reduce over two columns has no single-column kernel")
	}
	res, err := s.Apply(context.Background(), []rowops.Row{{"a": 1.0, "b": 10.0}, {"a": 2.0, "b": 20.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Scalars["out"] != 33.0 {
		t.Errorf("got %v, want 33", res.Scalars["out"])
	}
}
