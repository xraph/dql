# DQL Benchmark Suite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a benchmark suite covering the compile path, the in-memory row engine, and end-to-end execution, plus a pull-request gate that fails on significant slowdowns.

**Architecture:** Benchmarks live beside the code they measure as `*_bench_test.go`. A shared deterministic fixture generator lives in `internal/benchdata`, imported only from test files so it never enters a library consumer's build graph. Row-engine benchmarks sweep n=100/1000/10000. A new `bench.yml` workflow runs baseline and head on the same runner and compares with benchstat.

**Tech Stack:** Go 1.26, stdlib `testing` only. benchstat (`golang.org/x/perf/cmd/benchstat`) is installed as a CI tool, never as a module dependency.

## Global Constraints

- **No new entries in `go.mod`.** The module currently has no `require` block. Nothing in this plan may add one.
- **`internal/benchdata` must be imported only from `_test.go` files.** Importing it from a non-test file would put fixture code in consumers' builds.
- **All fixture randomness must use a fixed seed.** A clock-seeded generator makes commit-to-commit comparison meaningless, and fails silently.
- **`dsl.Row` is a type alias** (`type Row = map[string]any`), so `[]map[string]any` and `[]dsl.Row` are the same type. `benchdata` therefore does not import `dsl`.
- **Row-engine sweep is exactly `n = 100, 1000, 10000`.**
- **Benchmark names use `/`-separated dimensions** so benchstat can read them: `BenchmarkPipe/aggregate/n=1000`.
- Every benchmark calls `b.ReportAllocs()` and reports a `rows/s` metric.
- Fixture construction happens before `b.ResetTimer()`.

---

### Task 1: Deterministic fixture generator

**Files:**
- Create: `internal/benchdata/rows.go`
- Test: `internal/benchdata/rows_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `benchdata.Rows(n, cardinality int) []map[string]any`
  - `benchdata.RowsSeeded(n, cardinality int, seed int64) []map[string]any`
  - `benchdata.Sizes() []int` returning `[]int{100, 1000, 10000}`
  - Row fields: `id` (int), `status` (string), `assignee` (string), `score` (float64), `created_at` (time.Time), `tags` ([]string), `meta` (map[string]any).

- [ ] **Step 1: Write the failing test**

```go
package benchdata

import (
	"reflect"
	"testing"
)

func TestRows_isDeterministic(t *testing.T) {
	a := Rows(50, 5)
	b := Rows(50, 5)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two calls with the same arguments produced different rows; " +
			"benchmark comparison across commits requires byte-identical fixtures")
	}
}

func TestRows_respectsCardinality(t *testing.T) {
	rows := Rows(500, 7)
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r["status"].(string)] = true
	}
	if len(seen) != 7 {
		t.Fatalf("want 7 distinct status values, got %d", len(seen))
	}
}

func TestRows_count(t *testing.T) {
	if got := len(Rows(123, 3)); got != 123 {
		t.Fatalf("want 123 rows, got %d", got)
	}
}

