package processor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/internal/qhelp"
)

// ExprEvaluator evaluates DTL expressions against a row of data.
// Defined as an interface to decouple from the function registry.
type ExprEvaluator interface {
	Eval(ctx context.Context, expr string, row map[string]any) (any, error)
}

// Processor handles post-SQL in-memory operations: computed columns,
// expression-based filtering, sorting, and projection.
type Processor struct {
	eval ExprEvaluator
}

// NewProcessor creates a processor with the given expression evaluator.
// eval may be nil — computed columns and expr-based operations will return errors.
func NewProcessor(eval ExprEvaluator) *Processor {
	return &Processor{eval: eval}
}

// Process applies in-memory operations to raw database rows based on the query plan.
func (p *Processor) Process(ctx context.Context, plan *dsl.QueryPlan, q *dsl.QueryDSL, rows []dsl.Row) (*dsl.QueryResult, error) {
	var err error

	// 1. Evaluate computed columns
	if len(q.Computed) > 0 {
		rows, err = p.evalComputedColumns(ctx, q.Computed, rows)
		if err != nil {
			return nil, fmt.Errorf("computed columns: %w", err)
		}
	}

	// 2. Expression-based WHERE filtering (not pushed to SQL)
	if q.Where != nil && qhelp.HasExprWhere(q.Where) {
		rows, err = p.filterByExpr(ctx, q.Where, rows)
		if err != nil {
			return nil, fmt.Errorf("expression filter: %w", err)
		}
	}

	// 3. In-memory aggregation (if not pushed to SQL)
	if qhelp.ContainsAny(plan.InMemory, "aggregate") {
		rows, err = p.aggregate(ctx, q, rows)
		if err != nil {
			return nil, fmt.Errorf("aggregation: %w", err)
		}
	}

	// 4. In-memory HAVING filter
	if qhelp.ContainsAny(plan.InMemory, "having") && q.Having != nil {
		rows, err = p.filterByWhere(ctx, q.Having, rows)
		if err != nil {
			return nil, fmt.Errorf("having filter: %w", err)
		}
	}

	// 5. In-memory sort
	if qhelp.ContainsAny(plan.InMemory, "sort") {
		rows = p.sortRows(ctx, q.OrderBy, rows)
	}

	// 6. In-memory pagination
	total := len(rows)
	if qhelp.ContainsAny(plan.InMemory, "paginate") {
		rows = p.paginate(q, rows)
	}

	// 7. Select projection — keep only requested columns
	// Skip projection when select is a wildcard ("*") — return all fields.
	if len(q.Select) > 0 && len(q.Aggregate) == 0 && !isWildcardSelect(q.Select) {
		rows = p.project(q, rows)
	}

	return &dsl.QueryResult{
		Rows:    rows,
		Columns: plan.Columns,
		Total:   &total,
	}, nil
}

// evalComputedColumns evaluates DTL expressions and adds new columns to each row.
func (p *Processor) evalComputedColumns(ctx context.Context, computed []dsl.ComputedColumn, rows []dsl.Row) ([]dsl.Row, error) {
	if p.eval == nil {
		return nil, fmt.Errorf("expression evaluator not available")
	}

	for i, row := range rows {
		for _, c := range computed {
			val, err := p.eval.Eval(ctx, c.Expr, row)
			if err != nil {
				return nil, fmt.Errorf("row %d, column %q: %w", i, c.Name, err)
			}
			row[c.Name] = val
		}
	}
	return rows, nil
}

// filterByExpr filters rows by evaluating DTL expression WHERE clauses.
// Only processes expression-based conditions; simple conditions were already pushed to SQL.
func (p *Processor) filterByExpr(ctx context.Context, w *dsl.WhereClause, rows []dsl.Row) ([]dsl.Row, error) {
	if w == nil {
		return rows, nil
	}

	result := make([]dsl.Row, 0, len(rows))
	for _, row := range rows {
		match, err := p.evalWhereExpr(ctx, w, row)
		if err != nil {
			return nil, err
		}
		if match {
			result = append(result, row)
		}
	}
	return result, nil
}

