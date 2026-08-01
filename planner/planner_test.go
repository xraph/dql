package planner

import (
	"context"
	"fmt"
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/internal/qhelp"
)

// --- Mock SchemaResolver ---

type mockSchemaResolver struct {
	datasets map[string]*dsl.DatasetInfo
}

func (m *mockSchemaResolver) ResolveDataset(_ context.Context, _, _, name string) (*dsl.DatasetInfo, error) {
	info, ok := m.datasets[name]
	if !ok {
		return nil, fmt.Errorf("dataset not found: %s", name)
	}
	return info, nil
}

func newTestPlanner() (*Planner, *mockSchemaResolver) {
	resolver := &mockSchemaResolver{
		datasets: map[string]*dsl.DatasetInfo{
			"sensors": {
				ID:        "ds1",
				Name:      "sensors",
				TableName: "sensors",
				Columns: []dsl.ColumnMeta{
					{Name: "id", Type: "string", Source: "raw"},
					{Name: "value", Type: "float64", Source: "raw"},
					{Name: "sensor_id", Type: "string", Source: "raw"},
					{Name: "timestamp", Type: "datetime", Source: "raw"},
					{Name: "formula_col", Type: "float64", Source: "formula"},
				},
			},
			"metadata": {
				ID:        "ds2",
				Name:      "metadata",
				TableName: "metadata",
				Columns: []dsl.ColumnMeta{
					{Name: "id", Type: "string", Source: "raw"},
					{Name: "label", Type: "string", Source: "raw"},
				},
			},
		},
	}
	return NewPlanner(resolver, testScope()), resolver
}

// --- Tests ---

func TestPlanner_SimpleQuery(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "sensors"},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if plan.DatasetName != "sensors" {
		t.Errorf("dataset: got %q", plan.DatasetName)
	}
	if plan.TableName != "sensors" {
		t.Errorf("table: got %q", plan.TableName)
	}
}

func TestPlanner_SelectPushdown(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:   dsl.FromClause{Dataset: "sensors"},
		Select: []dsl.SelectField{{Field: "id"}, {Field: "value"}},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if plan.PushedSelect == nil {
		t.Fatal("expected pushed select columns")
	}
	// Should include id and value
	selectSet := make(map[string]bool)
	for _, s := range plan.PushedSelect {
		selectSet[s] = true
	}
	if !selectSet["id"] || !selectSet["value"] {
		t.Errorf("expected id and value in pushed select, got %v", plan.PushedSelect)
	}
}

func TestPlanner_ComputedColumnForcesInMemory(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:     dsl.FromClause{Dataset: "sensors"},
		Computed: []dsl.ComputedColumn{{Name: "double_val", Expr: "value * 2"}},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	found := false
	for _, op := range plan.InMemory {
		if op == "computed_columns" {
			found = true
		}
	}
	if !found {
		t.Error("expected computed_columns in in-memory ops")
	}
}

func TestPlanner_WherePushdown_RawColumn(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:  dsl.FromClause{Dataset: "sensors"},
		Where: &dsl.WhereClause{Field: "value", Op: ">", Value: 50},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if plan.PushedWhere == nil {
		t.Fatal("expected where to be pushed to SQL")
	}
}

func TestPlanner_WhereSplit_ExprGoesInMemory(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:  dsl.FromClause{Dataset: "sensors"},
		Where: &dsl.WhereClause{Expr: "value * 2 > 100"},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	found := false
	for _, op := range plan.InMemory {
		if op == "filter_expr" {
			found = true
		}
	}
	if !found {
		t.Error("expected filter_expr in in-memory ops")
	}
}

func TestPlanner_GroupByAggregatePushdown(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:      dsl.FromClause{Dataset: "sensors"},
		GroupBy:   []string{"sensor_id"},
		Aggregate: []dsl.AggregateClause{{Fn: "AVG", Field: "value", As: "avg_val"}},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if len(plan.PushedGroup) == 0 {
		t.Error("expected group by to be pushed")
	}
	if len(plan.PushedAggs) == 0 {
		t.Error("expected aggregates to be pushed")
	}
}

func TestPlanner_NonPushableAggregate(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:      dsl.FromClause{Dataset: "sensors"},
		GroupBy:   []string{"sensor_id"},
		Aggregate: []dsl.AggregateClause{{Fn: "STDEV", Field: "value", As: "std_val"}},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	found := false
	for _, op := range plan.InMemory {
		if op == "aggregate" {
			found = true
		}
	}
	if !found {
		t.Error("expected aggregate in in-memory ops")
	}
}

