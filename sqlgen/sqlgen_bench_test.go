package sqlgen

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/planner"
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
			{Name: "workspace_id", Type: "string"},
		},
	}, nil
}

func BenchmarkGenerateSQL(b *testing.B) {
	sc := scope.Scope{{Name: "workspace_id", Value: "w1", Required: true}}
	limit := 10
	cases := []struct {
		name string
		q    *dsl.QueryDSL
	}{
		{"simple", &dsl.QueryDSL{
			From:  dsl.FromClause{Dataset: "events"},
			Where: &dsl.WhereClause{Field: "status", Op: "==", Value: "open"},
		}},
		{"compound", &dsl.QueryDSL{
			From: dsl.FromClause{Dataset: "events"},
			Where: &dsl.WhereClause{And: []dsl.WhereClause{
				{Field: "status", Op: "==", Value: "open"},
				{Field: "score", Op: ">", Value: 50},
			}},
			OrderBy: []dsl.OrderByClause{{Field: "created_at", Dir: "desc"}},
			Limit:   &limit,
		}},
	}
	p := planner.NewPlanner(benchSchema{}, sc)
	for _, tc := range cases {
		plan, err := p.Plan(context.Background(), tc.q, "w1")
		if err != nil {
			b.Fatalf("setup plan %s: %v", tc.name, err)
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := GenerateSQL(plan, sc); err != nil {
					b.Fatalf("generate: %v", err)
				}
			}
		})
	}
}
