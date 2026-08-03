package pipe

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/internal/benchdata"
)

// stageRaw marshals a stage config. stageJSON in ops_test.go takes *testing.T
// and so cannot be called from a benchmark.
func stageRaw(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func buildOp(b *testing.B, cfg map[string]any, octx *OpContext) Operator {
	b.Helper()
	var stage dsl.PipeStage
	if err := json.Unmarshal(stageRaw(cfg), &stage); err != nil {
		b.Fatalf("unmarshal stage: %v", err)
	}
	op, err := Build(stage, octx)
	if err != nil {
		b.Fatalf("build %v: %v", cfg["op"], err)
	}
	return op
}

// benchOp runs one operator across the standard row sweep. Rows and the
// operator are built outside the timed region.
func benchOp(b *testing.B, name string, cardinality int, cfg map[string]any, octx *OpContext) {
	b.Helper()
	b.Run(name, func(b *testing.B) {
		for _, n := range benchdata.Sizes() {
			rows := benchdata.Rows(n, cardinality)
			op := buildOp(b, cfg, octx)
			// The join operators read workspace/project from the context, the
			// way the real Executor supplies it. Harmless for ops that ignore it.
			ctx := withScope(context.Background(), "w1", "p1")
			b.Run("n="+strconv.Itoa(n), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := op.Apply(ctx, rows); err != nil {
						b.Fatalf("apply: %v", err)
					}
				}
				b.ReportMetric(float64(n)*float64(b.N)/b.Elapsed().Seconds(), "rows/s")
			})
		}
	})
}

// BenchmarkPipe covers the operators that need no external service. These are
// the ones whose cost can scale non-linearly, plus filter — linear, but the
// hottest path in the language, so worth guarding.
func BenchmarkPipe(b *testing.B) {
	octx := &OpContext{}

	benchOp(b, "filter", 5, map[string]any{
		"op":    "filter",
		"where": map[string]any{"field": "status", "op": "==", "value": "open"},
	}, octx)

	benchOp(b, "groupBy", 20, map[string]any{
		"op": "groupBy", "keys": []string{"assignee"},
	}, octx)

	benchOp(b, "aggregate", 20, map[string]any{
		"op":   "aggregate",
		"keys": []string{"assignee"},
		"aggs": []map[string]any{{"fn": "count", "as": "total"}},
	}, octx)

	benchOp(b, "sort", 20, map[string]any{
		"op": "sort", "by": []map[string]any{{"field": "score", "dir": "desc"}},
	}, octx)

	benchOp(b, "distinct", 20, map[string]any{
		"op": "distinct", "by": []string{"assignee"},
	}, octx)

	benchOp(b, "dedupe", 20, map[string]any{
		"op": "dedupe", "by": []string{"assignee"}, "keep": "first",
	}, octx)

	benchOp(b, "window", 20, map[string]any{
		"op": "window", "fn": "row_number",
		"partitionBy": []string{"assignee"},
		"orderBy":     []map[string]any{{"field": "score", "dir": "desc"}},
		"as":          "rn",
	}, octx)

	benchOp(b, "topPerGroup", 20, map[string]any{
		"op": "topPerGroup", "n": 3,
		"by":          []map[string]any{{"field": "score", "dir": "desc"}},
		"partitionBy": []string{"assignee"},
	}, octx)

	benchOp(b, "histogram", 20, map[string]any{
		"op": "histogram", "field": "score", "bins": 10,
	}, octx)

	benchOp(b, "pivot", 20, map[string]any{
		"op": "pivot", "rowKeys": []string{"assignee"},
		"columnKey": "status", "valueField": "score", "aggregate": "sum",
	}, octx)

	benchOp(b, "unpivot", 20, map[string]any{
		"op": "unpivot", "idCols": []string{"id"},
		"valueCols": []string{"score"}, "nameAs": "k", "valueAs": "v",
	}, octx)

	benchOp(b, "gapfill", 20, map[string]any{
		"op": "gapfill", "field": "created_at", "interval": "1m", "method": "zero",
	}, octx)
}

// benchClassic serves the right-hand side of a join from memory. Unlike the
// callApp/callFunction operators — which are excluded from this suite because a
// benchmark would measure the stub standing in for a network — this keeps the
// measurement on DQL's own join logic.
type benchClassic struct{ rows []dsl.Row }

func (c *benchClassic) Execute(_ context.Context, _ *dsl.QueryDSL, _, _ string) (*dsl.QueryResult, error) {
	return dsl.NewQueryResult(c.rows), nil
}

// BenchmarkPipeJoins covers the operators that resolve a dataset through
// ClassicExecutor.
func BenchmarkPipeJoins(b *testing.B) {
	octx := &OpContext{Classic: &benchClassic{rows: benchdata.RowsSeeded(1000, 20, 99)}}

	benchOp(b, "lookup", 20, map[string]any{
		"op": "lookup", "dataset": "dim",
		"on": map[string]any{"left": "assignee", "right": "assignee"},
		"as": "dim",
	}, octx)

	benchOp(b, "asofJoin", 20, map[string]any{
		"op": "asofJoin", "dataset": "dim",
		"leftTime": "created_at", "rightTime": "created_at",
		"as": "prev",
	}, octx)

	// crossJoin multiplies both sides, so it gets a deliberately tiny right side.
	// Config `limit` caps the right-hand query, not the product: against the
	// shared 1000-row right side the 10k case emits 10M rows, allocates ~8.5GB,
	// and takes ~6s per iteration — enough on its own to blow the CI budget.
	// With 10 rows the numbers describe per-pair join cost, which is the thing
	// worth tracking. Read them as cost per emitted pair, not as the cost of an
	// unbounded cross join.
	small := &OpContext{Classic: &benchClassic{rows: benchdata.RowsSeeded(10, 5, 7)}}
	benchOp(b, "crossJoin", 5, map[string]any{
		"op": "crossJoin", "dataset": "dim", "as": "x",
	}, small)
}

// BenchmarkPipeSetOps covers except and intersect. Each source is a
// sub-pipeline run over a clone of the input, so no ClassicExecutor is needed.
func BenchmarkPipeSetOps(b *testing.B) {
	octx := &OpContext{}
	src := []map[string]any{{
		"op":    "filter",
		"where": map[string]any{"field": "status", "op": "!=", "value": "archived"},
	}}

	benchOp(b, "except", 20, map[string]any{
		"op":    "except",
		"left":  map[string]any{"pipe": src},
		"right": map[string]any{"pipe": src},
		"by":    []string{"assignee"},
	}, octx)

	benchOp(b, "intersect", 20, map[string]any{
		"op":      "intersect",
		"sources": []map[string]any{{"pipe": src}, {"pipe": src}},
		"by":      []string{"assignee"},
	}, octx)
}
