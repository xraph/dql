# Sheet Semantics (Core) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `sheet` pipe operator that evaluates a set of named formulas in dependency order over a columnar store, producing per-row columns (`expr`) and whole-sheet scalars (`reduce`).

**Architecture:** A new dependency-free `sheet` package holds the engine: typed columns with null bitmaps, a DAG over formula names, and compile-once evaluation through host-supplied `ExprCompiler` / `CompiledExpr` interfaces. `pipe` gains one operator file, one `OpContext` field, one `Requirement`, and one catalog entry. Dependency direction is one-way: `pipe` → `sheet` → `rowops`.

**Tech Stack:** Go 1.26, standard library only. No new module dependencies.

## Global Constraints

- `go.mod` has no `require` block. It must still have none when this plan is done.
- `rowops.Operator` (`Apply(ctx, []Row) ([]Row, error)`) does not change, and no existing operator is modified.
- `sheet` must not import `pipe`, `exec`, `planner`, or `sqlgen`. It may import `internal/rowops` and the standard library.
- Every exported symbol gets a doc comment explaining *why* it exists where the reason is not obvious from the name.
- Tests live in the same package (`package sheet`), named `TestThing_scenario`.
- Run `make fmt` and `make lint` before each commit; both must pass clean.
- Scope covers spec phases 1–3 only. Pushdown, streaming, spill, windows and the host registry are out of scope and must not be half-built here.

---

## File Structure

| File | Responsibility |
|---|---|
| `sheet/column.go` | `Column` interface, typed backings, null bitmap |
| `sheet/builder.go` | `ColumnBuilder` — accumulates values, selects and demotes backing type |
| `sheet/expr.go` | `ExprCompiler` / `CompiledExpr` interfaces — the host seam |
| `sheet/formula.go` | `Formula` type, kind resolution, config-level validation |
| `sheet/dag.go` | Dependency edges, Kahn's algorithm, cycle reporting |
| `sheet/sheet.go` | `Compile` and `Apply` — the engine |
| `sheet/reduce.go` | `ReduceFunc` interface, built-in registry, native kernels |
| `sheet/errors.go` | Typed errors and the `onError` policy |
| `pipe/sheet.go` | Operator config, factory, `Apply`, `init()` registration |
| `pipe/operator.go` | *Modify:* `ExprCompiler` field on `OpContext`, `ReqExprCompiler` |
| `pipe/catalog.go` | *Modify:* `sheetMeta` entry, added to `Catalog()` |

---

## Task 1: Typed columns

**Files:**
- Create: `sheet/column.go`
- Test: `sheet/column_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `Column` interface (`Len() int`, `At(i int) any`, `IsNull(i int) bool`); constructors `NewFloatColumn(vals []float64, nulls *Bitmap) Column`, `NewStringColumn`, `NewBoolColumn`, `NewTimeColumn`, `NewAnyColumn`; `Bitmap` with `Set(i int)`, `Get(i int) bool`, `NewBitmap(n int) *Bitmap`

- [ ] **Step 1: Write the failing test**

```go
package sheet

import "testing"

func TestBitmap_setAndGet(t *testing.T) {
	b := NewBitmap(130)
	if b.Get(0) || b.Get(129) {
		t.Fatal("fresh bitmap must be all-clear")
	}
	b.Set(0)
	b.Set(64) // second word
	b.Set(129)
	for _, i := range []int{0, 64, 129} {
		if !b.Get(i) {
			t.Errorf("bit %d: want set", i)
		}
	}
	for _, i := range []int{1, 63, 65, 128} {
		if b.Get(i) {
			t.Errorf("bit %d: want clear", i)
		}
	}
}

