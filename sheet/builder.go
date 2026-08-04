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

// minBitmapGrowth is the smallest bitmap a growing builder allocates. Below
// one word there is nothing to gain from being precise.
const minBitmapGrowth = 64

// ColumnBuilder accumulates values into the narrowest backing that fits.
//
// It starts unset, adopts a backing from the first non-null value, and demotes
// to []any on the first value that does not fit. Demotion is one-way: a column
// that has seen mixed types stays boxed, because the alternative — re-narrowing
// once the outlier passes — would mean rewriting the slice on every flip.
type ColumnBuilder struct {
	kind     backingKind
	n        int
	capacity int
	nulls    *Bitmap

	floats  []float64
	strings []string
	bools   []bool
	times   []time.Time
	anys    []any
}

// NewColumnBuilder returns a builder pre-sized for capacity values. Appending
// beyond it is allowed; the builder grows.
func NewColumnBuilder(capacity int) *ColumnBuilder {
	if capacity < 0 {
		capacity = 0
	}
	return &ColumnBuilder{capacity: capacity, nulls: NewBitmap(capacity)}
}

// alloc returns a slice of pad zero values with room for the full expected
// column. The typed backing is not chosen until the first non-null value, so
// this is the first point at which the caller's capacity hint can be spent —
// without it a 100k-row column reallocates its way up through ~17 doublings.
func (b *ColumnBuilder) allocLen(pad int) int {
	if b.capacity > pad {
		return b.capacity
	}
	return pad + 1
}

// Append adds one value. A nil value is recorded as null.
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
		if s, ok := v.(string); ok {
			b.strings = append(b.strings, s)
		} else {
			b.demoteAndAppend(v)
		}
	case b.kind == backingBool:
		if x, ok := v.(bool); ok {
			b.bools = append(b.bools, x)
		} else {
			b.demoteAndAppend(v)
		}
	case b.kind == backingTime:
		if x, ok := v.(time.Time); ok {
			b.times = append(b.times, x)
		} else {
			b.demoteAndAppend(v)
		}
	case b.kind == backingAny:
		b.anys = append(b.anys, v)
	default:
		b.demoteAndAppend(v)
	}
	b.n++
}

// growNulls keeps the bitmap large enough for index b.n. Callers may append
// past the capacity passed to NewColumnBuilder.
func (b *ColumnBuilder) growNulls() {
	if b.n < b.nulls.n {
		return
	}
	size := b.n*2 + 1
	if size < minBitmapGrowth {
		size = minBitmapGrowth
	}
	next := NewBitmap(size)
	copy(next.words, b.nulls.words)
	b.nulls = next
}

// adopt selects a backing from the first non-null value seen, allocating it at
// full expected capacity with the leading nulls already padded in — so index i
// in the typed slice is row i, and no reallocation follows for a column that
// stays within its hint.
func (b *ColumnBuilder) adopt(v any, f float64, isNum bool) {
	pad, size := b.n, b.allocLen(b.n)

	if isNum {
		b.kind = backingFloat
		b.floats = append(make([]float64, pad, size), f)
		return
	}
	switch x := v.(type) {
	case string:
		b.kind = backingString
		b.strings = append(make([]string, pad, size), x)
	case bool:
		b.kind = backingBool
		b.bools = append(make([]bool, pad, size), x)
	case time.Time:
		b.kind = backingTime
		b.times = append(make([]time.Time, pad, size), x)
	default:
		b.kind = backingAny
		b.anys = append(make([]any, pad, size), v)
	}
}

// appendZero reserves the current slot in whichever backing is active. A null
// still occupies an index so the typed slice stays aligned with row numbers.
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
	case backingUnset:
		// No backing yet; backfill will pad for this slot when one is chosen.
	}
}

func (b *ColumnBuilder) demoteAndAppend(v any) {
	b.demote()
	b.anys = append(b.anys, v)
}

// demote converts whatever has accumulated into []any, preserving nulls.
func (b *ColumnBuilder) demote() {
	if b.kind == backingAny {
		return
	}
	out := make([]any, 0, b.allocLen(b.n))
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
		case backingUnset, backingAny:
			out = append(out, nil)
		}
	}
	b.kind = backingAny
	b.anys = out
	b.floats, b.strings, b.bools, b.times = nil, nil, nil, nil
}

// Build finalises the column. The builder must not be used afterwards.
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
	case backingUnset:
		// Nothing but nulls: no backing was ever chosen.
		return NewAnyColumn(make([]any, b.n))
	}
	return NewAnyColumn(make([]any, b.n))
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
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}
