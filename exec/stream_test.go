package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/scope"
	"github.com/xraph/dql/sheet"
)

// --- harness ---

var streamCols = []string{"id", "revenue", "cost"}

type streamRows struct {
	rows []map[string]any
	i    int
}

func (r *streamRows) Close() error               { return nil }
func (r *streamRows) Columns() ([]string, error) { return streamCols, nil }
func (r *streamRows) Err() error                 { return nil }
func (r *streamRows) Next() bool                 { r.i++; return r.i <= len(r.rows) }

func (r *streamRows) Scan(dest ...any) error {
	if len(dest) != len(streamCols) {
		return fmt.Errorf("scan: want %d dest, got %d", len(streamCols), len(dest))
	}
	row := r.rows[r.i-1]
	for i, c := range streamCols {
		p, ok := dest[i].(*any)
		if !ok {
			return fmt.Errorf("scan: dest %d is not *any", i)
		}
		*p = row[c]
	}
	return nil
}

// streamQuerier records the SQL it was asked for, so a test can assert the
// probe row was requested without reaching into the engine.
type streamQuerier struct {
	rows   []map[string]any
	sqls   []string
	params [][]any
}

func (q *streamQuerier) Query(_ context.Context, sqlStr string, args ...any) (SQLRows, error) {
	q.sqls = append(q.sqls, sqlStr)
	q.params = append(q.params, args)
	return &streamRows{rows: q.rows}, nil
}

// limitArg returns the LIMIT bound into query i. The generator emits limits as
// placeholders, so the value is in the parameter list rather than the text.
func (q *streamQuerier) limitArg(t *testing.T, i int) int {
	t.Helper()
	if i >= len(q.sqls) {
		t.Fatalf("no query at index %d", i)
	}
	idx := strings.Index(q.sqls[i], "LIMIT $")
	if idx < 0 {
		t.Fatalf("no LIMIT placeholder in %q", q.sqls[i])
	}
	var n int
	if _, err := fmt.Sscanf(q.sqls[i][idx+len("LIMIT $"):], "%d", &n); err != nil {
		t.Fatalf("parse placeholder in %q: %v", q.sqls[i], err)
	}
	v, ok := q.params[i][n-1].(int)
	if !ok {
		t.Fatalf("LIMIT param is %T, want int", q.params[i][n-1])
	}
	return v
}

type streamSchema struct{}

func (streamSchema) ResolveDataset(_ context.Context, _, _, name string) (*dsl.DatasetInfo, error) {
	return &dsl.DatasetInfo{
		ID: name, Name: name, TableName: "ds_" + name,
		Columns: []dsl.ColumnMeta{
			{Name: "id", Type: "int"},
			{Name: "revenue", Type: "float"},
			{Name: "cost", Type: "float"},
			{Name: "workspace_id", Type: "string"},
		},
	}, nil
}

type nopEval struct{}

func (nopEval) Eval(_ context.Context, expr string, row map[string]any) (any, error) {
	return row[expr], nil
}

// postfixCompiler is the same whitespace-separated toy grammar the pipe tests
// use, so an engine-level test can build a sheet without a real language.
type postfixCompiler struct{}

func (postfixCompiler) FreeIdentifiers(expr string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.Fields(expr) {
		switch tok {
		case "+", "-", "*", "/", "sum", "count", "min", "max", "avg":
			continue
		}
		if _, err := strconv.ParseFloat(tok, 64); err == nil {
			continue
		}
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out, nil
}

func (postfixCompiler) Compile(expr string) (sheet.CompiledExpr, error) {
	toks := strings.Fields(expr)
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	return &postfixExpr{toks: toks}, nil
}

type postfixExpr struct{ toks []string }

func (e *postfixExpr) Eval(_ context.Context, args map[string]any) (any, error) {
	var stack []any
	for _, tok := range e.toks {
		switch tok {
		case "+", "-", "*", "/":
			if len(stack) < 2 {
				return nil, fmt.Errorf("stack underflow")
			}
			b, a := stack[len(stack)-1], stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			af, aok := a.(float64)
			bf, bok := b.(float64)
			if !aok || !bok {
				return nil, fmt.Errorf("non-numeric operand")
			}
			switch tok {
			case "+":
				stack = append(stack, af+bf)
			case "-":
				stack = append(stack, af-bf)
			case "*":
				stack = append(stack, af*bf)
			case "/":
				if bf == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				stack = append(stack, af/bf)
			}
		case "sum", "count", "min", "max", "avg":
			if len(stack) < 1 {
				return nil, fmt.Errorf("stack underflow")
			}
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			vals, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("%s: want a column, got %T", tok, v)
			}
			var acc, lo, hi float64
			var nonNull, numeric int64
			for _, x := range vals {
				if x == nil {
					continue
				}
				nonNull++
				f, ok := x.(float64)
				if !ok {
					continue
				}
				if numeric == 0 {
					lo, hi = f, f
				}
				if f < lo {
					lo = f
				}
				if f > hi {
					hi = f
				}
				acc += f
				numeric++
			}
			switch {
			case tok == "count":
				stack = append(stack, nonNull)
			case numeric == 0:
				stack = append(stack, nil)
			case tok == "sum":
				stack = append(stack, acc)
			case tok == "avg":
				stack = append(stack, acc/float64(numeric))
			case tok == "min":
				stack = append(stack, lo)
			case tok == "max":
				stack = append(stack, hi)
			}
		default:
			if f, err := strconv.ParseFloat(tok, 64); err == nil {
				stack = append(stack, f)
				continue
			}
			stack = append(stack, args[tok])
		}
	}
	if len(stack) != 1 {
		return nil, fmt.Errorf("expression left %d values", len(stack))
	}
	return stack[0], nil
}

