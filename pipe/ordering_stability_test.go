package pipe

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/xraph/dql/dsl"
)

// These tests pin the ordering semantics shared by every operator that sorts
// rows through rowsLess: window, topPerGroup, dedupe, and fillNulls.
//
// They exist so the comparator can be optimised without silently changing
// behaviour. Each one characterises the CURRENT implementation and must keep
// passing afterwards — in particular tie-breaking, which a stable sort gives
// for free and an unstable sort does not.
//
// The rule being pinned: rows comparing equal on every orderBy field keep their
// input order.

func applyOp(t *testing.T, cfg map[string]any, rows []dsl.Row) []dsl.Row {
	t.Helper()
	var stage dsl.PipeStage
	if err := json.Unmarshal(stageJSON(t, cfg), &stage); err != nil {
		t.Fatalf("unmarshal stage: %v", err)
	}
	op, err := Build(stage, &OpContext{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out, err := op.Apply(context.Background(), rows)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return out
}

// The fixture below is shaped deliberately, and two earlier attempts failed to
// detect a genuinely unstable sort:
//
//   - 4 tied rows: below Go's insertion-sort cutoff (~12), where sort.Slice is
//     stable by accident.
//   - 200 rows all tied: less() always returns false, so the input already
//     looks sorted and pdqsort never swaps anything.
//
// What actually has teeth is partial ties that force a full reordering: scores
// ascend in the input while the sort is descending, so every element must move,
// and each score is shared by tieGroup rows whose relative order only a stable
// sort preserves.
const (
	tieCount = 200
	tieGroup = 10 // rows sharing each score
)

func tiedRows() []dsl.Row {
	rows := make([]dsl.Row, tieCount)
	for i := range rows {
		rows[i] = dsl.Row{
			"id":    strconv.Itoa(i),
			"grp":   "g",
			"score": float64(i / tieGroup),
		}
	}
	return rows
}

// wantRank is the row_number a stable descending sort must assign to the row at
// input position i: score groups descend, and within a group input order holds.
func wantRank(i int) int {
	groups := tieCount / tieGroup
	g := i / tieGroup
	return (groups-1-g)*tieGroup + (i % tieGroup) + 1
}

func ids(rows []dsl.Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i], _ = r["id"].(string)
	}
	return out
}

func TestOrdering_windowTiesKeepInputOrder(t *testing.T) {
	rows := tiedRows()
	applyOp(t, map[string]any{
		"op": "window", "fn": "row_number",
		"partitionBy": []string{"grp"},
		"orderBy":     []map[string]any{{"field": "score", "dir": "desc"}},
		"as":          "rn",
	}, rows)

	// Scores descend by group; within a tied group, input order must hold.
	for i := range rows {
		if got := rows[i]["rn"]; got != wantRank(i) {
			t.Fatalf("row %s (input position %d): rn = %v, want %d — "+
				"tie ordering is not stable",
				rows[i]["id"], i, got, wantRank(i))
		}
	}
}

func TestOrdering_windowRanksByValueThenInputOrder(t *testing.T) {
	rows := []dsl.Row{
		{"id": "a", "grp": "g", "score": 1.0},
		{"id": "b", "grp": "g", "score": 3.0},
		{"id": "c", "grp": "g", "score": 3.0},
		{"id": "d", "grp": "g", "score": 2.0},
	}
	applyOp(t, map[string]any{
		"op": "window", "fn": "row_number",
		"partitionBy": []string{"grp"},
		"orderBy":     []map[string]any{{"field": "score", "dir": "desc"}},
		"as":          "rn",
	}, rows)

	// desc by score: b(3) and c(3) tie and keep input order, then d(2), then a(1).
	want := map[string]int{"b": 1, "c": 2, "d": 3, "a": 4}
	for _, r := range rows {
		id := r["id"].(string)
		if r["rn"] != want[id] {
			t.Fatalf("row %s: rn = %v, want %d", id, r["rn"], want[id])
		}
	}
}

func TestOrdering_windowDirIsCaseInsensitive(t *testing.T) {
	mk := func(dir string) []dsl.Row {
		rows := []dsl.Row{
			{"id": "a", "grp": "g", "score": 1.0},
			{"id": "b", "grp": "g", "score": 2.0},
		}
		applyOp(t, map[string]any{
			"op": "window", "fn": "row_number",
			"partitionBy": []string{"grp"},
			"orderBy":     []map[string]any{{"field": "score", "dir": dir}},
			"as":          "rn",
		}, rows)
		return rows
	}
	lower, upper := mk("desc"), mk("DESC")
	for i := range lower {
		if lower[i]["rn"] != upper[i]["rn"] {
			t.Fatalf("dir casing changed the result at %d: %v vs %v",
				i, lower[i]["rn"], upper[i]["rn"])
		}
	}
	// desc puts the higher score first.
	if upper[1]["rn"] != 1 {
		t.Fatalf("DESC: expected higher score to rank 1, got %v", upper[1]["rn"])
	}
}

func TestOrdering_windowEmptyFieldClauseIsSkipped(t *testing.T) {
	rows := []dsl.Row{
		{"id": "a", "grp": "g", "score": 1.0},
		{"id": "b", "grp": "g", "score": 2.0},
	}
	// A clause with no field must be ignored rather than treated as a
	// comparison on the empty key.
	applyOp(t, map[string]any{
		"op": "window", "fn": "row_number",
		"partitionBy": []string{"grp"},
		"orderBy": []map[string]any{
			{"field": "", "dir": "asc"},
			{"field": "score", "dir": "desc"},
		},
		"as": "rn",
	}, rows)
	if rows[1]["rn"] != 1 {
		t.Fatalf("expected score desc to decide, got rn=%v for the higher score", rows[1]["rn"])
	}
}

func TestOrdering_topPerGroupTiesKeepInputOrder(t *testing.T) {
	out := applyOp(t, map[string]any{
		"op": "topPerGroup", "n": 2,
		"by":          []map[string]any{{"field": "score", "dir": "desc"}},
		"partitionBy": []string{"grp"},
	}, tiedRows())

	// Highest score is the last tie group; within it, input order decides.
	first := strconv.Itoa(tieCount - tieGroup)
	second := strconv.Itoa(tieCount - tieGroup + 1)
	got := ids(out)
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("want [%s %s], got %v", first, second, got)
	}
}

func TestOrdering_dedupeTiesKeepInputOrder(t *testing.T) {
	out := applyOp(t, map[string]any{
		"op": "dedupe", "by": []string{"grp"}, "keep": "first",
		"orderBy": []map[string]any{{"field": "score", "dir": "desc"}},
	}, tiedRows())

	// Sorted desc, the winner is the first row of the highest tie group.
	want := strconv.Itoa(tieCount - tieGroup)
	got := ids(out)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("want [%s], got %v", want, got)
	}
}

func TestOrdering_fillNullsCarriesInSortedOrder(t *testing.T) {
	rows := []dsl.Row{
		{"id": "a", "grp": "g", "seq": 2.0, "v": nil},
		{"id": "b", "grp": "g", "seq": 1.0, "v": "filled"},
	}
	applyOp(t, map[string]any{
		"op": "fillNulls", "method": "lastValue",
		"columns":     []string{"v"},
		"partitionBy": []string{"grp"},
		"orderBy":     []map[string]any{{"field": "seq", "dir": "asc"}},
	}, rows)

	// Sorted by seq asc, b precedes a, so a inherits b's value.
	if rows[0]["v"] != "filled" {
		t.Fatalf("expected carry from the earlier row, got %v", rows[0]["v"])
	}
}
