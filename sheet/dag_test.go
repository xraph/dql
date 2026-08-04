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
	if len(got) != 3 {
		t.Fatalf("got %d formulas, want 3", len(got))
	}
	pos := map[string]int{}
	for i, f := range got {
		pos[f.As] = i
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

func TestTopoSort_selfReferenceIsACycle(t *testing.T) {
	fs := []Formula{{As: "a", Expr: "a 1 +"}}
	refs := map[string][]string{"a": {"a"}}

	_, err := topoSort(fs, refs)
	if err == nil || !strings.Contains(err.Error(), "a") {
		t.Fatalf("a formula referencing itself is a cycle of length one, got %v", err)
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

func TestTopoSort_handlesADiamond(t *testing.T) {
	fs := []Formula{
		{As: "d", Expr: "b c +"},
		{As: "b", Expr: "a 1 +"},
		{As: "c", Expr: "a 2 +"},
		{As: "a", Expr: "x"},
	}
	refs := map[string][]string{
		"d": {"b", "c"},
		"b": {"a"},
		"c": {"a"},
		"a": {"x"},
	}

	got, err := topoSort(fs, refs)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	pos := map[string]int{}
	for i, f := range got {
		pos[f.As] = i
	}
	if pos["a"] > pos["b"] || pos["a"] > pos["c"] || pos["b"] > pos["d"] || pos["c"] > pos["d"] {
		t.Errorf("diamond violated: %v", pos)
	}
}

func TestTopoSort_duplicateReferenceCountsOnce(t *testing.T) {
	// A compiler that reports the same identifier twice must not inflate the
	// in-degree past what the single edge can decrement.
	fs := []Formula{
		{As: "b", Expr: "a a +"},
		{As: "a", Expr: "x"},
	}
	refs := map[string][]string{"b": {"a", "a"}, "a": {"x"}}

	got, err := topoSort(fs, refs)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if len(got) != 2 || got[0].As != "a" {
		t.Errorf("got %v", got)
	}
}
