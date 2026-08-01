package sqlgen

import (
	"errors"
	"fmt"
	"strings"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/scope"
)

// GenerateSQL builds a parameterized PostgreSQL query from a QueryPlan.
// workspaceID is always injected for tenant isolation.
// projectID is added when non-empty.
func GenerateSQL(plan *dsl.QueryPlan, sc scope.Scope) (string, []any, error) {
	// A nil scope is refused; an explicitly empty one is honoured. The two are
	// very different intentions and only one of them is safe to guess at: a
	// caller that forgot to pass a scope would otherwise get SQL spanning every
	// tenant, silently. Deliberately unscoped callers pass Scope{}.
	if sc == nil {
		return "", nil, errors.New("dql: nil scope — pass Scope{} to query without partition scoping")
	}

	g := &sqlGen{
		params: make([]any, 0, 8),
	}

	g.buildSelect(plan)
	g.buildFrom(plan)
	g.buildJoins(plan, sc)
	g.buildWhere(plan, sc)
	g.buildGroupBy(plan)
	g.buildHaving(plan)
	g.buildOrderBy(plan)
	g.buildLimitOffset(plan)

	sql := g.buf.String()
	plan.SQL = sql
	plan.SQLParams = g.params

	return sql, g.params, nil
}

type sqlGen struct {
	buf    strings.Builder
	params []any
}

// placeholder appends a value and returns the $N placeholder.
func (g *sqlGen) placeholder(val any) string {
	g.params = append(g.params, val)
	return fmt.Sprintf("$%d", len(g.params))
}

