# DQL Benchmarks — Design

**Date:** 2026-08-03
**Status:** Approved, pending implementation

## Problem

The repository has no benchmarks. `make bench` and `make bench-compare` already
exist and pass `-bench=. -benchmem -benchtime=5s`, but no `Benchmark` function
exists anywhere in the module, so both targets report success without measuring
anything.

Three goals, which layer rather than compete:

1. **Catch regressions.** A change that makes DQL several times slower should
   fail a pull request rather than reach a release.
2. **Locate hot spots.** Show which operators dominate cost so optimisation
   work starts from evidence.
3. **Produce publishable numbers.** Credible end-to-end figures for the README.

The compile path serves goal 1, the row engine serves goal 2, and the
end-to-end suite serves goal 3. Running any of them under benchstat is what
turns measurement into a regression guard.

## Constraints

- **No new library dependencies.** `go.mod` has no `require` block at all. The
  dependency-free property is deliberate and must survive.
- **CI is delegated.** `.github/workflows/ci.yml` calls the shared reusable
  workflow `xraph/workflows/.github/workflows/go-ci.yml@v1`. Benchmark steps
  cannot be added there and need a separate workflow file in this repository.
- **The gate must fit roughly 5–8 minutes** on a pull request.

## Scope

### Covered

| Package | Benchmarked |
|---|---|
| `parser` | `Parse`, `Validate` |
| `planner` | `Plan`, including the pushdown/in-memory split |
| `sqlgen` | `GenerateSQL` |
| `processor` | computed columns, in-memory aggregation, in-memory sort |
| `pipe` | the 17 operators below |
| `exec` | end-to-end `Engine` against a fake `SQLQuerier` |

Pipe operators, chosen because their cost can scale non-linearly or involves
sorting, hashing, or joining:

`aggregate`, `groupBy`, `sort`, `distinct`, `dedupe`, `window`, `lookup`,
`asofJoin`, `crossJoin`, `pivot`, `unpivot`, `topPerGroup`, `histogram`,
`gapfill`, `except`, `intersect`

Plus one operator that is linear but included anyway:

- **`filter`** — evaluates an expression per row. The per-row evaluation cost is
  real work on the hottest, most common path, so it is a plausible regression
  site despite the linear shape. This is one operator beyond the sixteen agreed
  during design; it is called out here rather than folded in silently.

### Excluded, and why

- **`callApp`, `callFunction`, `algo`** — these dispatch to an external caller,
  so a benchmark measures the stub standing in for it, not DQL.
- **Field-shuffling operators** (`rename`, `drop`, `cast`, `project`, and
  similar) — linear map copies whose benchmarks measure allocator noise.
- **Historical trend storage.** Comparing a pull request against its base
  covers the regression goal without a results database to maintain.

## Design

### Layout

```
internal/benchdata/rows.go       # deterministic generator; imported only by _test.go
parser/parser_bench_test.go
planner/planner_bench_test.go
sqlgen/sqlgen_bench_test.go
processor/processor_bench_test.go
pipe/ops_bench_test.go
exec/engine_bench_test.go
```

Benchmarks sit beside the code they measure, so `go test -bench=. ./pipe/`
works while iterating on a single operator.

`internal/benchdata` holds ordinary (non-test) Go files so that every package
can import it. Because only `_test.go` files do import it, it never enters the
build graph of anything consuming the library, and `internal/` prevents
external import outright. No dependency is added.

The end-to-end suite lives in `exec/` rather than a new top-level directory,
since `exec.Engine` is already the orchestrator spanning the stack.

### Fixture data

`benchdata` generates rows from a `math/rand` source with a fixed seed — no new
dependency, and byte-identical output on every run. Determinism is a
precondition for comparing two commits at all; a generator reseeded from the
clock would make every comparison meaningless.

Row shape:

