package planner

import (
	"testing"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/scope"
)

// The generator emits join scoping from JoinPlan.ScopeColumns, and the planner
// is the only thing that populates it. Every other test in this package builds
// JoinPlan literals directly, so if the planner silently stopped deriving those
// columns, joins would become unscoped and nothing would fail. This covers that
// seam.
func TestPlanner_derivesJoinScopeColumns(t *testing.T) {
	resolver := &mockSchemaResolver{
		datasets: map[string]*dsl.DatasetInfo{
			"events": {
				ID: "ds1", Name: "events", TableName: "ds_events",
				Columns: []dsl.ColumnMeta{
					{Name: "id", Type: "string", Source: "raw"},
					{Name: "workspace_id", Type: "string", Source: "raw"},
					{Name: "created_by", Type: "string", Source: "raw"},
				},
			},
			// Carries the partition column — must be scoped.
			"people": {
				ID: "ds2", Name: "people", TableName: "ds_people",
				Columns: []dsl.ColumnMeta{
					{Name: "id", Type: "string", Source: "raw"},
					{Name: "workspace_id", Type: "string", Source: "raw"},
				},
			},
			// Does not — must not be, or the SQL references a missing column.
			"sysusers": {
				ID: "ds3", Name: "sysusers", TableName: "identity_users",
				Columns: []dsl.ColumnMeta{
					{Name: "id", Type: "string", Source: "raw"},
				},
			},
		},
	}
	planner := NewPlanner(resolver, scope.Scope{{Name: "workspace_id", Required: true, ScopeJoins: true}})

	for _, tc := range []struct {
		name    string
		join    string
		wantCol []string
	}{
		{"joined table carrying the partition column is scoped", "people", []string{"workspace_id"}},
		{"joined table without it is not scoped", "sysusers", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planner.Plan(t.Context(), &dsl.QueryDSL{
				From: dsl.FromClause{Dataset: "events"},
				Join: []dsl.JoinClause{{
					Dataset: tc.join, Alias: "j", Type: "inner",
					On: dsl.JoinOn{Left: "created_by", Right: "id"},
				}},
			}, "ws_a")
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if len(plan.Joins) != 1 {
				t.Fatalf("want 1 join, got %d", len(plan.Joins))
			}
			got := plan.Joins[0].ScopeColumns
			if len(got) != len(tc.wantCol) {
				t.Fatalf("ScopeColumns = %v, want %v", got, tc.wantCol)
			}
			for i := range got {
				if got[i] != tc.wantCol[i] {
					t.Errorf("ScopeColumns[%d] = %q, want %q", i, got[i], tc.wantCol[i])
				}
			}
		})
	}
}