// quoteIdent double-quotes a SQL identifier for safety.
func quoteIdent(name string) string {
	// Handle qualified names like "alias"."column"
	if idx := strings.LastIndex(name, "."); idx != -1 {
		return quoteIdent(name[:idx]) + "." + quoteIdent(name[idx+1:])
	}
	// Escape any embedded double-quotes
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (g *sqlGen) buildSelect(plan *dsl.QueryPlan) {
	g.buf.WriteString("SELECT ")

	// If we have pushed aggregates with GROUP BY, build that select list
	if len(plan.PushedAggs) > 0 && len(plan.PushedGroup) > 0 {
		var parts []string
		for _, col := range plan.PushedGroup {
			parts = append(parts, quoteIdent(col))
		}
		for _, agg := range plan.PushedAggs {
			parts = append(parts, g.formatAggregate(agg))
		}
		g.buf.WriteString(strings.Join(parts, ", "))
		return
	}

	// If we have pushed aggregates without GROUP BY (e.g., COUNT(*) on whole table)
	if len(plan.PushedAggs) > 0 {
		var parts []string
		for _, agg := range plan.PushedAggs {
			parts = append(parts, g.formatAggregate(agg))
		}
		g.buf.WriteString(strings.Join(parts, ", "))
		return
	}

	// Normal select
	if len(plan.PushedSelect) == 0 {
		g.buf.WriteString("*")
		return
	}

	cols := make([]string, 0, len(plan.PushedSelect))
	for _, col := range plan.PushedSelect {
		cols = append(cols, quoteIdent(col))
	}
	g.buf.WriteString(strings.Join(cols, ", "))
}

func (g *sqlGen) formatAggregate(agg dsl.AggregateClause) string {
	field := "*"
	if agg.Field != "" && agg.Field != "*" {
		field = quoteIdent(agg.Field)
	}

	var expr string
	switch agg.Fn {
	case "COUNT":
		expr = fmt.Sprintf("COUNT(%s)", field)
	case "SUM":
		expr = fmt.Sprintf("SUM(%s)", field)
	case "AVG":
		expr = fmt.Sprintf("AVG(%s)", field)
	case "MIN":
		expr = fmt.Sprintf("MIN(%s)", field)
	case "MAX":
		expr = fmt.Sprintf("MAX(%s)", field)
	default:
		// Fallback — use the function name directly
		expr = fmt.Sprintf("%s(%s)", agg.Fn, field)
	}

	return expr + " AS " + quoteIdent(agg.As)
}

func (g *sqlGen) buildFrom(plan *dsl.QueryPlan) {
	g.buf.WriteString(" FROM ")
	g.buf.WriteString(quoteIdent(plan.TableName))
}

func (g *sqlGen) buildJoins(plan *dsl.QueryPlan, sc scope.Scope) {
	for _, j := range plan.Joins {
		joinType := strings.ToUpper(j.Type)
		fmt.Fprintf(&g.buf, " %s JOIN %s AS %s ON %s.%s = %s.%s",
			joinType,
			quoteIdent(j.TableName),
			quoteIdent(j.Alias),
			quoteIdent(plan.TableName),
			quoteIdent(j.OnLeft),
			quoteIdent(j.Alias),
			quoteIdent(j.OnRight))

		// Scope the joined table in the ON clause. Putting it in ON (not WHERE)
		// keeps it correct for LEFT joins too: an out-of-scope row simply fails
		// the join condition rather than NULL-padding through.
		for _, name := range j.ScopeColumns {
			for _, col := range sc {
				if col.Name != name {
					continue
				}
				fmt.Fprintf(&g.buf, " AND %s.%s = %s",
					quoteIdent(j.Alias),
					quoteIdent(col.Name),
					g.placeholder(col.Value))
				break
			}
		}
	}
}

func (g *sqlGen) buildWhere(plan *dsl.QueryPlan, sc scope.Scope) {
	// In a join, both the primary table and the joined table typically carry the
	// scope columns. A bare column reference is ambiguous (Postgres 42702), so
	// qualify to the primary table when any join is present.
	qualify := len(plan.Joins) > 0
	column := func(name string) string {
		if qualify {
			return quoteIdent(plan.TableName) + "." + quoteIdent(name)
		}
		return quoteIdent(name)
	}

	// Collect predicates before writing, so a query with neither scope nor user
	// conditions does not emit a dangling WHERE.
	var preds []string
	for _, col := range sc {
		// A Required column is emitted even when the dataset does not declare
		// it. An optional one is emitted only when the dataset carries it —
		// some system-owned sources lack the finer-grained partition column,
		// and blindly adding the predicate would produce invalid SQL.
		if !col.Required && !datasetHasColumn(plan.Dataset, col.Name) {
			continue
		}
		preds = append(preds, column(col.Name)+" = "+g.placeholder(col.Value))
	}

	if len(preds) == 0 && plan.PushedWhere == nil {
		return
	}

	g.buf.WriteString(" WHERE ")
	g.buf.WriteString(strings.Join(preds, " AND "))

	if plan.PushedWhere != nil {
		if len(preds) > 0 {
			g.buf.WriteString(" AND ")
		}
		g.writeWhereClause(plan.PushedWhere)
	}
}

func (g *sqlGen) writeWhereClause(w *dsl.WhereClause) {
	if w.IsSimple() {
		g.writeSimpleCondition(w)
		return
	}

	if len(w.And) > 0 {
		g.buf.WriteString("(")
		for i := range w.And {
			if i > 0 {
				g.buf.WriteString(" AND ")
			}
			g.writeWhereClause(&w.And[i])
		}
		g.buf.WriteString(")")
		return
	}

	if len(w.Or) > 0 {
		g.buf.WriteString("(")
		for i := range w.Or {
			if i > 0 {
				g.buf.WriteString(" OR ")
			}
			g.writeWhereClause(&w.Or[i])
		}
		g.buf.WriteString(")")
		return
	}

	if w.Not != nil {
		g.buf.WriteString("NOT (")
		g.writeWhereClause(w.Not)
		g.buf.WriteString(")")
	}
}

func (g *sqlGen) writeSimpleCondition(w *dsl.WhereClause) {
	col := quoteIdent(w.Field)

	switch w.Op {
	case "is_null":
		g.buf.WriteString(col + " IS NULL")
	case "is_not_null":
		g.buf.WriteString(col + " IS NOT NULL")
	case "in":
		g.writeInCondition(col, w.Value, false)
	case "not_in":
		g.writeInCondition(col, w.Value, true)
	case "between":
		g.writeBetweenCondition(col, w.Value)
	case "starts_with":
		g.writeAffixCondition(col, w.Value, "", "%")
	case "ends_with":
		g.writeAffixCondition(col, w.Value, "%", "")
	case "contains":
		g.writeAffixCondition(col, w.Value, "%", "%")
	default:
		sqlOp, ok := dsl.SQLOpMap[w.Op]
		if !ok {
			sqlOp = w.Op // fallback
		}
		g.buf.WriteString(col + " " + sqlOp + " " + g.placeholder(w.Value))
	}
}

// writeAffixCondition renders a string-affix LIKE without exposing
// SQL wildcards to the caller. The caller's value is treated as a
// literal: any `%` or `_` it contains is escaped (with `\`) before the
// wildcards are appended, and the rendered SQL uses `ESCAPE '\'` so
// the literal pattern is interpreted correctly.
func (g *sqlGen) writeAffixCondition(col string, value any, prefix, suffix string) {
	literal := fmt.Sprintf("%v", value)
	literal = strings.ReplaceAll(literal, `\`, `\\`)
	literal = strings.ReplaceAll(literal, "%", `\%`)
	literal = strings.ReplaceAll(literal, "_", `\_`)
	g.buf.WriteString(col + " LIKE " + g.placeholder(prefix+literal+suffix) + ` ESCAPE '\'`)
}

func (g *sqlGen) writeInCondition(col string, value any, negate bool) {
	arr, ok := dsl.ToSlice(value)
	if !ok || len(arr) == 0 {
		// Degenerate case
		if negate {
			g.buf.WriteString("TRUE")
		} else {
			g.buf.WriteString("FALSE")
		}
		return
	}

	if negate {
		g.buf.WriteString(col + " NOT IN (")
	} else {
		g.buf.WriteString(col + " IN (")
	}

	for i, v := range arr {
		if i > 0 {
			g.buf.WriteString(", ")
		}
		g.buf.WriteString(g.placeholder(v))
	}
	g.buf.WriteString(")")
}

func (g *sqlGen) writeBetweenCondition(col string, value any) {
	arr, ok := dsl.ToSlice(value)
	if !ok || len(arr) != 2 {
		// Invalid between — write a degenerate condition
		g.buf.WriteString("FALSE")
		return
	}
	g.buf.WriteString(col + " BETWEEN " + g.placeholder(arr[0]) + " AND " + g.placeholder(arr[1]))
}

func (g *sqlGen) buildGroupBy(plan *dsl.QueryPlan) {
	if len(plan.PushedGroup) == 0 {
		return
	}

	cols := make([]string, 0, len(plan.PushedGroup))
	for _, col := range plan.PushedGroup {
		cols = append(cols, quoteIdent(col))
	}
	g.buf.WriteString(" GROUP BY " + strings.Join(cols, ", "))
}

func (g *sqlGen) buildHaving(plan *dsl.QueryPlan) {
	if !plan.HasHaving || plan.PushedHaving == nil {
		return
	}

	g.buf.WriteString(" HAVING ")
	g.writeWhereClause(plan.PushedHaving)
}

func (g *sqlGen) buildOrderBy(plan *dsl.QueryPlan) {
	if len(plan.PushedOrder) == 0 {
		return
	}

	g.buf.WriteString(" ORDER BY ")
	for i, o := range plan.PushedOrder {
		if i > 0 {
			g.buf.WriteString(", ")
		}
		g.buf.WriteString(quoteIdent(o.Field))
		if strings.ToUpper(o.Dir) == "DESC" {
			g.buf.WriteString(" DESC")
		} else {
			g.buf.WriteString(" ASC")
		}
	}
}

func (g *sqlGen) buildLimitOffset(plan *dsl.QueryPlan) {
	if plan.PushedLimit != nil {
		g.buf.WriteString(" LIMIT " + g.placeholder(*plan.PushedLimit))
	}
	if plan.PushedOffset != nil && *plan.PushedOffset > 0 {
		g.buf.WriteString(" OFFSET " + g.placeholder(*plan.PushedOffset))
	}
}

// --- Helpers ---

// datasetHasColumn reports whether the resolved dataset declares a column.
// When no column metadata is available (nil dataset or empty column list, as
// with the passthrough resolver) it returns true to preserve the historical
// always-scope-by-project behaviour for user datasets.
func datasetHasColumn(d *dsl.DatasetInfo, name string) bool {
	if d == nil || len(d.Columns) == 0 {
		return true
	}
	for _, c := range d.Columns {
		if c.Name == name {
			return true
		}
	}
	return false
}
