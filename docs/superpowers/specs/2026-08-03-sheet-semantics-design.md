# Sheet Semantics — Design

**Date:** 2026-08-03
**Status:** Approved, pending implementation

## Problem

A pipe expresses a sequence: each stage receives the previous stage's rows and
hands on its own. That is the right model for a transformation the author can
order by hand, and the wrong one for a set of interdependent calculations.

Spreadsheet work is the second kind. A sheet is a *set* of named formulas whose
execution order follows from what they reference, not from the order they were
written in, and where a per-row calculation may need a value derived from every
row — `revenue / total_revenue`. Expressing that as a pipe today means computing
the aggregate in one stage, joining it back in another, and getting the ordering
right by hand every time a formula is added.

Nothing in the operator catalog covers it, and the shape does not decompose into
the existing operators without the author doing the resolver's job themselves.

## Constraints

- **No new module dependencies.** `go.mod` has no `require` block, and this
  design does not add one — see *The expression seam*. `boundary_test.go`
  permits `github.com/xraph/dtl`, but nothing here needs to use that permission.
- **The existing operator contract does not change.** `rowops.Operator` stays
  `Apply(ctx, []Row) ([]Row, error)`. The other operators are not modified, and
  no second contract is imposed on them.
- **Pushdown and spill must not be observable.** A sheet's result may not depend
  on whether the planner pushed an aggregate, whether the input streamed, or
  whether a column spilled. These are optimisations, and the test suite treats
  any observable difference as a defect.
- **Portability.** A sheet written against the built-in function set must mean
  the same thing on every host. Host extensions are permitted but must be
  declared, discoverable, and diagnosed at plan time.

## Scope

### Covered

| Area | Delivered |
|---|---|
| Semantics | `expr` (column) and `reduce` (scalar) formulas, resolved by dependency |
| Execution | Columnar store, compile-once evaluation, topological walk |
| Expression seam | Required `ExprCompiler` on `OpContext`, declared as `ReqExprCompiler` |
| Scale | Streaming input when the sheet is the first stage; spill for computed columns |
| Pushdown | `count / sum / avg / min / max` over source columns, batched into one query |
| Extensibility | Host-registered `reduce` and `window` kernels, per-`OpContext` |
| Tooling | Catalog entry, config schema, completions, `/explain` attribution |

### Not covered

| Excluded | Reason |
|---|---|
| Cell addressing (`A1`, `$B$2`, ranges) | Positional and sheet-shaped. A host's spreadsheet-import layer translates these to column names before a query is ever built; admitting them here would prevent the same sheet running over a table and a JSON stream |
| Iterative / circular calculation | A convergence knob with no single correct answer. A cycle is an error |
| Inline aggregate sugar (`expr: "profit / sum(profit)"`) | Deferred, not rejected. Requires the reduce registry to exist first — see *Extensibility* |
| Compiling expressions to SQL | Would make aggregates over computed columns pushable. A separate project of comparable size |

---

## Core Concepts

```
┌───────────────────────────────────────────────────────────────┐
│                         sheet operator                        │
│                                                               │
│   RowSource ──► column store ──► DAG walk ──► []Row out       │
│   (cursor or    (typed cols,     (topological                 │
│    slice)        null bitmaps,    over expr +                 │
│                  spillable)       reduce)                     │
│                       ▲                                       │
│                       │                                       │
│              pushed reduces arrive here                       │
│              as scalars, before the walk                      │
└───────────────────────────────────────────────────────────────┘
         ▲
         │  SELECT count(*), sum(revenue), avg(cost) FROM (prefix) t
         │  — one query, derived from PipePlan.PushedDSL
```

---

## Semantic Model

A sheet is a set of named formulas over a row stream. Two kinds, distinguished
by which key carries the expression:

```yaml
- op: sheet
  formulas:
    - as: profit
      expr: "revenue - cost"          # column: one value per row

    - as: total_profit
      reduce: "sum(profit)"           # scalar: one value for the sheet

    - as: share
      expr: "profit / total_profit"
```

`expr` produces a column. `reduce` produces a scalar. Both are named, both are
nodes in one dependency graph, and the resolver orders them: `total_profit`
reduces over `profit` before `share` reads it. Writing them in any other order
produces the same result.