// evalWhereExpr evaluates a WHERE clause tree against a single row.
// Returns true if the row matches.
func (p *Processor) evalWhereExpr(ctx context.Context, w *dsl.WhereClause, row dsl.Row) (bool, error) {
	if w == nil {
		return true, nil
	}

	// DTL expression
	if w.IsExpr() {
		if p.eval == nil {
			return false, fmt.Errorf("expression evaluator not available")
		}
		val, err := p.eval.Eval(ctx, w.Expr, row)
		if err != nil {
			return false, err
		}
		return toBool(val), nil
	}

	// Simple condition — evaluate in Go
	if w.IsSimple() {
		return evalSimpleCondition(w, row), nil
	}

	// AND
	if len(w.And) > 0 {
		for i := range w.And {
			match, err := p.evalWhereExpr(ctx, &w.And[i], row)
			if err != nil {
				return false, err
			}
			if !match {
				return false, nil
			}
		}
		return true, nil
	}

	// OR
	if len(w.Or) > 0 {
		for i := range w.Or {
			match, err := p.evalWhereExpr(ctx, &w.Or[i], row)
			if err != nil {
				return false, err
			}
			if match {
				return true, nil
			}
		}
		return false, nil
	}

	// NOT
	if w.Not != nil {
		match, err := p.evalWhereExpr(ctx, w.Not, row)
		if err != nil {
			return false, err
		}
		return !match, nil
	}

	return true, nil
}

// evalSimpleCondition evaluates a field-op-value condition against a row.
func evalSimpleCondition(w *dsl.WhereClause, row dsl.Row) bool {
	val, exists := row[w.Field]

	switch w.Op {
	case "is_null":
		return !exists || val == nil
	case "is_not_null":
		return exists && val != nil
	}

	if val == nil {
		return false
	}

	switch w.Op {
	case "==":
		return fmt.Sprintf("%v", val) == fmt.Sprintf("%v", w.Value)
	case "!=":
		return fmt.Sprintf("%v", val) != fmt.Sprintf("%v", w.Value)
	case ">":
		return compareValues(val, w.Value) > 0
	case "<":
		return compareValues(val, w.Value) < 0
	case ">=":
		return compareValues(val, w.Value) >= 0
	case "<=":
		return compareValues(val, w.Value) <= 0
	case "like", "not_like":
		// Simplified LIKE: just check contains for now
		s := fmt.Sprintf("%v", val)
		pattern := fmt.Sprintf("%v", w.Value)
		pattern = strings.ReplaceAll(pattern, "%", "")
		match := strings.Contains(strings.ToLower(s), strings.ToLower(pattern))
		if w.Op == "not_like" {
			return !match
		}
		return match
	case "starts_with":
		return strings.HasPrefix(strings.ToLower(fmt.Sprintf("%v", val)), strings.ToLower(fmt.Sprintf("%v", w.Value)))
	case "ends_with":
		return strings.HasSuffix(strings.ToLower(fmt.Sprintf("%v", val)), strings.ToLower(fmt.Sprintf("%v", w.Value)))
	case "contains":
		return strings.Contains(strings.ToLower(fmt.Sprintf("%v", val)), strings.ToLower(fmt.Sprintf("%v", w.Value)))
	case "in":
		return valueIn(val, w.Value)
	case "not_in":
		return !valueIn(val, w.Value)
	default:
		return false
	}
}

// aggregate performs in-memory GROUP BY + aggregation.
func (p *Processor) aggregate(_ context.Context, q *dsl.QueryDSL, rows []dsl.Row) ([]dsl.Row, error) {
	if len(q.GroupBy) == 0 && len(q.Aggregate) > 0 {
		// Aggregate over all rows (no grouping)
		result := make(dsl.Row)
		for _, agg := range q.Aggregate {
			result[agg.As] = computeAggregate(agg, rows)
		}
		return []dsl.Row{result}, nil
	}

	// Group rows
	groups := make(map[string][]dsl.Row)
	groupOrder := make([]string, 0) // preserve insertion order
	for _, row := range rows {
		key := groupKey(q.GroupBy, row)
		if _, exists := groups[key]; !exists {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], row)
	}

	// Compute aggregates per group
	result := make([]dsl.Row, 0, len(groups))
	for _, key := range groupOrder {
		groupRows := groups[key]
		outRow := make(dsl.Row)

		// Copy group-by values from first row
		for _, col := range q.GroupBy {
			outRow[col] = groupRows[0][col]
		}

		// Compute each aggregate
		for _, agg := range q.Aggregate {
			outRow[agg.As] = computeAggregate(agg, groupRows)
		}

		result = append(result, outRow)
	}

	return result, nil
}