func TestSizes(t *testing.T) {
	if got := Sizes(); !reflect.DeepEqual(got, []int{100, 1000, 10000}) {
		t.Fatalf("unexpected sizes: %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/benchdata/ -v`
Expected: FAIL — `undefined: Rows`, `undefined: Sizes`

- [ ] **Step 3: Write the implementation**

```go
// Package benchdata generates deterministic row fixtures for benchmarks.
//
// Every generator is seeded from a constant, so repeated runs — and runs on
// different commits — produce byte-identical rows. That is a precondition for
// comparing benchmark results at all: a clock-seeded generator would make each
// run measure slightly different work, and the resulting numbers would look
// plausible while meaning nothing.
//
// This package is imported only from _test.go files, so it never enters the
// build graph of code depending on the dql module.
package benchdata

import (
	"fmt"
	"math/rand"
	"time"
)

// defaultSeed is fixed deliberately. See the package comment.
const defaultSeed int64 = 0x5C0FFEE

// epoch anchors created_at so time-based operators see a stable window.
var epoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

var statuses = []string{"open", "closed", "pending", "blocked", "archived"}

// Sizes is the row-count sweep every row-engine benchmark uses.
//
// 100 isolates per-call overhead, 1000 is the realistic middle, and 10000 is
// where an accidental quadratic becomes unmistakable — 100x the work of the
// 1000 case, which stands out even through CI runner noise.
func Sizes() []int { return []int{100, 1000, 10000} }

// Rows generates n rows with the given grouping cardinality, using the default
// seed. cardinality controls how many distinct status/assignee values appear,
// which is what separates a cheap group-by from an expensive one.
func Rows(n, cardinality int) []map[string]any {
	return RowsSeeded(n, cardinality, defaultSeed)
}

// RowsSeeded is Rows with an explicit seed, for benchmarks that need two
// independent-looking datasets (joins, set operations).
func RowsSeeded(n, cardinality int, seed int64) []map[string]any {
	if cardinality < 1 {
		cardinality = 1
	}
	// #nosec G404 -- deterministic fixture data, never security-sensitive.
	rng := rand.New(rand.NewSource(seed))
	out := make([]map[string]any, n)
	for i := range out {
		grp := i % cardinality
		out[i] = map[string]any{
			"id":         i,
			"status":     statuses[grp%len(statuses)],
			"assignee":   fmt.Sprintf("user-%d", grp),
			"score":      rng.Float64() * 100,
			"created_at": epoch.Add(time.Duration(i) * time.Minute),
			"tags":       []string{"a", "b"},
			"meta":       map[string]any{"k": grp},
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/benchdata/ -v`
Expected: PASS, 4 tests.

Note: `TestRows_respectsCardinality` requires `cardinality <= len(statuses)` to
produce that many distinct statuses. With cardinality 7 and 5 statuses the
`status` field wraps, so this test would fail. Fix by asserting on `assignee`
(which is unbounded) instead:

```go
		seen[r["assignee"].(string)] = true
```

- [ ] **Step 5: Commit**

```bash
git add internal/benchdata/
git commit -m "test(bench): add deterministic row fixture generator"
```

---

### Task 2: Compile-path benchmarks (parser, planner, sqlgen)

**Files:**
- Create: `parser/parser_bench_test.go`
- Create: `planner/planner_bench_test.go`
- Create: `sqlgen/sqlgen_bench_test.go`

**Interfaces:**
- Consumes: nothing from Task 1 (these operate on query documents, not rows).
- Produces: a `benchSchema` fake in `planner` and `sqlgen` implementing
  `planner.SchemaResolver`: `ResolveDataset(ctx, workspaceID, projectID, name string) (*dsl.DatasetInfo, error)`.

**Reference signatures** (already in the codebase):
- `parser.Parse(raw json.RawMessage) (*dsl.QueryDSL, []ParseError)`
- `parser.Validate(q *dsl.QueryDSL) []ParseError`
- `planner.NewPlanner(schema SchemaResolver, sc scope.Scope) *Planner`
- `(*planner.Planner).Plan(ctx, q *dsl.QueryDSL, workspaceID string) (*dsl.QueryPlan, error)`
- `sqlgen.GenerateSQL(plan *dsl.QueryPlan, sc scope.Scope) (string, []any, error)`

- [ ] **Step 1: Write the parser benchmark**

```go
package parser

import (
	"encoding/json"
	"testing"
)

var benchSimple = json.RawMessage(`{
  "from": {"dataset": "spaces"},
  "where": {"field": "parent_id", "op": "==", "value": "x"},
  "orderBy": [{"field": "sort_order", "dir": "asc"}]
}`)

var benchPipe = json.RawMessage(`{
  "mode": "pipe",
  "from": {"dataset": "events"},
  "pipe": [
    {"op": "filter", "where": {"field": "status", "op": "==", "value": "open"}},
    {"op": "groupBy", "keys": ["assignee"]},
    {"op": "aggregate", "aggs": [{"fn": "count", "as": "total"}]},
    {"op": "sort", "by": [{"field": "total", "dir": "desc"}]},
    {"op": "limit", "n": 10}
  ]
}`)

func BenchmarkParse(b *testing.B) {
	for _, tc := range []struct {
		name string
		doc  json.RawMessage
	}{{"simple", benchSimple}, {"pipe", benchPipe}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				q, errs := Parse(tc.doc)
				if len(errs) > 0 || q == nil {
					b.Fatalf("parse: %v", errs)
				}
			}
		})
	}
}

func BenchmarkValidate(b *testing.B) {
	for _, tc := range []struct {
		name string
		doc  json.RawMessage
	}{{"simple", benchSimple}, {"pipe", benchPipe}} {
		q, errs := Parse(tc.doc)
		if len(errs) > 0 {
			b.Fatalf("setup parse: %v", errs)
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if e := Validate(q); len(e) > 0 {
					b.Fatalf("validate: %v", e)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./parser/ -run '^$' -bench . -benchtime 10x`
Expected: PASS, both benchmarks report ns/op and allocs.

If `Validate` returns errors because the fixture references an unresolved
parameter, drop the `orderBy` clause or the `$`-prefixed value from
`benchSimple` until it validates clean. The benchmark must measure the success
path — an error path exits early and measures nothing.

- [ ] **Step 3: Write the planner benchmark**

```go
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
```

Note: `dsl.FromClause` field name must match the codebase. Check with
`grep -n "type FromClause struct" -A 6 dsl/types.go` and adjust before running.

- [ ] **Step 4: Write the sqlgen benchmark**

```go
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
		ID: name, Name: name, TableName: "ds_" + name,
		Columns: []dsl.ColumnMeta{
			{Name: "id", Type: "int"},
			{Name: "status", Type: "string"},
			{Name: "workspace_id", Type: "string"},
		},
	}, nil
}

func BenchmarkGenerateSQL(b *testing.B) {
	sc := scope.Scope{{Name: "workspace_id", Value: "w1", Required: true}}
	q := &dsl.QueryDSL{
		From:  dsl.FromClause{Dataset: "events"},
		Where: &dsl.WhereClause{Field: "status", Op: "==", Value: "open"},
	}
	plan, err := planner.NewPlanner(benchSchema{}, sc).Plan(context.Background(), q, "w1")
	if err != nil {
		b.Fatalf("setup plan: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := GenerateSQL(plan, sc); err != nil {
			b.Fatalf("generate: %v", err)
		}
	}
}
```

- [ ] **Step 5: Run all three and commit**

Run: `go test ./parser/ ./planner/ ./sqlgen/ -run '^$' -bench . -benchtime 10x`
Expected: PASS, all benchmarks report results.

```bash
git add parser/parser_bench_test.go planner/planner_bench_test.go sqlgen/sqlgen_bench_test.go
git commit -m "test(bench): benchmark the parse, plan, and SQL generation path"
```

---

### Task 3: Pure pipe operator benchmarks

**Files:**
- Create: `pipe/ops_bench_test.go`

**Interfaces:**
- Consumes: `benchdata.Rows`, `benchdata.Sizes` from Task 1.
- Produces: helper `stageRaw(v any) json.RawMessage` and `buildOp(b *testing.B, cfg map[string]any, octx *OpContext) Operator` used by Task 4.

**Constraints specific to this task:**
- The file is `package pipe` (internal), matching existing tests, so it can call
  `Build` directly.
- `pipe/ops_test.go` already declares `mockEval` and `stageJSON(t *testing.T, ...)`.
  Do **not** redeclare `mockEval` — reuse it. `stageJSON` takes `*testing.T` and
  is unusable from a benchmark, hence the new `stageRaw`.
- `aggregate` config keys are `keys` and `aggs` (not `groupBy`/`aggregate`).
- `Apply` may return the input slice; operators must be re-fed fresh rows only
  where they mutate. Rows are generated once outside the timer.

- [ ] **Step 1: Write the harness and the pure-operator benchmarks**

```go
package pipe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/internal/benchdata"
)

// stageRaw marshals a stage config for benchmarks. stageJSON in ops_test.go
// takes *testing.T and cannot be called from a benchmark.
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

// benchOp runs one operator across the standard row sweep.
func benchOp(b *testing.B, name string, cardinality int, cfg map[string]any, octx *OpContext) {
	b.Run(name, func(b *testing.B) {
		for _, n := range benchdata.Sizes() {
			rows := benchdata.Rows(n, cardinality)
			op := buildOp(b, cfg, octx)
			ctx := context.Background()
			b.Run("n="+itoa(n), func(b *testing.B) {
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

func itoa(n int) string {
	return strconv.Itoa(n)
}

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
		"op": "window", "fn": "rowNumber",
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
```

Add `"strconv"` to the import block.

- [ ] **Step 2: Run it**

Run: `go test ./pipe/ -run '^$' -bench 'BenchmarkPipe' -benchtime 10x`
Expected: PASS. Every sub-benchmark reports ns/op, allocs, and rows/s.

Any operator whose factory rejects the config will fail here with a clear
`build <op>: ...` message. Fix the config against that operator's `*Config`
struct — do not delete the benchmark.

- [ ] **Step 3: Commit**

```bash
git add pipe/ops_bench_test.go
git commit -m "test(bench): benchmark the pure pipe operators across a row sweep"
```

---

### Task 4: Dataset-backed pipe operator benchmarks

**Files:**
- Modify: `pipe/ops_bench_test.go` (append)

**Interfaces:**
- Consumes: `buildOp`, `benchOp` from Task 3.
- Produces: `benchClassic` implementing `ClassicExecutor`.

`lookup`, `crossJoin`, `asofJoin`, `except`, and `intersect` all resolve a
`Dataset` through `ClassicExecutor`. Unlike `callApp`/`callFunction`, the fake
here returns rows from memory, so the benchmark still measures DQL's own join
and set-operation logic rather than transport.

- [ ] **Step 1: Add the fake and the benchmarks**

```go
// benchClassic serves the right-hand side of joins and set operations from
// memory, so these benchmarks measure DQL's join logic rather than I/O.
type benchClassic struct{ rows []dsl.Row }

func (c *benchClassic) Execute(_ context.Context, _ *dsl.QueryDSL, _, _ string) (*dsl.QueryResult, error) {
	return dsl.NewQueryResult(c.rows), nil
}

func BenchmarkPipeJoins(b *testing.B) {
	right := benchdata.RowsSeeded(1000, 20, 99)
	octx := &OpContext{Classic: &benchClassic{rows: right}}

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

	benchOp(b, "crossJoin", 5, map[string]any{
		"op": "crossJoin", "dataset": "dim", "as": "x", "limit": 10,
	}, octx)
}
```

`LookupOn`'s JSON field names must be confirmed before running:
`grep -n "type LookupOn struct" -A 6 pipe/lookup.go`. Adjust the `on` map to match.

`crossJoin` is quadratic by nature; the `limit: 10` keeps the 10000-row case
from dominating the suite runtime. This is a deliberate cap — note it in the
commit message so nobody later reads the number as unbounded cross-join cost.

- [ ] **Step 2: Add set-operation benchmarks**

`except` and `intersect` take `MergeSource{Pipe []dsl.PipeStage}` rather than a
dataset, so each source is itself a sub-pipeline. Build them from a stage list:

```go
func BenchmarkPipeSetOps(b *testing.B) {
	octx := &OpContext{Classic: &benchClassic{rows: benchdata.RowsSeeded(1000, 20, 99)}}
	src := []map[string]any{{"op": "filter",
		"where": map[string]any{"field": "status", "op": "!=", "value": "archived"}}}

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
```

- [ ] **Step 3: Run and commit**

Run: `go test ./pipe/ -run '^$' -bench 'BenchmarkPipeJoins|BenchmarkPipeSetOps' -benchtime 10x`
Expected: PASS.

```bash
git add pipe/ops_bench_test.go
git commit -m "test(bench): benchmark dataset-backed joins and set operations

crossJoin is capped at limit 10 so the 10k case does not dominate suite
runtime; its numbers are not unbounded cross-join cost."
```

---

### Task 5: Processor benchmarks

**Files:**
- Create: `processor/processor_bench_test.go`

**Interfaces:**
- Consumes: `benchdata.Rows`, `benchdata.Sizes`.
- Produces: nothing consumed later.

`processor.NewProcessor(eval ExprEvaluator) *Processor` and
`(*Processor).Process(ctx, plan *dsl.QueryPlan, q *dsl.QueryDSL, rows []dsl.Row) (*dsl.QueryResult, error)`.

- [ ] **Step 1: Write the benchmark**

```go
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

func BenchmarkProcess(b *testing.B) {
	cases := []struct {
		name string
		q    *dsl.QueryDSL
	}{
		{"passthrough", &dsl.QueryDSL{}},
		{"aggregate", &dsl.QueryDSL{
			GroupBy:   []string{"assignee"},
			Aggregate: []dsl.AggregateClause{{Fn: "COUNT", Field: "*", As: "total"}},
		}},
		{"sort", &dsl.QueryDSL{
			OrderBy: []dsl.OrderByClause{{Field: "score", Dir: "desc"}},
		}},
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
						if _, err := p.Process(ctx, &dsl.QueryPlan{}, tc.q, rows); err != nil {
							b.Fatalf("process: %v", err)
						}
					}
					b.ReportMetric(float64(n)*float64(b.N)/b.Elapsed().Seconds(), "rows/s")
				})
			}
		})
	}
}
```

If `Process` requires non-zero fields on `*dsl.QueryPlan`, populate them by
copying what `processor/processor_test.go` builds — it already constructs valid
plans.

- [ ] **Step 2: Run and commit**

Run: `go test ./processor/ -run '^$' -bench . -benchtime 10x`
Expected: PASS.

```bash
git add processor/processor_bench_test.go
git commit -m "test(bench): benchmark in-memory processor operations"
```

---

### Task 6: End-to-end engine benchmark

**Files:**
- Create: `exec/engine_bench_test.go`

**Interfaces:**
- Consumes: `benchdata.Rows`.
- Produces: `benchQuerier` / `benchRows` implementing `SQLQuerier` / `SQLRows`.

`exec.NewEngine(db SQLQuerier, schema planner.SchemaResolver, eval processor.ExprEvaluator, config EngineConfig) *Engine`.
`EngineConfig.ScopeFor` is required — a nil value is refused at construction.

`SQLRows` method set: `Close() error`, `Columns() ([]string, error)`,
`Next() bool`, `Scan(dest ...any) error`, `Err() error`.

- [ ] **Step 1: Write the fake cursor and the benchmark**

```go
package exec

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/internal/benchdata"
	"github.com/xraph/dql/scope"
)

var benchCols = []string{"id", "status", "assignee", "score", "created_at"}

type benchRows struct {
	rows []map[string]any
	i    int
}

func (r *benchRows) Close() error                { return nil }
func (r *benchRows) Columns() ([]string, error)  { return benchCols, nil }
func (r *benchRows) Err() error                  { return nil }
func (r *benchRows) Next() bool                  { r.i++; return r.i <= len(r.rows) }

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
		ID: name, Name: name, TableName: "ds_" + name,
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

func BenchmarkExecuteEndToEnd(b *testing.B) {
	ctx := context.Background()
	for _, n := range benchdata.Sizes() {
		eng := NewEngine(
			&benchQuerier{rows: benchdata.Rows(n, 20)},
			benchSchema{},
			benchEval{},
			EngineConfig{ScopeFor: func(primary, _ string) scope.Scope {
				return scope.Scope{{Name: "workspace_id", Value: primary, Required: true}}
			}},
		)
		q := &dsl.QueryDSL{
			From:  dsl.FromClause{Dataset: "events"},
			Where: &dsl.WhereClause{Field: "status", Op: "==", Value: "open"},
		}
		b.Run("classic/n="+strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := eng.Execute(ctx, q, "w1", ""); err != nil {
					b.Fatalf("execute: %v", err)
				}
			}
			b.ReportMetric(float64(n)*float64(b.N)/b.Elapsed().Seconds(), "rows/s")
		})
	}
}
```

Confirm `Engine.Execute`'s exact signature before running:
`grep -n "func (e \*Engine) Execute" exec/engine.go`. Adjust the call if the
parameter list differs.

- [ ] **Step 2: Run and commit**

Run: `go test ./exec/ -run '^$' -bench . -benchtime 10x`
Expected: PASS.

```bash
git add exec/engine_bench_test.go
git commit -m "test(bench): benchmark end-to-end execution against an in-memory querier"
```

---

### Task 7: Makefile targets

**Files:**
- Modify: `Makefile` (the `BENCH_FLAGS` line and the bench targets)

- [ ] **Step 1: Fix BENCH_FLAGS and add the CI profile**

Replace:

```make
BENCH_FLAGS := -bench=. -benchmem -benchtime=5s
```

with:

```make
# -run=^$$ skips tests so the target measures benchmarks only. Without it,
# `make bench` runs the entire test suite first.
BENCH_FLAGS := -run=^$$ -bench=. -benchmem -benchtime=5s
# CI profile: short benchtime with enough repeats for benchstat to compute a
# p-value, sized to keep the pull-request gate inside its time budget.
BENCH_CI_FLAGS := -run=^$$ -bench=. -benchmem -benchtime=100ms -count=6
```

- [ ] **Step 2: Add the bench-ci target after bench-compare**

```make
.PHONY: bench-ci
## bench-ci: Run benchmarks with the CI profile (usage: make bench-ci OUT=new.txt)
bench-ci:
	@if [ -z "$(OUT)" ]; then echo "$(COLOR_RED)OUT required. Usage: make bench-ci OUT=new.txt$(COLOR_RESET)"; exit 1; fi
	@$(GOTEST) $(BENCH_CI_FLAGS) $(TEST_DIRS) | tee $(OUT)
```

- [ ] **Step 3: Verify and commit**

Run: `make bench-ci OUT=/tmp/bench-check.txt`
Expected: benchmarks run, no test output precedes them, file written.

```bash
git add Makefile
git commit -m "build: add bench-ci profile and skip tests in bench targets"
```

---

### Task 8: Pull-request performance gate

**Files:**
- Create: `.github/workflows/bench.yml`
- Create: `scripts/bench-gate.sh`

**Design constraint:** baseline and head run **in the same job on the same
runner**. Splitting them across jobs would put machine-to-machine variance —
routinely larger than the 20% threshold — directly into the comparison.

- [ ] **Step 1: Write the gate script**

```bash
#!/usr/bin/env bash
# Fails when benchstat reports a statistically significant slowdown beyond
# THRESHOLD percent. benchstat itself always exits 0, so the decision is made here.
set -euo pipefail

OLD="${1:?usage: bench-gate.sh old.txt new.txt}"
NEW="${2:?usage: bench-gate.sh old.txt new.txt}"
THRESHOLD="${THRESHOLD:-20}"

benchstat -format csv "$OLD" "$NEW" > /tmp/benchstat.csv

# Column layout: name,...,delta%,p-value. Rows benchstat considers insignificant
# carry a "~" delta and are skipped.
awk -F, -v t="$THRESHOLD" '
  /^[^,]+,/ {
    for (i = 1; i <= NF; i++) {
      if ($i ~ /^\+[0-9.]+%$/) {
        pct = $i; gsub(/[+%]/, "", pct)
        if (pct + 0 > t) { print "REGRESSION: " $1 " " $i; bad = 1 }
      }
    }
  }
  END { exit bad ? 1 : 0 }
' /tmp/benchstat.csv
```

- [ ] **Step 2: Write the workflow**

```yaml
name: Bench

on:
  pull_request:
    branches: [main]

permissions:
  contents: read
  pull-requests: write

jobs:
  bench:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'

      - name: Install benchstat
        run: go install golang.org/x/perf/cmd/benchstat@latest

      # Baseline and head run in the same job on the same runner. Splitting
      # them across jobs would put machine-to-machine variance into the result.
      - name: Benchmark merge base
        run: |
          git checkout --detach ${{ github.event.pull_request.base.sha }}
          make bench-ci OUT=/tmp/old.txt

      - name: Benchmark pull request head
        run: |
          git checkout --detach ${{ github.event.pull_request.head.sha }}
          make bench-ci OUT=/tmp/new.txt

      - name: Compare
        id: compare
        run: |
          benchstat /tmp/old.txt /tmp/new.txt | tee /tmp/report.txt
          bash scripts/bench-gate.sh /tmp/old.txt /tmp/new.txt

      - name: Comment results
        if: always()
        uses: actions/github-script@v7
        with:
          script: |
            const fs = require('fs');
            const body = '## Benchmark comparison\n\n```\n'
              + fs.readFileSync('/tmp/report.txt', 'utf8') + '\n```';
            await github.rest.issues.createComment({
              issue_number: context.issue.number,
              owner: context.repo.owner,
              repo: context.repo.repo,
              body,
            });
```

- [ ] **Step 3: Commit**

```bash
chmod +x scripts/bench-gate.sh
git add .github/workflows/bench.yml scripts/bench-gate.sh
git commit -m "ci: gate pull requests on benchmark regressions"
```

---

### Task 9: Verification pass

**Files:** none created; this records findings.

- [ ] **Step 1: Confirm every benchmark runs clean**

Run: `go test ./... -run '^$' -bench . -benchtime 1x`
Expected: PASS with no panics. Every benchmark executes one iteration.

- [ ] **Step 2: Confirm allocation counts are stable**

Run the same command twice and diff the `allocs/op` column. Values must be
identical. A drifting count means fixture setup leaked inside the timed region.

- [ ] **Step 3: Check scaling against expected complexity**

Run: `make bench 2>&1 | tee /tmp/bench-baseline.txt`

For each operator compare n=100 → 1000 → 10000:
- `filter`, `aggregate`, `groupBy`, `histogram` — roughly linear (10x per step).
- `sort`, `topPerGroup`, `window` — n log n (slightly worse than 10x).
- `crossJoin` — quadratic, capped by `limit`.

An operator scaling worse than expected is a **finding, not a broken
benchmark**. Record it in the commit message or an issue rather than adjusting
the benchmark to hide it.

- [ ] **Step 4: Commit the baseline**

```bash
git add docs/superpowers/plans/2026-08-03-dql-benchmarks.md
git commit -m "docs(bench): record benchmark implementation plan"
```

---

## Self-Review

**Spec coverage:**

| Spec requirement | Task |
|---|---|
| `internal/benchdata` deterministic generator, cardinality param | 1 |
| parser / planner / sqlgen benchmarks | 2 |
| 17 pipe operators | 3 (12 pure) + 4 (5 dataset-backed) |
| processor benchmarks | 5 |
| end-to-end via fake SQLQuerier | 6 |
| n=100/1000/10000 sweep | 1 (`Sizes`), applied in 3–6 |
| `ReportAllocs` + `rows/s` | 3–6 |
| `-run=^$` fix, `bench-ci` target | 7 |
| same-runner CI gate, 20% threshold, PR comment | 8 |
| verification (clean run, stable allocs, complexity check) | 9 |
| no new go.mod entries | Global Constraints |

**Known gaps, deliberately left:** the spec lists `except`/`intersect` as
operators; they are benchmarked in Task 4 but their `MergeSource` sub-pipeline
config is the least certain part of this plan and may need adjustment against
`pipe/setops.go` at implementation time. Task 4 Step 2 says so explicitly.