### Why the shape declares the kind

An earlier draft carried a separate `scope:` enum. It is not in this design,
because the key already says everything the enum would: you do not annotate a
formula as a summary, you write `reduce:`. There is no combination of key and
annotation that can disagree, and nothing to keep in sync.

### One rule for identifiers

Inside `expr`, a bare identifier always denotes the current row's value. Inside
`reduce`, the aggregate's argument always denotes the whole column. There is no
position-dependent meaning and no set of functions whose arguments are treated
specially.

This is the point of separating the two kinds. The natural spreadsheet spelling
— `revenue / sum(revenue)` — requires `revenue` to mean a scalar and a column in
the same expression, which is implementable but context-sensitive: correct only
for a hardcoded list of aggregate names, and silently wrong for any name outside
it. Routing whole-column values through a named `reduce` removes the ambiguity
rather than managing it.

### Errors, not surprises

- A cycle is an error naming its participants.
- Two formulas with the same `as` is an error.
- A reference to a name that is neither a source column nor another formula is
  an error.

All three are detected before any row is read. See *Error Handling*.

---

## Execution Architecture

A new `sheet` package. `pipe` gains one factory registration and one catalog
entry; nothing else in it changes.

### Column store

```go
type Column interface {
    Len() int
    At(i int) any        // boxed, for the expression evaluator
    IsNull(i int) bool
}
```

Backings are `[]float64`, `[]string`, `[]bool`, `[]time.Time`, and an `[]any`
fallback, each with a `[]uint64` null bitmap. A builder starts optimistic — the
first non-null value selects the backing — and demotes to `[]any` on the first
type mismatch. The expression language is dynamically typed, so a formula column
can be heterogeneous; demotion is the escape hatch rather than a failure.

Against `[]map[string]any`, a typed column costs roughly an order of magnitude
less: eight bytes for a float against a map slot, an interface word pair, and a
heap-boxed value.

### The expression seam

The sheet does not import an expression language. It reaches one through
`OpContext`, as every other operator does — but the existing interface cannot
support compile-once:

```go
type ExprEvaluator interface {
    Eval(ctx context.Context, expr string, row map[string]any) (any, error)
}
```

It takes the source text on every call, so a host implementing only this must
re-parse per row. That is precisely the cost this design exists to remove, and
it is not a cost DQL can remove on the host's behalf.

So `OpContext` gains a companion interface, and the `sheet` operator **requires**
it:

```go
// ExprCompiler prepares an expression once for repeated evaluation, and
// reports what it references. The sheet operator requires it: dependency
// resolution is not optional for a sheet, and neither is the analysis it
// rests on.
type ExprCompiler interface {
    // FreeIdentifiers reports the unbound names the expression references,
    // excluding names bound within it (lambda parameters, let bindings) and
    // excluding called function names.
    FreeIdentifiers(expr string) ([]string, error)

    // Compile prepares an expression whose free identifiers will be supplied
    // as args. Returns a parse or resolution error rather than deferring it
    // to evaluation.
    Compile(expr string) (CompiledExpr, error)
}

type CompiledExpr interface {
    // Eval binds args and evaluates. Implementations must not retain args:
    // the sheet reuses one map across every row.
    Eval(ctx context.Context, args map[string]any) (any, error)
}
```

`FreeIdentifiers` is on the host side because it cannot be anywhere else.
Extracting the names an expression references means parsing it, parsing means
owning the grammar, and DQL does not own the expression language — that is the
entire point of the `ExprEvaluator` seam. A regex over expression text is the
alternative, and it is not a viable one: it matches identifiers inside string
literals, so a formula named `status` and an expression containing `"status"`
yield an edge that does not exist, which surfaces as a circular-dependency
error for an acyclic graph.

This keeps `go.mod` empty: DQL defines the interfaces, the host satisfies them.

There is no degraded fallback to `ExprEvaluator`. Without free identifiers
there is no dependency graph, and without a dependency graph a sheet is just a
`compute` chain the author has to order by hand — the thing this design exists
to replace. A host that has not supplied an `ExprCompiler` cannot run `sheet`,
and says so through the existing mechanism: the operator declares
`ReqExprCompiler`, `MissingRequirements` reports it, and completions omit the
stage.