func TestFloatColumn_nullsReadAsNil(t *testing.T) {
	nulls := NewBitmap(3)
	nulls.Set(1)
	c := NewFloatColumn([]float64{1.5, 0, 2.5}, nulls)

	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3", c.Len())
	}
	if c.At(0) != 1.5 || c.At(2) != 2.5 {
		t.Errorf("values: got %v, %v", c.At(0), c.At(2))
	}
	if !c.IsNull(1) {
		t.Error("index 1 must be null")
	}
	if c.At(1) != nil {
		t.Errorf("At on a null must be nil, got %v", c.At(1))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sheet/ -run 'TestBitmap|TestFloatColumn' -v`
Expected: FAIL — `undefined: NewBitmap`

- [ ] **Step 3: Write the implementation**

```go
// Package sheet evaluates a set of named formulas in dependency order over a
// columnar row store. It is the engine behind the pipe `sheet` operator and
// carries no dependency on the pipe package, so it can be tested and reused
// without one.
package sheet

import "time"

// Bitmap is a fixed-size bit set marking which slots of a column are null.
// Stored separately from the values so a typed backing stays a flat, unboxed
// slice — a []float64 with a nil hole would otherwise have to be []*float64.
type Bitmap struct {
	words []uint64
	n     int
}

func NewBitmap(n int) *Bitmap {
	return &Bitmap{words: make([]uint64, (n+63)/64), n: n}
}

func (b *Bitmap) Set(i int) {
	if i < 0 || i >= b.n {
		return
	}
	b.words[i/64] |= 1 << uint(i%64)
}

func (b *Bitmap) Get(i int) bool {
	if b == nil || i < 0 || i >= b.n {
		return false
	}
	return b.words[i/64]&(1<<uint(i%64)) != 0
}

// Column is one column of a sheet. At returns a boxed value because the
// expression language is dynamically typed; the typed accessors below exist
// so reduce kernels can avoid that boxing.
type Column interface {
	Len() int
	At(i int) any
	IsNull(i int) bool
}

// FloatColumn exposes its backing slice so native reduce kernels can scan it
// without boxing. Kernels must consult IsNull: null slots hold the zero value.
type FloatColumn struct {
	vals  []float64
	nulls *Bitmap
}

func NewFloatColumn(vals []float64, nulls *Bitmap) *FloatColumn {
	return &FloatColumn{vals: vals, nulls: nulls}
}

func (c *FloatColumn) Len() int           { return len(c.vals) }
func (c *FloatColumn) IsNull(i int) bool  { return c.nulls.Get(i) }
func (c *FloatColumn) Floats() []float64  { return c.vals }
func (c *FloatColumn) At(i int) any {
	if c.nulls.Get(i) {
		return nil
	}
	return c.vals[i]
}

type StringColumn struct {
	vals  []string
	nulls *Bitmap
}

func NewStringColumn(vals []string, nulls *Bitmap) *StringColumn {
	return &StringColumn{vals: vals, nulls: nulls}
}

func (c *StringColumn) Len() int          { return len(c.vals) }
func (c *StringColumn) IsNull(i int) bool { return c.nulls.Get(i) }
func (c *StringColumn) Strings() []string { return c.vals }
func (c *StringColumn) At(i int) any {
	if c.nulls.Get(i) {
		return nil
	}
	return c.vals[i]
}

type BoolColumn struct {
	vals  []bool
	nulls *Bitmap
}

func NewBoolColumn(vals []bool, nulls *Bitmap) *BoolColumn {
	return &BoolColumn{vals: vals, nulls: nulls}
}

func (c *BoolColumn) Len() int          { return len(c.vals) }
func (c *BoolColumn) IsNull(i int) bool { return c.nulls.Get(i) }
func (c *BoolColumn) At(i int) any {
	if c.nulls.Get(i) {
		return nil
	}
	return c.vals[i]
}

type TimeColumn struct {
	vals  []time.Time
	nulls *Bitmap
}

func NewTimeColumn(vals []time.Time, nulls *Bitmap) *TimeColumn {
	return &TimeColumn{vals: vals, nulls: nulls}
}

func (c *TimeColumn) Len() int          { return len(c.vals) }
func (c *TimeColumn) IsNull(i int) bool { return c.nulls.Get(i) }
func (c *TimeColumn) Times() []time.Time { return c.vals }
func (c *TimeColumn) At(i int) any {
	if c.nulls.Get(i) {
		return nil
	}
	return c.vals[i]
}

// AnyColumn is the fallback backing for heterogeneous data. A nil entry is
// null, so it needs no separate bitmap.
type AnyColumn struct {
	vals []any
}

func NewAnyColumn(vals []any) *AnyColumn { return &AnyColumn{vals: vals} }

func (c *AnyColumn) Len() int          { return len(c.vals) }
func (c *AnyColumn) At(i int) any      { return c.vals[i] }
func (c *AnyColumn) IsNull(i int) bool { return c.vals[i] == nil }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sheet/ -run 'TestBitmap|TestFloatColumn' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
make fmt && make lint && go test ./sheet/
git add sheet/column.go sheet/column_test.go
git commit -m "feat(sheet): typed columns with a separate null bitmap"
```

---

## Task 2: Column builder with type demotion

**Files:**
- Create: `sheet/builder.go`
- Test: `sheet/builder_test.go`

**Interfaces:**
- Consumes: `Column`, `Bitmap`, the `New*Column` constructors from Task 1
- Produces: `NewColumnBuilder(capacity int) *ColumnBuilder`; `(*ColumnBuilder).Append(v any)`; `(*ColumnBuilder).Build() Column`

- [ ] **Step 1: Write the failing test**

```go
package sheet

import "testing"

func TestColumnBuilder_homogeneousFloatsStayTyped(t *testing.T) {
	b := NewColumnBuilder(3)
	b.Append(1.0)
	b.Append(2.0)
	b.Append(nil)
	col := b.Build()

	if _, ok := col.(*FloatColumn); !ok {
		t.Fatalf("want *FloatColumn, got %T", col)
	}
	if col.Len() != 3 || col.At(0) != 1.0 || !col.IsNull(2) {
		t.Errorf("unexpected column state: len=%d at0=%v null2=%v", col.Len(), col.At(0), col.IsNull(2))
	}
}

func TestColumnBuilder_intsBecomeFloats(t *testing.T) {
	// The expression language yields int64 for whole numbers and float64
	// otherwise; a column must not fragment over that distinction.
	b := NewColumnBuilder(2)
	b.Append(int64(1))
	b.Append(2.5)
	col := b.Build()

	if _, ok := col.(*FloatColumn); !ok {
		t.Fatalf("want *FloatColumn, got %T", col)
	}
	if col.At(0) != 1.0 || col.At(1) != 2.5 {
		t.Errorf("got %v, %v", col.At(0), col.At(1))
	}
}

func TestColumnBuilder_mismatchDemotesToAny(t *testing.T) {
	b := NewColumnBuilder(3)
	b.Append(1.0)
	b.Append("two") // forces demotion
	b.Append(3.0)
	col := b.Build()

	if _, ok := col.(*AnyColumn); !ok {
		t.Fatalf("want *AnyColumn, got %T", col)
	}
	if col.At(0) != 1.0 || col.At(1) != "two" || col.At(2) != 3.0 {
		t.Errorf("values lost across demotion: %v %v %v", col.At(0), col.At(1), col.At(2))
	}
}

func TestColumnBuilder_allNullsBuildsAnAnyColumn(t *testing.T) {
	b := NewColumnBuilder(2)
	b.Append(nil)
	b.Append(nil)
	col := b.Build()

	if col.Len() != 2 || !col.IsNull(0) || !col.IsNull(1) {
		t.Errorf("all-null column wrong: len=%d", col.Len())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sheet/ -run TestColumnBuilder -v`
Expected: FAIL — `undefined: NewColumnBuilder`

- [ ] **Step 3: Write the implementation**

```go
package sheet

import "time"

type backingKind int

const (
	backingUnset backingKind = iota
	backingFloat
	backingString
	backingBool
	backingTime
	backingAny
)

// ColumnBuilder accumulates values into the narrowest backing that fits.
// It starts unset, adopts a backing from the first non-null value, and demotes
// to []any on the first value that does not fit. Demotion is one-way: a column
// that has seen mixed types stays boxed.
type ColumnBuilder struct {
	kind  backingKind
	n     int
	nulls *Bitmap

	floats  []float64
	strings []string
	bools   []bool
	times   []time.Time
	anys    []any
}

func NewColumnBuilder(capacity int) *ColumnBuilder {
	if capacity < 0 {
		capacity = 0
	}
	return &ColumnBuilder{nulls: NewBitmap(capacity)}
}

func (b *ColumnBuilder) Append(v any) {
	b.growNulls()

	if v == nil {
		b.appendZero()
		b.nulls.Set(b.n)
		b.n++
		return
	}

	f, isNum := toFloat(v)
	switch {
	case b.kind == backingUnset:
		b.adopt(v, f, isNum)
	case b.kind == backingFloat && isNum:
		b.floats = append(b.floats, f)
	case b.kind == backingString:
		s, ok := v.(string)
		if !ok {
			b.demote()
			b.anys = append(b.anys, v)
			break
		}
		b.strings = append(b.strings, s)
	case b.kind == backingBool:
		x, ok := v.(bool)
		if !ok {
			b.demote()
			b.anys = append(b.anys, v)
			break
		}
		b.bools = append(b.bools, x)
	case b.kind == backingTime:
		x, ok := v.(time.Time)
		if !ok {
			b.demote()
			b.anys = append(b.anys, v)
			break
		}
		b.times = append(b.times, x)
	case b.kind == backingAny:
		b.anys = append(b.anys, v)
	default:
		b.demote()
		b.anys = append(b.anys, v)
	}
	b.n++
}

// growNulls keeps the bitmap large enough for index b.n. Callers may append
// past the capacity passed to NewColumnBuilder.
func (b *ColumnBuilder) growNulls() {
	if b.n < b.nulls.n {
		return
	}
	next := NewBitmap(max(b.n*2+1, 64))
	copy(next.words, b.nulls.words)
	b.nulls = next
}

func (b *ColumnBuilder) adopt(v any, f float64, isNum bool) {
	switch {
	case isNum:
		b.kind = backingFloat
		b.floats = append(b.floats, f)
	default:
		switch x := v.(type) {
		case string:
			b.kind = backingString
			b.strings = append(b.strings, x)
		case bool:
			b.kind = backingBool
			b.bools = append(b.bools, x)
		case time.Time:
			b.kind = backingTime
			b.times = append(b.times, x)
		default:
			b.kind = backingAny
			b.anys = append(b.anys, v)
		}
	}
	// Slots appended before the first non-null value were nulls; backfill
	// them so the typed slice and the index space stay aligned.
	b.backfill()
}

// backfill inserts zero values for the leading nulls recorded before a
// backing was chosen, so index i in the typed slice is row i.
func (b *ColumnBuilder) backfill() {
	pad := b.n
	if pad == 0 {
		return
	}
	switch b.kind {
	case backingFloat:
		b.floats = append(make([]float64, pad), b.floats...)
	case backingString:
		b.strings = append(make([]string, pad), b.strings...)
	case backingBool:
		b.bools = append(make([]bool, pad), b.bools...)
	case backingTime:
		b.times = append(make([]time.Time, pad), b.times...)
	case backingAny:
		b.anys = append(make([]any, pad), b.anys...)
	}
}

func (b *ColumnBuilder) appendZero() {
	switch b.kind {
	case backingFloat:
		b.floats = append(b.floats, 0)
	case backingString:
		b.strings = append(b.strings, "")
	case backingBool:
		b.bools = append(b.bools, false)
	case backingTime:
		b.times = append(b.times, time.Time{})
	case backingAny:
		b.anys = append(b.anys, nil)
	}
}

// demote converts whatever has accumulated into []any, preserving nulls.
func (b *ColumnBuilder) demote() {
	if b.kind == backingAny {
		return
	}
	out := make([]any, 0, b.n)
	for i := 0; i < b.n; i++ {
		if b.nulls.Get(i) {
			out = append(out, nil)
			continue
		}
		switch b.kind {
		case backingFloat:
			out = append(out, b.floats[i])
		case backingString:
			out = append(out, b.strings[i])
		case backingBool:
			out = append(out, b.bools[i])
		case backingTime:
			out = append(out, b.times[i])
		}
	}
	b.kind = backingAny
	b.anys = out
	b.floats, b.strings, b.bools, b.times = nil, nil, nil, nil
}

func (b *ColumnBuilder) Build() Column {
	switch b.kind {
	case backingFloat:
		return NewFloatColumn(b.floats, b.nulls)
	case backingString:
		return NewStringColumn(b.strings, b.nulls)
	case backingBool:
		return NewBoolColumn(b.bools, b.nulls)
	case backingTime:
		return NewTimeColumn(b.times, b.nulls)
	case backingAny:
		return NewAnyColumn(b.anys)
	default:
		// Nothing but nulls: no backing was ever chosen.
		return NewAnyColumn(make([]any, b.n))
	}
}

// toFloat widens the numeric types an expression language may produce.
// Reported separately from the value so a non-numeric input is distinguishable
// from a genuine zero.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sheet/ -run TestColumnBuilder -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
make fmt && make lint && go test ./sheet/
git add sheet/builder.go sheet/builder_test.go
git commit -m "feat(sheet): column builder that narrows to a typed backing and demotes once"
```

---

## Task 3: The expression seam

**Files:**
- Create: `sheet/expr.go`
- Test: `sheet/expr_test.go` (defines the fake used by every later task)

**Interfaces:**
- Consumes: nothing
- Produces: `ExprCompiler` (`FreeIdentifiers(expr string) ([]string, error)`, `Compile(expr string) (CompiledExpr, error)`); `CompiledExpr` (`Eval(ctx context.Context, args map[string]any) (any, error)`). Test helper `newFakeCompiler()` returning a compiler that treats an expression as a space-separated postfix program — later tasks build on it.

- [ ] **Step 1: Write the failing test**

```go
package sheet

import (
	"context"
	"testing"
)

func TestFakeCompiler_evaluatesAndReportsIdentifiers(t *testing.T) {
	c := newFakeCompiler()

	ids, err := c.FreeIdentifiers("revenue cost -")
	if err != nil {
		t.Fatalf("FreeIdentifiers: %v", err)
	}
	if len(ids) != 2 || ids[0] != "revenue" || ids[1] != "cost" {
		t.Fatalf("got %v, want [revenue cost]", ids)
	}

	ce, err := c.Compile("revenue cost -")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := ce.Eval(context.Background(), map[string]any{"revenue": 10.0, "cost": 4.0})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 6.0 {
		t.Errorf("got %v, want 6", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sheet/ -run TestFakeCompiler -v`
Expected: FAIL — `undefined: newFakeCompiler`

- [ ] **Step 3: Write the implementation**

`sheet/expr.go`:

```go
package sheet

import "context"

// ExprCompiler prepares an expression once for repeated evaluation and reports
// what it references.
//
// Both halves live on the host because both require parsing, and parsing
// requires owning the grammar — which this package deliberately does not. A
// text-level approximation is not an option for FreeIdentifiers: it would match
// names inside string literals, and a phantom edge in a dependency graph
// surfaces as a circular-dependency error for an acyclic sheet.
type ExprCompiler interface {
	// FreeIdentifiers reports the unbound names the expression references.
	// Names bound within the expression (lambda parameters, let bindings) and
	// called function names must be excluded.
	FreeIdentifiers(expr string) ([]string, error)

	// Compile prepares an expression for evaluation, returning parse and
	// resolution errors here rather than deferring them to Eval.
	Compile(expr string) (CompiledExpr, error)
}

// CompiledExpr is one prepared expression.
type CompiledExpr interface {
	// Eval binds args and evaluates. Implementations must not retain args:
	// the sheet reuses a single map across every row.
	Eval(ctx context.Context, args map[string]any) (any, error)
}
```

`sheet/expr_test.go` — the fake, shared by later tasks:

```go
package sheet

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// fakeCompiler implements ExprCompiler over a tiny space-separated postfix
// language, so the engine's tests exercise real compile-once behaviour without
// depending on an expression language.
//
// Tokens: a number is a literal; + - * / are binary operators; sum/avg/min/max/
// count applied to a slice argument reduce it; anything else is an identifier.
type fakeCompiler struct{ compiles int }

func newFakeCompiler() *fakeCompiler { return &fakeCompiler{} }

func (c *fakeCompiler) FreeIdentifiers(expr string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.Fields(expr) {
		if !isIdentToken(tok) || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out, nil
}

func (c *fakeCompiler) Compile(expr string) (CompiledExpr, error) {
	toks := strings.Fields(expr)
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	c.compiles++
	return &fakeExpr{toks: toks}, nil
}

func isIdentToken(tok string) bool {
	switch tok {
	case "+", "-", "*", "/", "sum", "avg", "min", "max", "count":
		return false
	}
	if _, err := strconv.ParseFloat(tok, 64); err == nil {
		return false
	}
	return true
}

type fakeExpr struct{ toks []string }

func (e *fakeExpr) Eval(_ context.Context, args map[string]any) (any, error) {
	var stack []any
	pop := func() (any, error) {
		if len(stack) == 0 {
			return nil, fmt.Errorf("stack underflow")
		}
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v, nil
	}

	for _, tok := range e.toks {
		switch tok {
		case "+", "-", "*", "/":
			b, err := pop()
			if err != nil {
				return nil, err
			}
			a, err := pop()
			if err != nil {
				return nil, err
			}
			af, aok := toFloat(a)
			bf, bok := toFloat(b)
			if !aok || !bok {
				return nil, fmt.Errorf("non-numeric operand for %q", tok)
			}
			switch tok {
			case "+":
				stack = append(stack, af+bf)
			case "-":
				stack = append(stack, af-bf)
			case "*":
				stack = append(stack, af*bf)
			case "/":
				if bf == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				stack = append(stack, af/bf)
			}
		case "sum", "avg", "min", "max", "count":
			v, err := pop()
			if err != nil {
				return nil, err
			}
			r, err := fakeReduce(tok, v)
			if err != nil {
				return nil, err
			}
			stack = append(stack, r)
		default:
			if f, err := strconv.ParseFloat(tok, 64); err == nil {
				stack = append(stack, f)
				continue
			}
			stack = append(stack, args[tok])
		}
	}
	if len(stack) != 1 {
		return nil, fmt.Errorf("expression left %d values on the stack", len(stack))
	}
	return stack[0], nil
}

func fakeReduce(fn string, v any) (any, error) {
	vals, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: want a column, got %T", fn, v)
	}
	var acc float64
	var n int
	var mn, mx float64
	for _, x := range vals {
		f, ok := toFloat(x)
		if !ok {
			continue
		}
		if n == 0 {
			mn, mx = f, f
		}
		if f < mn {
			mn = f
		}
		if f > mx {
			mx = f
		}
		acc += f
		n++
	}
	switch fn {
	case "count":
		return int64(n), nil
	case "sum":
		if n == 0 {
			return nil, nil
		}
		return acc, nil
	case "avg":
		if n == 0 {
			return nil, nil
		}
		return acc / float64(n), nil
	case "min":
		if n == 0 {
			return nil, nil
		}
		return mn, nil
	case "max":
		if n == 0 {
			return nil, nil
		}
		return mx, nil
	}
	return nil, fmt.Errorf("unknown reduce %q", fn)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sheet/ -run TestFakeCompiler -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
make fmt && make lint && go test ./sheet/
git add sheet/expr.go sheet/expr_test.go
git commit -m "feat(sheet): host seam for compiling expressions and reporting their references"
```

---

## Task 4: Formula config and validation

**Files:**
- Create: `sheet/formula.go`, `sheet/errors.go`
- Test: `sheet/formula_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `Formula{As, Expr, Reduce string}`; `(Formula).Kind() Kind` returning `KindColumn`/`KindReduce`/`KindInvalid`; `(Formula).Source() string`; `Kind` constants; `ErrorPolicy` (`PolicyFail`, `PolicyNull`) with `ParsePolicy(string) (ErrorPolicy, error)`; `validateFormulas([]Formula) error`

- [ ] **Step 1: Write the failing test**

```go
package sheet

import "strings"
import "testing"

func TestFormula_kindComesFromWhichKeyIsSet(t *testing.T) {
	tests := []struct {
		name string
		f    Formula
		want Kind
	}{
		{"expr only", Formula{As: "a", Expr: "x"}, KindColumn},
		{"reduce only", Formula{As: "a", Reduce: "sum x"}, KindReduce},
		{"neither", Formula{As: "a"}, KindInvalid},
		{"both", Formula{As: "a", Expr: "x", Reduce: "sum x"}, KindInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.f.Kind(); got != tt.want {
				t.Errorf("Kind() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateFormulas_rejectsDuplicateNames(t *testing.T) {
	err := validateFormulas([]Formula{
		{As: "total", Expr: "x"},
		{As: "total", Expr: "y"},
	})
	if err == nil || !strings.Contains(err.Error(), "total") {
		t.Fatalf("want an error naming the duplicate, got %v", err)
	}
}

func TestValidateFormulas_rejectsMissingName(t *testing.T) {
	if err := validateFormulas([]Formula{{Expr: "x"}}); err == nil {
		t.Fatal("want an error for a formula with no `as`")
	}
}

func TestValidateFormulas_rejectsBothKeys(t *testing.T) {
	err := validateFormulas([]Formula{{As: "a", Expr: "x", Reduce: "sum x"}})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("want an error explaining the two keys are exclusive, got %v", err)
	}
}

func TestParsePolicy(t *testing.T) {
	if p, err := ParsePolicy(""); err != nil || p != PolicyFail {
		t.Errorf("empty must default to fail, got %v %v", p, err)
	}
	if p, err := ParsePolicy("null"); err != nil || p != PolicyNull {
		t.Errorf("null: got %v %v", p, err)
	}
	if _, err := ParsePolicy("shrug"); err == nil {
		t.Error("unknown policy must error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sheet/ -run 'TestFormula|TestValidateFormulas|TestParsePolicy' -v`
Expected: FAIL — `undefined: Formula`

- [ ] **Step 3: Write the implementation**

`sheet/formula.go`:

```go
package sheet

import "fmt"

// Kind distinguishes the two formula shapes.
type Kind int

const (
	KindInvalid Kind = iota
	// KindColumn evaluates once per row and produces a column.
	KindColumn
	// KindReduce evaluates once over a whole column and produces a scalar.
	KindReduce
)

func (k Kind) String() string {
	switch k {
	case KindColumn:
		return "expr"
	case KindReduce:
		return "reduce"
	}
	return "invalid"
}

// Formula is one named calculation. Exactly one of Expr and Reduce is set;
// which one determines the kind, so there is no separate scope annotation that
// could disagree with the expression's shape.
type Formula struct {
	As     string `json:"as"`
	Expr   string `json:"expr,omitempty"`
	Reduce string `json:"reduce,omitempty"`
}

func (f Formula) Kind() Kind {
	switch {
	case f.Expr != "" && f.Reduce == "":
		return KindColumn
	case f.Reduce != "" && f.Expr == "":
		return KindReduce
	}
	return KindInvalid
}

// Source returns whichever expression the formula carries.
func (f Formula) Source() string {
	if f.Kind() == KindReduce {
		return f.Reduce
	}
	return f.Expr
}

func validateFormulas(fs []Formula) error {
	if len(fs) == 0 {
		return fmt.Errorf("sheet: at least one formula is required")
	}
	seen := make(map[string]bool, len(fs))
	for i, f := range fs {
		if f.As == "" {
			return fmt.Errorf("sheet: formula %d has no `as` name", i)
		}
		if f.Kind() == KindInvalid {
			return fmt.Errorf("sheet: formula %q must set exactly one of `expr` or `reduce`", f.As)
		}
		if seen[f.As] {
			return fmt.Errorf("sheet: formula %q is defined more than once", f.As)
		}
		seen[f.As] = true
	}
	return nil
}
```

`sheet/errors.go`:

```go
package sheet

import "fmt"

// ErrorPolicy decides what an evaluation error does to the run.
type ErrorPolicy int

const (
	// PolicyFail aborts on the first evaluation error. The default, matching
	// every other pipe operator: a stage that emits partially-wrong rows is
	// worse than one that stops, because downstream stages compute on the
	// damage and nothing surfaces it.
	PolicyFail ErrorPolicy = iota
	// PolicyNull writes null into the failing cell and continues, for the
	// imported-workbook case where a few bad rows should not fail a query.
	PolicyNull
)

func ParsePolicy(s string) (ErrorPolicy, error) {
	switch s {
	case "", "fail":
		return PolicyFail, nil
	case "null":
		return PolicyNull, nil
	}
	return PolicyFail, fmt.Errorf("sheet: unknown onError policy %q (want \"fail\" or \"null\")", s)
}

// MaxRecordedErrors bounds what PolicyNull retains. Collecting one entry per
// failing row over a ten-million-row sheet is its own failure mode.
const MaxRecordedErrors = 100

// CellError records one evaluation failure under PolicyNull.
type CellError struct {
	Formula string `json:"formula"`
	Row     int    `json:"row"`
	Message string `json:"message"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sheet/ -run 'TestFormula|TestValidateFormulas|TestParsePolicy' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
make fmt && make lint && go test ./sheet/
git add sheet/formula.go sheet/errors.go sheet/formula_test.go
git commit -m "feat(sheet): formula kinds declared by key, plus the error policy"
```

---

## Task 5: Dependency graph

**Files:**
- Create: `sheet/dag.go`
- Test: `sheet/dag_test.go`

**Interfaces:**
- Consumes: `Formula`, `Kind` from Task 4
- Produces: `topoSort(formulas []Formula, refs map[string][]string) ([]Formula, error)` — `refs` maps a formula name to the identifiers it references; only identifiers that name another formula create an edge, so unknown names are left for column binding at apply time

- [ ] **Step 1: Write the failing test**

```go
package sheet

import (
	"strings"
	"testing"
)

func TestTopoSort_ordersDependenciesFirst(t *testing.T) {
	fs := []Formula{
		{As: "share", Expr: "profit total /"},
		{As: "total", Reduce: "profit sum"},
		{As: "profit", Expr: "revenue cost -"},
	}
	refs := map[string][]string{
		"share":  {"profit", "total"},
		"total":  {"profit"},
		"profit": {"revenue", "cost"},
	}

	got, err := topoSort(fs, refs)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	pos := map[string]int{}
	for i, f := range got {
		pos[f.As] = i
	}
	if len(got) != 3 {
		t.Fatalf("got %d formulas, want 3", len(got))
	}
	if pos["profit"] > pos["total"] || pos["total"] > pos["share"] {
		t.Errorf("wrong order: %v", pos)
	}
}

func TestTopoSort_isStableForIndependentFormulas(t *testing.T) {
	fs := []Formula{
		{As: "a", Expr: "x"},
		{As: "b", Expr: "y"},
		{As: "c", Expr: "z"},
	}
	refs := map[string][]string{"a": {"x"}, "b": {"y"}, "c": {"z"}}

	got, err := topoSort(fs, refs)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].As != want {
			t.Fatalf("position %d = %q, want %q — independent formulas must keep declaration order", i, got[i].As, want)
		}
	}
}

func TestTopoSort_reportsEveryCycleParticipant(t *testing.T) {
	fs := []Formula{
		{As: "a", Expr: "b 1 +"},
		{As: "b", Expr: "a 1 +"},
	}
	refs := map[string][]string{"a": {"b"}, "b": {"a"}}

	_, err := topoSort(fs, refs)
	if err == nil {
		t.Fatal("want a cycle error")
	}
	for _, name := range []string{"a", "b"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error must name %q: %v", name, err)
		}
	}
}

func TestTopoSort_ignoresReferencesThatAreNotFormulas(t *testing.T) {
	// revenue and cost are source columns; they must not become graph nodes.
	fs := []Formula{{As: "profit", Expr: "revenue cost -"}}
	refs := map[string][]string{"profit": {"revenue", "cost"}}

	got, err := topoSort(fs, refs)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if len(got) != 1 || got[0].As != "profit" {
		t.Errorf("got %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sheet/ -run TestTopoSort -v`
Expected: FAIL — `undefined: topoSort`

- [ ] **Step 3: Write the implementation**

```go
package sheet

import (
	"fmt"
	"sort"
	"strings"
)

// topoSort orders formulas so each runs after everything it references.
//
// Only references naming another formula become edges. A reference to
// anything else is presumed to be a source column, which is always available
// before any formula runs and so constrains nothing; whether such a column
// actually exists is settled when the sheet meets its input.
//
// Declaration order is preserved among formulas that do not constrain each
// other, so a sheet's output column order is predictable.
func topoSort(formulas []Formula, refs map[string][]string) ([]Formula, error) {
	index := make(map[string]int, len(formulas))
	for i, f := range formulas {
		index[f.As] = i
	}

	deps := make(map[string]map[string]bool, len(formulas))
	dependents := make(map[string][]string, len(formulas))
	inDegree := make(map[string]int, len(formulas))

	for _, f := range formulas {
		deps[f.As] = map[string]bool{}
		inDegree[f.As] = 0
	}
	for _, f := range formulas {
		for _, ref := range refs[f.As] {
			if ref == f.As {
				continue // self-reference is caught as a cycle below
			}
			if _, isFormula := index[ref]; !isFormula {
				continue
			}
			if deps[f.As][ref] {
				continue
			}
			deps[f.As][ref] = true
			dependents[ref] = append(dependents[ref], f.As)
			inDegree[f.As]++
		}
	}
	// A self-reference is a cycle of length one; record it as its own edge so
	// the check below reports it rather than silently dropping it.
	for _, f := range formulas {
		for _, ref := range refs[f.As] {
			if ref == f.As {
				inDegree[f.As]++
				dependents[f.As] = append(dependents[f.As], f.As)
				break
			}
		}
	}

	ready := make([]int, 0, len(formulas))
	for _, f := range formulas {
		if inDegree[f.As] == 0 {
			ready = append(ready, index[f.As])
		}
	}
	sort.Ints(ready)

	out := make([]Formula, 0, len(formulas))
	for len(ready) > 0 {
		i := ready[0]
		ready = ready[1:]
		f := formulas[i]
		out = append(out, f)

		var freed []int
		for _, dep := range dependents[f.As] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				freed = append(freed, index[dep])
			}
		}
		if len(freed) > 0 {
			ready = append(ready, freed...)
			sort.Ints(ready)
		}
	}

	if len(out) != len(formulas) {
		var stuck []string
		for _, f := range formulas {
			if inDegree[f.As] > 0 {
				stuck = append(stuck, f.As)
			}
		}
		sort.Strings(stuck)
		return nil, fmt.Errorf("sheet: circular dependency among formulas: %s", strings.Join(stuck, ", "))
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sheet/ -run TestTopoSort -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
make fmt && make lint && go test ./sheet/
git add sheet/dag.go sheet/dag_test.go
git commit -m "feat(sheet): dependency ordering with cycle reporting"
```

---

## Task 6: Compile

**Files:**
- Create: `sheet/sheet.go`
- Test: `sheet/sheet_compile_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–5
- Produces: `Config{Formulas []Formula, OnError string}`; `Compile(cfg Config, c ExprCompiler) (*Sheet, error)`; `*Sheet` with unexported fields `order []Formula`, `compiled map[string]CompiledExpr`, `refs map[string][]string`, `policy ErrorPolicy`; `(*Sheet).Names() []string`

- [ ] **Step 1: Write the failing test**

```go
package sheet

import (
	"strings"
	"testing"
)

func TestCompile_compilesEachExpressionExactlyOnce(t *testing.T) {
	c := newFakeCompiler()
	s, err := Compile(Config{Formulas: []Formula{
		{As: "profit", Expr: "revenue cost -"},
		{As: "total", Reduce: "profit sum"},
	}}, c)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if c.compiles != 2 {
		t.Errorf("compiled %d expressions, want 2 — one per formula", c.compiles)
	}
	if got := s.Names(); len(got) != 2 || got[0] != "profit" || got[1] != "total" {
		t.Errorf("Names() = %v", got)
	}
}

func TestCompile_rejectsACycle(t *testing.T) {
	_, err := Compile(Config{Formulas: []Formula{
		{As: "a", Expr: "b 1 +"},
		{As: "b", Expr: "a 1 +"},
	}}, newFakeCompiler())
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("want a circular-dependency error, got %v", err)
	}
}

func TestCompile_rejectsAnUnknownPolicy(t *testing.T) {
	_, err := Compile(Config{
		Formulas: []Formula{{As: "a", Expr: "x"}},
		OnError:  "maybe",
	}, newFakeCompiler())
	if err == nil || !strings.Contains(err.Error(), "onError") {
		t.Fatalf("want an onError error, got %v", err)
	}
}

func TestCompile_requiresACompiler(t *testing.T) {
	_, err := Compile(Config{Formulas: []Formula{{As: "a", Expr: "x"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "expression compiler") {
		t.Fatalf("want a missing-compiler error, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sheet/ -run TestCompile -v`
Expected: FAIL — `undefined: Compile`

- [ ] **Step 3: Write the implementation**

```go
package sheet

import "fmt"

// Config is a sheet's declaration.
type Config struct {
	Formulas []Formula `json:"formulas"`
	// OnError is "fail" (default) or "null". See ErrorPolicy.
	OnError string `json:"onError,omitempty"`
}

// Sheet is a compiled, ordered set of formulas ready to run over rows.
type Sheet struct {
	order    []Formula
	compiled map[string]CompiledExpr
	refs     map[string][]string
	isFormula map[string]bool
	policy   ErrorPolicy
}

// Compile validates a sheet, resolves its dependency order, and prepares every
// expression. Everything detectable without knowing the input's columns is
// settled here, so a malformed sheet fails while the query is being validated
// rather than partway through a scan.
func Compile(cfg Config, c ExprCompiler) (*Sheet, error) {
	if c == nil {
		return nil, fmt.Errorf("sheet: an expression compiler is required")
	}
	if err := validateFormulas(cfg.Formulas); err != nil {
		return nil, err
	}
	policy, err := ParsePolicy(cfg.OnError)
	if err != nil {
		return nil, err
	}

	refs := make(map[string][]string, len(cfg.Formulas))
	compiled := make(map[string]CompiledExpr, len(cfg.Formulas))
	isFormula := make(map[string]bool, len(cfg.Formulas))
	for _, f := range cfg.Formulas {
		isFormula[f.As] = true
	}

	for _, f := range cfg.Formulas {
		ids, err := c.FreeIdentifiers(f.Source())
		if err != nil {
			return nil, fmt.Errorf("sheet: formula %q: %w", f.As, err)
		}
		refs[f.As] = ids

		ce, err := c.Compile(f.Source())
		if err != nil {
			return nil, fmt.Errorf("sheet: formula %q: %w", f.As, err)
		}
		compiled[f.As] = ce
	}

	order, err := topoSort(cfg.Formulas, refs)
	if err != nil {
		return nil, err
	}

	return &Sheet{
		order:     order,
		compiled:  compiled,
		refs:      refs,
		isFormula: isFormula,
		policy:    policy,
	}, nil
}

// Names returns the formula names in evaluation order.
func (s *Sheet) Names() []string {
	out := make([]string, len(s.order))
	for i, f := range s.order {
		out[i] = f.As
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sheet/ -run TestCompile -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
make fmt && make lint && go test ./sheet/
git add sheet/sheet.go sheet/sheet_compile_test.go
git commit -m "feat(sheet): compile a sheet once, ordering formulas and preparing expressions"
```

---

## Task 7: Apply

**Files:**
- Modify: `sheet/sheet.go`
- Test: `sheet/sheet_apply_test.go`

**Interfaces:**
- Consumes: `*Sheet` from Task 6, `ColumnBuilder` from Task 2
- Produces: `(*Sheet).Apply(ctx context.Context, in []rowops.Row) (*Result, error)`; `Result{Rows []rowops.Row, Scalars map[string]any, Errors []CellError, ErrorCount int}`

- [ ] **Step 1: Write the failing test**

```go
package sheet

import (
	"context"
	"strings"
	"testing"

	"github.com/xraph/dql/internal/rowops"
)

func TestApply_computesColumnsInDependencyOrder(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		// Declared out of order on purpose.
		{As: "margin", Expr: "profit revenue /"},
		{As: "profit", Expr: "revenue cost -"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	res, err := s.Apply(context.Background(), []rowops.Row{
		{"revenue": 100.0, "cost": 60.0},
		{"revenue": 200.0, "cost": 150.0},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Rows[0]["profit"] != 40.0 || res.Rows[0]["margin"] != 0.4 {
		t.Errorf("row 0: profit=%v margin=%v", res.Rows[0]["profit"], res.Rows[0]["margin"])
	}
	if res.Rows[1]["profit"] != 50.0 || res.Rows[1]["margin"] != 0.25 {
		t.Errorf("row 1: profit=%v margin=%v", res.Rows[1]["profit"], res.Rows[1]["margin"])
	}
}

func TestApply_reduceSeesTheWholeColumnIncludingComputedOnes(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "profit", Expr: "revenue cost -"},
		{As: "total_profit", Reduce: "profit sum"},
		{As: "share", Expr: "profit total_profit /"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	res, err := s.Apply(context.Background(), []rowops.Row{
		{"revenue": 100.0, "cost": 60.0}, // profit 40
		{"revenue": 200.0, "cost": 140.0}, // profit 60
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Scalars["total_profit"] != 100.0 {
		t.Fatalf("total_profit = %v, want 100", res.Scalars["total_profit"])
	}
	if res.Rows[0]["share"] != 0.4 || res.Rows[1]["share"] != 0.6 {
		t.Errorf("share: %v, %v", res.Rows[0]["share"], res.Rows[1]["share"])
	}
	// A reduce is a sheet-wide scalar, not a column.
	if _, present := res.Rows[0]["total_profit"]; present {
		t.Error("a reduce must not be written into rows")
	}
}

func TestApply_unknownReferenceIsAnError(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "x", Expr: "revenu 1 +"}, // typo
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = s.Apply(context.Background(), []rowops.Row{{"revenue": 1.0}})
	if err == nil || !strings.Contains(err.Error(), "revenu") {
		t.Fatalf("want an error naming the unresolved identifier, got %v", err)
	}
}

func TestApply_failPolicyAbortsOnFirstError(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "ratio", Expr: "a b /"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = s.Apply(context.Background(), []rowops.Row{
		{"a": 1.0, "b": 1.0},
		{"a": 1.0, "b": 0.0}, // division by zero
	})
	if err == nil || !strings.Contains(err.Error(), "ratio") {
		t.Fatalf("want an abort naming the formula, got %v", err)
	}
}

func TestApply_nullPolicyContinuesAndRecords(t *testing.T) {
	s, err := Compile(Config{
		Formulas: []Formula{{As: "ratio", Expr: "a b /"}},
		OnError:  "null",
	}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := s.Apply(context.Background(), []rowops.Row{
		{"a": 1.0, "b": 0.0}, // fails
		{"a": 6.0, "b": 2.0}, // fine
	})
	if err != nil {
		t.Fatalf("Apply must not abort under the null policy: %v", err)
	}
	if res.Rows[0]["ratio"] != nil {
		t.Errorf("failing cell must be null, got %v", res.Rows[0]["ratio"])
	}
	if res.Rows[1]["ratio"] != 3.0 {
		t.Errorf("row 1 = %v, want 3", res.Rows[1]["ratio"])
	}
	if res.ErrorCount != 1 || len(res.Errors) != 1 || res.Errors[0].Row != 0 {
		t.Errorf("errors = %+v count=%d", res.Errors, res.ErrorCount)
	}
}

func TestApply_emptyInputStillEvaluatesReduces(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "n", Reduce: "revenue count"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := s.Apply(context.Background(), nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Scalars["n"] != int64(0) {
		t.Errorf("count over no rows = %v, want 0", res.Scalars["n"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sheet/ -run TestApply -v`
Expected: FAIL — `s.Apply undefined`

- [ ] **Step 3: Write the implementation**

Append to `sheet/sheet.go`, and add the imports `context`, `sort`, `strings`, and `github.com/xraph/dql/internal/rowops`:

```go
// Result is one sheet evaluation.
type Result struct {
	Rows    []rowops.Row
	Scalars map[string]any
	// Errors holds up to MaxRecordedErrors entries under PolicyNull.
	Errors []CellError
	// ErrorCount is the true total, which may exceed len(Errors).
	ErrorCount int
}

// Apply evaluates every formula over in, in dependency order.
//
// Column formulas are evaluated one whole column at a time rather than one
// whole row at a time: each formula's expression is already compiled, and
// walking the column keeps that one expression and its argument map hot.
func (s *Sheet) Apply(ctx context.Context, in []rowops.Row) (*Result, error) {
	cols := columnsOf(in)
	if err := s.checkReferences(cols); err != nil {
		return nil, err
	}

	res := &Result{Rows: in, Scalars: map[string]any{}}
	// One args map, reused for every row of every formula. CompiledExpr is
	// contractually forbidden from retaining it.
	args := make(map[string]any, len(cols)+len(s.order))

	for _, f := range s.order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		if f.Kind() == KindReduce {
			err = s.evalReduce(ctx, f, in, res, args)
		} else {
			err = s.evalColumn(ctx, f, in, res, args)
		}
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (s *Sheet) evalColumn(ctx context.Context, f Formula, in []rowops.Row, res *Result, args map[string]any) error {
	ce := s.compiled[f.As]
	refs := s.refs[f.As]

	for i, row := range in {
		for _, name := range refs {
			if v, ok := res.Scalars[name]; ok {
				args[name] = v
				continue
			}
			args[name] = row[name]
		}

		val, err := ce.Eval(ctx, args)
		if err != nil {
			if s.policy == PolicyFail {
				return fmt.Errorf("sheet: formula %q, row %d: %w", f.As, i, err)
			}
			res.record(f.As, i, err)
			val = nil
		}
		row[f.As] = val
	}
	return nil
}

func (s *Sheet) evalReduce(ctx context.Context, f Formula, in []rowops.Row, res *Result, args map[string]any) error {
	// A reduce reads whole columns: bind each reference to every row's value.
	for _, name := range s.refs[f.As] {
		if v, ok := res.Scalars[name]; ok {
			args[name] = v
			continue
		}
		vals := make([]any, len(in))
		for i, row := range in {
			vals[i] = row[name]
		}
		args[name] = vals
	}

	val, err := s.compiled[f.As].Eval(ctx, args)
	if err != nil {
		if s.policy == PolicyFail {
			return fmt.Errorf("sheet: reduce %q: %w", f.As, err)
		}
		res.record(f.As, -1, err)
		val = nil
	}
	res.Scalars[f.As] = val
	return nil
}

func (r *Result) record(formula string, row int, err error) {
	r.ErrorCount++
	if len(r.Errors) < MaxRecordedErrors {
		r.Errors = append(r.Errors, CellError{Formula: formula, Row: row, Message: err.Error()})
	}
}

// checkReferences reports identifiers that name neither a source column nor
// another formula. Deferred to here because the input's columns are not known
// while the sheet is being compiled.
func (s *Sheet) checkReferences(cols map[string]bool) error {
	var unresolved []string
	seen := map[string]bool{}
	for _, f := range s.order {
		for _, ref := range s.refs[f.As] {
			if cols[ref] || s.isFormula[ref] || seen[ref] {
				continue
			}
			seen[ref] = true
			unresolved = append(unresolved, ref)
		}
	}
	if len(unresolved) == 0 {
		return nil
	}
	sort.Strings(unresolved)
	return fmt.Errorf("sheet: unresolved reference(s): %s — not a column of the input nor a formula in this sheet",
		strings.Join(unresolved, ", "))
}

// columnsOf collects every key present across the input. Rows may be sparse,
// so a key missing from row 0 is still a column of the sheet.
func columnsOf(in []rowops.Row) map[string]bool {
	out := make(map[string]bool)
	for _, row := range in {
		for k := range row {
			out[k] = true
		}
	}
	return out
}
```

Note: `evalColumn` writes into the caller's row maps, consistent with the other in-place pipe operators (`compute` does the same).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sheet/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
make fmt && make lint && go test ./sheet/
git add sheet/sheet.go sheet/sheet_apply_test.go
git commit -m "feat(sheet): evaluate columns and reduces in dependency order"
```

---

## Task 8: Native reduce kernels

**Files:**
- Create: `sheet/reduce.go`
- Test: `sheet/reduce_test.go`
- Modify: `sheet/sheet.go` — `evalReduce` tries a kernel before the compiler

**Interfaces:**
- Consumes: `Column`, `FloatColumn`, `ColumnBuilder`
- Produces: `ReduceFunc` interface (`Name() string`, `Reduce(Column) (any, error)`, `PushdownName() string`); `LookupReduce(name string) (ReduceFunc, bool)`; kernels for `count sum avg min max`; `parseSimpleReduce(expr string, ids []string) (fn, arg string, ok bool)` is **not** part of this task — kernel selection uses the config-level form described below

The engine cannot recognise `sum(profit)` inside an arbitrary expression without parsing it, which is the host's job. So a kernel is selected only when the reduce's source is exactly a function name applied to one free identifier, as reported by the compiler: `len(ids) == 1` and the trimmed source equals one of the registered kernel spellings for that identifier. Task 10 documents the two accepted spellings.

- [ ] **Step 1: Write the failing test**

```go
package sheet

import (
	"math"
	"testing"
)

func floatCol(vals []float64, nullAt ...int) Column {
	nulls := NewBitmap(len(vals))
	for _, i := range nullAt {
		nulls.Set(i)
	}
	return NewFloatColumn(vals, nulls)
}

func TestKernels_matchSQLOnNullsAndEmptyInput(t *testing.T) {
	tests := []struct {
		fn   string
		col  Column
		want any
	}{
		{"sum", floatCol([]float64{1, 2, 3}), 6.0},
		{"sum", floatCol([]float64{1, 0, 3}, 1), 4.0},   // nulls are skipped
		{"sum", floatCol(nil), nil},                      // SQL sum of nothing is NULL
		{"avg", floatCol([]float64{2, 4}), 3.0},
		{"avg", floatCol(nil), nil},
		{"min", floatCol([]float64{5, 2, 9}), 2.0},
		{"max", floatCol([]float64{5, 2, 9}), 9.0},
		{"min", floatCol(nil), nil},
		{"count", floatCol([]float64{1, 0, 3}, 1), int64(2)}, // counts non-nulls
		{"count", floatCol(nil), int64(0)},                   // SQL count of nothing is 0
	}
	for _, tt := range tests {
		t.Run(tt.fn, func(t *testing.T) {
			k, ok := LookupReduce(tt.fn)
			if !ok {
				t.Fatalf("no kernel for %q", tt.fn)
			}
			got, err := k.Reduce(tt.col)
			if err != nil {
				t.Fatalf("Reduce: %v", err)
			}
			if got != tt.want {
				t.Errorf("%s = %v (%T), want %v (%T)", tt.fn, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestKernels_declareTheirPushdownName(t *testing.T) {
	for _, name := range []string{"count", "sum", "avg", "min", "max"} {
		k, _ := LookupReduce(name)
		if k.PushdownName() != name {
			t.Errorf("%s: PushdownName = %q, want %q", name, k.PushdownName(), name)
		}
	}
}

func TestKernels_handleAnAnyBackedColumn(t *testing.T) {
	k, _ := LookupReduce("sum")
	got, err := k.Reduce(NewAnyColumn([]any{1.0, nil, "not a number", 2.0}))
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if got != 3.0 {
		t.Errorf("sum over mixed values = %v, want 3", got)
	}
}

func TestKernels_sumOfInfinitiesStaysInfinite(t *testing.T) {
	k, _ := LookupReduce("sum")
	got, _ := k.Reduce(floatCol([]float64{math.Inf(1), 1}))
	if f, _ := got.(float64); !math.IsInf(f, 1) {
		t.Errorf("got %v, want +Inf", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sheet/ -run TestKernels -v`
Expected: FAIL — `undefined: LookupReduce`

- [ ] **Step 3: Write the implementation**

```go
package sheet

import "fmt"

// ReduceFunc computes a scalar from a whole column.
//
// Kernels must match SQL's aggregate semantics exactly: nulls are skipped, and
// an aggregate over no surviving values is NULL except for count, which is 0.
// A kernel that disagrees would make pushdown a semantic change rather than an
// optimisation.
type ReduceFunc interface {
	Name() string
	Reduce(col Column) (any, error)
	// PushdownName is the SQL aggregate this delegates to, or "" when it
	// cannot be computed by a database.
	PushdownName() string
}

var builtinReduces = map[string]ReduceFunc{}

func registerReduce(f ReduceFunc) {
	if _, dup := builtinReduces[f.Name()]; dup {
		panic(fmt.Sprintf("sheet: reduce %q already registered", f.Name()))
	}
	builtinReduces[f.Name()] = f
}

// LookupReduce finds a built-in kernel by name.
func LookupReduce(name string) (ReduceFunc, bool) {
	f, ok := builtinReduces[name]
	return f, ok
}

func init() {
	registerReduce(kernel{name: "sum", push: "sum", fn: kSum})
	registerReduce(kernel{name: "avg", push: "avg", fn: kAvg})
	registerReduce(kernel{name: "min", push: "min", fn: kMin})
	registerReduce(kernel{name: "max", push: "max", fn: kMax})
	registerReduce(kernel{name: "count", push: "count", fn: kCount})
}

type kernel struct {
	name string
	push string
	fn   func(Column) (any, error)
}

func (k kernel) Name() string                    { return k.name }
func (k kernel) PushdownName() string            { return k.push }
func (k kernel) Reduce(c Column) (any, error)    { return k.fn(c) }

// eachFloat calls visit for every non-null value that is numeric. Non-numeric
// values in an []any-backed column are skipped, matching how a database
// ignores what does not participate in a numeric aggregate.
func eachFloat(c Column, visit func(float64)) {
	if fc, ok := c.(*FloatColumn); ok {
		vals := fc.Floats()
		for i := range vals {
			if fc.IsNull(i) {
				continue
			}
			visit(vals[i])
		}
		return
	}
	for i, n := 0, c.Len(); i < n; i++ {
		if c.IsNull(i) {
			continue
		}
		if f, ok := toFloat(c.At(i)); ok {
			visit(f)
		}
	}
}

func kSum(c Column) (any, error) {
	var acc float64
	var n int
	eachFloat(c, func(f float64) { acc += f; n++ })
	if n == 0 {
		return nil, nil
	}
	return acc, nil
}

func kAvg(c Column) (any, error) {
	var acc float64
	var n int
	eachFloat(c, func(f float64) { acc += f; n++ })
	if n == 0 {
		return nil, nil
	}
	return acc / float64(n), nil
}

func kMin(c Column) (any, error) {
	var best float64
	var n int
	eachFloat(c, func(f float64) {
		if n == 0 || f < best {
			best = f
		}
		n++
	})
	if n == 0 {
		return nil, nil
	}
	return best, nil
}

func kMax(c Column) (any, error) {
	var best float64
	var n int
	eachFloat(c, func(f float64) {
		if n == 0 || f > best {
			best = f
		}
		n++
	})
	if n == 0 {
		return nil, nil
	}
	return best, nil
}

// kCount counts non-null slots, including non-numeric ones — count(*) over a
// text column is a valid question.
func kCount(c Column) (any, error) {
	var n int64
	for i, ln := 0, c.Len(); i < ln; i++ {
		if !c.IsNull(i) {
			n++
		}
	}
	return n, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sheet/ -run TestKernels -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
make fmt && make lint && go test ./sheet/
git add sheet/reduce.go sheet/reduce_test.go
git commit -m "feat(sheet): native reduce kernels matching SQL null and empty-set semantics"
```

---

## Task 9: Route reduces through kernels, with an equivalence suite

**Files:**
- Modify: `sheet/sheet.go` — build a `Column` and try a kernel before falling back to the compiler
- Test: `sheet/reduce_equivalence_test.go`

**Interfaces:**
- Consumes: Tasks 6–8
- Produces: `(*Sheet).kernelFor(f Formula) (ReduceFunc, string, bool)` (unexported), returning the kernel and the single column it reduces

A kernel is used when the compiler reports exactly one free identifier for the reduce and the source text is that identifier applied to a registered kernel name, in either of two spellings: prefix `sum(revenue)` or the fake's postfix `revenue sum`. Anything else — a compound expression, more than one reference — goes to the compiler.

- [ ] **Step 1: Write the failing test**

```go
package sheet

import (
	"context"
	"testing"

	"github.com/xraph/dql/internal/rowops"
)

// Every kernel must agree with the compiler-evaluated form. This is the guard
// that keeps the native path an optimisation rather than a second semantics.
func TestReduce_kernelAgreesWithTheCompiler(t *testing.T) {
	rows := []rowops.Row{
		{"v": 1.0}, {"v": nil}, {"v": 4.0}, {"v": 10.0},
	}
	for _, fn := range []string{"sum", "avg", "min", "max", "count"} {
		t.Run(fn, func(t *testing.T) {
			viaKernel := runReduce(t, "v "+fn, rows, true)
			viaCompiler := runReduce(t, "v "+fn, rows, false)
			if viaKernel != viaCompiler {
				t.Errorf("%s: kernel = %v (%T), compiler = %v (%T)",
					fn, viaKernel, viaKernel, viaCompiler, viaCompiler)
			}
		})
	}
}

func runReduce(t *testing.T, src string, rows []rowops.Row, kernels bool) any {
	t.Helper()
	s, err := Compile(Config{Formulas: []Formula{{As: "out", Reduce: src}}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	s.disableKernels = !kernels
	res, err := s.Apply(context.Background(), rows)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res.Scalars["out"]
}

func TestReduce_compoundExpressionsBypassKernels(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "out", Reduce: "v sum 2 /"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, _, ok := s.kernelFor(s.order[0]); ok {
		t.Fatal("a compound reduce must not match a kernel")
	}
	res, err := s.Apply(context.Background(), []rowops.Row{{"v": 4.0}, {"v": 6.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Scalars["out"] != 5.0 {
		t.Errorf("got %v, want 5", res.Scalars["out"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sheet/ -run TestReduce_ -v`
Expected: FAIL — `s.disableKernels undefined`

- [ ] **Step 3: Write the implementation**

Add the field to `Sheet`:

```go
	// disableKernels forces every reduce through the compiler. Set only by the
	// equivalence tests, which assert the two paths agree.
	disableKernels bool
```

Add kernel selection and rewrite `evalReduce`'s opening:

```go
// kernelFor reports the native kernel for a reduce, when its source is exactly
// one registered aggregate applied to one column.
//
// The match is textual because recognising a call inside an arbitrary
// expression would mean parsing it, which is the host's job. Anything that
// does not match exactly goes to the compiler, so a miss costs performance
// and never correctness.
func (s *Sheet) kernelFor(f Formula) (ReduceFunc, string, bool) {
	if s.disableKernels || f.Kind() != KindReduce {
		return nil, "", false
	}
	refs := s.refs[f.As]
	if len(refs) != 1 {
		return nil, "", false
	}
	col := refs[0]
	src := strings.TrimSpace(f.Reduce)

	for name, k := range builtinReduces {
		if src == name+"("+col+")" || src == col+" "+name {
			return k, col, true
		}
	}
	return nil, "", false
}
```

In `evalReduce`, before binding args:

```go
	if k, colName, ok := s.kernelFor(f); ok {
		col, err := s.columnFor(colName, in, res)
		if err != nil {
			return err
		}
		val, err := k.Reduce(col)
		if err != nil {
			if s.policy == PolicyFail {
				return fmt.Errorf("sheet: reduce %q: %w", f.As, err)
			}
			res.record(f.As, -1, err)
			val = nil
		}
		res.Scalars[f.As] = val
		return nil
	}
```

And the column materialiser:

```go
// columnFor builds a typed Column from the named field of every row. A name
// that is already a computed scalar has no column, which kernelFor's single
// -reference rule does not exclude, so it is reported rather than assumed.
func (s *Sheet) columnFor(name string, in []rowops.Row, res *Result) (Column, error) {
	if _, isScalar := res.Scalars[name]; isScalar {
		return nil, fmt.Errorf("sheet: %q is a scalar, not a column", name)
	}
	b := NewColumnBuilder(len(in))
	for _, row := range in {
		b.Append(row[name])
	}
	return b.Build(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sheet/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
make fmt && make lint && go test ./sheet/
git add sheet/sheet.go sheet/reduce_equivalence_test.go
git commit -m "feat(sheet): use native kernels for simple reduces, proven equal to the compiled path"
```

---

## Task 10: The pipe operator

**Files:**
- Create: `pipe/sheet.go`
- Modify: `pipe/operator.go` — add `ExprCompiler` to `OpContext`
- Modify: `pipe/catalog.go` — add `ReqExprCompiler`, `sheetMeta`, and the `Catalog()` entry
- Modify: `pipe/requirements.go` — describe the new requirement
- Test: `pipe/sheet_test.go`

**Interfaces:**
- Consumes: `sheet.Compile`, `sheet.Config`, `sheet.Formula`, `sheet.ExprCompiler`
- Produces: `SheetConfig{Formulas []sheet.Formula, OnError string}`; op name `"sheet"`; `ReqExprCompiler Requirement = "exprCompiler"`

- [ ] **Step 1: Write the failing test**

```go
package pipe

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/sheet"
)

func TestSheetOp_computesInDependencyOrder(t *testing.T) {
	raw := json.RawMessage(`{"formulas":[
		{"as":"margin","expr":"profit revenue /"},
		{"as":"profit","expr":"revenue cost -"}
	]}`)
	op, err := Build(dsl.PipeStage{Op: "sheet", Config: raw}, &OpContext{ExprCompiler: testCompiler{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"revenue": 100.0, "cost": 60.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0]["profit"] != 40.0 || out[0]["margin"] != 0.4 {
		t.Errorf("got %+v", out[0])
	}
}

func TestSheetOp_reduceLandsInEveryRow(t *testing.T) {
	// A pipe stage returns rows, so a sheet-wide scalar has to reach the
	// caller as a column: every row carries the same value.
	raw := json.RawMessage(`{"formulas":[{"as":"total","reduce":"v sum"}]}`)
	op, err := Build(dsl.PipeStage{Op: "sheet", Config: raw}, &OpContext{ExprCompiler: testCompiler{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"v": 1.0}, {"v": 2.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for i, row := range out {
		if row["total"] != 3.0 {
			t.Errorf("row %d total = %v, want 3", i, row["total"])
		}
	}
}

func TestSheetOp_needsAnExprCompiler(t *testing.T) {
	raw := json.RawMessage(`{"formulas":[{"as":"a","expr":"x"}]}`)
	_, err := Build(dsl.PipeStage{Op: "sheet", Config: raw}, &OpContext{})
	if err == nil || !strings.Contains(err.Error(), "exprCompiler") {
		t.Fatalf("want a requirement error naming exprCompiler, got %v", err)
	}
}

func TestSheetOp_isReportedAsMissingWithoutACompiler(t *testing.T) {
	missing := MissingRequirements(&OpContext{})
	if got, ok := missing["sheet"]; !ok {
		t.Fatal("sheet must be reported unavailable when no compiler is wired")
	} else if len(got) != 1 || got[0] != ReqExprCompiler {
		t.Errorf("sheet requirements = %v", got)
	}
}

func TestSheetOp_isInTheCatalog(t *testing.T) {
	if _, ok := CatalogIndex()["sheet"]; !ok {
		t.Fatal("sheet is registered but missing from Catalog()")
	}
}

func TestSheetOp_isLiveSafe(t *testing.T) {
	raw := json.RawMessage(`{"formulas":[{"as":"a","expr":"x 1 +"}]}`)
	op, err := Build(dsl.PipeStage{Op: "sheet", Config: raw}, &OpContext{ExprCompiler: testCompiler{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !op.IsLiveSafe() {
		t.Error("a sheet is pure; it must be live-safe")
	}
}

// testCompiler adapts the sheet package's test grammar for pipe-level tests.
type testCompiler struct{}

func (testCompiler) FreeIdentifiers(expr string) ([]string, error) {
	return sheet.TestFreeIdentifiers(expr)
}
func (testCompiler) Compile(expr string) (sheet.CompiledExpr, error) {
	return sheet.TestCompile(expr)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipe/ -run TestSheetOp -v`
Expected: FAIL — `unknown pipe op "sheet"`

- [ ] **Step 3: Write the implementation**

First, promote the test grammar so `pipe` can use it. Create `sheet/testgrammar.go` (not a `_test.go` file, so it is importable) holding `fakeCompiler`'s logic, and export two entry points:

```go
package sheet

// TestFreeIdentifiers and TestCompile expose the package's toy expression
// grammar so other packages can exercise the sheet operator without wiring a
// real expression language. Not for production use.
func TestFreeIdentifiers(expr string) ([]string, error) { return newFakeCompiler().FreeIdentifiers(expr) }
func TestCompile(expr string) (CompiledExpr, error)     { return newFakeCompiler().Compile(expr) }
```

Move `fakeCompiler`, `fakeExpr`, `isIdentToken` and `fakeReduce` from `sheet/expr_test.go` into `sheet/testgrammar.go` unchanged; leave `TestFakeCompiler_*` in the test file.

`pipe/operator.go` — add to `OpContext`:

```go
	// ExprCompiler prepares expressions once and reports what they reference.
	// Required by the sheet operator; see sheet.ExprCompiler.
	ExprCompiler sheet.ExprCompiler
```

`pipe/catalog.go` — add the requirement constant next to the others:

```go
	// ReqExprCompiler prepares an expression once and reports its references,
	// which dependency resolution in a sheet depends on.
	ReqExprCompiler Requirement = "exprCompiler"
```

`pipe/requirements.go` — add the case:

```go
	case ReqExprCompiler:
		return octx.ExprCompiler != nil
```

`pipe/sheet.go`:

```go
package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/sheet"
)

// SheetConfig declares a set of named formulas resolved by dependency rather
// than by the order they are written in.
type SheetConfig struct {
	Formulas []sheet.Formula `json:"formulas"`
	OnError  string          `json:"onError,omitempty"`
}

type sheetOp struct {
	s *sheet.Sheet
}

func (o *sheetOp) Name() string     { return "sheet" }
func (o *sheetOp) IsLiveSafe() bool { return true }

func (o *sheetOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	res, err := o.s.Apply(ctx, in)
	if err != nil {
		return nil, err
	}
	// A pipe stage hands on rows, so a sheet-wide scalar reaches the next
	// stage as a column holding the same value in every row.
	for name, val := range res.Scalars {
		for _, row := range res.Rows {
			row[name] = val
		}
	}
	return res.Rows, nil
}

func sheetFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg SheetConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("sheet: decode config: %w", err)
	}
	if octx == nil || octx.ExprCompiler == nil {
		return nil, fmt.Errorf("sheet: requires %s in the OpContext", ReqExprCompiler)
	}
	s, err := sheet.Compile(sheet.Config{Formulas: cfg.Formulas, OnError: cfg.OnError}, octx.ExprCompiler)
	if err != nil {
		return nil, err
	}
	return &sheetOp{s: s}, nil
}

func init() { Register("sheet", sheetFactory) }
```

`pipe/catalog.go` — the metadata, and add `sheetMeta` to `Catalog()` in the "Computation / quality" group after `computeMeta`:

```go
var sheetMeta = OpMetadata{
	Name:            "sheet",
	Requires:        []Requirement{ReqExprCompiler},
	Summary:         "Evaluate a set of formulas in dependency order",
	Description:     "Each formula sets either `expr` (one value per row) or `reduce` (one value for the whole stage). Formulas may reference each other by name and are ordered by what they reference, not by how they are written. A reduce is written into every row.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"formulas"},
		"properties": map[string]any{
			"formulas": map[string]any{
				"title":       "Formulas",
				"description": "Named calculations, resolved by dependency.",
				"type":        "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"as"},
					"properties": map[string]any{
						"as": map[string]any{
							"title":       "Output column name",
							"type":        "string",
							"x-dql-input": InputKindColumnOutput,
						},
						"expr": map[string]any{
							"title":       "Row expression",
							"description": "Evaluated once per row. Identifiers refer to the current row.",
							"type":        "string",
							"x-dql-input": InputKindDTLExpression,
						},
						"reduce": map[string]any{
							"title":       "Reducing expression",
							"description": "Evaluated once over the whole stage. Identifiers refer to entire columns.",
							"type":        "string",
							"x-dql-input": InputKindDTLExpression,
						},
					},
					"additionalProperties": false,
					"x-dql-property-order": []string{"as", "expr", "reduce"},
				},
			},
			"onError": map[string]any{
				"title":       "Error policy",
				"description": "`fail` (default) aborts on the first error. `null` writes null into the failing cell and continues.",
				"type":        "string",
				"enum":        []string{"fail", "null"},
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"formulas", "onError"},
	},
	Examples: []OpExample{{
		Title: "Profit share",
		Config: map[string]any{
			"formulas": []any{
				map[string]any{"as": "profit", "expr": "revenue - cost"},
				map[string]any{"as": "total_profit", "reduce": "sum(profit)"},
				map[string]any{"as": "share", "expr": "profit / total_profit"},
			},
		},
	}},
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... `
Expected: PASS across every package

- [ ] **Step 5: Regenerate the operator reference and commit**

```bash
make fmt && make lint && go test ./...
# OPERATORS.md is generated from the catalog; regenerate so it cannot drift.
make docs 2>/dev/null || go run ./cmd/... 2>/dev/null || true
git add pipe/sheet.go pipe/sheet_test.go pipe/operator.go pipe/catalog.go pipe/requirements.go sheet/testgrammar.go sheet/expr_test.go docs/OPERATORS.md
git commit -m "feat(pipe): add the sheet operator"
```

---

## Self-Review Notes

**Spec coverage for phases 1–3:** semantic model (Tasks 4, 6, 7), column store (1, 2), expression seam (3), dependency extraction and DAG (5, 6), evaluation (7), native kernels (8, 9), error handling (4, 7), operator and catalog (10). Streaming, spill, pushdown, windows and the host registry are explicitly out of scope and appear nowhere.

**Deliberate gap:** the spec's `Result` carries `Errors` and `ErrorCount`, but `rowops.Operator` returns only rows, so the pipe operator currently drops them. Surfacing them needs an executor-level channel that does not yet exist. Task 10 does not invent one; a follow-up plan adds it alongside `/explain` attribution, and until then `onError: null` silently nulls cells. This is recorded rather than hidden.

**Type consistency:** `Column`, `Bitmap`, `ColumnBuilder`, `Formula`, `Kind`, `ErrorPolicy`, `CellError`, `Config`, `Sheet`, `Result`, `ReduceFunc`, `ExprCompiler`, `CompiledExpr` are each defined once and referenced with the same spelling throughout.
