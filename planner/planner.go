package planner

import (
	"context"
	"fmt"
	"strings"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/internal/qhelp"
	"github.com/xraph/dql/scope"
)

// SchemaResolver resolves dataset names to column metadata.
// Defined as an interface to avoid import cycles with the schema package.
type SchemaResolver interface {
	ResolveDataset(ctx context.Context, workspaceID, projectID, name string) (*dsl.DatasetInfo, error)
}

// Planner builds a QueryPlan from a validated QueryDSL by resolving datasets
// and determining which operations to push down to SQL vs compute in-memory.
type Planner struct {
	schema SchemaResolver
	scope  scope.Scope
}

// NewPlanner creates a planner with the given schema resolver and the host's
// partition scope. The scope tells the planner which columns to look for on
// joined tables; it does not decide what they mean.
func NewPlanner(schema SchemaResolver, scope scope.Scope) *Planner {
	return &Planner{schema: schema, scope: scope}
}

// Plan resolves datasets and produces an execution plan.
func (p *Planner) Plan(ctx context.Context, q *dsl.QueryDSL, workspaceID string) (*dsl.QueryPlan, error) {
	projectID := q.ProjectID

	// Resolve primary dataset
	info, err := p.schema.ResolveDataset(ctx, workspaceID, projectID, q.From.Dataset)
	if err != nil {
		return nil, fmt.Errorf("resolve dataset %q: %w", q.From.Dataset, err)
	}

	// Build column lookup for pushdown decisions
	rawColumns := make(map[string]bool, len(info.Columns))
	for _, col := range info.Columns {
		if col.IsRaw() {
			rawColumns[col.Name] = true
		}
	}

	// Build set of computed column names
	computedNames := make(map[string]bool, len(q.Computed))
	for _, c := range q.Computed {
		computedNames[c.Name] = true
	}

	plan := &dsl.QueryPlan{
		Dataset:     info,
		DatasetName: info.Name,
		TableName:   info.TableName,
	}

	// Determine which SELECT columns to push
	plan.PushedSelect = p.planSelect(q, rawColumns, computedNames)

	// For versioned datasets, auto-inject _is_current = true filter unless
	// the query already references version columns (user wants historical data).
	// Only inject when the _is_current column actually exists in the schema.
	if info.Versioned && rawColumns["_is_current"] && !referencesVersionColumn(q.Where) {
		isCurrentFilter := dsl.WhereClause{
			Field: "_is_current",
			Op:    "==",
			Value: true,
		}
		if q.Where != nil {
			q.Where = &dsl.WhereClause{
				And: []dsl.WhereClause{isCurrentFilter, *q.Where},
			}
		} else {
			q.Where = &isCurrentFilter
		}
	}

	// Determine which WHERE conditions to push vs keep in-memory
	if q.Where != nil {
		pushed, inMem := p.splitWhere(q.Where, rawColumns, computedNames)
		plan.PushedWhere = pushed
		if inMem {
			plan.InMemory = append(plan.InMemory, "filter_expr")
		}
	}

	// Plan JOINs — all dataset joins are pushed to SQL
	for _, j := range q.Join {
		joinInfo, joinErr := p.schema.ResolveDataset(ctx, workspaceID, projectID, j.Dataset)
		if joinErr != nil {
			return nil, fmt.Errorf("resolve join dataset %q: %w", j.Dataset, joinErr)
		}

		alias := j.Alias
		if alias == "" {
			alias = joinInfo.TableName
		}

		// Parse the on-columns: strip alias prefixes for SQL generation
		onLeft := stripAlias(j.On.Left)
		onRight := stripAlias(j.On.Right)

		// Record which of the host's join-scoping columns this table actually
		// carries. Without these predicates, out-of-scope rows in the joined
		// table could match.
		var joinScopeCols []string
		for _, sc := range p.scope.JoinScoped() {
			for _, col := range joinInfo.Columns {
				if col.Name == sc.Name {
					joinScopeCols = append(joinScopeCols, sc.Name)
					break
				}
			}
		}

		plan.Joins = append(plan.Joins, dsl.JoinPlan{
			TableName:    joinInfo.TableName,
			Alias:        alias,
			OnLeft:       onLeft,
			OnRight:      onRight,
			Type:         j.Type,
			ScopeColumns: joinScopeCols,
		})

		// Add join table's raw columns to the known set
		for _, col := range joinInfo.Columns {
			if col.IsRaw() {
				rawColumns[alias+"."+col.Name] = true
				rawColumns[col.Name] = true // allow unqualified access too
			}
		}
	}

	// Plan GROUP BY — push if all group columns are raw
	allGroupRaw := true
	for _, g := range q.GroupBy {
		if !rawColumns[g] {
			allGroupRaw = false
			break
		}
	}

	// Plan aggregates — push standard ones on raw fields
	hasInMemoryAggs := false
	if allGroupRaw && len(q.GroupBy) > 0 {
		plan.PushedGroup = q.GroupBy
	}

	for _, agg := range q.Aggregate {
		if allGroupRaw && agg.IsPushable() && rawColumns[agg.Field] {
			plan.PushedAggs = append(plan.PushedAggs, agg)
		} else {
			hasInMemoryAggs = true
		}
	}

	if hasInMemoryAggs {
		plan.InMemory = append(plan.InMemory, "aggregate")
	}

	// Plan HAVING — push if GROUP BY is pushed and condition references pushed aggs
	if q.Having != nil && allGroupRaw {
		plan.HasHaving = true
		plan.PushedHaving = q.Having
	} else if q.Having != nil {
		plan.HasHaving = true
		plan.InMemory = append(plan.InMemory, "having")
	}

	// Plan ORDER BY — push if ordering by raw columns (not computed/aggregate expr)
	hasInMemorySort := false
	for _, o := range q.OrderBy {
		if o.Field != "" && rawColumns[o.Field] && !computedNames[o.Field] {
			plan.PushedOrder = append(plan.PushedOrder, o)
		} else {
			hasInMemorySort = true
		}
	}
	if hasInMemorySort {
		plan.InMemory = append(plan.InMemory, "sort")
	}

	// Plan computed columns (always in-memory)
	if len(q.Computed) > 0 {
		plan.InMemory = append(plan.InMemory, "computed_columns")
	}

	// Plan LIMIT/OFFSET — push only if no in-memory ops that change row count
	hasRowChangingOps := qhelp.ContainsAny(plan.InMemory, "filter_expr", "aggregate", "having")
	if !hasRowChangingOps {
		plan.PushedLimit = q.Limit
		plan.PushedOffset = q.Offset
	} else {
		if q.Limit != nil || q.Offset != nil {
			plan.InMemory = append(plan.InMemory, "paginate")
		}
	}

	// Build result column info
	plan.Columns = p.buildColumnInfo(q, info, rawColumns, computedNames)

	return plan, nil
}

