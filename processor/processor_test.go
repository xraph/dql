package processor

import (
	"context"
	"fmt"
	"testing"

	"github.com/xraph/dql/dsl"
)

// --- Mock ExprEvaluator ---

type mockExprEvaluator struct {
	results map[string]any
	err     error
}

func (m *mockExprEvaluator) Eval(_ context.Context, expr string, row map[string]any) (any, error) {
	if m.err != nil {
		return nil, m.err
	}
	if v, ok := m.results[expr]; ok {
		return v, nil
	}
	// Default: return nil
	return nil, nil
}

// --- Helper ---

func makeRows(data ...map[string]any) []dsl.Row {
	rows := make([]dsl.Row, len(data))
	copy(rows, data)
	return rows
}

// --- Processor Tests ---

func TestProcessor_Passthrough(t *testing.T) {
	proc := NewProcessor(nil)
	rows := makeRows(
		map[string]any{"id": "1", "value": 10.0},
		map[string]any{"id": "2", "value": 20.0},
	)
	plan := &dsl.QueryPlan{Columns: []dsl.ColumnInfo{{Name: "id"}, {Name: "value"}}}
	q := &dsl.QueryDSL{}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Errorf("rows: got %d, want 2", len(result.Rows))
	}
}

func TestProcessor_ComputedColumns(t *testing.T) {
	eval := &mockExprEvaluator{results: map[string]any{"value * 2": 20.0}}
	proc := NewProcessor(eval)
	rows := makeRows(map[string]any{"value": 10.0})
	plan := &dsl.QueryPlan{
		InMemory: []string{"computed_columns"},
		Columns:  []dsl.ColumnInfo{{Name: "value"}, {Name: "doubled", Source: "computed"}},
	}
	q := &dsl.QueryDSL{
		Computed: []dsl.ComputedColumn{{Name: "doubled", Expr: "value * 2"}},
	}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Rows[0]["doubled"] != 20.0 {
		t.Errorf("doubled: got %v, want 20.0", result.Rows[0]["doubled"])
	}
}

func TestProcessor_ComputedColumns_NilEvaluator(t *testing.T) {
	proc := NewProcessor(nil)
	rows := makeRows(map[string]any{"value": 10.0})
	plan := &dsl.QueryPlan{InMemory: []string{"computed_columns"}}
	q := &dsl.QueryDSL{
		Computed: []dsl.ComputedColumn{{Name: "doubled", Expr: "value * 2"}},
	}

	_, err := proc.Process(context.Background(), plan, q, rows)
	if err == nil {
		t.Fatal("expected error for nil evaluator with computed columns")
	}
}

func TestProcessor_InMemoryAggregation_NoGroupBy(t *testing.T) {
	proc := NewProcessor(nil)
	rows := makeRows(
		map[string]any{"value": 10.0},
		map[string]any{"value": 20.0},
		map[string]any{"value": 30.0},
	)
	plan := &dsl.QueryPlan{
		InMemory: []string{"aggregate"},
		Columns:  []dsl.ColumnInfo{{Name: "total", Source: "aggregate"}},
	}
	q := &dsl.QueryDSL{
		Aggregate: []dsl.AggregateClause{{Fn: "SUM", Field: "value", As: "total"}},
	}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0]["total"] != 60.0 {
		t.Errorf("total: got %v, want 60.0", result.Rows[0]["total"])
	}
}

func TestProcessor_InMemoryAggregation_WithGroupBy(t *testing.T) {
	proc := NewProcessor(nil)
	rows := makeRows(
		map[string]any{"sensor": "A", "value": 10.0},
		map[string]any{"sensor": "B", "value": 20.0},
		map[string]any{"sensor": "A", "value": 30.0},
	)
	plan := &dsl.QueryPlan{
		InMemory: []string{"aggregate"},
	}
	q := &dsl.QueryDSL{
		GroupBy:   []string{"sensor"},
		Aggregate: []dsl.AggregateClause{{Fn: "SUM", Field: "value", As: "total"}},
	}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(result.Rows))
	}
	// First group should be A (insertion order preserved)
	if result.Rows[0]["sensor"] != "A" || result.Rows[0]["total"] != 40.0 {
		t.Errorf("group A: got %v", result.Rows[0])
	}
	if result.Rows[1]["sensor"] != "B" || result.Rows[1]["total"] != 20.0 {
		t.Errorf("group B: got %v", result.Rows[1])
	}
}

