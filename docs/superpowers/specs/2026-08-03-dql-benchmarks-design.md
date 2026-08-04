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
- Complexity matches expectations for `filter` (10.2x, 9.9x per 10x rows).

### Corrected: the original `sort` benchmark was invalid

The first recorded run reported `sort` at 10.1x/11.5x, apparently clean n log n.
That was wrong. `sortOp.Apply` reorders its input **in place**, and the
benchmark reused one slice across iterations — so every iteration after the
first sorted already-sorted data, the best case for a stable sort.

| `sort` | n=100 | n=1000 | n=10000 | ratios |
|---|---|---|---|---|
| Reused slice (original, invalid) | 3.2µs | 33µs | 423µs | 10.3x, 12.7x |
| Fresh order each iteration (correct) | 29.1µs | 453µs | 6.90ms | 15.6x, 15.2x |

The real cost is roughly 15x higher. `benchOpReordering` now restores input
order before each timed iteration; `sort` is the only operator in the suite that
reorders in place (`dedupe` copies first, and the rest build new slices).

`window` was checked for the same class of error — it writes a field into each
row, so iteration 1 inserts a map key and later ones only update it — but fresh
versus reused measured within noise, so its numbers stand.

### Root cause: the row comparator, shared by four operators

With the baseline corrected, `window` is not anomalous. Both it and `sort` scale
at ~15-17x per 10x rows, and a CPU profile of `window` at n=10000 shows why:

| Cost | Share | Why |
|---|---|---|
| `runtime.mapaccess1_faststr` | 27% | every comparison re-reads fields out of the row map |
| `aeshashbody` | 8% | hashing those string keys |
| `compareValues` | 20% | the comparison itself |
| `strings.ToLower` | 4.7% | `rowsLess` lowercases the **constant** `ob.Dir` on every comparison |
| `sort.symMerge` + `rotate` | ~13% | `sort.SliceStable`'s O(n log² n) swap machinery |

So a comparison sort with an expensive comparator: O(n log n) comparisons, each
paying two map lookups and a redundant `strings.ToLower`, on top of a stable sort
whose swap count carries an extra log factor.

`rowsLess` is shared by four call sites — `window`, `topPerGroup`, `dedupe`, and
the `fillNulls` path in `quality.go` — so all four carry the same cost.

### Fix applied (commit f7aa834)

Decorate-sort-undecorate, in `orderSpec` (pipe/helpers.go): extract each row's
order-key values once (O(n) map lookups instead of O(n log n)), resolve
direction at build time instead of per comparison, and sort an index permutation
with an original-index tiebreak — which reproduces `sort.SliceStable`'s output
without symMerge's rotations.

Measured with benchstat, `-benchtime=200ms -count=6`, all at p=0.002:

| Benchmark | n=1000 | n=10000 |
|---|---|---|
| `window` | −39.79% | **−60.11%** |
| `topPerGroup` | −39.99% | **−58.52%** |
| `dedupeOrdered` | −61.36% | **−62.02%** |

The result lands on clean n log n, the correct complexity for a comparison sort
— the excess was the comparator, not the algorithm.

**Trade:** holding extracted keys costs ~72% more B/op. A flat backing array
keeps the increase in *allocs/op* to ~5%, but the bytes are real. Time was the
constraint worth optimising here; if memory ever becomes the binding one, this
is the knob.

Tie ordering is pinned by characterisation tests (commit 5679584) and is
unchanged. `rowsLess` is now unused and was removed.

Two things this surfaced about the benchmarks themselves:

- `dedupe` skips sorting entirely without an `orderBy`, so the original case
  never exercised the comparator. A `dedupeOrdered` case now covers it.
- `sort` was initially unaffected, because `sortOp` does **not** use `rowsLess`
  — it had its own inline comparator carrying the identical defects. It was
  fixed in a follow-up (below).

### `sortOp` follow-up, and the memory trade

