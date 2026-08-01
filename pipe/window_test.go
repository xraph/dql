package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func runWindow(t *testing.T, cfg map[string]any, in []dsl.Row) []dsl.Row {
	t.Helper()
	cfg["op"] = "window"
	op, err := windowFactory(stageJSON(t, cfg), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, err := op.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return out
}

func TestWindow_rowNumber_perPartition(t *testing.T) {
	in := []dsl.Row{
		{"host": "a", "ts": 1.0},
		{"host": "b", "ts": 1.0},
		{"host": "a", "ts": 2.0},
		{"host": "a", "ts": 3.0},
	}
	out := runWindow(t, map[string]any{
		"fn":          "row_number",
		"partitionBy": []string{"host"},
		"orderBy":     []map[string]any{{"field": "ts", "dir": "asc"}},
		"as":          "rn",
	}, in)
	// Output preserves input order.
	if out[0]["rn"] != 1 || out[1]["rn"] != 1 || out[2]["rn"] != 2 || out[3]["rn"] != 3 {
		t.Fatalf("row_number wrong: %+v", out)
	}
}

func TestWindow_rank_handlesTies(t *testing.T) {
	in := []dsl.Row{
		{"score": 90.0},
		{"score": 80.0},
		{"score": 80.0},
		{"score": 70.0},
	}
	out := runWindow(t, map[string]any{
		"fn":      "rank",
		"orderBy": []map[string]any{{"field": "score", "dir": "desc"}},
		"as":      "rk",
	}, in)
	// 90 → 1; both 80 → 2; 70 → 4 (gap after tie).
	got := []int{out[0]["rk"].(int), out[1]["rk"].(int), out[2]["rk"].(int), out[3]["rk"].(int)}
	if got[0] != 1 || got[1] != 2 || got[2] != 2 || got[3] != 4 {
		t.Fatalf("rank wrong: %+v", got)
	}
}

func TestWindow_denseRank_noGaps(t *testing.T) {
	in := []dsl.Row{
		{"score": 90.0},
		{"score": 80.0},
		{"score": 80.0},
		{"score": 70.0},
	}
	out := runWindow(t, map[string]any{
		"fn":      "dense_rank",
		"orderBy": []map[string]any{{"field": "score", "dir": "desc"}},
		"as":      "dr",
	}, in)
	got := []int{out[0]["dr"].(int), out[1]["dr"].(int), out[2]["dr"].(int), out[3]["dr"].(int)}
	if got[0] != 1 || got[1] != 2 || got[2] != 2 || got[3] != 3 {
		t.Fatalf("dense_rank wrong: %+v", got)
	}
}

func TestWindow_lag(t *testing.T) {
	in := []dsl.Row{
		{"ts": 1.0, "v": 10.0},
		{"ts": 2.0, "v": 20.0},
		{"ts": 3.0, "v": 30.0},
	}
	// JSON round-trips numeric literals as float64, so use a string default
	// here to keep the test free of float/int interface-equality traps.
	out := runWindow(t, map[string]any{
		"fn":      "lag",
		"field":   "v",
		"offset":  1,
		"orderBy": []map[string]any{{"field": "ts", "dir": "asc"}},
		"as":      "prev",
		"default": "MISSING",
	}, in)
	if out[0]["prev"] != "MISSING" {
		t.Fatalf("first row lag should be default: %+v", out[0])
	}
	if out[1]["prev"] != 10.0 {
		t.Fatalf("row1 lag: %+v", out[1])
	}
	if out[2]["prev"] != 20.0 {
		t.Fatalf("row2 lag: %+v", out[2])
	}
}

func TestWindow_lead(t *testing.T) {
	in := []dsl.Row{
		{"ts": 1.0, "v": 10.0},
		{"ts": 2.0, "v": 20.0},
		{"ts": 3.0, "v": 30.0},
	}
	out := runWindow(t, map[string]any{
		"fn":      "lead",
		"field":   "v",
		"orderBy": []map[string]any{{"field": "ts", "dir": "asc"}},
		"as":      "next",
	}, in)
	if out[0]["next"] != 20.0 {
		t.Fatalf("row0 lead: %+v", out[0])
	}
	if out[2]["next"] != nil {
		t.Fatalf("last row lead should be nil default: %+v", out[2])
	}
}

func TestWindow_firstValue_lastValue(t *testing.T) {
	in := []dsl.Row{
		{"part": "a", "ts": 1.0, "v": 100.0},
		{"part": "a", "ts": 2.0, "v": 200.0},
		{"part": "b", "ts": 1.0, "v": 50.0},
	}
	out := runWindow(t, map[string]any{
		"fn":          "first_value",
		"field":       "v",
		"partitionBy": []string{"part"},
		"orderBy":     []map[string]any{{"field": "ts", "dir": "asc"}},
		"as":          "fv",
	}, in)
	if out[0]["fv"] != 100.0 || out[1]["fv"] != 100.0 || out[2]["fv"] != 50.0 {
		t.Fatalf("first_value wrong: %+v", out)
	}
	out = runWindow(t, map[string]any{
		"fn":          "last_value",
		"field":       "v",
		"partitionBy": []string{"part"},
		"orderBy":     []map[string]any{{"field": "ts", "dir": "asc"}},
		"as":          "lv",
	}, in)
	if out[0]["lv"] != 200.0 || out[1]["lv"] != 200.0 || out[2]["lv"] != 50.0 {
		t.Fatalf("last_value wrong: %+v", out)
	}
}

func TestWindow_factory_validation(t *testing.T) {
	cases := []map[string]any{
		{"fn": "row_number", "as": "rn"},                    // missing orderBy
		{"fn": "lag", "as": "p", "orderBy": []any{}},        // missing field + orderBy
		{"fn": "row_number", "orderBy": []any{}},            // missing as
		{"as": "x"},                                         // missing fn
		{"fn": "frobnicate", "as": "x", "orderBy": []any{}}, // unknown fn
	}
	for i, c := range cases {
		c["op"] = "window"
		_, err := windowFactory(stageJSON(t, c), nil)
		if err == nil {
			t.Fatalf("case %d: expected error for %+v", i, c)
		}
	}
}

func TestWindow_isLiveSafe(t *testing.T) {
	op, err := windowFactory(stageJSON(t, map[string]any{
		"op":      "window",
		"fn":      "row_number",
		"orderBy": []map[string]any{{"field": "ts"}},
		"as":      "rn",
	}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if !op.IsLiveSafe() {
		t.Fatalf("window should be live-safe")
	}
}