func TestProcessor_InMemorySort_Asc(t *testing.T) {
	proc := NewProcessor(nil)
	rows := makeRows(
		map[string]any{"name": "C", "val": 3.0},
		map[string]any{"name": "A", "val": 1.0},
		map[string]any{"name": "B", "val": 2.0},
	)
	plan := &dsl.QueryPlan{InMemory: []string{"sort"}}
	q := &dsl.QueryDSL{
		OrderBy: []dsl.OrderByClause{{Field: "val", Dir: "asc"}},
	}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Rows[0]["name"] != "A" {
		t.Errorf("first row should be A, got %v", result.Rows[0]["name"])
	}
	if result.Rows[2]["name"] != "C" {
		t.Errorf("last row should be C, got %v", result.Rows[2]["name"])
	}
}

func TestProcessor_InMemorySort_Desc(t *testing.T) {
	proc := NewProcessor(nil)
	rows := makeRows(
		map[string]any{"name": "A", "val": 1.0},
		map[string]any{"name": "B", "val": 2.0},
		map[string]any{"name": "C", "val": 3.0},
	)
	plan := &dsl.QueryPlan{InMemory: []string{"sort"}}
	q := &dsl.QueryDSL{
		OrderBy: []dsl.OrderByClause{{Field: "val", Dir: "desc"}},
	}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Rows[0]["name"] != "C" {
		t.Errorf("first row should be C, got %v", result.Rows[0]["name"])
	}
}

func TestProcessor_Pagination(t *testing.T) {
	proc := NewProcessor(nil)
	rows := make([]dsl.Row, 20)
	for i := range rows {
		rows[i] = map[string]any{"idx": i}
	}
	plan := &dsl.QueryPlan{InMemory: []string{"paginate"}}
	limit := 5
	offset := 3
	q := &dsl.QueryDSL{Limit: &limit, Offset: &offset}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result.Rows) != 5 {
		t.Errorf("expected 5 rows, got %d", len(result.Rows))
	}
	if result.Rows[0]["idx"] != 3 {
		t.Errorf("first row idx: got %v, want 3", result.Rows[0]["idx"])
	}
	if result.Total == nil || *result.Total != 20 {
		t.Errorf("total: got %v, want 20", result.Total)
	}
}

func TestProcessor_PaginationOffsetBeyondRows(t *testing.T) {
	proc := NewProcessor(nil)
	rows := makeRows(
		map[string]any{"id": 1},
		map[string]any{"id": 2},
	)
	plan := &dsl.QueryPlan{InMemory: []string{"paginate"}}
	offset := 100
	q := &dsl.QueryDSL{Offset: &offset}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result.Rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result.Rows))
	}
}

func TestProcessor_ExpressionFilter(t *testing.T) {
	eval := &mockExprEvaluator{results: map[string]any{"value > 15": true}}
	proc := NewProcessor(eval)
	rows := makeRows(
		map[string]any{"value": 10.0},
		map[string]any{"value": 20.0},
	)
	plan := &dsl.QueryPlan{InMemory: []string{"filter_expr"}}
	q := &dsl.QueryDSL{
		Where: &dsl.WhereClause{Expr: "value > 15"},
	}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Both rows pass because mock always returns true for "value > 15"
	if len(result.Rows) != 2 {
		t.Errorf("expected 2 rows (mock returns true), got %d", len(result.Rows))
	}
}

