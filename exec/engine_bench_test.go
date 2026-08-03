package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/internal/benchdata"
	"github.com/xraph/dql/scope"
)

var benchCols = []string{"id", "status", "assignee", "score", "created_at"}

// benchRows is an in-memory SQLRows cursor. Serving rows from memory keeps the
// measurement on the engine — planning, generation, scanning, and the in-memory
// tail — rather than on database latency.
type benchRows struct {
	rows []map[string]any
	i    int
}

func (r *benchRows) Close() error               { return nil }
func (r *benchRows) Columns() ([]string, error) { return benchCols, nil }
func (r *benchRows) Err() error                 { return nil }
func (r *benchRows) Next() bool                 { r.i++; return r.i <= len(r.rows) }

func (r *benchRows) Scan(dest ...any) error {
	if len(dest) != len(benchCols) {
		return fmt.Errorf("scan: want %d dest, got %d", len(benchCols), len(dest))
	}
	row := r.rows[r.i-1]
	for i, c := range benchCols {
		p, ok := dest[i].(*any)
		if !ok {
			return fmt.Errorf("scan: dest %d is not *any", i)
		}
		*p = row[c]
	}
	return nil
}

type benchQuerier struct{ rows []map[string]any }

func (q *benchQuerier) Query(_ context.Context, _ string, _ ...any) (SQLRows, error) {
	return &benchRows{rows: q.rows}, nil
}

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

type benchEval struct{}

func (benchEval) Eval(_ context.Context, expr string, row map[string]any) (any, error) {
	return row[expr], nil
}

// mustStages decodes a pipe stage list. PipeStage.UnmarshalJSON stashes the
// whole stage object as Config, so stages must come from real JSON rather than
// struct literals.
func mustStages(b *testing.B, raw string) []dsl.PipeStage {
	b.Helper()
	var out []dsl.PipeStage
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		b.Fatalf("decode stages: %v", err)
	}
	return out
}

func newBenchEngine(rows []map[string]any) *Engine {
	return NewEngine(
		&benchQuerier{rows: rows},
		benchSchema{},
		benchEval{},
		EngineConfig{ScopeFor: func(primary, _ string) scope.Scope {
			return scope.Scope{{Name: "workspace_id", Value: primary, Required: true}}
		}},
	)
}

// BenchmarkExecuteEndToEnd measures a whole query: plan, generate SQL, scan the
// cursor, and run the in-memory tail.
func BenchmarkExecuteEndToEnd(b *testing.B) {
	ctx := context.Background()
	classic := &dsl.QueryDSL{
		From:  dsl.FromClause{Dataset: "events"},
		Where: &dsl.WhereClause{Field: "status", Op: "==", Value: "open"},
	}
	pipeQ := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: mustStages(b, `[
			{"op": "filter", "where": {"field": "status", "op": "==", "value": "open"}},
			{"op": "groupBy", "keys": ["assignee"]},
			{"op": "aggregate", "aggs": [{"fn": "count", "as": "total"}]},
			{"op": "sort", "by": [{"field": "total", "dir": "desc"}]}
		]`),
	}
	for _, tc := range []struct {
		name string
		q    *dsl.QueryDSL
	}{{"classic", classic}, {"pipe", pipeQ}} {
		b.Run(tc.name, func(b *testing.B) {
			for _, n := range benchdata.Sizes() {
				eng := newBenchEngine(benchdata.Rows(n, 20))
				b.Run("n="+strconv.Itoa(n), func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if _, err := eng.Execute(ctx, tc.q, "w1", ""); err != nil {
							b.Fatalf("execute: %v", err)
						}
					}
					b.ReportMetric(float64(n)*float64(b.N)/b.Elapsed().Seconds(), "rows/s")
				})
			}
		})
	}
}
