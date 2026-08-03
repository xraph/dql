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
			"comparing benchmarks across commits requires byte-identical fixtures")
	}
}

func TestRowsSeeded_differentSeedsDiffer(t *testing.T) {
	a := RowsSeeded(50, 5, 1)
	b := RowsSeeded(50, 5, 2)
	if reflect.DeepEqual(a, b) {
		t.Fatal("different seeds produced identical rows")
	}
}

func TestRows_respectsCardinality(t *testing.T) {
	rows := Rows(500, 7)
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r["assignee"].(string)] = true
	}
	if len(seen) != 7 {
		t.Fatalf("want 7 distinct assignees, got %d", len(seen))
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