// computeAggregate computes a single aggregate over a set of rows.
func computeAggregate(agg dsl.AggregateClause, rows []dsl.Row) any {
	switch agg.Fn {
	case "COUNT":
		if agg.Field == "*" || agg.Field == "" {
			return len(rows)
		}
		count := 0
		for _, row := range rows {
			if row[agg.Field] != nil {
				count++
			}
		}
		return count

	case "SUM":
		sum := 0.0
		for _, row := range rows {
			sum += toFloat(row[agg.Field])
		}
		return sum

	case "AVG":
		if len(rows) == 0 {
			return nil
		}
		sum := 0.0
		for _, row := range rows {
			sum += toFloat(row[agg.Field])
		}
		return sum / float64(len(rows))

	case "MIN":
		if len(rows) == 0 {
			return nil
		}
		min := toFloat(rows[0][agg.Field])
		for _, row := range rows[1:] {
			v := toFloat(row[agg.Field])
			if v < min {
				min = v
			}
		}
		return min

	case "MAX":
		if len(rows) == 0 {
			return nil
		}
		max := toFloat(rows[0][agg.Field])
		for _, row := range rows[1:] {
			v := toFloat(row[agg.Field])
			if v > max {
				max = v
			}
		}
		return max

	default:
		return nil
	}
}

// filterByWhere applies a simple WHERE clause as an in-memory filter.
func (p *Processor) filterByWhere(ctx context.Context, w *dsl.WhereClause, rows []dsl.Row) ([]dsl.Row, error) {
	return p.filterByExpr(ctx, w, rows)
}

// sortRows sorts rows by ORDER BY clauses.
func (p *Processor) sortRows(_ context.Context, orderBy []dsl.OrderByClause, rows []dsl.Row) []dsl.Row {
	if len(orderBy) == 0 {
		return rows
	}

	sort.SliceStable(rows, func(i, j int) bool {
		for _, o := range orderBy {
			field := o.Field
			if field == "" {
				continue // skip expr-based (would need eval)
			}

			cmp := compareValues(rows[i][field], rows[j][field])
			if cmp == 0 {
				continue
			}
			if strings.ToLower(o.Dir) == "desc" {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})

	return rows
}

// paginate applies in-memory LIMIT/OFFSET.
func (p *Processor) paginate(q *dsl.QueryDSL, rows []dsl.Row) []dsl.Row {
	offset := 0
	if q.Offset != nil {
		offset = *q.Offset
	}
	if offset > len(rows) {
		return nil
	}
	rows = rows[offset:]

	if q.Limit != nil && *q.Limit < len(rows) {
		rows = rows[:*q.Limit]
	}

	return rows
}

// isWildcardSelect returns true if the select clause is a wildcard ("*"),
// meaning all fields should be returned without projection.
func isWildcardSelect(sel []dsl.SelectField) bool {
	for _, s := range sel {
		if s.Field == "*" {
			return true
		}
	}
	return false
}

// project keeps only the selected columns in each row.
func (p *Processor) project(q *dsl.QueryDSL, rows []dsl.Row) []dsl.Row {
	// Safety: if wildcard is present, return rows unmodified
	if isWildcardSelect(q.Select) {
		return rows
	}

	// Build the set of output column names
	outputCols := make([]struct{ from, to string }, 0, len(q.Select))
	for _, s := range q.Select {
		from := s.Field
		if from == "" {
			from = s.As // computed or expression result
		}
		to := s.OutputName()
		outputCols = append(outputCols, struct{ from, to string }{from, to})
	}

	// Also include computed columns
	for _, c := range q.Computed {
		found := false
		for _, oc := range outputCols {
			if oc.from == c.Name || oc.to == c.Name {
				found = true
				break
			}
		}
		if !found {
			outputCols = append(outputCols, struct{ from, to string }{c.Name, c.Name})
		}
	}

	result := make([]dsl.Row, 0, len(rows))
	for _, row := range rows {
		out := make(dsl.Row, len(outputCols))
		for _, oc := range outputCols {
			if v, ok := row[oc.from]; ok {
				out[oc.to] = v
			}
		}
		result = append(result, out)
	}
	return result
}

// --- Helpers ---

// groupKey produces a string key for GROUP BY grouping.
func groupKey(groupBy []string, row dsl.Row) string {
	parts := make([]string, 0, len(groupBy))
	for _, col := range groupBy {
		parts = append(parts, fmt.Sprintf("%v", row[col]))
	}
	return strings.Join(parts, "\x00")
}

// toFloat converts a value to float64 for numeric operations.
func toFloat(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case int32:
		return float64(val)
	case string:
		var f float64
		_, _ = fmt.Sscanf(val, "%f", &f)
		return f
	default:
		return 0
	}
}

// toBool coerces a value to boolean.
func toBool(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case int:
		return val != 0
	case int64:
		return val != 0
	case float64:
		return val != 0
	case string:
		return val != "" && val != "false" && val != "0"
	case nil:
		return false
	default:
		return true
	}
}