func newStreamEngine(t *testing.T, q *streamQuerier, maxRows int) *Engine {
	t.Helper()
	eng := NewEngine(q, streamSchema{}, nopEval{}, EngineConfig{
		PipeMaxRows: maxRows,
		ScopeFor: func(primary, _ string) scope.Scope {
			return scope.Scope{{Name: "workspace_id", Value: primary, Required: true}}
		},
	})
	eng.SetExprCompiler(postfixCompiler{})
	return eng
}

func sheetPipe(t *testing.T, raw string) *dsl.QueryDSL {
	t.Helper()
	var stages []dsl.PipeStage
	if err := json.Unmarshal([]byte(raw), &stages); err != nil {
		t.Fatalf("decode stages: %v", err)
	}
	return &dsl.QueryDSL{Mode: "pipe", From: dsl.FromClause{Dataset: "sales"}, Pipe: stages}
}

func srcRows(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{"id": int64(i), "revenue": float64(10 * (i + 1)), "cost": float64(i + 1)}
	}
	return out
}

// --- tests ---

func TestExecuteStream_computesTheSheetOverTheCursor(t *testing.T) {
	q := &streamQuerier{rows: srcRows(3)}
	eng := newStreamEngine(t, q, 1000)

	got, err := eng.ExecutePipeDetailed(context.Background(), sheetPipe(t, `[
		{"op":"sheet","formulas":[
			{"as":"profit","expr":"revenue cost -"},
			{"as":"total","reduce":"profit sum"}
		]}
	]`), "ws1", "")
	if err != nil {
		t.Fatalf("ExecutePipeDetailed: %v", err)
	}
	if len(got.Result.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(got.Result.Rows))
	}
	// revenue 10,20,30 minus cost 1,2,3
	for i, want := range []float64{9, 18, 27} {
		if got.Result.Rows[i]["profit"] != want {
			t.Errorf("row %d profit = %v, want %v", i, got.Result.Rows[i]["profit"], want)
		}
	}
	if got.Result.Rows[0]["total"] != 54.0 {
		t.Errorf("total = %v, want 54", got.Result.Rows[0]["total"])
	}
}

func TestExecuteStream_asksForOneRowBeyondTheLimit(t *testing.T) {
	// The probe is how a full page is told from a clipped one. Without it the
	// cursor cannot report completeness, which is the point of this path.
	q := &streamQuerier{rows: srcRows(3)}
	eng := newStreamEngine(t, q, 10)

	if _, err := eng.ExecutePipeDetailed(context.Background(), sheetPipe(t, `[
		{"op":"sheet","formulas":[{"as":"n","reduce":"revenue count"}]}
	]`), "ws1", ""); err != nil {
		t.Fatalf("ExecutePipeDetailed: %v", err)
	}
	// The prefix is the first query. A delegated reduce may add a second; this
	// test is only about how the rows themselves were asked for.
	if len(q.sqls) == 0 {
		t.Fatal("no query was issued")
	}
	// PipeMaxRows is 10, so the executor asks for 10 and the cursor probes 11.
	if got := q.limitArg(t, 0); got != 11 {
		t.Errorf("prefix LIMIT = %d, want 11 (the cap plus the probe row)", got)
	}
}

