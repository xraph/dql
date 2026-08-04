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

func TestBitmap_nilReadsAsAllClear(t *testing.T) {
	var b *Bitmap
	if b.Get(0) || b.Get(1000) {
		t.Error("a nil bitmap must read as all-clear so a null-free column need not allocate one")
	}
}

func TestBitmap_outOfRangeIsIgnored(t *testing.T) {
	b := NewBitmap(4)
	b.Set(-1)
	b.Set(99)
	if b.Get(-1) || b.Get(99) {
		t.Error("out-of-range bits must not read back as set")
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

func TestAnyColumn_nilEntryIsNull(t *testing.T) {
	c := NewAnyColumn([]any{1.0, nil, "x"})
	if c.IsNull(0) || !c.IsNull(1) || c.IsNull(2) {
		t.Errorf("null detection wrong: %v %v %v", c.IsNull(0), c.IsNull(1), c.IsNull(2))
	}
}