// planSelect determines which columns to SELECT in the SQL query.
// We need all raw columns referenced in WHERE, ORDER BY, computed expressions,
// aggregates, and the final SELECT list.
func (p *Planner) planSelect(q *dsl.QueryDSL, rawColumns, computedNames map[string]bool) []string {
	needed := make(map[string]bool)

	// Columns from SELECT — wildcard means select all
	for _, s := range q.Select {
		if s.Field == "*" {
			return nil // SELECT * — no column restriction
		}
		if s.Field != "" && rawColumns[s.Field] && !computedNames[s.Field] {
			needed[s.Field] = true
		}
	}

	// Columns from GROUP BY
	for _, g := range q.GroupBy {
		if rawColumns[g] {
			needed[g] = true
		}
	}

	// Columns from aggregate fields
	for _, a := range q.Aggregate {
		if a.Field != "" && a.Field != "*" && rawColumns[a.Field] {
			needed[a.Field] = true
		}
	}

	// Columns from ORDER BY
	for _, o := range q.OrderBy {
		if o.Field != "" && rawColumns[o.Field] && !computedNames[o.Field] {
			needed[o.Field] = true
		}
	}

	// If no specific columns needed, select all (for computed cols, WHERE expr, etc.)
	if len(needed) == 0 && (len(q.Computed) > 0 || qhelp.HasExprWhere(q.Where)) {
		return nil // nil means SELECT *
	}

	// Also select all if the select list is empty (user wants everything)
	if len(q.Select) == 0 && len(q.Aggregate) == 0 {
		return nil
	}

	result := make([]string, 0, len(needed))
	for col := range needed {
		result = append(result, col)
	}
	return result
}

