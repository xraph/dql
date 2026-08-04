package pipe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xraph/dql/dsl"
)

// A sheet needs no window functions of its own: the window operator computes
// them into an ordinary column, and a sheet reads that column like any other.
//
// These tests exist to hold that claim up. Without them the composition is
// only an assertion in a design document, and the pressure to add lag() and
// rank() inside a sheet — a third implementation of semantics that already
// exist twice — comes back every time someone wants a running delta.

func windowThenSheet(t *testing.T, stages string, rows []dsl.Row) []dsl.Row {
	t.Helper()
	var parsed []dsl.PipeStage
	if err := json.Unmarshal([]byte(stages), &parsed); err != nil {
		t.Fatalf("decode stages: %v", err)
	}
	q := &dsl.QueryDSL{Mode: "pipe", From: dsl.FromClause{Dataset: "events"}, Pipe: parsed}

	e := NewExecutor(plainClassic{rows: rows}, &OpContext{ExprCompiler: testCompiler{}},
		ExecutorConfig{MaxRows: 1000})
	got, err := e.ExecuteDetailed(context.Background(), q, "ws", "proj")
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	return got.Result.Rows
}

func TestSheetComposesWithWindow_periodOverPeriodDelta(t *testing.T) {
	// The canonical spreadsheet formula, `revenue - LAG(revenue)`, expressed
	// as the two operators that already exist.
	rows := []dsl.Row{
		{"ts": 1.0, "revenue": 100.0},
		{"ts": 2.0, "revenue": 150.0},
		{"ts": 3.0, "revenue": 120.0},
	}
	out := windowThenSheet(t, `[
		{"op":"window","fn":"lag","field":"revenue","orderBy":[{"field":"ts"}],"as":"prev","default":0},
		{"op":"sheet","formulas":[{"as":"delta","expr":"revenue prev -"}]}
	]`, rows)

	for i, want := range []float64{100, 50, -30} {
		if out[i]["delta"] != want {
			t.Errorf("row %d delta = %v, want %v", i, out[i]["delta"], want)
		}
	}
}

func TestSheetComposesWithWindow_rankFeedsAReduce(t *testing.T) {
	// A window column is an ordinary column, so a sheet may reduce over it.
	rows := []dsl.Row{
		{"score": 30.0},
		{"score": 10.0},
		{"score": 20.0},
	}
	out := windowThenSheet(t, `[
		{"op":"window","fn":"rank","orderBy":[{"field":"score"}],"as":"position"},
		{"op":"sheet","formulas":[
			{"as":"worst","reduce":"position max"},
			{"as":"from_bottom","expr":"worst position -"}
		]}
	]`, rows)

	// Ascending rank: 10 is 1st, 20 is 2nd, 30 is 3rd.
	if out[0]["position"] != 3 || out[1]["position"] != 1 || out[2]["position"] != 2 {
		t.Fatalf("ranks = %v %v %v", out[0]["position"], out[1]["position"], out[2]["position"])
	}
	if out[0]["worst"] != 3.0 {
		t.Errorf("worst = %v, want 3", out[0]["worst"])
	}
	for i, want := range []float64{0, 2, 1} {
		if out[i]["from_bottom"] != want {
			t.Errorf("row %d from_bottom = %v, want %v", i, out[i]["from_bottom"], want)
		}
	}
}

func TestSheetComposesWithWindow_partitionedWindowFeedsASheet(t *testing.T) {
	// Partitioning is the thing an inline lag() inside a sheet could not
	// express without reinventing partitionBy, which is the clearest argument
	// against putting one there.
	rows := []dsl.Row{
		{"host": "a", "ts": 1.0, "v": 10.0},
		{"host": "a", "ts": 2.0, "v": 15.0},
		{"host": "b", "ts": 1.0, "v": 100.0},
		{"host": "b", "ts": 2.0, "v": 130.0},
	}
	out := windowThenSheet(t, `[
		{"op":"window","fn":"lag","field":"v","partitionBy":["host"],"orderBy":[{"field":"ts"}],"as":"prev","default":0},
		{"op":"sheet","formulas":[{"as":"growth","expr":"v prev -"}]}
	]`, rows)

	// Each host's first row has no predecessor within its own partition.
	for i, want := range []float64{10, 5, 100, 30} {
		if out[i]["growth"] != want {
			t.Errorf("row %d growth = %v, want %v", i, out[i]["growth"], want)
		}
	}
}

func TestSheetComposesWithWindow_sheetOutputFeedsAWindow(t *testing.T) {
	// The other direction: a sheet's computed column is an ordinary column, so
	// a window may rank by it.
	rows := []dsl.Row{
		{"revenue": 100.0, "cost": 90.0},
		{"revenue": 100.0, "cost": 10.0},
		{"revenue": 100.0, "cost": 50.0},
	}
	out := windowThenSheet(t, `[
		{"op":"sheet","formulas":[{"as":"profit","expr":"revenue cost -"}]},
		{"op":"window","fn":"rank","orderBy":[{"field":"profit"}],"as":"rank_by_profit"}
	]`, rows)

	// profits 10, 90, 50 → ascending ranks 1, 3, 2.
	for i, want := range []int{1, 3, 2} {
		if out[i]["rank_by_profit"] != want {
			t.Errorf("row %d rank = %v, want %v", i, out[i]["rank_by_profit"], want)
		}
	}
}