### Compile once, bind narrow

Two properties the DAG depends on:

**Each expression is compiled once**, not once per row, and executed against a
reused args map. A conforming `CompiledExpr` must bind parameters out of that
map without retaining it, so one map serves every row.

**Only referenced columns are bound.** `profit = revenue - cost` binds two
values per row regardless of how wide the sheet is. A generated wrapper
declaring every column as a parameter would cost fifty map writes per row on a
fifty-column sheet to read two of them.

### Dependency extraction

`ExprCompiler.FreeIdentifiers` supplies the names each formula references; see
*The expression seam* for why the analysis lives on the host side.

The sheet keeps only the names that resolve to something — a source column or
another formula. Anything else is an error naming the unresolved identifier, so
a typo fails at plan time rather than binding to nil at row time.

Those edges feed Kahn's algorithm over the combined `expr` and `reduce` set.

### Evaluation

```
1. run the pushdown query, if any; pushed reduces become leaf scalars
2. resolve the DAG
3. for each formula in topological order:
     expr   → evaluate per row into a column builder
     reduce → native kernel over the typed column, else evaluator over boxed values
4. project the requested columns out as []Row
```

### Where columnar pays

Row expressions still box at the evaluator boundary; that is inherent to a
dynamically-typed evaluator and this design does not avoid it. The columnar
representation buys two things:

- **Storage**, which is what makes multi-million-row sheets fit at all.
- **Reduces.** `count`, `sum`, `avg`, `min`, `max`, `median`, `percentile`,
  `stdev`, `variance` get native kernels over typed columns — a linear scan with
  no boxing. Anything else falls back to the evaluator over a boxed iterator.

Reduces are what a sheet does most of, which is where the representation earns
its cost.

Both claims are load-bearing enough to measure rather than assert, and the
first implementation realised neither. Two things had to be true for them to
hold, and `sheet/sheet_bench_test.go` now pins both:

- The builder must spend its capacity hint on the **typed backing**, not only
  on the null bitmap. Without that a 100k-row column reallocates through the
  doublings and the store measures 1.8x smaller than row maps rather than 9x.
- Columns must be **built once per run and reused**. Rebuilding one per reduce
  makes construction dominate the scan, and the kernel path measures slower
  than the boxed path it replaces.

With both, over 100k rows x 10 columns: 8.2MB of columns against 75.2MB of row
maps, and four reduces over one column at 1.5x the speed of the compiled path
in an eighth of the memory — flat as reduces are added, where the boxed path is
linear.

### Streaming input

An optional interface, used only when the sheet is the first stage:

```go
type SourceOperator interface {
    Operator
    ApplyStream(ctx context.Context, src RowSource) ([]Row, error)
}

type RowSource interface {
    Next() bool
    Row() Row
    Err() error
}
```

The engine already holds a cursor and drains it into a slice before the pipe
runs. When the sheet is first, it is handed the cursor as a `RowSource` instead.
When it is not first, it receives the materialised slice through plain `Apply`;
a slice-backed `RowSource` makes both paths one implementation.

### Spill

The store carries a memory budget. On exceeding it, **completed** columns spill
least-recently-touched first. A spilled column is always read back whole and
sequentially, so the format is length-prefixed typed blocks — no index, no
random access.

On lifecycle: create with `os.CreateTemp`, then **immediately `os.Remove` the
file while holding the descriptor open**. From that moment the file has no name
and the kernel reclaims it when the descriptor closes — on return, on panic, on
`SIGKILL`. No code path can orphan it, which is not a property any `defer`-based
cleanup has. Windows does not permit unlink-while-open; there it falls back to a
per-execution directory with `defer os.RemoveAll` and a `ctx.Done()` watchdog.

Cancellation is checked between columns rather than between rows. A `select` per
row costs measurably on a ten-million-row scan and buys nothing: one column's
evaluation is already bounded work.

---

## SQL Pushdown

### What pushes

