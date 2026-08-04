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

// NewBitmap returns a bitmap sized for n bits, all clear.
func NewBitmap(n int) *Bitmap {
	if n < 0 {
		n = 0
	}
	return &Bitmap{words: make([]uint64, (n+63)/64), n: n}
}

// Set marks bit i. Out-of-range indices are ignored rather than panicking:
// a builder may probe past its initial capacity while growing.
func (b *Bitmap) Set(i int) {
	if i < 0 || i >= b.n {
		return
	}
	b.words[i/64] |= 1 << uint(i%64)
}

// Get reports whether bit i is set. A nil bitmap reads as all-clear, so a
// column built with no nulls need not allocate one.
func (b *Bitmap) Get(i int) bool {
	if b == nil || i < 0 || i >= b.n {
		return false
	}
	return b.words[i/64]&(1<<uint(i%64)) != 0
}

// Column is one column of a sheet. At returns a boxed value because the
// expression language is dynamically typed; the typed accessors on the
// concrete types below exist so reduce kernels can avoid that boxing.
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

// NewFloatColumn wraps vals; nulls may be nil when there are none.
func NewFloatColumn(vals []float64, nulls *Bitmap) *FloatColumn {
	return &FloatColumn{vals: vals, nulls: nulls}
}

func (c *FloatColumn) Len() int          { return len(c.vals) }
func (c *FloatColumn) IsNull(i int) bool { return c.nulls.Get(i) }

// Floats returns the backing slice. Callers must not retain or mutate it.
func (c *FloatColumn) Floats() []float64 { return c.vals }

func (c *FloatColumn) At(i int) any {
	if c.nulls.Get(i) {
		return nil
	}
	return c.vals[i]
}

// StringColumn is the text backing.
type StringColumn struct {
	vals  []string
	nulls *Bitmap
}

// NewStringColumn wraps vals; nulls may be nil when there are none.
func NewStringColumn(vals []string, nulls *Bitmap) *StringColumn {
	return &StringColumn{vals: vals, nulls: nulls}
}

func (c *StringColumn) Len() int          { return len(c.vals) }
func (c *StringColumn) IsNull(i int) bool { return c.nulls.Get(i) }

// Strings returns the backing slice. Callers must not retain or mutate it.
func (c *StringColumn) Strings() []string { return c.vals }

func (c *StringColumn) At(i int) any {
	if c.nulls.Get(i) {
		return nil
	}
	return c.vals[i]
}

// BoolColumn is the boolean backing.
type BoolColumn struct {
	vals  []bool
	nulls *Bitmap
}

// NewBoolColumn wraps vals; nulls may be nil when there are none.
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

// TimeColumn is the timestamp backing.
type TimeColumn struct {
	vals  []time.Time
	nulls *Bitmap
}

// NewTimeColumn wraps vals; nulls may be nil when there are none.
func NewTimeColumn(vals []time.Time, nulls *Bitmap) *TimeColumn {
	return &TimeColumn{vals: vals, nulls: nulls}
}

func (c *TimeColumn) Len() int          { return len(c.vals) }
func (c *TimeColumn) IsNull(i int) bool { return c.nulls.Get(i) }

// Times returns the backing slice. Callers must not retain or mutate it.
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

// NewAnyColumn wraps vals.
func NewAnyColumn(vals []any) *AnyColumn { return &AnyColumn{vals: vals} }

func (c *AnyColumn) Len() int          { return len(c.vals) }
func (c *AnyColumn) At(i int) any      { return c.vals[i] }
func (c *AnyColumn) IsNull(i int) bool { return c.vals[i] == nil }