func TestProcessor_SelectProjection(t *testing.T) {
	proc := NewProcessor(nil)
	rows := makeRows(
		map[string]any{"id": "1", "name": "alice", "secret": "hidden"},
	)
	plan := &dsl.QueryPlan{Columns: []dsl.ColumnInfo{{Name: "id"}, {Name: "name"}}}
	q := &dsl.QueryDSL{
		Select: []dsl.SelectField{{Field: "id"}, {Field: "name"}},
	}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	row := result.Rows[0]
	if _, ok := row["secret"]; ok {
		t.Error("secret should be projected out")
	}
	if row["id"] != "1" || row["name"] != "alice" {
		t.Errorf("expected id=1, name=alice, got %v", row)
	}
}

func TestProcessor_EmptyRows(t *testing.T) {
	proc := NewProcessor(nil)
	plan := &dsl.QueryPlan{}
	q := &dsl.QueryDSL{}

	result, err := proc.Process(context.Background(), plan, q, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Total == nil || *result.Total != 0 {
		t.Errorf("total: got %v, want 0", result.Total)
	}
}

func TestProcessor_InMemoryHaving(t *testing.T) {
	proc := NewProcessor(nil)
	rows := makeRows(
		map[string]any{"sensor": "A", "value": 10.0},
		map[string]any{"sensor": "A", "value": 20.0},
		map[string]any{"sensor": "B", "value": 5.0},
	)
	plan := &dsl.QueryPlan{InMemory: []string{"aggregate", "having"}}
	q := &dsl.QueryDSL{
		GroupBy:   []string{"sensor"},
		Aggregate: []dsl.AggregateClause{{Fn: "SUM", Field: "value", As: "total"}},
		Having:    &dsl.WhereClause{Field: "total", Op: ">", Value: 10.0},
	}

	result, err := proc.Process(context.Background(), plan, q, rows)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Only sensor A has total 30 > 10
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row after HAVING, got %d", len(result.Rows))
	}
	if result.Rows[0]["sensor"] != "A" {
		t.Errorf("expected sensor A, got %v", result.Rows[0]["sensor"])
	}
}

// --- computeAggregate tests ---

func TestComputeAggregate_COUNT(t *testing.T) {
	rows := makeRows(
		map[string]any{"x": 1},
		map[string]any{"x": nil},
		map[string]any{"x": 3},
	)
	result := computeAggregate(dsl.AggregateClause{Fn: "COUNT", Field: "x", As: "c"}, rows)
	if result != 2 {
		t.Errorf("COUNT(x): got %v, want 2", result)
	}
}

func TestComputeAggregate_COUNTStar(t *testing.T) {
	rows := makeRows(
		map[string]any{"x": 1},
		map[string]any{"y": 2},
	)
	result := computeAggregate(dsl.AggregateClause{Fn: "COUNT", Field: "*", As: "c"}, rows)
	if result != 2 {
		t.Errorf("COUNT(*): got %v, want 2", result)
	}
}

func TestComputeAggregate_SUM(t *testing.T) {
	rows := makeRows(
		map[string]any{"v": 10.0},
		map[string]any{"v": 20.0},
		map[string]any{"v": 30.0},
	)
	result := computeAggregate(dsl.AggregateClause{Fn: "SUM", Field: "v", As: "s"}, rows)
	if result != 60.0 {
		t.Errorf("SUM: got %v, want 60.0", result)
	}
}

func TestComputeAggregate_AVG(t *testing.T) {
	rows := makeRows(
		map[string]any{"v": 10.0},
		map[string]any{"v": 20.0},
	)
	result := computeAggregate(dsl.AggregateClause{Fn: "AVG", Field: "v", As: "a"}, rows)
	if result != 15.0 {
		t.Errorf("AVG: got %v, want 15.0", result)
	}
}

func TestComputeAggregate_AVG_Empty(t *testing.T) {
	result := computeAggregate(dsl.AggregateClause{Fn: "AVG", Field: "v", As: "a"}, nil)
	if result != nil {
		t.Errorf("AVG of empty: got %v, want nil", result)
	}
}

