package sheet

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xraph/dql/internal/rowops"
)

type recordingDelegate struct {
	answers map[string]any
	err     error
	calls   int
	got     []AggRequest
}

func (d *recordingDelegate) Delegate(_ context.Context, reqs []AggRequest) (map[string]any, error) {
	d.calls++
	d.got = append(d.got, reqs...)
	if d.err != nil {
		return nil, d.err
	}
	return d.answers, nil
}

func completeCtx() context.Context { return WithSourceComplete(context.Background(), true) }

func TestDelegate_isUsedWhenTheInputIsComplete(t *testing.T) {
	s := compileFor(t, []Formula{{As: "total", Reduce: "revenue sum"}})
	// A wrong answer on purpose: if it appears, the delegate was consulted.
	d := &recordingDelegate{answers: map[string]any{"total": 999.0}}
	s.SetReduceDelegate(d)

	res, err := s.Apply(completeCtx(), []rowops.Row{{"revenue": 1.0}, {"revenue": 2.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if d.calls != 1 {
		t.Fatalf("delegate called %d times, want 1", d.calls)
	}
	if len(d.got) != 1 || d.got[0].Fn != "sum" || d.got[0].Column != "revenue" || d.got[0].As != "total" {
		t.Errorf("request = %+v", d.got)
	}
	if res.Scalars["total"] != 999.0 {
		t.Errorf("total = %v, want the delegated 999", res.Scalars["total"])
	}
}

func TestDelegate_isNotUsedWhenTheInputIsIncomplete(t *testing.T) {
	// The rule the whole design turns on. An aggregate computed over the
	// source spans every matching row; the rows in hand are a prefix. Mixing
	// them would divide a total the caller cannot see by rows that are not it.
	s := compileFor(t, []Formula{{As: "total", Reduce: "revenue sum"}})
	d := &recordingDelegate{answers: map[string]any{"total": 999.0}}
	s.SetReduceDelegate(d)

	res, err := s.Apply(WithSourceComplete(context.Background(), false),
		[]rowops.Row{{"revenue": 1.0}, {"revenue": 2.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if d.calls != 0 {
		t.Errorf("delegate consulted on an incomplete input")
	}
	if res.Scalars["total"] != 3.0 {
		t.Errorf("total = %v, want the locally computed 3", res.Scalars["total"])
	}
}

func TestDelegate_isNotUsedWhenProvenanceIsUnknown(t *testing.T) {
	s := compileFor(t, []Formula{{As: "total", Reduce: "revenue sum"}})
	d := &recordingDelegate{answers: map[string]any{"total": 999.0}}
	s.SetReduceDelegate(d)

	if _, err := s.Apply(context.Background(), []rowops.Row{{"revenue": 1.0}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if d.calls != 0 {
		t.Error("silence about provenance must not license delegation")
	}
}

func TestDelegate_skipsReducesOverComputedColumns(t *testing.T) {
	// profit does not exist outside this evaluation, so nothing else can
	// aggregate it.
	s := compileFor(t, []Formula{
		{As: "profit", Expr: "revenue cost -"},
		{As: "byCol", Reduce: "revenue sum"},
		{As: "byFormula", Reduce: "profit sum"},
	})
	d := &recordingDelegate{answers: map[string]any{"byCol": 100.0, "byFormula": 999.0}}
	s.SetReduceDelegate(d)

	res, err := s.Apply(completeCtx(), []rowops.Row{{"revenue": 10.0, "cost": 4.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(d.got) != 1 || d.got[0].As != "byCol" {
		t.Errorf("requested %+v, want only the source-column reduce", d.got)
	}
	if res.Scalars["byFormula"] != 6.0 {
		t.Errorf("byFormula = %v, want the locally computed 6", res.Scalars["byFormula"])
	}
}

func TestDelegate_fallsBackWhenItFails(t *testing.T) {
	// Delegation is an optimisation and may never be the difference between a
	// right answer and no answer.
	s := compileFor(t, []Formula{{As: "total", Reduce: "revenue sum"}})
	s.SetReduceDelegate(&recordingDelegate{err: errors.New("connection reset")})

	res, err := s.Apply(completeCtx(), []rowops.Row{{"revenue": 1.0}, {"revenue": 2.0}})
	if err != nil {
		t.Fatalf("a failing delegate must not fail the sheet: %v", err)
	}
	if res.Scalars["total"] != 3.0 {
		t.Errorf("total = %v, want the locally computed 3", res.Scalars["total"])
	}
}

func TestDelegate_fallsBackForRequestsItDoesNotAnswer(t *testing.T) {
	s := compileFor(t, []Formula{
		{As: "a", Reduce: "revenue sum"},
		{As: "b", Reduce: "cost sum"},
	})
	s.SetReduceDelegate(&recordingDelegate{answers: map[string]any{"a": 100.0}})

	res, err := s.Apply(completeCtx(), []rowops.Row{{"revenue": 1.0, "cost": 7.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Scalars["a"] != 100.0 {
		t.Errorf("a = %v, want the delegated 100", res.Scalars["a"])
	}
	if res.Scalars["b"] != 7.0 {
		t.Errorf("b = %v, want the locally computed 7", res.Scalars["b"])
	}
}

func TestDelegate_rejectsANonScalarAnswer(t *testing.T) {
	s := compileFor(t, []Formula{{As: "total", Reduce: "revenue sum"}})
	s.SetReduceDelegate(&recordingDelegate{answers: map[string]any{"total": []any{1, 2}}})

	_, err := s.Apply(completeCtx(), []rowops.Row{{"revenue": 1.0}})
	if err == nil || !strings.Contains(err.Error(), "scalar") {
		t.Fatalf("want a rejection naming the shape, got %v", err)
	}
}

// Delegation must not be observable. Same sheet, same rows, delegate returning
// exactly what a correct source would — identical output either way.
func TestDelegate_isNotObservableWhenItAnswersCorrectly(t *testing.T) {
	rows := []rowops.Row{
		{"revenue": 10.0, "cost": 4.0},
		{"revenue": 20.0, "cost": 6.0},
		{"revenue": 30.0, "cost": 10.0},
	}
	formulas := []Formula{
		{As: "revenue_total", Reduce: "revenue sum"},
		{As: "share", Expr: "revenue revenue_total /"},
	}

	local, err := compileFor(t, formulas).Apply(completeCtx(), cloneRows(rows))
	if err != nil {
		t.Fatalf("local: %v", err)
	}

	s := compileFor(t, formulas)
	s.SetReduceDelegate(&recordingDelegate{answers: map[string]any{"revenue_total": 60.0}})
	delegated, err := s.Apply(completeCtx(), cloneRows(rows))
	if err != nil {
		t.Fatalf("delegated: %v", err)
	}

	if local.Scalars["revenue_total"] != delegated.Scalars["revenue_total"] {
		t.Errorf("total: local %v, delegated %v",
			local.Scalars["revenue_total"], delegated.Scalars["revenue_total"])
	}
	for i := range local.Rows {
		if local.Rows[i]["share"] != delegated.Rows[i]["share"] {
			t.Errorf("row %d share: local %v, delegated %v",
				i, local.Rows[i]["share"], delegated.Rows[i]["share"])
		}
	}
}

func TestDelegate_asksOnceForEveryEligibleReduce(t *testing.T) {
	s := compileFor(t, []Formula{
		{As: "a", Reduce: "revenue sum"},
		{As: "b", Reduce: "revenue max"},
		{As: "c", Reduce: "cost min"},
	})
	d := &recordingDelegate{answers: map[string]any{}}
	s.SetReduceDelegate(d)

	if _, err := s.Apply(completeCtx(), []rowops.Row{{"revenue": 1.0, "cost": 2.0}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if d.calls != 1 {
		t.Errorf("delegate called %d times, want 1 round trip for all three", d.calls)
	}
	if len(d.got) != 3 {
		t.Errorf("requested %d aggregates, want 3", len(d.got))
	}
}
