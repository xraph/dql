package parser

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/pipe"
)

// ValidatePipeShape checks the structural correctness of a pipe-mode query.
// Service-availability checks are deferred to execution time.
func ValidatePipeShape(q *dsl.QueryDSL) []ParseError {
	shapeErrs := pipe.ValidateShape(q.Pipe)
	if len(shapeErrs) == 0 {
		return nil
	}
	out := make([]ParseError, 0, len(shapeErrs))
	for _, e := range shapeErrs {
		out = append(out, ParseError{Field: fmt.Sprintf("pipe[%d]", e.StageIndex), Message: e.Message})
	}
	return out
}

// ParseError describes a validation error in the query DSL.
type ParseError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ParseError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s", e.Field, e.Message)
	}
	return e.Message
}

// Parse unmarshals raw JSON into a validated QueryDSL.
func Parse(raw json.RawMessage) (*dsl.QueryDSL, []ParseError) {
	var q dsl.QueryDSL
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, []ParseError{{Field: "body", Message: fmt.Sprintf("invalid JSON: %s", err.Error())}}
	}

	errs := Validate(&q)
	if len(errs) > 0 {
		return nil, errs
	}

	ResolveParameters(&q)
	return &q, nil
}

// Validate checks structural correctness of a QueryDSL.
func Validate(q *dsl.QueryDSL) []ParseError {
	var errs []ParseError

	// from is required for every mode
	if q.From.Dataset == "" {
		errs = append(errs, ParseError{Field: "from", Message: "dataset name is required"})
	}

	// Pipe mode is validated by a separate, lighter-weight shape check. The
	// classic top-level clauses (where/select/aggregate/...) are ignored when
	// mode == "pipe", so we skip their validation to avoid spurious errors.
	if q.IsPipeMode() {
		return append(errs, ValidatePipeShape(q)...)
	}

	// Validate WHERE clause
	if q.Where != nil {
		errs = append(errs, validateWhere(q.Where, "where")...)
	}

	// Validate HAVING clause
	if q.Having != nil {
		errs = append(errs, validateWhere(q.Having, "having")...)
	}

	// Validate joins
	for i, j := range q.Join {
		prefix := fmt.Sprintf("join[%d]", i)
		if j.Dataset == "" {
			errs = append(errs, ParseError{Field: prefix + ".dataset", Message: "dataset is required"})
		}
		if j.On.Left == "" || j.On.Right == "" {
			errs = append(errs, ParseError{Field: prefix + ".on", Message: "both left and right columns are required"})
		}
		if j.Type == "" {
			errs = append(errs, ParseError{Field: prefix + ".type", Message: "join type is required"})
		} else if !dsl.ValidJoinTypes[j.Type] {
			errs = append(errs, ParseError{Field: prefix + ".type", Message: fmt.Sprintf("unknown join type %q", j.Type)})
		}
	}

	// Validate aggregates
	for i, a := range q.Aggregate {
		prefix := fmt.Sprintf("aggregate[%d]", i)
		if a.Fn == "" {
			errs = append(errs, ParseError{Field: prefix + ".fn", Message: "aggregate function is required"})
		} else if !dsl.ValidAggregateFns[strings.ToUpper(a.Fn)] {
			errs = append(errs, ParseError{Field: prefix + ".fn", Message: fmt.Sprintf("unknown aggregate function %q", a.Fn)})
		}
		if a.As == "" {
			errs = append(errs, ParseError{Field: prefix + ".as", Message: "output alias is required"})
		}
		// EXPR aggregate needs an expression
		if strings.ToUpper(a.Fn) == "EXPR" && a.Expr == "" {
			errs = append(errs, ParseError{Field: prefix + ".expr", Message: "expr is required for EXPR aggregate"})
		}
	}

	// Validate computed columns
	for i, c := range q.Computed {
		prefix := fmt.Sprintf("computed[%d]", i)
		if c.Name == "" {
			errs = append(errs, ParseError{Field: prefix + ".name", Message: "computed column name is required"})
		}
		if c.Expr == "" {
			errs = append(errs, ParseError{Field: prefix + ".expr", Message: "computed column expression is required"})
		}
	}

	// Validate order_by
	for i, o := range q.OrderBy {
		prefix := fmt.Sprintf("order_by[%d]", i)
		if o.Field == "" && o.Expr == "" {
			errs = append(errs, ParseError{Field: prefix, Message: "field or expr is required"})
		}
		if o.Dir != "" {
			dir := strings.ToLower(o.Dir)
			if dir != "asc" && dir != "desc" {
				errs = append(errs, ParseError{Field: prefix + ".dir", Message: fmt.Sprintf("invalid direction %q, must be asc or desc", o.Dir)})
			}
		}
	}

	// Validate select fields
	for i, s := range q.Select {
		prefix := fmt.Sprintf("select[%d]", i)
		if s.Field == "" && s.Expr == "" {
			errs = append(errs, ParseError{Field: prefix, Message: "field or expr is required"})
		}
	}

	// Validate limit/offset
	if q.Limit != nil && *q.Limit < 0 {
		errs = append(errs, ParseError{Field: "limit", Message: "limit must be non-negative"})
	}
	if q.Offset != nil && *q.Offset < 0 {
		errs = append(errs, ParseError{Field: "offset", Message: "offset must be non-negative"})
	}

	// HAVING without GROUP BY is meaningless
	if q.Having != nil && len(q.GroupBy) == 0 && len(q.Aggregate) == 0 {
		errs = append(errs, ParseError{Field: "having", Message: "having requires group_by or aggregate"})
	}

	return errs
}

