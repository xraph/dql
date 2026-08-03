package planner

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/scope"
)

type benchSchema struct{}

func (benchSchema) ResolveDataset(_ context.Context, _, _, name string) (*dsl.DatasetInfo, error) {
	return &dsl.DatasetInfo{
		ID:        name,
		Name:      name,
		TableName: "ds_" + name,
		Columns: []dsl.ColumnMeta{
			{Name: "id", Type: "int"},
			{Name: "status", Type: "string"},
			{Name: "assignee", Type: "string"},
			{Name: "score", Type: "float"},
			{Name: "created_at", Type: "datetime"},
			{Name: "workspace_id", Type: "string"},
		},
	}, nil
}

func benchScope() scope.Scope {
	return scope.Scope{{Name: "workspace_id", Value: "w1", Required: true}}
}

// BenchmarkPlan covers both sides of the planner's central decision: a query
// that pushes down entirely, and one whose expression predicate forces an
// in-memory tail.
func BenchmarkPlan(b *testing.B) {
	limit := 10
	cases := []struct {
		name string
		q    *dsl.QueryDSL
	}{
		{"pushdown", &dsl.QueryDSL{
			From:    dsl.FromClause{Dataset: "events"},
			Where:   &dsl.WhereClause{Field: "status", Op: "==", Value: "open"},
			OrderBy: []dsl.OrderByClause{{Field: "created_at", Dir: "desc"}},
			Limit:   &limit,
		}},
		{"inmemory", &dsl.QueryDSL{
			From:  dsl.FromClause{Dataset: "events"},
			Where: &dsl.WhereClause{Expr: "score > 50"},
		}},
		{"groupBy", &dsl.QueryDSL{
			From:      dsl.FromClause{Dataset: "events"},
			GroupBy:   []string{"assignee"},
			Aggregate: []dsl.AggregateClause{{Fn: "COUNT", Field: "*", As: "total"}},
		}},
	}
	p := NewPlanner(benchSchema{}, benchScope())
	ctx := context.Background()
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := p.Plan(ctx, tc.q, "w1"); err != nil {
					b.Fatalf("plan: %v", err)
				}
			}
		})
	}
}
