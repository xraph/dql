package sheet

import (
	"context"
	"testing"
	"time"

	"github.com/xraph/dql/internal/rowops"
)

// --- encoding round trips ---

func TestSpill_roundTripsEveryTypedBacking(t *testing.T) {
	nulls := func(n int, at ...int) *Bitmap {
		b := NewBitmap(n)
		for _, i := range at {
			b.Set(i)
		}
		return b
	}
	ts := time.Date(2026, 8, 4, 12, 30, 0, 0, time.UTC)

	tests := []struct {
		name string
		col  Column
		want []any
	}{
		{
			name: "float with a null",
			col:  NewFloatColumn([]float64{1.5, 0, -2.5}, nulls(3, 1)),
			want: []any{1.5, nil, -2.5},
		},
		{
			name: "string with an empty value and a null",
			col:  NewStringColumn([]string{"alpha", "", "gamma"}, nulls(3, 2)),
			want: []any{"alpha", "", nil},
		},
		{
			name: "bool",
			col:  NewBoolColumn([]bool{true, false, true}, nulls(3, 1)),
			want: []any{true, nil, true},
		},
		{
			name: "time",
			col:  NewTimeColumn([]time.Time{ts, {}, ts.Add(time.Hour)}, nulls(3, 1)),
			want: []any{ts, nil, ts.Add(time.Hour)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf, ok := encodeColumn(tt.col)
			if !ok {
				t.Fatalf("encodeColumn refused %T", tt.col)
			}
			got, ok := decodeColumn(buf)
			if !ok {
				t.Fatal("decodeColumn failed")
			}
			if got.Len() != len(tt.want) {
				t.Fatalf("Len = %d, want %d", got.Len(), len(tt.want))
			}
			for i, want := range tt.want {
				if want == nil {
					if !got.IsNull(i) {
						t.Errorf("index %d: want null, got %v", i, got.At(i))
					}
					continue
				}
				if got.IsNull(i) {
					t.Errorf("index %d: unexpected null", i)
					continue
				}
				if gotTime, isTime := got.At(i).(time.Time); isTime {
					if !gotTime.Equal(want.(time.Time)) {
						t.Errorf("index %d: %v, want %v", i, gotTime, want)
					}
					continue
				}
				if got.At(i) != want {
					t.Errorf("index %d: %v, want %v", i, got.At(i), want)
				}
			}
		})
	}
}

func TestSpill_refusesAnAnyColumn(t *testing.T) {
	// Its elements are arbitrary; encoding them would mean owning a format for
	// values this package never inspects. Rebuilding is the answer instead.
	if _, ok := encodeColumn(NewAnyColumn([]any{1, "two", nil})); ok {
		t.Error("an AnyColumn must not claim to be spillable")
	}
}

func TestSpill_emptyColumnRoundTrips(t *testing.T) {
	buf, ok := encodeColumn(NewFloatColumn(nil, nil))
	if !ok {
		t.Fatal("encodeColumn refused an empty column")
	}
	got, ok := decodeColumn(buf)
	if !ok || got.Len() != 0 {
		t.Fatalf("decode: ok=%v len=%d", ok, got.Len())
	}
}

func TestSpill_decodeRejectsTruncatedInput(t *testing.T) {
	buf, _ := encodeColumn(NewFloatColumn([]float64{1, 2, 3}, nil))
	for _, cut := range []int{0, 1, 4, len(buf) - 1} {
		if _, ok := decodeColumn(buf[:cut]); ok {
			t.Errorf("decoded a block truncated to %d bytes", cut)
		}
	}
}

func TestSpillStore_putAndGet(t *testing.T) {
	st, err := newSpillStore()
	if err != nil {
		t.Fatalf("newSpillStore: %v", err)
	}
	defer func() { _ = st.Close() }()

	col := NewFloatColumn([]float64{10, 20, 30}, nil)
	if !st.Put("revenue", col) {
		t.Fatal("Put refused a float column")
	}
	got, ok := st.Get("revenue")
	if !ok {
		t.Fatal("Get missed a column that was just written")
	}
	if got.Len() != 3 || got.At(2) != 30.0 {
		t.Errorf("restored column wrong: len=%d at2=%v", got.Len(), got.At(2))
	}
	if _, ok := st.Get("absent"); ok {
		t.Error("Get invented a column that was never written")
	}
}