| Field | Type | Exercises |
|---|---|---|
| `id` | int | identity, joins |
| `status`, `assignee` | string enum | grouping, filtering |
| `score` | float64 | aggregation |
| `created_at` | time.Time | time bucketing, gapfill, asofJoin |
| `tags` | []string | flatten |
| `meta` | map[string]any | nested access |

The generator takes an explicit **cardinality** parameter. `groupBy` over 5
groups and over 5,000 groups are different benchmarks, and a generator that
fixes cardinality can only express one of them.

Generation happens outside the timed region, via `b.ResetTimer()` after setup.

### Row-count sweep

`n = 100, 1000, 10000` for every row-engine benchmark.

- `100` isolates per-call overhead.
- `1000` is the realistic middle.
- `10000` is where an accidental O(n²) becomes unmistakable — 100× the work of
  the 1k case, which stands out even through runner noise.

Three points, rather than one, are what distinguish "the constant factor got
worse" from "the complexity got worse". A single point cannot show a change in
slope.

### Naming

Sub-benchmark names encode dimensions so benchstat can read them:

```
BenchmarkPipe/aggregate/n=1000
BenchmarkPipe/sort/n=10000
BenchmarkParse/simple
BenchmarkExecuteEndToEnd/pipe/n=1000
```

### Metrics

- `b.ReportAllocs()` on every benchmark, matching the existing `-benchmem`.
- A `rows/s` figure via `b.ReportMetric`. This is the number worth publishing
  and it stays comparable across row counts, where raw ns/op does not.

### CI gate

New `.github/workflows/bench.yml`, triggered on `pull_request`, separate from
the delegated `ci.yml`.

**Baseline and pull request run in the same job on the same runner.** This is
the single most important decision for gate reliability: machine-to-machine
variance on shared runners dwarfs the regressions being hunted, so removing it
does more than any threshold tuning. The job checks out the merge base, runs
the suite, checks out the pull request head, runs it again, then compares.

Profile: `-benchtime=100ms -count=6`. Six runs give benchstat enough samples to
compute a p-value; the short benchtime keeps the whole gate inside the 5–8
minute budget.

Gating rules:

- Only changes benchstat reports as **statistically significant** are considered.
- Fail above a **30%** slowdown. This started at 20% pending real measurement;
  the first verification run flagged an untouched benchmark at +20.06%, so the
  noise tail reaches 20 and the threshold moved to 30. See Baseline findings.
- Post the benchstat table as a pull request comment either way, so an
  improvement is as visible as a regression.

benchstat exits zero regardless of findings, so a small script parses its CSV
output and applies the threshold. benchstat is installed as a CI tool via
`go install golang.org/x/perf/cmd/benchstat@latest`; it never enters `go.mod`.

**Accepted risk:** even same-runner comparison leaves some variance, so
occasional false positives are expected. The 30% threshold and the
significance filter are the mitigations; a re-run is the escape hatch. This
trade-off was raised and accepted in favour of an automatic gate.

### Makefile

- `bench` and `bench-compare` keep their current behaviour and 5s benchtime for
  local precision.
- Add `bench-ci` for the fast profile the workflow uses.
- **Fix:** `BENCH_FLAGS` lacks `-run=^$`, so `make bench` currently runs the
  entire test suite before reaching any benchmark. Adding `-run=^$` makes the
  target benchmarks-only.

## Verification

Benchmarks are code and can be wrong in ways that produce plausible numbers.
Before the work is considered done:

1. Every benchmark runs clean under `go test -bench=. -benchtime=1x ./...`.
2. Reported allocation counts are stable across repeated runs, confirming
   fixture setup sits outside the timed region.
3. Each operator's `n=100 → 1000 → 10000` timings are checked against its
   expected complexity — linear for `filter` and `aggregate`, n log n for
   `sort`, quadratic for `crossJoin` — confirming the sweep measures what it
   claims. An operator that scales worse than expected on first run is a
   finding, not a broken benchmark, and should be recorded.
4. The workflow is exercised on a real pull request, including a deliberately
   slowed operator, to confirm the gate actually fails.

## Baseline findings (2026-08-03, Apple M3 Max)

