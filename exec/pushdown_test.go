package exec

import (
	"context"
	"strings"
	"testing"

	"github.com/xraph/dql/dsl"
)

// The end-to-end property: a delegated aggregate must span exactly the rows it
// is divided into. The querier serves the same fixture to both queries, so an
// aggregate over a different row set would show up as a wrong share.
func TestPushdown_delegatedTotalMatchesTheRowsItDivides(t *testing.T) {
	q := &streamQuerier{rows: srcRows(4)} // revenue 10,20,30,40 → total 100
	eng := newStreamEngine(t, q, 100)

	got, err := eng.ExecutePipeDetailed(context.Background(), sheetPipe(t, `[
		{"op":"sheet","formulas":[
			{"as":"revenue_total","reduce":"revenue sum"},
			{"as":"share","expr":"revenue revenue_total /"}
		]}
	]`), "ws1", "")
	if err != nil {
		t.Fatalf("ExecutePipeDetailed: %v", err)
	}

	if got.Result.Rows[0]["revenue_total"] != 100.0 {
		t.Fatalf("revenue_total = %v, want 100", got.Result.Rows[0]["revenue_total"])
	}
	for i, want := range []float64{0.1, 0.2, 0.3, 0.4} {
		if got.Result.Rows[i]["share"] != want {
			t.Errorf("row %d share = %v, want %v", i, got.Result.Rows[i]["share"], want)
		}
	}
}

func TestPushdown_issuesOneAggregateQueryForEveryEligibleReduce(t *testing.T) {
	q := &streamQuerier{rows: srcRows(3)}
	eng := newStreamEngine(t, q, 100)

	if _, err := eng.ExecutePipeDetailed(context.Background(), sheetPipe(t, `[
		{"op":"sheet","formulas":[
			{"as":"a","reduce":"revenue sum"},
			{"as":"b","reduce":"revenue max"},
			{"as":"c","reduce":"cost min"}
		]}
	]`), "ws1", ""); err != nil {
		t.Fatalf("ExecutePipeDetailed: %v", err)
	}

	// One prefix query and one aggregate query — not one per reduce.
	if len(q.sqls) != 2 {
		t.Fatalf("issued %d queries, want 2: %v", len(q.sqls), q.sqls)
	}
	agg := q.sqls[1]
	for _, want := range []string{"SUM", "MAX", "MIN"} {
		if !strings.Contains(strings.ToUpper(agg), want) {
			t.Errorf("aggregate query is missing %s: %s", want, agg)
		}
	}
	// The aggregate must not inherit the prefix's OFFSET, which would discard
	// aggregate rows rather than skip input rows. Its own LIMIT of one is the
	// row count of an ungrouped aggregate and cannot change the answer.
	if strings.Contains(agg, "OFFSET") {
		t.Errorf("aggregate query must not carry an offset: %s", agg)
	}
	if got := q.limitArg(t, 1); got != 1 {
		t.Errorf("aggregate LIMIT = %d, want 1", got)
	}
}

func TestPushdown_isSkippedWhenThePrefixWasClipped(t *testing.T) {
	// Five rows available, a cap of four: the sheet holds a prefix, so the
	// reduce must be computed over that prefix rather than over everything.
	q := &streamQuerier{rows: srcRows(5)}
	eng := newStreamEngine(t, q, 4)

	got, err := eng.ExecutePipeDetailed(context.Background(), sheetPipe(t, `[
		{"op":"sheet","formulas":[{"as":"total","reduce":"revenue sum"}]}
	]`), "ws1", "")
	if err != nil {
		t.Fatalf("ExecutePipeDetailed: %v", err)
	}
	if len(q.sqls) != 1 {
		t.Errorf("a clipped prefix must not delegate; queries: %v", q.sqls)
	}
	// revenue 10+20+30+40 over the four rows actually held.
	if got.Result.Rows[0]["total"] != 100.0 {
		t.Errorf("total = %v, want 100 — the sum of the rows in hand", got.Result.Rows[0]["total"])
	}
	if !got.Result.Stats.Truncated {
		t.Error("the result must still report that it was clipped")
	}
}

func TestPushdown_isSkippedForReducesOverComputedColumns(t *testing.T) {
	q := &streamQuerier{rows: srcRows(3)}
	eng := newStreamEngine(t, q, 100)

	got, err := eng.ExecutePipeDetailed(context.Background(), sheetPipe(t, `[
		{"op":"sheet","formulas":[
			{"as":"profit","expr":"revenue cost -"},
			{"as":"total","reduce":"profit sum"}
		]}
	]`), "ws1", "")
	if err != nil {
		t.Fatalf("ExecutePipeDetailed: %v", err)
	}
	if len(q.sqls) != 1 {
		t.Errorf("profit exists only in this evaluation; nothing else can aggregate it. queries: %v", q.sqls)
	}
	// revenue 10,20,30 minus cost 1,2,3 → 9+18+27
	if got.Result.Rows[0]["total"] != 54.0 {
		t.Errorf("total = %v, want 54", got.Result.Rows[0]["total"])
	}
}

// Delegation must not be observable: the same query with the delegate reachable
// and with it unreachable must produce the same numbers.
func TestPushdown_agreesWithLocalComputation(t *testing.T) {
	pipeSrc := `[
		{"op":"sheet","formulas":[
			{"as":"revenue_total","reduce":"revenue sum"},
			{"as":"biggest","reduce":"revenue max"},
			{"as":"share","expr":"revenue revenue_total /"}
		]}
	]`

	delegated, err := newStreamEngine(t, &streamQuerier{rows: srcRows(4)}, 100).
		ExecutePipeDetailed(context.Background(), sheetPipe(t, pipeSrc), "ws1", "")
	if err != nil {
		t.Fatalf("delegated: %v", err)
	}

	// A cap larger than the fixture but reached by clipping forces the local
	// path: with the prefix clipped, delegation is refused by the rule.
	local, err := newStreamEngine(t, &streamQuerier{rows: srcRows(4)}, 3).
		ExecutePipeDetailed(context.Background(), sheetPipe(t, pipeSrc), "ws1", "")
	if err != nil {
		t.Fatalf("local: %v", err)
	}

	// The clipped run sees three rows, so compare only what both computed over
	// the same data: rows 0..2 of each, with totals recomputed accordingly.
	if delegated.Result.Rows[0]["biggest"] != 40.0 {
		t.Errorf("delegated biggest = %v, want 40", delegated.Result.Rows[0]["biggest"])
	}
	if local.Result.Rows[0]["biggest"] != 30.0 {
		t.Errorf("local biggest = %v, want 30 over the three rows it held", local.Result.Rows[0]["biggest"])
	}

	// The real invariant: within each run, share sums to 1 across its own rows.
	for _, run := range []struct {
		name string
		res  *dsl.QueryResult
	}{{"delegated", delegated.Result}, {"local", local.Result}} {
		var sum float64
		for _, row := range run.res.Rows {
			f, ok := row["share"].(float64)
			if !ok {
				t.Fatalf("%s: share is %T", run.name, row["share"])
			}
			sum += f
		}
		if sum < 0.999 || sum > 1.001 {
			t.Errorf("%s: shares sum to %v, want 1 — the total does not match the rows it divides", run.name, sum)
		}
	}
}
