package pipe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xraph/dql/dsl"
)

// streamingClassic is a ClassicExecutor that also serves a cursor, so the
// executor's streaming branch can be exercised without a database.
type streamingClassic struct {
	rows      []dsl.Row
	truncated bool
	// decline makes ExecuteStream return a nil result, standing in for a host
	// that can stream in general but not for this particular query.
	decline bool

	streamCalls int
	plainCalls  int
}

func (c *streamingClassic) Execute(_ context.Context, _ *dsl.QueryDSL, _, _ string) (*dsl.QueryResult, error) {
	c.plainCalls++
	return &dsl.QueryResult{Rows: cloneForTest(c.rows)}, nil
}

func (c *streamingClassic) ExecuteStream(_ context.Context, _ *dsl.QueryDSL, _, _ string) (*StreamResult, error) {
	c.streamCalls++
	if c.decline {
		return nil, nil
	}
	src := &testSource{rows: cloneForTest(c.rows), i: -1, truncated: c.truncated}
	return &StreamResult{Source: src}, nil
}

type testSource struct {
	rows      []dsl.Row
	i         int
	truncated bool
}

func (s *testSource) Next() bool      { s.i++; return s.i < len(s.rows) }
func (s *testSource) Row() dsl.Row    { return s.rows[s.i] }
func (s *testSource) Err() error      { return nil }
func (s *testSource) Close() error    { return nil }
func (s *testSource) Truncated() bool { return s.truncated }

func cloneForTest(in []dsl.Row) []dsl.Row {
	out := make([]dsl.Row, len(in))
	for i, r := range in {
		c := make(dsl.Row, len(r))
		for k, v := range r {
			c[k] = v
		}
		out[i] = c
	}
	return out
}

func sheetQuery() *dsl.QueryDSL {
	return &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "sales"},
		Pipe: []dsl.PipeStage{{
			Op: "sheet",
			Config: json.RawMessage(`{"formulas":[
				{"as":"profit","expr":"revenue cost -"},
				{"as":"total","reduce":"profit sum"}
			]}`),
		}},
	}
}

func newSheetExecutor(classic ClassicExecutor) *Executor {
	return NewExecutor(classic, &OpContext{ExprCompiler: testCompiler{}}, ExecutorConfig{MaxRows: 1000})
}

// The property the design rests on: where rows come from must not change what
// the sheet computes.
func TestStreaming_producesTheSameResultAsMaterialising(t *testing.T) {
	rows := []dsl.Row{
		{"revenue": 100.0, "cost": 60.0},
		{"revenue": 200.0, "cost": 140.0},
		{"revenue": 50.0, "cost": 20.0},
	}

	streamed := &streamingClassic{rows: rows}
	gotStream, err := newSheetExecutor(streamed).ExecuteDetailed(context.Background(), sheetQuery(), "ws", "proj")
	if err != nil {
		t.Fatalf("streamed: %v", err)
	}

	// plainClassic implements only ClassicExecutor, so the streaming branch is
	// skipped entirely rather than declined.
	gotPlain, err := newSheetExecutor(plainClassic{rows: rows}).ExecuteDetailed(context.Background(), sheetQuery(), "ws", "proj")
	if err != nil {
		t.Fatalf("materialised: %v", err)
	}

	if streamed.streamCalls != 1 {
		t.Errorf("expected the streaming path to be taken, stream calls = %d", streamed.streamCalls)
	}
	if len(gotStream.Result.Rows) != len(gotPlain.Result.Rows) {
		t.Fatalf("row counts differ: %d vs %d", len(gotStream.Result.Rows), len(gotPlain.Result.Rows))
	}
	for i := range gotStream.Result.Rows {
		for _, col := range []string{"profit", "total"} {
			if gotStream.Result.Rows[i][col] != gotPlain.Result.Rows[i][col] {
				t.Errorf("row %d %s: streamed %v, materialised %v",
					i, col, gotStream.Result.Rows[i][col], gotPlain.Result.Rows[i][col])
			}
		}
	}
}

type plainClassic struct{ rows []dsl.Row }

func (c plainClassic) Execute(_ context.Context, _ *dsl.QueryDSL, _, _ string) (*dsl.QueryResult, error) {
	return &dsl.QueryResult{Rows: cloneForTest(c.rows)}, nil
}