func TestComputeAggregate_MIN(t *testing.T) {
	rows := makeRows(
		map[string]any{"v": 30.0},
		map[string]any{"v": 10.0},
		map[string]any{"v": 20.0},
	)
	result := computeAggregate(dsl.AggregateClause{Fn: "MIN", Field: "v", As: "m"}, rows)
	if result != 10.0 {
		t.Errorf("MIN: got %v, want 10.0", result)
	}
}

func TestComputeAggregate_MAX(t *testing.T) {
	rows := makeRows(
		map[string]any{"v": 30.0},
		map[string]any{"v": 10.0},
		map[string]any{"v": 20.0},
	)
	result := computeAggregate(dsl.AggregateClause{Fn: "MAX", Field: "v", As: "m"}, rows)
	if result != 30.0 {
		t.Errorf("MAX: got %v, want 30.0", result)
	}
}

func TestComputeAggregate_Unknown(t *testing.T) {
	rows := makeRows(map[string]any{"v": 1})
	result := computeAggregate(dsl.AggregateClause{Fn: "STDEV", Field: "v", As: "s"}, rows)
	if result != nil {
		t.Errorf("Unknown agg: got %v, want nil", result)
	}
}

// --- evalSimpleCondition tests ---

func TestEvalSimpleCondition(t *testing.T) {
	row := dsl.Row{"name": "alice", "age": 30.0, "score": nil}

	tests := []struct {
		name string
		w    *dsl.WhereClause
		want bool
	}{
		{"eq match", &dsl.WhereClause{Field: "name", Op: "==", Value: "alice"}, true},
		{"eq mismatch", &dsl.WhereClause{Field: "name", Op: "==", Value: "bob"}, false},
		{"neq", &dsl.WhereClause{Field: "name", Op: "!=", Value: "bob"}, true},
		{"gt true", &dsl.WhereClause{Field: "age", Op: ">", Value: 20.0}, true},
		{"gt false", &dsl.WhereClause{Field: "age", Op: ">", Value: 40.0}, false},
		{"lt", &dsl.WhereClause{Field: "age", Op: "<", Value: 40.0}, true},
		{"gte", &dsl.WhereClause{Field: "age", Op: ">=", Value: 30.0}, true},
		{"lte", &dsl.WhereClause{Field: "age", Op: "<=", Value: 30.0}, true},
		{"is_null true", &dsl.WhereClause{Field: "score", Op: "is_null"}, true},
		{"is_null false", &dsl.WhereClause{Field: "name", Op: "is_null"}, false},
		{"is_not_null", &dsl.WhereClause{Field: "name", Op: "is_not_null"}, true},
		{"is_null missing", &dsl.WhereClause{Field: "missing", Op: "is_null"}, true},
		{"like match", &dsl.WhereClause{Field: "name", Op: "like", Value: "%ali%"}, true},
		{"like mismatch", &dsl.WhereClause{Field: "name", Op: "like", Value: "%bob%"}, false},
		{"not_like", &dsl.WhereClause{Field: "name", Op: "not_like", Value: "%bob%"}, true},
		{"in match", &dsl.WhereClause{Field: "name", Op: "in", Value: []any{"alice", "bob"}}, true},
		{"in mismatch", &dsl.WhereClause{Field: "name", Op: "in", Value: []any{"bob", "charlie"}}, false},
		{"not_in", &dsl.WhereClause{Field: "name", Op: "not_in", Value: []any{"bob"}}, true},
		{"nil value false", &dsl.WhereClause{Field: "score", Op: "==", Value: 0}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evalSimpleCondition(tt.w, row); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Helper function tests ---

func TestToFloat(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want float64
	}{
		{"float64", 3.14, 3.14},
		{"float32", float32(2.5), 2.5},
		{"int", 42, 42.0},
		{"int64", int64(100), 100.0},
		{"int32", int32(50), 50.0},
		{"string", "3.14", 3.14},
		{"nil", nil, 0},
		{"bool", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toFloat(tt.v)
			if fmt.Sprintf("%.2f", got) != fmt.Sprintf("%.2f", tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToBool(t *testing.T) {
	tests := []struct {
		v    any
		want bool
	}{
		{true, true},
		{false, false},
		{1, true},
		{0, false},
		{int64(1), true},
		{1.0, true},
		{0.0, false},
		{"hello", true},
		{"", false},
		{"false", false},
		{"0", false},
		{nil, false},
		{[]int{1}, true},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			if got := toBool(tt.v); got != tt.want {
				t.Errorf("toBool(%v): got %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}

func TestCompareValues(t *testing.T) {
	tests := []struct {
		a, b any
		want int
	}{
		{nil, nil, 0},
		{nil, 1, -1},
		{1, nil, 1},
		{1.0, 2.0, -1},
		{2.0, 1.0, 1},
		{1.0, 1.0, 0},
		{"abc", "def", -1},
		{"def", "abc", 1},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			got := compareValues(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("compareValues(%v, %v): got %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestIsNumeric(t *testing.T) {
	if !isNumeric(42) {
		t.Error("int should be numeric")
	}
	if !isNumeric(3.14) {
		t.Error("float64 should be numeric")
	}
	if isNumeric("42") {
		t.Error("string should not be numeric")
	}
	if isNumeric(nil) {
		t.Error("nil should not be numeric")
	}
}

func TestValueIn(t *testing.T) {
	if !valueIn("a", []any{"a", "b", "c"}) {
		t.Error("expected 'a' in list")
	}
	if valueIn("d", []any{"a", "b", "c"}) {
		t.Error("'d' should not be in list")
	}
	if valueIn("a", "not-a-slice") {
		t.Error("non-slice should return false")
	}
}

func TestGroupKey(t *testing.T) {
	row := dsl.Row{"a": "x", "b": "y"}
	key := groupKey([]string{"a", "b"}, row)
	if key != "x\x00y" {
		t.Errorf("got %q", key)
	}
}

func TestProcessInMemory_fullResidual(t *testing.T) {
	p := NewProcessor(nil)
	rows := []dsl.Row{
		{"host": "a", "v": 1.0}, {"host": "a", "v": 3.0},
		{"host": "b", "v": 5.0}, {"host": "b", "v": 7.0},
	}
	lim := 1
	q := &dsl.QueryDSL{
		Where:     &dsl.WhereClause{Field: "v", Op: ">", Value: 1},
		GroupBy:   []string{"host"},
		Aggregate: []dsl.AggregateClause{{Fn: "SUM", Field: "v", As: "total"}},
		OrderBy:   []dsl.OrderByClause{{Field: "total", Dir: "desc"}},
		Limit:     &lim,
	}
	res, err := p.ProcessInMemory(context.Background(), q, rows, true)
	if err != nil {
		t.Fatalf("ProcessInMemory: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
	if res.Rows[0]["host"] != "b" || res.Rows[0]["total"] != 12.0 {
		t.Fatalf("unexpected top row: %+v", res.Rows[0])
	}
	if res.Total == nil || *res.Total != 2 {
		t.Fatalf("total = %v, want 2 (pre-pagination group count)", res.Total)
	}
}

func TestProcessInMemory_skipWhere(t *testing.T) {
	p := NewProcessor(nil)
	rows := []dsl.Row{{"v": 1.0}, {"v": 2.0}}
	q := &dsl.QueryDSL{Where: &dsl.WhereClause{Field: "v", Op: ">", Value: 1}}
	res, err := p.ProcessInMemory(context.Background(), q, rows, false)
	if err != nil {
		t.Fatalf("ProcessInMemory: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("applyWhere=false must not filter; rows = %d", len(res.Rows))
	}
}

func TestFilterRows_orTree(t *testing.T) {
	p := NewProcessor(nil)
	rows := []dsl.Row{{"a": 1.0}, {"a": 2.0}, {"a": 9.0}}
	w := &dsl.WhereClause{Or: []dsl.WhereClause{
		{Field: "a", Op: "==", Value: 1}, {Field: "a", Op: ">", Value: 8},
	}}
	out, err := p.FilterRows(context.Background(), w, rows)
	if err != nil {
		t.Fatalf("FilterRows: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("rows = %d, want 2", len(out))
	}
}