`sortOp` got the same treatment, plus a single-field fast path in
`sortPermRows`: ordering by one field skips the general `[][]any` key layout,
whose 24-byte slice header per row was the largest component of the added
memory. That fast path improved every operator, not just `sort`.

Final, measured against the pre-optimisation baseline (p=0.002 throughout):

| Benchmark | n=1000 | n=10000 |
|---|---|---|
| `sort` | −71.6% | **−73.2%** |
| `window` | −42.8% | **−61.8%** |
| `topPerGroup` | −45.4% | −45.9% |
| `dedupeOrdered` | −61.4% | −62.0% |

**`sort` used to be allocation-free**, and this is the one place where the trade
is stark rather than incidental: 56 B/op flat, at any row count, became ~33
bytes per row (327KB at n=10000, down from 573KB before the fast path).
`allocs/op` stays constant at 8.

Whether that is the right call depends on the workload. Against the rows being
sorted it is modest — 10,000 rows as Go maps run to several megabytes, so the
permutation and key arrays add single-digit percent — and 3.7x less CPU is a
good trade for it. It would be the wrong trade in a memory-constrained embedder
sorting large result sets, and that is worth revisiting if one appears. The
older behaviour is recoverable by keeping the in-place `sort.SliceStable` path
for inputs below some row threshold.

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

## Recorded baseline (2026-08-04, post-optimisation)

`make bench` (`-run=^$ -bench=. -benchmem -benchtime=5s`), 81 benchmarks, no
failures. Apple M3 Max, darwin/arm64, Go 1.26.

**Read these as a reference point, not as a second measurement of the
optimisation.** They were taken at `-benchtime=5s`, while the before/after
percentages above came from matched `-benchtime=200ms -count=6` benchstat runs.
Comparing across the two conditions would overstate the gains; the benchstat
figures remain authoritative for before/after.

### Scaling — the check that matters

Every optimised operator now sits at n log n rather than the 15-17x per 10x rows
that opened the investigation.

| Operator | 100→1k | 1k→10k | Expected |
|---|---|---|---|
| `filter` | 11.0x | 8.8x | linear |
| `distinct` | 9.8x | 10.0x | linear |
| `histogram` | 7.3x | 9.2x | linear |
| `aggregate` | 7.9x | 6.9x | linear (sub-linear from amortised map growth) |
| `sort` | 11.9x | 13.4x | n log n |
| `dedupeOrdered` | 13.6x | 13.7x | n log n |
| `topPerGroup` | 9.8x | 12.4x | n log n |
| `window` | 14.8x | 10.7x | n log n |

A ratio drifting toward 17x+ on any of these is the signal that the comparator
regression has come back.

### Cost at n=10000

| Operator | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `crossJoin` | 64,990,834 | 85,254,889 | 1,200,004 |
| `asofJoin` | 9,679,595 | 8,820,013 | 120,017 |
| `intersect` | 7,344,104 | 8,500,918 | 104,076 |
| `lookup` | 7,189,005 | 8,787,069 | 131,014 |
| `except` | 6,829,325 | 7,999,843 | 96,040 |
| `pivot` | 3,338,237 | 3,915,061 | 40,334 |
| `window` | 2,563,687 | 1,024,666 | 15,155 |
| `dedupeOrdered` | 2,432,320 | 926,807 | 10,041 |
| `topPerGroup` | 1,781,418 | 598,850 | 10,292 |
| `sort` | 1,558,940 | 327,768 | 7 |
| `unpivot` | 1,386,887 | 3,601,921 | 30,001 |
| `filter` | 764,808 | 209,978 | 20,001 |
| `gapfill` | 705,707 | 439,600 | 22 |
| `aggregate` | 677,278 | 276,984 | 10,270 |
| `distinct` | 603,252 | 598,954 | 10,034 |
| `dedupe` | 586,430 | 598,954 | 10,034 |
| `histogram` | 209,675 | 3,760 | 52 |
| `groupBy` | 1.04 | 0 | 0 |