Recorded from the first full run. Absolute numbers are machine-specific; the
scaling ratios are not.

### Verification results

- All benchmarks run clean; the full test suite still passes.
- Fixture setup is outside the timed region, proven by contrast: generating
  10,000 rows costs 119,758 allocs/op, while `sort` at n=10000 reports 2
  allocs/op and `groupBy` reports 0.
- Allocation counts drift by 1–3 out of 14,000–1,200,000 (~0.002%) between runs,
  only at n≥1000. That is `allocs/op` integer rounding plus Go's randomised map
  iteration order, not setup leaking into the measurement.
- Complexity matches expectations for `filter` (10.2x, 9.9x per 10x rows) and
  `sort` (10.1x, 11.5x — n log n).

### Finding: `window` and `topPerGroup` are superlinear in partition size

Both scale at roughly 18x per 10x rows (≈ n^1.27) when the partition count is
held at 20, well above the n log n their per-partition sort would predict.

Isolated by holding partition *size* constant instead of partition *count*:

| Shape | n=100 → 1000 → 10000 | Ratios |
|---|---|---|
| `window`, partition size fixed at 50 | 28.2µs → 335µs → 3.73ms | 11.9x, 11.1x — linear |
| `window`, single partition of size n | 34.9µs → 589µs → 8.58ms | 16.9x, 14.6x — superlinear |

So the cost is superlinear in **partition size**, not in row count. Allocations
follow the same split: linear (9.2x, 10.7x) with fixed partition size,
superlinear (15.7x, 12.5x) with one growing partition.

Practical impact: a `window` over a low-cardinality partition key on a large
result set costs disproportionately more than the row count suggests. Not
investigated further — it needs its own diagnosis, and the point of this suite
was to surface it.

### Gate verification (pull request #1, since closed)

A throwaway pull request added four redundant passes to `histogramOp.Apply` to
confirm the gate actually fails a build. It did:

```
Pipe/histogram/n=100-4     +117.45% (p=0.002 n=6)
Pipe/histogram/n=1000-4    +173.27% (p=0.002 n=6)
Pipe/histogram/n=10000-4   +187.87% (p=0.002 n=6)
```

Base/head checkout, benchstat, CSV parsing, threshold logic, and the pull
request comment all worked.

**It also produced one false positive:** `Pipe/sort/n=1000` at **+20.06%**, on a
change that never touched `sort`. Same-runner comparison held most benchmarks to
±1–3%, but the tail reaches 20%. That is why the threshold is now 30 rather than
20 — the real regression measured +117% to +188%, so the extra 10 points cost no
detection power.

Unrelated but worth recording: `ci / Test (windows-latest, go1.26)` was already
failing before this work (confirmed on commit 2093860 and earlier). Not caused
by the benchmarks.

### Reference throughput

End-to-end via an in-memory querier, n=10000: ~3.4M rows/s classic,
~2.8M rows/s pipe.

### CI budget

Measured on a real pull request: **2m48s** end to end, including both benchmark
runs, benchstat install, and the comment. The 5–8 minute budget has plenty of
headroom.

The earlier 5–7 minute estimate was wrong, and wrong for an instructive reason.
`-benchtime=100ms` bounds each benchmark by wall clock rather than by iteration
count, so total runtime is roughly *(benchmark count × 100ms × count)* almost
independently of machine speed. A 4-core EPYC runner took about as long as an
M3 Max. Adding benchmarks costs gate time; slower hardware barely does.

### Note on `crossJoin`

`crossJoin` uses a deliberately small 10-row right side. Against the shared
1000-row side, n=10000 emits 10M rows, allocates ~8.5GB, and takes ~6s per
iteration — that single case took 79s of a 1.4s suite. Its numbers describe
per-pair join cost, not unbounded cross-join cost.

## Out of scope

- Benchmarks for the `cmd/dql-lsp` module.
- Profiling integration (`-cpuprofile`, pprof endpoints).
- Comparison against other query languages or engines.