// splitWhere separates a WHERE clause into pushable (SQL) and in-memory parts.
// Returns the pushable clause (may be nil) and whether in-memory filtering is needed.
func (p *Planner) splitWhere(w *dsl.WhereClause, rawColumns, computedNames map[string]bool) (*dsl.WhereClause, bool) {
	if w == nil {
		return nil, false
	}

	// DTL expression → always in-memory
	if w.IsExpr() {
		return nil, true
	}

	// Simple condition
	if w.IsSimple() {
		// If field references a computed column, can't push
		if computedNames[w.Field] {
			return nil, true
		}
		// If field is a raw column, push to SQL
		if rawColumns[w.Field] || w.Field == "workspace_id" || w.Field == "project_id" {
			return w, false
		}
		// Unknown column — still push and let DB report the error
		return w, false
	}

	// AND compound: push as much as possible
	if len(w.And) > 0 {
		var pushed []dsl.WhereClause
		needsInMem := false
		for i := range w.And {
			p, inMem := p.splitWhere(&w.And[i], rawColumns, computedNames)
			if p != nil {
				pushed = append(pushed, *p)
			}
			if inMem {
				needsInMem = true
			}
		}
		var result *dsl.WhereClause
		if len(pushed) > 0 {
			result = &dsl.WhereClause{And: pushed}
		}
		return result, needsInMem
	}

	// OR compound: can only push if ALL conditions are pushable
	if len(w.Or) > 0 {
		var pushed []dsl.WhereClause
		for i := range w.Or {
			p, inMem := p.splitWhere(&w.Or[i], rawColumns, computedNames)
			if inMem || p == nil {
				// Can't partially push OR — keep entire thing in-memory
				return nil, true
			}
			pushed = append(pushed, *p)
		}
		return &dsl.WhereClause{Or: pushed}, false
	}

	// NOT
	if w.Not != nil {
		p, inMem := p.splitWhere(w.Not, rawColumns, computedNames)
		if inMem || p == nil {
			return nil, true
		}
		return &dsl.WhereClause{Not: p}, false
	}

	return w, false
}

// buildColumnInfo constructs the expected result columns.
func (p *Planner) buildColumnInfo(q *dsl.QueryDSL, info *dsl.DatasetInfo, rawColumns, computedNames map[string]bool) []dsl.ColumnInfo {
	var cols []dsl.ColumnInfo

	// If we have aggregates, output is: group_by columns + aggregate aliases
	if len(q.Aggregate) > 0 {
		for _, g := range q.GroupBy {
			colType := "string"
			for _, cm := range info.Columns {
				if cm.Name == g {
					colType = cm.Type
					break
				}
			}
			cols = append(cols, dsl.ColumnInfo{Name: g, Type: colType, Source: "raw"})
		}
		for _, a := range q.Aggregate {
			cols = append(cols, dsl.ColumnInfo{Name: a.As, Type: "number", Source: "aggregate"})
		}
		return cols
	}

	// Otherwise build from SELECT list (or all raw columns if no select)
	if len(q.Select) > 0 {
		for _, s := range q.Select {
			name := s.OutputName()
			source := "raw"
			colType := "any"
			if s.Expr != "" {
				source = "computed"
			} else if computedNames[s.Field] {
				source = "computed"
			} else {
				for _, cm := range info.Columns {
					if cm.Name == s.Field {
						colType = cm.Type
						break
					}
				}
			}
			cols = append(cols, dsl.ColumnInfo{Name: name, Type: colType, Source: source})
		}
	} else {
		// SELECT * — all raw columns
		for _, cm := range info.Columns {
			if cm.IsRaw() {
				cols = append(cols, dsl.ColumnInfo{Name: cm.Name, Type: cm.Type, Source: "raw"})
			}
		}
	}

	// Add computed columns
	for _, c := range q.Computed {
		cols = append(cols, dsl.ColumnInfo{Name: c.Name, Type: "any", Source: "computed"})
	}

	return cols
}

// --- Helpers ---

// stripAlias removes a "alias." prefix from a column reference.
// e.g., "meta.id" → "id", "sensor_id" → "sensor_id"
func stripAlias(ref string) string {
	if idx := strings.LastIndex(ref, "."); idx != -1 {
		return ref[idx+1:]
	}
	return ref
}

// referencesVersionColumn checks whether a WHERE clause tree references
// any version-tracking columns (_version, _is_current). If so, the user
// is explicitly querying historical data and we should not auto-inject
// the _is_current filter.
func referencesVersionColumn(w *dsl.WhereClause) bool {
	if w == nil {
		return false
	}
	if w.Field == "_version" || w.Field == "_is_current" ||
		w.Field == "_committed_at" || w.Field == "_committed_by" {
		return true
	}
	for i := range w.And {
		if referencesVersionColumn(&w.And[i]) {
			return true
		}
	}
	for i := range w.Or {
		if referencesVersionColumn(&w.Or[i]) {
			return true
		}
	}
	if w.Not != nil && referencesVersionColumn(w.Not) {
		return true
	}
	return false
}
