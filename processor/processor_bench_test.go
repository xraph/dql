package processor

import (
	"context"
	"strconv"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/internal/benchdata"
)

type benchEval struct{}

func (benchEval) Eval(_ context.Context, expr string, row map[string]any) (any, error) {
	return row[expr], nil
}

// BenchmarkProcess measures the in-memory tail. plan.InMemory is what gates
// each stage, so every case names the stage it exercises — an empty InMemory
// would run nothing and quietly measure a no-op.
func BenchmarkProcess(b *testing.B) {
	cases := []struct {
		name string
		plan *dsl.QueryPlan
		q    *dsl.QueryDSL
	}{
		{
			name: "passthrough",
			plan: &dsl.QueryPlan{},
			q:    &dsl.QueryDSL{},
		},
		{
			name: "aggregate",
			plan: &dsl.QueryPlan{InMemory: []string{"aggregate"}},
			q: &dsl.QueryDSL{
				GroupBy:   []string{"assignee"},
				Aggregate: []dsl.AggregateClause{{Fn: "SUM", Field: "score", As: "total"}},
			},
		},
		{
			name: "sort",
			plan: &dsl.QueryPlan{InMemory: []string{"sort"}},
			q: &dsl.QueryDSL{
				OrderBy: []dsl.OrderByClause{{Field: "score", Dir: "desc"}},
			},
		},
		{
			name: "computed",
			plan: &dsl.QueryPlan{},
			q: &dsl.QueryDSL{
				Computed: []dsl.ComputedColumn{{Name: "copy", Expr: "score"}},
			},
		},
	}
	p := NewProcessor(benchEval{})
	ctx := context.Background()
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			for _, n := range benchdata.Sizes() {
				rows := benchdata.Rows(n, 20)
				b.Run("n="+strconv.Itoa(n), func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if _, err := p.Process(ctx, tc.plan, tc.q, rows); err != nil {
							b.Fatalf("process: %v", err)
						}
					}
					b.ReportMetric(float64(n)*float64(b.N)/b.Elapsed().Seconds(), "rows/s")
				})
			}
		})
	}
}