func TestStreaming_isSkippedWhenNoStageWantsIt(t *testing.T) {
	// A pipe with no sheet gains nothing from the signal, so the cursor should
	// not be opened at all.
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "sales"},
		Pipe: []dsl.PipeStage{{Op: "distinct", Config: json.RawMessage(`{}`)}},
	}
	classic := &streamingClassic{rows: []dsl.Row{{"a": 1.0}}}
	if _, err := newSheetExecutor(classic).ExecuteDetailed(context.Background(), q, "ws", "proj"); err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if classic.streamCalls != 0 {
		t.Errorf("no stage wanted the signal, but the cursor was opened %d times", classic.streamCalls)
	}
	if classic.plainCalls != 1 {
		t.Errorf("plain calls = %d, want 1", classic.plainCalls)
	}
}

func TestStreaming_fallsBackWhenTheHostDeclines(t *testing.T) {
	classic := &streamingClassic{rows: []dsl.Row{{"revenue": 10.0, "cost": 4.0}}, decline: true}
	got, err := newSheetExecutor(classic).ExecuteDetailed(context.Background(), sheetQuery(), "ws", "proj")
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if classic.streamCalls != 1 || classic.plainCalls != 1 {
		t.Errorf("a declined stream must fall back: stream=%d plain=%d", classic.streamCalls, classic.plainCalls)
	}
	if got.Result.Rows[0]["profit"] != 6.0 {
		t.Errorf("profit = %v, want 6", got.Result.Rows[0]["profit"])
	}
}

func TestStreaming_marksStatsTruncatedWhenTheSourceClipped(t *testing.T) {
	classic := &streamingClassic{rows: []dsl.Row{{"revenue": 10.0, "cost": 4.0}}, truncated: true}
	got, err := newSheetExecutor(classic).ExecuteDetailed(context.Background(), sheetQuery(), "ws", "proj")
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !got.Result.Stats.Truncated {
		t.Error("a clipped source must surface as Truncated so a caller can see the reduces are partial")
	}
}

func TestStreaming_capsAtMaxRowsAndReportsTruncation(t *testing.T) {
	rows := make([]dsl.Row, 10)
	for i := range rows {
		rows[i] = dsl.Row{"revenue": float64(i), "cost": 0.0}
	}
	e := NewExecutor(&streamingClassic{rows: rows}, &OpContext{ExprCompiler: testCompiler{}},
		ExecutorConfig{MaxRows: 4})

	got, err := e.ExecuteDetailed(context.Background(), sheetQuery(), "ws", "proj")
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if len(got.Result.Rows) != 4 {
		t.Errorf("read %d rows, want the cap of 4", len(got.Result.Rows))
	}
	if !got.Result.Stats.Truncated {
		t.Error("hitting the cap must surface as Truncated")
	}
}

// Live replay resumes from PrimedAt with PrimedRows. The streaming path must
// not shift either, or a replay would skip the sheet or double-apply it.
func TestStreaming_leavesLiveReplayPrimingUnchanged(t *testing.T) {
	rows := []dsl.Row{{"revenue": 10.0, "cost": 4.0}}

	streamed, err := newSheetExecutor(&streamingClassic{rows: rows}).
		ExecuteDetailed(context.Background(), sheetQuery(), "ws", "proj")
	if err != nil {
		t.Fatalf("streamed: %v", err)
	}
	plain, err := newSheetExecutor(plainClassic{rows: rows}).
		ExecuteDetailed(context.Background(), sheetQuery(), "ws", "proj")
	if err != nil {
		t.Fatalf("materialised: %v", err)
	}

	if streamed.PrimedAt != plain.PrimedAt {
		t.Errorf("PrimedAt differs: streamed %d, materialised %d", streamed.PrimedAt, plain.PrimedAt)
	}
	if len(streamed.PrimedRows) != len(plain.PrimedRows) {
		t.Errorf("PrimedRows differ: streamed %d rows, materialised %d",
			len(streamed.PrimedRows), len(plain.PrimedRows))
	}
	// The snapshot must be the sheet's input, not its output — otherwise a
	// replay from stage 0 would run the sheet over rows it already computed.
	if _, computed := streamed.PrimedRows[0]["profit"]; computed {
		t.Error("PrimedRows must be the tail's input, before any operator ran")
	}
}