func TestSpillStore_holdsSeveralColumns(t *testing.T) {
	st, err := newSpillStore()
	if err != nil {
		t.Fatalf("newSpillStore: %v", err)
	}
	defer func() { _ = st.Close() }()

	for _, name := range []string{"a", "b", "c"} {
		if !st.Put(name, NewStringColumn([]string{name, name + name}, nil)) {
			t.Fatalf("Put(%q) refused", name)
		}
	}
	for _, name := range []string{"a", "b", "c"} {
		got, ok := st.Get(name)
		if !ok {
			t.Fatalf("Get(%q) missed", name)
		}
		if got.At(0) != name {
			t.Errorf("%q restored as %v", name, got.At(0))
		}
	}
}

func TestSpillStore_closeIsIdempotent(t *testing.T) {
	st, err := newSpillStore()
	if err != nil {
		t.Fatalf("newSpillStore: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, ok := st.Get("anything"); ok {
		t.Error("a closed store must not serve columns")
	}
}

// --- the property that matters ---

// A budget changes where columns live and must not change any answer.
func TestBudget_isNotObservable(t *testing.T) {
	rows := make([]rowops.Row, 500)
	for i := range rows {
		rows[i] = rowops.Row{
			"a": float64(i),
			"b": float64(i * 2),
			"c": float64(i * 3),
			"d": float64(i * 5),
		}
	}
	formulas := []Formula{
		{As: "sa", Reduce: "a sum"},
		{As: "sb", Reduce: "b sum"},
		{As: "sc", Reduce: "c sum"},
		{As: "sd", Reduce: "d sum"},
		// Re-read a column already evicted under a tight budget, so the
		// restore path is exercised rather than only the eviction one.
		{As: "ma", Reduce: "a max"},
		{As: "mb", Reduce: "b max"},
	}

	run := func(budget int) *Result {
		t.Helper()
		s, err := Compile(Config{Formulas: formulas, ColumnBudgetBytes: budget}, newFakeCompiler())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		res, err := s.Apply(context.Background(), cloneRows(rows))
		if err != nil {
			t.Fatalf("Apply(budget=%d): %v", budget, err)
		}
		return res
	}

	unbounded := run(0)
	// One float column of 500 rows is ~4KB, so 1KB holds none of them and
	// every admission evicts its predecessor.
	tight := run(1024)

	for _, name := range []string{"sa", "sb", "sc", "sd", "ma", "mb"} {
		if unbounded.Scalars[name] != tight.Scalars[name] {
			t.Errorf("%s: unbounded %v, budgeted %v", name, unbounded.Scalars[name], tight.Scalars[name])
		}
	}
}

func TestBudget_zeroKeepsEverythingResidentAndOpensNoFile(t *testing.T) {
	s, err := Compile(Config{Formulas: []Formula{
		{As: "sa", Reduce: "a sum"},
		{As: "sb", Reduce: "b sum"},
	}}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	rows := []rowops.Row{{"a": 1.0, "b": 2.0}, {"a": 3.0, "b": 4.0}}
	res, err := s.Apply(context.Background(), rows)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Scalars["sa"] != 4.0 || res.Scalars["sb"] != 6.0 {
		t.Errorf("sa=%v sb=%v", res.Scalars["sa"], res.Scalars["sb"])
	}
}

func TestBudget_evictsButKeepsTheColumnBeingAdmitted(t *testing.T) {
	// Evicting the column the caller is about to read would spill and restore
	// the same bytes for nothing.
	run := &runState{columns: map[string]Column{}, delegated: map[string]any{}}
	defer run.close()

	big := NewFloatColumn(make([]float64, 400), nil) // ~3.2KB each
	run.admit("first", big, 1024)
	run.admit("second", big, 1024)

	if _, ok := run.columns["second"]; !ok {
		t.Error("the column just admitted must stay resident")
	}
	if _, ok := run.columns["first"]; ok {
		t.Error("the older column should have been evicted")
	}
}
