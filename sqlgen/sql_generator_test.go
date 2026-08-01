package sqlgen

import (
	"strings"
	"testing"

	"github.com/xraph/dql/dsl"
)

// --- SQL Generator Tests ---

func TestGenerateSQL_SimpleSelect(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName: "sensors",
	}
	sql, params, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, `SELECT *`) {
		t.Errorf("expected SELECT *, got: %s", sql)
	}
	if !strings.Contains(sql, `FROM "sensors"`) {
		t.Errorf("expected FROM sensors, got: %s", sql)
	}
	if !strings.Contains(sql, `"workspace_id" = $1`) {
		t.Errorf("expected workspace_id filter, got: %s", sql)
	}
	if len(params) != 1 || params[0] != "ws1" {
		t.Errorf("params: got %v", params)
	}
}

func TestGenerateSQL_WithProjectID(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName: "sensors",
	}
	sql, params, err := GenerateSQL(plan, testScope("ws1", "proj1"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, `"project_id" = $2`) {
		t.Errorf("expected project_id filter, got: %s", sql)
	}
	if len(params) != 2 || params[1] != "proj1" {
		t.Errorf("params: got %v", params)
	}
}

func TestGenerateSQL_SpecificColumns(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:    "sensors",
		PushedSelect: []string{"id", "value"},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, `"id"`) || !strings.Contains(sql, `"value"`) {
		t.Errorf("expected quoted column names, got: %s", sql)
	}
	if strings.Contains(sql, "SELECT *") {
		t.Errorf("should not be SELECT *, got: %s", sql)
	}
}

func TestGenerateSQL_WhereEq(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:   "sensors",
		PushedWhere: &dsl.WhereClause{Field: "status", Op: "==", Value: "active"},
	}
	sql, params, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, `"status" = $2`) {
		t.Errorf("expected = operator, got: %s", sql)
	}
	if len(params) < 2 || params[1] != "active" {
		t.Errorf("params: got %v", params)
	}
}

func TestGenerateSQL_WhereOperators(t *testing.T) {
	tests := []struct {
		op      string
		wantSQL string
	}{
		{"==", "="},
		{"!=", "!="},
		{">", ">"},
		{"<", "<"},
		{">=", ">="},
		{"<=", "<="},
		{"like", "LIKE"},
		{"not_like", "NOT LIKE"},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			plan := &dsl.QueryPlan{
				TableName:   "t",
				PushedWhere: &dsl.WhereClause{Field: "col", Op: tt.op, Value: "x"},
			}
			sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if !strings.Contains(sql, tt.wantSQL) {
				t.Errorf("op %q: expected %q in SQL, got: %s", tt.op, tt.wantSQL, sql)
			}
		})
	}
}

func TestGenerateSQL_IsNull(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:   "t",
		PushedWhere: &dsl.WhereClause{Field: "col", Op: "is_null"},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, `"col" IS NULL`) {
		t.Errorf("expected IS NULL, got: %s", sql)
	}
}

func TestGenerateSQL_IsNotNull(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:   "t",
		PushedWhere: &dsl.WhereClause{Field: "col", Op: "is_not_null"},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, `"col" IS NOT NULL`) {
		t.Errorf("expected IS NOT NULL, got: %s", sql)
	}
}

func TestGenerateSQL_InOperator(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:   "t",
		PushedWhere: &dsl.WhereClause{Field: "status", Op: "in", Value: []any{"a", "b", "c"}},
	}
	sql, params, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, `"status" IN (`) {
		t.Errorf("expected IN clause, got: %s", sql)
	}
	// 1 workspace + 3 IN values = 4 params
	if len(params) != 4 {
		t.Errorf("expected 4 params, got %d", len(params))
	}
}

func TestGenerateSQL_NotInOperator(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:   "t",
		PushedWhere: &dsl.WhereClause{Field: "status", Op: "not_in", Value: []any{"x"}},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, `"status" NOT IN (`) {
		t.Errorf("expected NOT IN, got: %s", sql)
	}
}

func TestGenerateSQL_Between(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:   "t",
		PushedWhere: &dsl.WhereClause{Field: "value", Op: "between", Value: []any{10, 100}},
	}
	sql, params, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, "BETWEEN") {
		t.Errorf("expected BETWEEN, got: %s", sql)
	}
	// 1 workspace + 2 between values = 3 params
	if len(params) != 3 {
		t.Errorf("expected 3 params, got %d", len(params))
	}
}

