package pipe

import (
	"context"
	"fmt"
	"strings"

	"github.com/xraph/dql/dsl"
)

// evalWhere evaluates a WHERE clause tree against a single row. It mirrors the
// classic processor's evalWhereExpr so pipe-mode filter behaviour is identical.
func evalWhere(ctx context.Context, w *dsl.WhereClause, row dsl.Row, eval ExprEvaluator) (bool, error) {
	if w == nil {
		return true, nil
	}
	if w.IsExpr() {
		if eval == nil {
			return false, fmt.Errorf("expression evaluator not available")
		}
		val, err := eval.Eval(ctx, w.Expr, row)
		if err != nil {
			return false, err
		}
		return toBool(val), nil
	}
	if w.IsSimple() {
		return evalSimple(w, row), nil
	}
	if len(w.And) > 0 {
		for i := range w.And {
			ok, err := evalWhere(ctx, &w.And[i], row, eval)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	if len(w.Or) > 0 {
		for i := range w.Or {
			ok, err := evalWhere(ctx, &w.Or[i], row, eval)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	if w.Not != nil {
		ok, err := evalWhere(ctx, w.Not, row, eval)
		if err != nil {
			return false, err
		}
		return !ok, nil
	}
	return true, nil
}

func evalSimple(w *dsl.WhereClause, row dsl.Row) bool {
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
		s := fmt.Sprintf("%v", val)
		pattern := fmt.Sprintf("%v", w.Value)
		pattern = strings.ReplaceAll(pattern, "%", "")
		match := strings.Contains(strings.ToLower(s), strings.ToLower(pattern))
		if w.Op == "not_like" {
			return !match
		}
		return match
	case "in":
		return valueIn(val, w.Value)
	case "not_in":
		return !valueIn(val, w.Value)
	default:
		return false
	}
}

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
	af := toFloat(a)
	bf := toFloat(b)
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
	as := fmt.Sprintf("%v", a)
	bs := fmt.Sprintf("%v", b)
	return strings.Compare(as, bs)
}

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

func isNumeric(v any) bool {
	switch v.(type) {
	case float64, float32, int, int64, int32, int16, int8, uint, uint64, uint32:
		return true
	default:
		return false
	}
}

func valueIn(val, list any) bool {
	arr, ok := toSlice(list)
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

func toSlice(v any) ([]any, bool) {
	switch s := v.(type) {
	case []any:
		return s, true
	case []string:
		out := make([]any, len(s))
		for i, v := range s {
			out[i] = v
		}
		return out, true
	case []int:
		out := make([]any, len(s))
		for i, v := range s {
			out[i] = v
		}
		return out, true
	case []float64:
		out := make([]any, len(s))
		for i, v := range s {
			out[i] = v
		}
		return out, true
	default:
		return nil, false
	}
}

func groupKey(groupBy []string, row dsl.Row) string {
	parts := make([]string, 0, len(groupBy))
	for _, col := range groupBy {
		parts = append(parts, fmt.Sprintf("%v", row[col]))
	}
	return strings.Join(parts, "\x00")
}