`groupBy` is a pass-through that only records keys — the real work happens in
the following `aggregate`, so its rows/s figure is meaningless.

`sort`'s 327,768 B/op against 7 allocs is the memory trade described above:
constant allocation count, bytes linear in row count.

### Compile path

| Benchmark | ns/op | allocs/op |
|---|---:|---:|
| `Parse/simple` | 2,981 | 35 |
| `Parse/pipe` | 10,927 | 125 |
| `Validate/simple` | 69.5 | 1 |
| `Validate/pipe` | 4,440 | 61 |
| `Plan/pushdown` | 487.4 | 9 |
| `Plan/inmemory` | 547.7 | 9 |
| `Plan/groupBy` | 446.4 | 8 |
| `GenerateSQL/simple` | 583.4 | 13 |
| `GenerateSQL/compound` | 571.7 | 16 |

Planning and generation are sub-microsecond and nowhere near the bottleneck;
parsing dominates the compile path, and all of it is negligible beside row work.

### End to end

| Benchmark | ns/op | rows/s |
|---|---:|---:|
| `classic/n=100` | 28,140 | 3,553,697 |
| `classic/n=1000` | 319,998 | 3,125,021 |
| `classic/n=10000` | 2,939,795 | 3,401,598 |
| `pipe/n=100` | 52,449 | 1,906,607 |
| `pipe/n=1000` | 414,993 | 2,409,677 |
| `pipe/n=10000` | 3,620,049 | 2,762,394 |

### Joins: the merge plan was rebuilt per row (commit cc205ec)

`asofJoin` was flagged above as the slowest non-quadratic operator. It turned
out not to be an algorithmic problem at all — it scales linearly (5.3x, 10.5x).
The cost was constant-factor, and a profile put it somewhere the operator's name
did not suggest: **`mergeLookup`, at 99.3% of allocations and 36.6% of CPU**.

The first hypothesis was wrong and measurement killed it. `parseRowTime` is
called inside both the sort comparator and the binary search — exactly the
pattern just fixed in `window` — but it short-circuits on `time.Time` values, so
it is nearly free.

The real waste: for every left row, `mergeLookup` rebuilt two things that do not
depend on the row — the right row's key list, materialised into a fresh slice,
and `as + "_" + column`, one string concatenation per column per row. About 12
allocations per row. A `mergePlan` now hoists both out of the loop, and the
no-Select path iterates the right row directly instead of collecting its keys
first. The plan is per-Apply and never shared, so its cache needs no locking.

`lookup` and `crossJoin` share the function, so all three improved:

| Operator | n=100 | n=1000 | n=10000 |
|---|---|---|---|
| `lookup` | −17.9% | −27.5% | **−33.6%** |
| `asofJoin` | −16.5% | −25.3% | **−32.3%** |
| `crossJoin` | −32.2% | −31.6% | **−33.9%** |

Geomean −28.1% time, +39.1% rows/s, −20.5% bytes, −62.1% allocations
(~12 per row down to ~4). All at p=0.002, variance ±2–11%.

**On trusting these numbers.** The first attempt produced garbage — variance to
±895%, one nonsensical "+374% regression", and an `asofJoin` baseline of 69ms
against a recorded 9.7ms. The machine was at load average 32 from unrelated
Xcode and VM work. Nothing was reported from it. The run above waited for load
below 4.0, used a git worktree for the baseline rather than stashing, and
interleaved old/new rounds so drift hit both equally. Its `crossJoin` baseline
of 64.71ms against the 64.99ms recorded independently is what makes it
credible.

Allocation counts were reportable throughout, being deterministic and
independent of machine load — worth remembering when a machine is too busy to
time anything.

### Open, not investigated

- `crossJoin` still allocates 64MB at n=10000 against a deliberately small
  10-row right side. Down from 81MB, but it is inherent to materialising the
  product.

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