func TestGenerateSQL_CompoundWhereAND(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName: "t",
		PushedWhere: &dsl.WhereClause{
			And: []dsl.WhereClause{
				{Field: "a", Op: "==", Value: 1},
				{Field: "b", Op: ">", Value: 2},
			},
		},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, " AND ") {
		t.Errorf("expected AND in WHERE, got: %s", sql)
	}
}

func TestGenerateSQL_CompoundWhereOR(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName: "t",
		PushedWhere: &dsl.WhereClause{
			Or: []dsl.WhereClause{
				{Field: "a", Op: "==", Value: 1},
				{Field: "b", Op: "==", Value: 2},
			},
		},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, " OR ") {
		t.Errorf("expected OR in WHERE, got: %s", sql)
	}
}

func TestGenerateSQL_CompoundWhereNOT(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName: "t",
		PushedWhere: &dsl.WhereClause{
			Not: &dsl.WhereClause{Field: "deleted", Op: "==", Value: true},
		},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, "NOT (") {
		t.Errorf("expected NOT in WHERE, got: %s", sql)
	}
}

func TestGenerateSQL_Joins(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName: "sensors",
		Joins: []dsl.JoinPlan{
			{TableName: "metadata", Alias: "m", OnLeft: "sensor_id", OnRight: "id", Type: "left"},
		},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, "LEFT JOIN") {
		t.Errorf("expected LEFT JOIN, got: %s", sql)
	}
	if !strings.Contains(sql, `"metadata"`) {
		t.Errorf("expected metadata table, got: %s", sql)
	}
}

func TestGenerateSQL_GroupBy(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:   "sensors",
		PushedGroup: []string{"sensor_id"},
		PushedAggs:  []dsl.AggregateClause{{Fn: "AVG", Field: "value", As: "avg_val"}},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, "GROUP BY") {
		t.Errorf("expected GROUP BY, got: %s", sql)
	}
	if !strings.Contains(sql, `AVG("value") AS "avg_val"`) {
		t.Errorf("expected aggregate in SELECT, got: %s", sql)
	}
}

func TestGenerateSQL_Having(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:    "sensors",
		PushedGroup:  []string{"sensor_id"},
		PushedAggs:   []dsl.AggregateClause{{Fn: "COUNT", As: "cnt"}},
		HasHaving:    true,
		PushedHaving: &dsl.WhereClause{Field: "cnt", Op: ">", Value: 5},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, "HAVING") {
		t.Errorf("expected HAVING, got: %s", sql)
	}
}

func TestGenerateSQL_OrderBy(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName: "sensors",
		PushedOrder: []dsl.OrderByClause{
			{Field: "value", Dir: "desc"},
			{Field: "id", Dir: "asc"},
		},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, "ORDER BY") {
		t.Errorf("expected ORDER BY, got: %s", sql)
	}
	if !strings.Contains(sql, "DESC") {
		t.Errorf("expected DESC, got: %s", sql)
	}
}

func TestGenerateSQL_LimitOffset(t *testing.T) {
	limit := 10
	offset := 20
	plan := &dsl.QueryPlan{
		TableName:    "sensors",
		PushedLimit:  &limit,
		PushedOffset: &offset,
	}
	sql, params, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, "LIMIT") {
		t.Errorf("expected LIMIT, got: %s", sql)
	}
	if !strings.Contains(sql, "OFFSET") {
		t.Errorf("expected OFFSET, got: %s", sql)
	}
	// workspace + limit + offset = 3 params
	if len(params) != 3 {
		t.Errorf("expected 3 params, got %d", len(params))
	}
}

func TestGenerateSQL_ZeroOffset(t *testing.T) {
	offset := 0
	plan := &dsl.QueryPlan{
		TableName:    "sensors",
		PushedOffset: &offset,
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Zero offset should not emit OFFSET
	if strings.Contains(sql, "OFFSET") {
		t.Errorf("expected no OFFSET for zero, got: %s", sql)
	}
}

func TestGenerateSQL_CountStar(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:  "sensors",
		PushedAggs: []dsl.AggregateClause{{Fn: "COUNT", As: "total"}},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(sql, `COUNT(*) AS "total"`) {
		t.Errorf("expected COUNT(*), got: %s", sql)
	}
}

// --- quoteIdent Tests ---

func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"col", `"col"`},
		{`has"quote`, `"has""quote"`},
		{"a.b", `"a"."b"`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := quoteIdent(tt.input); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- toSlice Tests ---

func TestToSlice(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		wantOk  bool
		wantLen int
	}{
		{"[]any", []any{1, 2, 3}, true, 3},
		{"[]string", []string{"a", "b"}, true, 2},
		{"[]float64", []float64{1.0, 2.0}, true, 2},
		{"[]int", []int{1, 2, 3, 4}, true, 4},
		{"string", "not a slice", false, 0},
		{"nil", nil, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := dsl.ToSlice(tt.input)
			if ok != tt.wantOk {
				t.Errorf("ok: got %v, want %v", ok, tt.wantOk)
			}
			if ok && len(result) != tt.wantLen {
				t.Errorf("len: got %d, want %d", len(result), tt.wantLen)
			}
		})
	}
}

