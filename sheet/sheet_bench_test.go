package sheet

import (
	"context"
	"testing"

	"github.com/xraph/dql/internal/rowops"
)

// These benchmarks answer one question the design rests on: where do a sheet's
// bytes actually go? The columnar store was justified by storage savings over
// []map[string]any, and the streaming and pushdown phases were justified by
// avoiding materialisation. Both claims are only worth acting on if the row
// maps dominate — measure rather than assume.

func benchRows(n, cols int) []rowops.Row {
	names := []string{"revenue", "cost", "qty", "region", "sku", "ts", "a", "b", "c", "d"}
	if cols > len(names) {
		cols = len(names)
	}
	out := make([]rowops.Row, n)
	for i := range out {
		row := make(rowops.Row, cols)
		for c := 0; c < cols; c++ {
			row[names[c]] = float64(i + c)
		}
		out[i] = row
	}
	return out
}

// BenchmarkRowsVsColumns measures the two representations of the same data
// side by side. The ratio is the entire case for the columnar store.
func BenchmarkRowsVsColumns(b *testing.B) {
	const n = 100_000

	b.Run("build_rows", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = benchRows(n, 10)
		}
	})

	rows := benchRows(n, 10)
	b.Run("build_columns_from_rows", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, name := range []string{"revenue", "cost", "qty", "region", "sku", "ts", "a", "b", "c", "d"} {
				cb := NewColumnBuilder(len(rows))
				for _, r := range rows {
					cb.Append(r[name])
				}
				_ = cb.Build()
			}
		}
	})
}

// BenchmarkSheetApply is the operator's own cost across a row sweep, for a
// sheet with two column formulas and one reduce.
func BenchmarkSheetApply(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(sizeName(n), func(b *testing.B) {
			s, err := Compile(Config{Formulas: []Formula{
				{As: "profit", Expr: "revenue cost -"},
				{As: "total", Reduce: "profit sum"},
				{As: "share", Expr: "profit total /"},
			}}, newFakeCompiler())
			if err != nil {
				b.Fatalf("Compile: %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				rows := benchRows(n, 10)
				b.StartTimer()
				if _, err := s.Apply(context.Background(), rows); err != nil {
					b.Fatalf("Apply: %v", err)
				}
			}
		})
	}
}

// BenchmarkReducePath isolates the native kernel against the compiled path
// over the same column, which is the only place the columnar store is claimed
// to buy speed rather than storage.
func BenchmarkReducePath(b *testing.B) {
	const n = 100_000
	rows := benchRows(n, 2)

	for _, kernels := range []bool{true, false} {
		name := "kernel"
		if !kernels {
			name = "compiled"
		}
		// One reduce, then four over the same column. The second case is what
		// the per-run column cache exists for: build once, scan repeatedly.
		for _, shape := range []struct {
			label    string
			formulas []Formula
		}{
			{"one", []Formula{
				{As: "total", Reduce: "revenue sum"},
			}},
			{"four_same_column", []Formula{
				{As: "total", Reduce: "revenue sum"},
				{As: "mean", Reduce: "revenue avg"},
				{As: "lo", Reduce: "revenue min"},
				{As: "hi", Reduce: "revenue max"},
			}},
		} {
			b.Run(shape.label+"/"+name, func(b *testing.B) {
				s, err := Compile(Config{Formulas: shape.formulas}, newFakeCompiler())
				if err != nil {
					b.Fatalf("Compile: %v", err)
				}
				s.disableKernels = !kernels
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := s.Apply(context.Background(), rows); err != nil {
						b.Fatalf("Apply: %v", err)
					}
				}
			})
		}
	}
}

func sizeName(n int) string {
	switch {
	case n >= 1_000_000:
		return "1M"
	case n >= 100_000:
		return "100k"
	case n >= 10_000:
		return "10k"
	}
	return "1k"
}
