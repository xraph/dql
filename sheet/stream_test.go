package sheet

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xraph/dql/internal/rowops"
)

// countingSource yields rows one at a time and can claim truncation, so the
// completeness signal can be tested independently of any database.
type countingSource struct {
	rows      []rowops.Row
	i         int
	truncated bool
	err       error
	closed    int
}

func newCountingSource(rows []rowops.Row) *countingSource {
	return &countingSource{rows: rows, i: -1}
}

func (s *countingSource) Next() bool {
	s.i++
	return s.i < len(s.rows)
}

func (s *countingSource) Row() rowops.Row {
	if s.i < 0 || s.i >= len(s.rows) {
		return nil
	}
	return s.rows[s.i]
}

func (s *countingSource) Err() error      { return s.err }
func (s *countingSource) Truncated() bool { return s.truncated }
func (s *countingSource) Close() error    { s.closed++; return nil }

func TestSliceSource_iteratesEveryRowAndIsNeverTruncated(t *testing.T) {
	src := SliceSource([]rowops.Row{{"a": 1}, {"a": 2}})
	var seen int
	for src.Next() {
		seen++
	}
	if seen != 2 {
		t.Errorf("saw %d rows, want 2", seen)
	}
	if src.Truncated() {
		t.Error("a slice is by definition everything the caller had")
	}
	if err := src.Err(); err != nil {
		t.Errorf("Err = %v", err)
	}
}

func TestApplyStream_matchesApplyOnTheSameRows(t *testing.T) {
	// The whole point: reading through a cursor changes where rows come from,
	// never what the sheet computes.
	rows := []rowops.Row{
		{"revenue": 100.0, "cost": 60.0},
		{"revenue": 200.0, "cost": 140.0},
	}
	formulas := []Formula{
		{As: "profit", Expr: "revenue cost -"},
		{As: "total", Reduce: "profit sum"},
	}

	direct, err := compileFor(t, formulas).Apply(context.Background(), cloneRows(rows))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	streamed, err := compileFor(t, formulas).ApplyStream(
		context.Background(), newCountingSource(cloneRows(rows)), 0)
	if err != nil {
		t.Fatalf("ApplyStream: %v", err)
	}

	if direct.Scalars["total"] != streamed.Scalars["total"] {
		t.Errorf("total: direct %v, streamed %v", direct.Scalars["total"], streamed.Scalars["total"])
	}
	for i := range direct.Rows {
		if direct.Rows[i]["profit"] != streamed.Rows[i]["profit"] {
			t.Errorf("row %d profit: direct %v, streamed %v",
				i, direct.Rows[i]["profit"], streamed.Rows[i]["profit"])
		}
	}
}

func TestApplyStream_reportsCompleteWhenTheSourceIsExhausted(t *testing.T) {
	res, err := compileFor(t, []Formula{{As: "n", Reduce: "v count"}}).ApplyStream(
		context.Background(), newCountingSource([]rowops.Row{{"v": 1.0}, {"v": 2.0}}), 0)
	if err != nil {
		t.Fatalf("ApplyStream: %v", err)
	}
	if !res.Complete {
		t.Error("a drained source that ran out is complete")
	}
}

func TestApplyStream_reportsIncompleteWhenCapped(t *testing.T) {
	res, err := compileFor(t, []Formula{{As: "n", Reduce: "v count"}}).ApplyStream(
		context.Background(),
		newCountingSource([]rowops.Row{{"v": 1.0}, {"v": 2.0}, {"v": 3.0}}),
		2,
	)
	if err != nil {
		t.Fatalf("ApplyStream: %v", err)
	}
	if res.Complete {
		t.Error("stopping at a cap is not complete — an aggregate must not be delegated on it")
	}
	if res.Scalars["n"] != int64(2) {
		t.Errorf("count = %v, want 2 — the reduce must span only what was read", res.Scalars["n"])
	}
}

func TestApplyStream_reportsIncompleteWhenTheSourceSaysSo(t *testing.T) {
	src := newCountingSource([]rowops.Row{{"v": 1.0}})
	src.truncated = true

	res, err := compileFor(t, []Formula{{As: "n", Reduce: "v count"}}).ApplyStream(
		context.Background(), src, 0)
	if err != nil {
		t.Fatalf("ApplyStream: %v", err)
	}
	if res.Complete {
		t.Error("a source that clipped its own result is not complete")
	}
}

func TestApplyStream_closesTheSource(t *testing.T) {
	src := newCountingSource([]rowops.Row{{"v": 1.0}})
	if _, err := compileFor(t, []Formula{{As: "n", Reduce: "v count"}}).ApplyStream(
		context.Background(), src, 0); err != nil {
		t.Fatalf("ApplyStream: %v", err)
	}
	if src.closed != 1 {
		t.Errorf("Close called %d times, want 1", src.closed)
	}
}

func TestApplyStream_closesTheSourceOnAReadError(t *testing.T) {
	src := newCountingSource([]rowops.Row{{"v": 1.0}})
	src.err = errors.New("connection reset")

	_, err := compileFor(t, []Formula{{As: "n", Reduce: "v count"}}).ApplyStream(
		context.Background(), src, 0)
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("want the source error surfaced, got %v", err)
	}
	if src.closed != 1 {
		t.Errorf("Close called %d times on the error path, want 1", src.closed)
	}
}

func TestApplyStream_rejectsANilSource(t *testing.T) {
	if _, err := compileFor(t, []Formula{{As: "n", Reduce: "v count"}}).ApplyStream(
		context.Background(), nil, 0); err == nil {
		t.Fatal("a nil source must be an error, not an empty sheet")
	}
}

func TestApply_isIncompleteWhenProvenanceIsUnknown(t *testing.T) {
	// A caller that materialised rows itself has not established that they are
	// every matching row. Silence must not read as yes.
	res, err := compileFor(t, []Formula{{As: "n", Reduce: "v count"}}).Apply(
		context.Background(), []rowops.Row{{"v": 1.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Complete {
		t.Error("unknown provenance must not report complete")
	}
}

func TestApply_honoursAnExplicitCompletenessSignal(t *testing.T) {
	ctx := WithSourceComplete(context.Background(), true)
	res, err := compileFor(t, []Formula{{As: "n", Reduce: "v count"}}).Apply(
		ctx, []rowops.Row{{"v": 1.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Complete {
		t.Error("an explicit signal must be honoured")
	}
}

func compileFor(t *testing.T, formulas []Formula) *Sheet {
	t.Helper()
	s, err := Compile(Config{Formulas: formulas}, newFakeCompiler())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return s
}

func cloneRows(in []rowops.Row) []rowops.Row {
	out := make([]rowops.Row, len(in))
	for i, r := range in {
		c := make(rowops.Row, len(r))
		for k, v := range r {
			c[k] = v
		}
		out[i] = c
	}
	return out
}