func TestGenerateSQL_InEmptySlice(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:   "t",
		PushedWhere: &dsl.WhereClause{Field: "status", Op: "in", Value: []any{}},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Empty IN should produce FALSE
	if !strings.Contains(sql, "FALSE") {
		t.Errorf("expected FALSE for empty IN, got: %s", sql)
	}
}

func TestGenerateSQL_InvalidBetween(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName:   "t",
		PushedWhere: &dsl.WhereClause{Field: "val", Op: "between", Value: "not-a-slice"},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws1", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Invalid between should produce FALSE
	if !strings.Contains(sql, "FALSE") {
		t.Errorf("expected FALSE for invalid BETWEEN, got: %s", sql)
	}
}

func TestGenerateSQL_PlanStoresSQL(t *testing.T) {
	plan := &dsl.QueryPlan{
		TableName: "sensors",
	}
	sql, params, _ := GenerateSQL(plan, testScope("ws1", ""))
	if plan.SQL != sql {
		t.Error("expected plan.SQL to be set")
	}
	if len(plan.SQLParams) != len(params) {
		t.Error("expected plan.SQLParams to be set")
	}
}

func TestGenerateSQL_systemTableSkipsProjectFilter(t *testing.T) {
	plan := &dsl.QueryPlan{
		DatasetName: "users",
		TableName:   "identity_users",
		Dataset: &dsl.DatasetInfo{
			Name:      "users",
			TableName: "identity_users",
			Columns: []dsl.ColumnMeta{
				{Name: "id", Type: "string", Source: "raw"},
				{Name: "email", Type: "string", Source: "raw"},
			},
		},
		PushedSelect: []string{"id", "email"},
	}
	sql, params, err := GenerateSQL(plan, testScope("ws_1", "proj_1"))
	if err != nil {
		t.Fatalf("GenerateSQL: %v", err)
	}
	if strings.Contains(sql, "project_id") {
		t.Fatalf("system table has no project_id column; SQL must not filter it: %s", sql)
	}
	if len(params) != 1 || params[0] != "ws_1" {
		t.Fatalf("expected only workspace param, got %v", params)
	}
}

func TestGenerateSQL_datasetKeepsProjectFilter(t *testing.T) {
	plan := &dsl.QueryPlan{
		DatasetName: "sensors",
		TableName:   "ds_sensors",
		Dataset: &dsl.DatasetInfo{
			Name:      "sensors",
			TableName: "ds_sensors",
			Columns: []dsl.ColumnMeta{
				{Name: "id", Type: "string", Source: "raw"},
				{Name: "workspace_id", Type: "string", Source: "raw"},
				{Name: "project_id", Type: "string", Source: "raw"},
			},
		},
		PushedSelect: []string{"id"},
	}
	sql, _, err := GenerateSQL(plan, testScope("ws_1", "proj_1"))
	if err != nil {
		t.Fatalf("GenerateSQL: %v", err)
	}
	if !strings.Contains(sql, "project_id") {
		t.Fatalf("dataset with project_id column must still filter it: %s", sql)
	}
}