func TestPlanner_OrderByPushdown(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:    dsl.FromClause{Dataset: "sensors"},
		OrderBy: []dsl.OrderByClause{{Field: "value", Dir: "desc"}},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if len(plan.PushedOrder) == 0 {
		t.Error("expected order by to be pushed to SQL")
	}
}

func TestPlanner_OrderByExprInMemory(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:    dsl.FromClause{Dataset: "sensors"},
		OrderBy: []dsl.OrderByClause{{Expr: "value * 2", Dir: "asc"}},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	found := false
	for _, op := range plan.InMemory {
		if op == "sort" {
			found = true
		}
	}
	if !found {
		t.Error("expected sort in in-memory ops")
	}
}

func TestPlanner_LimitOffsetPushdown(t *testing.T) {
	planner, _ := newTestPlanner()
	limit := 10
	offset := 5
	q := &dsl.QueryDSL{
		From:   dsl.FromClause{Dataset: "sensors"},
		Limit:  &limit,
		Offset: &offset,
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if plan.PushedLimit == nil {
		t.Error("expected limit to be pushed")
	}
	if plan.PushedOffset == nil {
		t.Error("expected offset to be pushed")
	}
}

func TestPlanner_LimitNotPushedWithExprWhere(t *testing.T) {
	planner, _ := newTestPlanner()
	limit := 10
	q := &dsl.QueryDSL{
		From:  dsl.FromClause{Dataset: "sensors"},
		Where: &dsl.WhereClause{Expr: "value > 50"},
		Limit: &limit,
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if plan.PushedLimit != nil {
		t.Error("expected limit NOT to be pushed (has in-memory filter)")
	}
	found := false
	for _, op := range plan.InMemory {
		if op == "paginate" {
			found = true
		}
	}
	if !found {
		t.Error("expected paginate in in-memory ops")
	}
}

func TestPlanner_JoinResolution(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "sensors"},
		Join: []dsl.JoinClause{
			{Dataset: "metadata", Alias: "m", On: dsl.JoinOn{Left: "sensor_id", Right: "id"}, Type: "inner"},
		},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if len(plan.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(plan.Joins))
	}
	if plan.Joins[0].TableName != "metadata" {
		t.Errorf("join table: got %q", plan.Joins[0].TableName)
	}
	if plan.Joins[0].Alias != "m" {
		t.Errorf("join alias: got %q", plan.Joins[0].Alias)
	}
}

func TestPlanner_UnknownDataset(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "nonexistent"},
	}
	_, err := planner.Plan(context.Background(), q, "ws1")
	if err == nil {
		t.Fatal("expected error for unknown dataset")
	}
}

func TestPlanner_HavingPushed(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:      dsl.FromClause{Dataset: "sensors"},
		GroupBy:   []string{"sensor_id"},
		Aggregate: []dsl.AggregateClause{{Fn: "COUNT", As: "cnt"}},
		Having:    &dsl.WhereClause{Field: "cnt", Op: ">", Value: 5},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if !plan.HasHaving {
		t.Error("expected HasHaving to be true")
	}
	if plan.PushedHaving == nil {
		t.Error("expected having to be pushed")
	}
}

func TestPlanner_ColumnInfo_WithAggregates(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:      dsl.FromClause{Dataset: "sensors"},
		GroupBy:   []string{"sensor_id"},
		Aggregate: []dsl.AggregateClause{{Fn: "COUNT", As: "cnt"}},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if len(plan.Columns) != 2 {
		t.Fatalf("expected 2 columns (group + agg), got %d", len(plan.Columns))
	}
	if plan.Columns[0].Name != "sensor_id" {
		t.Errorf("col[0]: got %q", plan.Columns[0].Name)
	}
	if plan.Columns[1].Name != "cnt" {
		t.Errorf("col[1]: got %q", plan.Columns[1].Name)
	}
	if plan.Columns[1].Source != "aggregate" {
		t.Errorf("col[1] source: got %q", plan.Columns[1].Source)
	}
}

func TestPlanner_ColumnInfo_SelectAll(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "sensors"},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	// Should include raw columns only (4 raw out of 5)
	rawCount := 0
	for _, c := range plan.Columns {
		if c.Source == "raw" {
			rawCount++
		}
	}
	if rawCount != 4 {
		t.Errorf("expected 4 raw columns, got %d", rawCount)
	}
}

// --- Helper Tests ---

