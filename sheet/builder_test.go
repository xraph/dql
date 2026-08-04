package sheet

import (
	"testing"
	"time"
)

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

func TestColumnBuilder_demotionPreservesNulls(t *testing.T) {
	b := NewColumnBuilder(4)
	b.Append(1.0)
	b.Append(nil)
	b.Append("three") // demotes; the null before it must survive
	b.Append(nil)
	col := b.Build()

	if col.Len() != 4 {
		t.Fatalf("Len = %d, want 4", col.Len())
	}
	if col.At(0) != 1.0 || !col.IsNull(1) || col.At(2) != "three" || !col.IsNull(3) {
		t.Errorf("got %v %v %v %v", col.At(0), col.At(1), col.At(2), col.At(3))
	}
}

func TestColumnBuilder_leadingNullsKeepIndicesAligned(t *testing.T) {
	// A backing is not chosen until the first non-null value, so the slots
	// before it have to be backfilled or every index shifts.
	b := NewColumnBuilder(3)
	b.Append(nil)
	b.Append(nil)
	b.Append(7.0)
	col := b.Build()

	if col.Len() != 3 {
		t.Fatalf("Len = %d, want 3", col.Len())
	}
	if !col.IsNull(0) || !col.IsNull(1) {
		t.Error("leading nulls lost")
	}
	if col.At(2) != 7.0 {
		t.Errorf("At(2) = %v, want 7 — the value shifted", col.At(2))
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

func TestColumnBuilder_stringsAndBoolsAndTimesGetTheirOwnBacking(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		val  any
		want any
	}{
		{"string", "x", &StringColumn{}},
		{"bool", true, &BoolColumn{}},
		{"time", now, &TimeColumn{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewColumnBuilder(1)
			b.Append(tt.val)
			col := b.Build()
			if gotT, wantT := typeName(col), typeName(tt.want); gotT != wantT {
				t.Errorf("got %s, want %s", gotT, wantT)
			}
			if col.At(0) != tt.val {
				t.Errorf("At(0) = %v, want %v", col.At(0), tt.val)
			}
		})
	}
}

func TestColumnBuilder_growsPastItsInitialCapacity(t *testing.T) {
	b := NewColumnBuilder(1)
	for i := 0; i < 200; i++ {
		if i%3 == 0 {
			b.Append(nil)
			continue
		}
		b.Append(float64(i))
	}
	col := b.Build()

	if col.Len() != 200 {
		t.Fatalf("Len = %d, want 200", col.Len())
	}
	for i := 0; i < 200; i++ {
		if i%3 == 0 {
			if !col.IsNull(i) {
				t.Fatalf("index %d should be null", i)
			}
			continue
		}
		if col.At(i) != float64(i) {
			t.Fatalf("index %d = %v, want %d", i, col.At(i), i)
		}
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *FloatColumn:
		return "float"
	case *StringColumn:
		return "string"
	case *BoolColumn:
		return "bool"
	case *TimeColumn:
		return "time"
	case *AnyColumn:
		return "any"
	}
	return "unknown"
}