// TestGenerateSQL_joinQualifiesScopePredicate asserts that when joins are
// present the auto-injected scope predicate is qualified to the primary table
// (e.g. "ds_events"."workspace_id"), not left as a bare "workspace_id". The
// bare form is ambiguous (Postgres 42702) when both the primary table and a
// joined table carry a workspace_id column. The no-join path must remain
// unchanged — bare form — so existing single-table tests are not regressed.
func TestGenerateSQL_joinQualifiesScopePredicate(t *testing.T) {
	dataset := &dsl.DatasetInfo{
		Name:      "events",
		TableName: "ds_events",
		Columns: []dsl.ColumnMeta{
			{Name: "id", Type: "string", Source: "raw"},
			{Name: "workspace_id", Type: "string", Source: "raw"},
			{Name: "created_by", Type: "string", Source: "raw"},
		},
	}

	t.Run("with_join_qualifies_workspace_id", func(t *testing.T) {
		plan := &dsl.QueryPlan{
			DatasetName:  "events",
			TableName:    "ds_events",
			Dataset:      dataset,
			PushedSelect: []string{"created_by"},
			Joins: []dsl.JoinPlan{
				{
					TableName: "identity_users",
					Alias:     "u",
					OnLeft:    "created_by",
					OnRight:   "id",
					Type:      "inner",
				},
			},
		}
		sql, params, err := GenerateSQL(plan, testScope("ws_a", ""))
		if err != nil {
			t.Fatalf("GenerateSQL: %v", err)
		}

		// The scope predicate must be table-qualified when a join is present.
		qualified := `"ds_events"."workspace_id"`
		if !strings.Contains(sql, qualified) {
			t.Errorf("expected qualified %s in SQL with join; got: %s", qualified, sql)
		}

		// The bare unqualified form must not appear (it is ambiguous in a join).
		bare := ` "workspace_id" = `
		if strings.Contains(sql, bare) {
			t.Errorf("bare unqualified workspace_id must not appear in a join query; got: %s", sql)
		}

		if len(params) != 1 || params[0] != "ws_a" {
			t.Errorf("expected exactly one workspace param, got %v", params)
		}
	})

	t.Run("workspace_scoped_join_predicate", func(t *testing.T) {
		plan := &dsl.QueryPlan{
			DatasetName:  "events",
			TableName:    "ds_events",
			Dataset:      dataset,
			PushedSelect: []string{"created_by"},
			Joins: []dsl.JoinPlan{
				{
					TableName:    "identity_users",
					Alias:        "u",
					OnLeft:       "created_by",
					OnRight:      "id",
					Type:         "inner",
					ScopeColumns: []string{"workspace_id"},
				},
			},
		}
		sql, params, err := GenerateSQL(plan, testScope("ws_a", ""))
		if err != nil {
			t.Fatalf("GenerateSQL: %v", err)
		}

		// The join's ON clause must scope the joined table to the workspace.
		// The workspace placeholder is appended during buildJoins (before WHERE),
		// so the join predicate uses $1 and the primary scope predicate uses $2.
		wantJoinScope := `"u"."workspace_id" = $1`
		if !strings.Contains(sql, wantJoinScope) {
			t.Errorf("expected join ON clause to scope workspace (%s); got: %s", wantJoinScope, sql)
		}

		// workspace value repeats: once for the join scope, once for the primary
		// table scope. Both must be "ws_a".
		if len(params) != 2 || params[0] != "ws_a" || params[1] != "ws_a" {
			t.Errorf("expected two repeated workspace params, got %v", params)
		}
	})

	t.Run("join_without_workspace_id_no_predicate", func(t *testing.T) {
		plan := &dsl.QueryPlan{
			DatasetName:  "events",
			TableName:    "ds_events",
			Dataset:      dataset,
			PushedSelect: []string{"created_by"},
			Joins: []dsl.JoinPlan{
				{
					TableName:    "meta",
					Alias:        "m",
					OnLeft:       "id",
					OnRight:      "event_id",
					Type:         "left",
					ScopeColumns: nil,
				},
			},
		}
		sql, _, err := GenerateSQL(plan, testScope("ws_a", ""))
		if err != nil {
			t.Fatalf("GenerateSQL: %v", err)
		}

		// A join without a workspace_id column must not get a scope predicate.
		if strings.Contains(sql, `"m"."workspace_id"`) {
			t.Errorf("join without workspace_id must not be workspace-scoped; got: %s", sql)
		}
	})

	t.Run("no_join_keeps_bare_workspace_id", func(t *testing.T) {
		plan := &dsl.QueryPlan{
			DatasetName:  "events",
			TableName:    "ds_events",
			Dataset:      dataset,
			PushedSelect: []string{"created_by"},
		}
		sql, params, err := GenerateSQL(plan, testScope("ws_a", ""))
		if err != nil {
			t.Fatalf("GenerateSQL: %v", err)
		}

		// Without a join the bare form must remain (backward-compatible).
		if !strings.Contains(sql, `"workspace_id" = $1`) {
			t.Errorf("expected bare workspace_id predicate for single-table query; got: %s", sql)
		}

		// Qualified form must not appear for the no-join case.
		qualified := `"ds_events"."workspace_id"`
		if strings.Contains(sql, qualified) {
			t.Errorf("qualified %s must not appear in single-table query; got: %s", qualified, sql)
		}

		if len(params) != 1 || params[0] != "ws_a" {
			t.Errorf("expected exactly one workspace param, got %v", params)
		}
	})
}