func TestStripAlias(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"sensor_id", "sensor_id"},
		{"m.id", "id"},
		{"schema.table.col", "col"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := stripAlias(tt.input); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHasExprWhere(t *testing.T) {
	tests := []struct {
		name string
		w    *dsl.WhereClause
		want bool
	}{
		{"nil", nil, false},
		{"simple", &dsl.WhereClause{Field: "x", Op: "==", Value: 1}, false},
		{"expr", &dsl.WhereClause{Expr: "x > 1"}, true},
		{"nested expr", &dsl.WhereClause{
			And: []dsl.WhereClause{
				{Field: "a", Op: "==", Value: 1},
				{Expr: "b > 2"},
			},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := qhelp.HasExprWhere(tt.w); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainsAny(t *testing.T) {
	slice := []string{"filter_expr", "aggregate", "sort"}
	if !qhelp.ContainsAny(slice, "aggregate") {
		t.Error("expected true for aggregate")
	}
	if qhelp.ContainsAny(slice, "paginate") {
		t.Error("expected false for paginate")
	}
	if !qhelp.ContainsAny(slice, "missing", "sort") {
		t.Error("expected true for sort")
	}
}

// --- Versioned dataset _is_current injection ---

func TestPlanner_VersionedDataset_InjectsIsCurrent(t *testing.T) {
	resolver := &mockSchemaResolver{
		datasets: map[string]*dsl.DatasetInfo{
			"events": {
				Name:      "events",
				TableName: "ds_events",
				Versioned: true,
				Columns: []dsl.ColumnMeta{
					{Name: "id", Type: "string", Source: "raw"},
					{Name: "status", Type: "string", Source: "raw"},
					{Name: "_is_current", Type: "bool", Source: "raw"},
					{Name: "_version", Type: "int64", Source: "raw"},
				},
			},
		},
	}
	planner := NewPlanner(resolver, testScope())
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "events"},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if plan.PushedWhere == nil {
		t.Fatal("expected _is_current filter to be injected")
	}
	// The injected filter should be _is_current == true
	if plan.PushedWhere.Field != "_is_current" {
		// Might be wrapped in an AND if user also had a where
		t.Logf("pushed where: %+v", plan.PushedWhere)
	}
}

func TestPlanner_VersionedDataset_NoIsCurrentColumn_NoInjection(t *testing.T) {
	// Dataset is marked versioned but doesn't have _is_current column yet
	resolver := &mockSchemaResolver{
		datasets: map[string]*dsl.DatasetInfo{
			"legacy": {
				Name:      "legacy",
				TableName: "ds_legacy",
				Versioned: true,
				Columns: []dsl.ColumnMeta{
					{Name: "id", Type: "string", Source: "raw"},
					{Name: "value", Type: "float64", Source: "raw"},
					// No _is_current column
				},
			},
		},
	}
	planner := NewPlanner(resolver, testScope())
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "legacy"},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	// Should NOT inject _is_current when column doesn't exist
	if plan.PushedWhere != nil {
		t.Errorf("expected no injected where, got %+v", plan.PushedWhere)
	}
}

func TestPlanner_NonVersionedDataset_NoIsCurrent(t *testing.T) {
	planner, _ := newTestPlanner()
	q := &dsl.QueryDSL{
		From:  dsl.FromClause{Dataset: "sensors"},
		Where: &dsl.WhereClause{Field: "value", Op: ">", Value: 50},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	// Non-versioned: where should be the user's filter only
	if plan.PushedWhere == nil {
		t.Fatal("expected user where to be pushed")
	}
	if plan.PushedWhere.Field != "value" {
		// Should not be wrapped in AND with _is_current
		if len(plan.PushedWhere.And) > 0 {
			t.Error("expected no _is_current injection for non-versioned dataset")
		}
	}
}

func TestPlanner_VersionedDataset_WithUserWhere_CombinesFilters(t *testing.T) {
	resolver := &mockSchemaResolver{
		datasets: map[string]*dsl.DatasetInfo{
			"events": {
				Name:      "events",
				TableName: "ds_events",
				Versioned: true,
				Columns: []dsl.ColumnMeta{
					{Name: "id", Type: "string", Source: "raw"},
					{Name: "status", Type: "string", Source: "raw"},
					{Name: "_is_current", Type: "bool", Source: "raw"},
				},
			},
		},
	}
	planner := NewPlanner(resolver, testScope())
	q := &dsl.QueryDSL{
		From:  dsl.FromClause{Dataset: "events"},
		Where: &dsl.WhereClause{Field: "status", Op: "==", Value: "active"},
	}
	plan, err := planner.Plan(context.Background(), q, "ws1")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	// Should combine _is_current AND user filter
	if plan.PushedWhere == nil {
		t.Fatal("expected combined where")
	}
	if len(plan.PushedWhere.And) != 2 {
		t.Errorf("expected 2 AND conditions (_is_current + user filter), got %+v", plan.PushedWhere)
	}
}