// validateWhere recursively validates a WHERE clause tree.
func validateWhere(w *dsl.WhereClause, path string) []ParseError {
	var errs []ParseError

	if w.IsSimple() {
		if !dsl.ValidWhereOps[w.Op] {
			errs = append(errs, ParseError{
				Field:   path + ".op",
				Message: fmt.Sprintf("unknown operator %q", w.Op),
			})
		}
		// Unary operators don't need a value
		if w.Op != "is_null" && w.Op != "is_not_null" && w.Value == nil {
			errs = append(errs, ParseError{
				Field:   path + ".value",
				Message: "value is required for operator " + w.Op,
			})
		}
	} else if !w.IsExpr() && !w.IsCompound() {
		errs = append(errs, ParseError{
			Field:   path,
			Message: "where clause must have field+op, expr, or and/or/not",
		})
	}

	// Recurse into compound conditions
	for i, child := range w.And {
		errs = append(errs, validateWhere(&child, fmt.Sprintf("%s.and[%d]", path, i))...)
	}
	for i, child := range w.Or {
		errs = append(errs, validateWhere(&child, fmt.Sprintf("%s.or[%d]", path, i))...)
	}
	if w.Not != nil {
		errs = append(errs, validateWhere(w.Not, path+".not")...)
	}

	return errs
}

// ResolveParameters substitutes $param references in WHERE values with
// values from the parameters map.
func ResolveParameters(q *dsl.QueryDSL) {
	if len(q.Parameters) == 0 {
		return
	}

	if q.Where != nil {
		resolveWhereParams(q.Where, q.Parameters)
	}
	if q.Having != nil {
		resolveWhereParams(q.Having, q.Parameters)
	}
}

// resolveWhereParams recursively replaces $param references in WHERE values.
func resolveWhereParams(w *dsl.WhereClause, params map[string]any) {
	if w.Value != nil {
		if s, ok := w.Value.(string); ok && strings.HasPrefix(s, "$") {
			if v, exists := params[s]; exists {
				w.Value = v
			}
		}
	}

	for i := range w.And {
		resolveWhereParams(&w.And[i], params)
	}
	for i := range w.Or {
		resolveWhereParams(&w.Or[i], params)
	}
	if w.Not != nil {
		resolveWhereParams(w.Not, params)
	}
}

// NormalizeAggregateFns uppercases all aggregate function names in-place.
func NormalizeAggregateFns(q *dsl.QueryDSL) {
	for i := range q.Aggregate {
		q.Aggregate[i].Fn = strings.ToUpper(q.Aggregate[i].Fn)
	}
}

// NormalizeOrderDir lowercases and defaults order_by directions.
func NormalizeOrderDir(q *dsl.QueryDSL) {
	for i := range q.OrderBy {
		if q.OrderBy[i].Dir == "" {
			q.OrderBy[i].Dir = "asc"
		} else {
			q.OrderBy[i].Dir = strings.ToLower(q.OrderBy[i].Dir)
		}
	}
}

// FormatParseErrors formats multiple parse errors into a single string.
func FormatParseErrors(errs []ParseError) string {
	if len(errs) == 1 {
		return errs[0].Error()
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return fmt.Sprintf("%d errors: %s", len(errs), strings.Join(msgs, "; "))
}