A `reduce` qualifies when all three hold:

1. Its expression is a bare aggregate call over a single identifier —
   `sum(revenue)`, not `sum(revenue) / 2`
2. The aggregate is `count`, `sum`, `avg`, `min` or `max`. This is the set
   `AggregateClause.IsPushable` already uses in classic mode, chosen because
   these five mean the same thing in every SQL dialect. `median`, `percentile`
   and `stdev` stay in Go, where `stddev_samp`-versus-`stddev_pop` cannot bite
3. The identifier is a **source** column present in the pushed prefix, not a
   column another formula produces

### What never pushes

Any reduce over a computed column. `sum(profit)` where `profit = revenue - cost`
cannot push without compiling expressions to SQL, which is out of scope. In a
real sheet this is the majority, so a mixed plan — some reduces from the
database, the rest from native kernels — is the normal case, not the edge.

### Deriving the aggregate query

The aggregate must span exactly the rows the sheet evaluates: same `WHERE`, same
`LIMIT`, same `OFFSET`, same scope predicates. Generating SQL for it
independently is how an aggregate over a whole table gets divided into rows from
page three.

So the aggregate query is derived from `PipePlan.PushedDSL` — the synthetic
classic `QueryDSL` the planner already builds for the folded prefix — by
replacing its projection with the aggregate list and leaving every other clause
untouched. One plan object, not two code paths to keep in sync.

### One round trip

All pushable reduces batch into a single query:

```sql
SELECT count(*), sum(revenue), avg(cost), min(ts), max(ts)
FROM ( <pushed prefix> ) t
```

Twelve pushable reduces cost one extra query, not twelve, and the database scans
once.

### Compounding with streaming

Pushed reduces are resolved before the topological walk begins, so they enter
the DAG as leaves with no dependencies. A sheet whose reduces all push never
materialises the source columns they read — combined with streaming input, it
holds only its computed columns in memory.

### Null semantics

SQL `sum()` over zero rows returns `NULL`; a naive Go kernel returns `0`. SQL
aggregates skip `NULL` inputs; a naive kernel does not. Native kernels **must**
match SQL on both counts, or the same sheet returns different answers depending
on whether the planner happened to push — which would make pushdown a semantic
change rather than an optimisation.

### Failure

If the aggregate query fails, the sheet fails. It does not silently fall back to
in-memory computation: that would mask a real fault — bad permissions, a
malformed prefix — and turn a fast query into a slow one non-deterministically.

---

## Extensibility

Three kinds of function, of which two need new machinery:

| Kind | Shape | Mechanism |
|---|---|---|
| scalar | values → value, per row | Already solved by the expression language's own builtin registration. No DQL involvement |
| reduce | column → scalar | New |
| window | column → column | **Not built — see below** |

### Why there is no window kind

An earlier revision called for one, on the reasoning that `lag`, `rank` and
their neighbours existed twice — once in a host's spreadsheet layer and once in
this repository — and that giving the sheet its own would collapse them.

That reasoning was wrong, and inverted: `pipe/window.go` already implements the
whole set with `partitionBy`, `orderBy`, `offset` and `default`. Adding a
window kind to the sheet would have produced a **third** implementation, and a
weaker one — a sheet has no notion of partitioning, so an inline `lag()` would
either ignore it or reinvent it.

The composition already works and is now pinned by tests:

```yaml
- op: window
  fn: lag
  field: revenue
  partitionBy: [host]
  orderBy: [{ field: ts }]
  as: prev
- op: sheet
  formulas:
    - as: growth
      expr: "revenue - prev"
```

A window column is an ordinary column: a sheet may read it, reduce over it, and
a window may equally rank by a column a sheet computed. Both directions are
covered in `pipe/sheet_window_test.go`.

Making the window stage explicit is also better than the inline form it
replaces, because ordering and partitioning are precisely what a spreadsheet's
`LAG()` leaves implicit and what makes it fragile.

The duplication the phase was meant to resolve is real, but it lives in the
host's spreadsheet layer, not here. Retiring it there in favour of this
operator is work for that repository.