func TestExecuteStream_theProbeRowIsNeverYielded(t *testing.T) {
	// Exactly PipeMaxRows+1 rows available: the caller must see PipeMaxRows of
	// them, and the extra one must only set the truncation flag.
	q := &streamQuerier{rows: srcRows(5)}
	eng := newStreamEngine(t, q, 4)

	got, err := eng.ExecutePipeDetailed(context.Background(), sheetPipe(t, `[
		{"op":"sheet","formulas":[{"as":"n","reduce":"revenue count"}]}
	]`), "ws1", "")
	if err != nil {
		t.Fatalf("ExecutePipeDetailed: %v", err)
	}
	if len(got.Result.Rows) != 4 {
		t.Errorf("got %d rows, want the cap of 4 — the probe row leaked", len(got.Result.Rows))
	}
	if got.Result.Rows[0]["n"] != int64(4) {
		t.Errorf("count = %v, want 4 — the reduce must span only what was yielded", got.Result.Rows[0]["n"])
	}
	if !got.Result.Stats.Truncated {
		t.Error("a clipped result must report Truncated so the reduces are known to be partial")
	}
}

func TestExecuteStream_completeResultIsNotMarkedTruncated(t *testing.T) {
	q := &streamQuerier{rows: srcRows(3)}
	eng := newStreamEngine(t, q, 100)

	got, err := eng.ExecutePipeDetailed(context.Background(), sheetPipe(t, `[
		{"op":"sheet","formulas":[{"as":"n","reduce":"revenue count"}]}
	]`), "ws1", "")
	if err != nil {
		t.Fatalf("ExecutePipeDetailed: %v", err)
	}
	if got.Result.Stats.Truncated {
		t.Error("every matching row was read; this must not report truncation")
	}
}

// The decline path is tested against the engine method directly. Going through
// a pipe would not reach it: PlanPipe builds the prefix from `from`, project
// and parameters alone, so a clause on the outer query never lands on the
// query the cursor would serve.
func TestExecuteClassicStream_declinesWhenTheResultNeedsPostProcessing(t *testing.T) {
	eng := newStreamEngine(t, &streamQuerier{rows: srcRows(3)}, 10)

	for _, tt := range []struct {
		name  string
		query *dsl.QueryDSL
	}{
		{
			name: "expansion rewrites a materialised result",
			query: &dsl.QueryDSL{
				From:   dsl.FromClause{Dataset: "sales"},
				Expand: &dsl.ExpandConfig{},
			},
		},
		{
			name: "an expression filter cannot push",
			query: &dsl.QueryDSL{
				From:  dsl.FromClause{Dataset: "sales"},
				Where: &dsl.WhereClause{Expr: "revenue > cost"},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := eng.executeClassicStream(context.Background(), tt.query, "ws1", "")
			if err != nil {
				t.Fatalf("executeClassicStream: %v", err)
			}
			if got != nil {
				t.Error("expected a nil result declining the query, not a cursor")
			}
		})
	}
}

// The two row-producing paths must agree. Compared here because this is where
// they diverge — above it they are the same code.
func TestExecuteClassicStream_yieldsTheSameRowsAsExecuteClassic(t *testing.T) {
	query := func() *dsl.QueryDSL {
		return &dsl.QueryDSL{From: dsl.FromClause{Dataset: "sales"}}
	}

	materialised, err := newStreamEngine(t, &streamQuerier{rows: srcRows(4)}, 10).
		executeClassic(context.Background(), query(), "ws1", "")
	if err != nil {
		t.Fatalf("executeClassic: %v", err)
	}

	sr, err := newStreamEngine(t, &streamQuerier{rows: srcRows(4)}, 10).
		executeClassicStream(context.Background(), query(), "ws1", "")
	if err != nil {
		t.Fatalf("executeClassicStream: %v", err)
	}
	if sr == nil {
		t.Fatal("expected a cursor for a plain query")
	}
	defer func() { _ = sr.Source.Close() }()

	var streamed []dsl.Row
	for sr.Source.Next() {
		streamed = append(streamed, sr.Source.Row())
	}
	if err := sr.Source.Err(); err != nil {
		t.Fatalf("source: %v", err)
	}

	if len(streamed) != len(materialised.Rows) {
		t.Fatalf("row counts differ: streamed %d, materialised %d", len(streamed), len(materialised.Rows))
	}
	for i := range streamed {
		for _, col := range streamCols {
			if streamed[i][col] != materialised.Rows[i][col] {
				t.Errorf("row %d %s: streamed %v, materialised %v",
					i, col, streamed[i][col], materialised.Rows[i][col])
			}
		}
	}
}

func TestExecuteStream_isNotUsedWhenNoSheetIsPresent(t *testing.T) {
	q := &streamQuerier{rows: srcRows(3)}
	eng := newStreamEngine(t, q, 100)

	if _, err := eng.ExecutePipeDetailed(context.Background(), sheetPipe(t, `[
		{"op":"distinct"}
	]`), "ws1", ""); err != nil {
		t.Fatalf("ExecutePipeDetailed: %v", err)
	}
	// No probe: the materialised path asks for exactly the executor's cap.
	if got := q.limitArg(t, 0); got != 100 {
		t.Errorf("LIMIT = %d, want the cap of 100 — a pipe with no sheet must not probe", got)
	}
}
