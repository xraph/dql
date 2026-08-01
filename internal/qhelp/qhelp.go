// Package qhelp holds small pure helpers shared by more than one of the query
// packages. They lived together when the planner, generator and processor were
// one package; splitting that package left them with two callers each, and a
// shared internal home beats duplicating them or exporting them as API.
package qhelp

import "github.com/xraph/dql/dsl"

// HasExprWhere reports whether a WHERE tree contains an expression condition
// anywhere, which forces in-memory evaluation instead of push-down.
func HasExprWhere(w *dsl.WhereClause) bool {
	if w == nil {
		return false
	}
	if w.IsExpr() {
		return true
	}
	for i := range w.And {
		if HasExprWhere(&w.And[i]) {
			return true
		}
	}
	for i := range w.Or {
		if HasExprWhere(&w.Or[i]) {
			return true
		}
	}
	return HasExprWhere(w.Not)
}

// ContainsAny reports whether slice holds any of values.
func ContainsAny(slice []string, values ...string) bool {
	for _, v := range values {
		for _, s := range slice {
			if s == v {
				return true
			}
		}
	}
	return false
}