```go
type ReduceFunc interface {
    Name() string
    Reduce(ctx context.Context, col Column) (any, error)
    // SQL aggregate to delegate to, or "" for never-push.
    PushdownName() string
}

type WindowFunc interface {
    Name() string
    Apply(ctx context.Context, col Column, args []any) (Column, error)
}
```

### Reconciling extension with portability

The operator set is the language: a query that runs against one host should mean
the same thing against another. Extension does not violate that, and the
codebase already shows how — `callApp` needs a host `AppCaller`, `callFunction`
needs a host registry, and `MissingRequirements` exists so a deployment can
report what it cannot run and completions can omit it.

The same three rules apply:

1. **The built-in set ships with DQL and is fixed** — `count sum avg min max
   median percentile stdev variance mode first last`, plus the window set. A
   sheet using only these means the same thing everywhere. This is the language.
2. **Host additions are per-`OpContext`, not global.** `pipe.Register` is
   init-time and global, which is right for DQL's own operators and wrong for
   host functions: two hosts in one process — a test binary — would collide, and
   "what does this deployment support" stops being answerable per query. So
   `OpContext` gains a nullable `SheetFuncs *sheet.Registry`, alongside `Eval`,
   `Registry`, `AppCaller` and `Formula`. Nil means built-ins only.
3. **Unknown names fail in `ValidateStages`**, with the available set in the
   message.

Registering a name that collides with a built-in returns an error rather than
shadowing it. Shadowing `sum` would make a sheet mean something different on one
host, which is the one outcome the portability rule forbids.

### What this unlocks

Because reduce and scalar live in separate registries, the engine can diagnose
the confusion between them:

```
expr: "p95(latency)"
  → p95 is a reduce function; move it to a `reduce:` entry
```

And the deferred inline sugar becomes well-defined: the reduce registry *is* the
aggregate set. A call to a registered reduce name appearing inside an `expr`
lifts into an anonymous `reduce` and is replaced by a reference to it — a
mechanical transformation over a known set. Still a follow-up, but no longer a
guess.

### Tooling

The catalog entry's `ConfigSchema` carries `x-dql-input` kinds for `expr` and
`reduce` so a form builder renders the right editor. Completions inside a sheet
consult `octx.SheetFuncs` the way `CompleteText` already consults `Services`: a
host without `p95` is not offered `p95`.

---

## Error Handling

### Plan time

Everything below is detected in `ValidateStages`, before a row is read. This
means compiling every expression at plan time, which is the intended cost — a
sheet that cannot run should say so while the query is being validated.

| Condition | Message carries |
|---|---|
| Expression fails to parse | The parse error, the formula name |
| Unknown function | The name, and the available set for this `OpContext` |
| Reduce used in `expr` position, or scalar in `reduce` | Which registry the name is in, and where to move it |
| Reference to an unknown name | The name, and whether near-matches exist among columns and formulas |
| Duplicate `as` | The repeated name |
| Cycle | Every participating formula name |

### Evaluation time

Two policies, set per sheet:

```yaml
- op: sheet
  onError: fail      # default
```

**`fail`** (default) aborts on the first evaluation error, reporting the formula
name, row index, and underlying error. This matches every other operator in the
catalog. It is the default because a pipe stage that emits partially-wrong rows
is worse than one that stops: downstream stages compute on the damage, and
nothing surfaces it. A spreadsheet's `#DIV/0!` model works because a human is
looking at the cell; a query pipeline has no such observer.

**`null`** writes null into the failing cell and continues. This exists for the
imported-workbook case, where a handful of bad rows should not fail the query.
Under this policy the sheet records at most 100 distinct errors with their row
indices plus a total count — unbounded error collection over ten million rows is
its own failure mode — and reports them alongside the result.

Nulls propagate through the DAG by the expression language's own null
semantics, and native reduce kernels skip them, matching SQL.

### Resources

| Condition | Behaviour |
|---|---|
| Memory budget exceeded, spill enabled | Spill completed columns, continue |
| Memory budget exceeded, spill disabled | Fail, naming the budget and the column that exceeded it |
| Spill I/O failure | Fail; descriptors close and storage is reclaimed by the unlink-while-open property |
| Context cancelled | Abort between columns, same cleanup |
| `ExecutorConfig.MaxRows` exceeded | Fail, as other operators do |