// compareValues compares two values numerically or as strings.
// Returns -1, 0, or 1.
func compareValues(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	// Try numeric comparison
	af := toFloat(a)
	bf := toFloat(b)
	// Check if both are actually numeric
	if isNumeric(a) && isNumeric(b) {
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		default:
			return 0
		}
	}

	// Fall back to string comparison
	as := fmt.Sprintf("%v", a)
	bs := fmt.Sprintf("%v", b)
	return strings.Compare(as, bs)
}

// isNumeric checks if a value is a numeric type.
func isNumeric(v any) bool {
	switch v.(type) {
	case float64, float32, int, int64, int32, int16, int8, uint, uint64, uint32:
		return true
	default:
		return false
	}
}

// valueIn checks if val is in a slice-like value.
func valueIn(val, list any) bool {
	arr, ok := dsl.ToSlice(list)
	if !ok {
		return false
	}
	s := fmt.Sprintf("%v", val)
	for _, item := range arr {
		if fmt.Sprintf("%v", item) == s {
			return true
		}
	}
	return false
}

// FilterRows applies a full WHERE tree (simple/AND/OR/NOT/expr) in memory.
// Unlike Process, which only handles the expression-bearing residue of a
// SQL-pushed query, this evaluates every predicate — app sources use it
// because pushed filters are hints the app may have ignored.
func (p *Processor) FilterRows(ctx context.Context, w *dsl.WhereClause, rows []dsl.Row) ([]dsl.Row, error) {
	return p.filterByExpr(ctx, w, rows)
}

// ProcessInMemory runs the classic post-source pipeline entirely in memory
// over rows that did not come from SQL (app sources). applyWhere is false
// when the app verifiably applied every pushed filter. Columns are left to
// the caller — only rows and the pre-pagination total are produced.
func (p *Processor) ProcessInMemory(ctx context.Context, q *dsl.QueryDSL, rows []dsl.Row, applyWhere bool) (*dsl.QueryResult, error) {
	var err error

	if len(q.Computed) > 0 {
		if rows, err = p.evalComputedColumns(ctx, q.Computed, rows); err != nil {
			return nil, fmt.Errorf("computed columns: %w", err)
		}
	}
	if applyWhere && q.Where != nil {
		if rows, err = p.FilterRows(ctx, q.Where, rows); err != nil {
			return nil, fmt.Errorf("where filter: %w", err)
		}
	}
	if len(q.Aggregate) > 0 {
		if rows, err = p.aggregate(ctx, q, rows); err != nil {
			return nil, fmt.Errorf("aggregation: %w", err)
		}
	}
	if q.Having != nil {
		if rows, err = p.filterByWhere(ctx, q.Having, rows); err != nil {
			return nil, fmt.Errorf("having filter: %w", err)
		}
	}
	rows = p.sortRows(ctx, q.OrderBy, rows)

	total := len(rows)
	rows = p.paginate(q, rows)

	if len(q.Select) > 0 && len(q.Aggregate) == 0 && !isWildcardSelect(q.Select) {
		rows = p.project(q, rows)
	}

	return &dsl.QueryResult{Rows: rows, Total: &total}, nil
}