---

## Testing

Three of this design's mechanisms — pushdown, streaming, spill — are
optimisations that must not be observable. That gives the suite its spine: one
fixture set, run under each combination of three flags, asserting identical
output.

| Layer | Tests |
|---|---|
| Column store | Builder type selection, demotion to `[]any`, null bitmap boundaries |
| Free identifiers | Binding forms — lambda parameters, `let`, shadowing; identifiers inside string literals are *not* dependencies |
| DAG | Ordering, diamonds, cycle detection and the names reported |
| Reduce kernels | Each native kernel against the evaluator fallback, including empty input, all-null input, and mixed types |
| **Parity: pushdown** | Every fixture with pushdown enabled and forced off — identical results |
| **Parity: streaming** | Every fixture with a cursor source and a slice source — identical results |
| **Parity: spill** | Every fixture with the budget at its natural size and at near-zero — identical results |
| Null semantics | Kernel results against the database's own for the pushable five, over empty and null-bearing columns |
| Errors | Each plan-time condition produces its message; `onError` policies behave and the error cap holds |
| Doc examples | Every example in the operator reference executes and produces its documented output |
| Benchmarks | Sheet over 1M rows: with and without pushdown, streaming and materialised, columnar against a `[]Row` baseline |

`pipe/parity_test.go` is the existing precedent for the parity shape, and
`doc_examples_test.go` for executing documentation.

---

## Implementation Order

Each phase leaves the tree green and useful.

| Phase | Delivers |
|---|---|
| 1 | `sheet` package: column store, builders, free-identifier walker, DAG. No operator yet — pure units |
| 2 | The operator: `expr` and `reduce`, compiler-backed reduces only, materialised input, registered in the catalog with `ReqExprCompiler`. End to end and shippable |
| 3 | Native reduce kernels, with the kernel-versus-fallback equivalence suite |
| 4 | Pushdown, with the pushdown parity suite |
| 5 | `RowSource` and streaming input, with the streaming parity suite |
| 6 | Spill, with the spill parity suite |
| 7 | ~~Window kind and the built-in window set~~ — **cancelled**, see below |
| 8 | Host registry on `OpContext`, completions, `/explain` attribution |

Phases 5 and 6 are independent of each other. **Phase 4 is not independent of
phase 5**, contrary to an earlier revision of this table.

Pushdown's correct form needs a capability streaming provides, and its benefit
needs one too. Three facts establish it, all verified against the code:

- The sheet always receives materialised rows: the executor runs the classic
  prefix, holds the result as `[]dsl.Row`, and only then runs the in-memory
  tail. When a reduce executes, every value it needs is already in memory, so
  pushing it to the database buys a round trip and saves a linear scan over
  data already held.
- The prefix always carries a `LIMIT` — the executor sets one to `MaxRows`
  whenever in-memory ops follow — and `sqlgen` emits `LIMIT` at statement
  level. `SELECT sum(x) FROM t WHERE … LIMIT n` aggregates every matching row
  and then limits the single result row, which is not "aggregate the first n".
  The correct form is `SELECT sum(x) FROM (… LIMIT n) t`, and the document
  format has no subquery source.
- Nothing reports that the prefix was clipped. `Stats.Truncated` is set only
  for app-source fetches, so the sheet cannot tell whether its rows are the
  complete set — which is the one condition under which an unlimited aggregate
  query would agree with the in-memory answer.

Build 5 before 4. With the sheet driving the cursor, completeness is knowable
and there is a materialisation worth avoiding.

---

## Deferred

| Item | Blocked on |
|---|---|
| Inline aggregate sugar | Phase 8 — the reduce registry defines the aggregate set |
| Compiling expressions to SQL, making `sum(profit)` pushable | Nothing in this design; it is simply a separate project |
| A DQL-side expression analyser, removing `ReqExprCompiler` | Would require DQL to own an expression grammar, which the `ExprEvaluator` seam exists to avoid |
| Sheet-to-sheet references across stages | No demonstrated need; a second sheet can read the first's output columns as ordinary columns |
