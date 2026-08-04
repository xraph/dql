package pipe

import (
	"context"
	"fmt"
	"sort"
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

// orderSpec is a precompiled OrderBy list.
//
// Sorting rows through a comparator that reads fields straight out of the row
// map costs two map lookups per comparison, and a comparison sort calls the
// comparator O(n log n) times. Profiling `window` at 10k rows put
// runtime.mapaccess1_faststr plus string hashing at ~35% of total time, with a
// further 4.7% in strings.ToLower re-lowering a constant direction on every
// comparison.
//
// orderSpec lifts both out: directions resolve once at build time, and keys
// reads each row's sort fields once, making field access O(n) instead of
// O(n log n).
type orderSpec struct {
	fields []string
	desc   []bool
}

// newOrderSpec precompiles order. Clauses with no field are dropped here rather
// than skipped on every comparison, matching the old rowsLess behaviour of
// ignoring them.
func newOrderSpec(order []dsl.OrderByClause) orderSpec {
	s := orderSpec{
		fields: make([]string, 0, len(order)),
		desc:   make([]bool, 0, len(order)),
	}
	for _, ob := range order {
		if ob.Field == "" {
			continue
		}
		s.fields = append(s.fields, ob.Field)
		s.desc = append(s.desc, strings.EqualFold(ob.Dir, "desc"))
	}
	return s
}

// empty reports whether this spec would order nothing. Callers skip sorting
// entirely, which matches the old behaviour: a comparator that always returned
// false left a stable sort's input untouched.
func (s orderSpec) empty() bool { return len(s.fields) == 0 }

// keys reads the sort fields out of every row once. One flat backing array
// keeps this to two allocations rather than one per row.
func (s orderSpec) keys(rows []dsl.Row) [][]any {
	w := len(s.fields)
	if w == 0 {
		return nil
	}
	flat := make([]any, len(rows)*w)
	out := make([][]any, len(rows))
	for i, r := range rows {
		k := flat[i*w : (i+1)*w : (i+1)*w]
		for j, f := range s.fields {
			k[j] = r[f]
		}
		out[i] = k
	}
	return out
}

// compare orders two pre-extracted key tuples, returning a value with the same
// sign convention as compareValues.
func (s orderSpec) compare(a, b []any) int {
	for j := range s.fields {
		c := compareValues(a[j], b[j])
		if c == 0 {
			continue
		}
		if s.desc[j] {
			return -c
		}
		return c
	}
	return 0
}

// sortPerm returns the row indices ordered by this spec. Ties fall back to the
// original index, which makes an unstable sort.Slice produce exactly what
// sort.SliceStable did — without SliceStable's symMerge rotations, whose swap
// count carries an extra log factor.
func (s orderSpec) sortPerm(keys [][]any, n int) []int {
	perm := newPerm(n)
	sort.Slice(perm, func(a, b int) bool {
		ia, ib := perm[a], perm[b]
		if c := s.compare(keys[ia], keys[ib]); c != 0 {
			return c < 0
		}
		return ia < ib
	})
	return perm
}

func newPerm(n int) []int {
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	return perm
}

// sortPermRows orders whole rows by this spec, extracting keys itself.
//
// The single-field case gets a flat []any rather than the general [][]any.
// That matters: a slice-of-slices costs a 24-byte header per row, which was the
// largest single component of the extra memory this approach introduces, and
// ordering by one field is overwhelmingly the common case.
//
// Callers that sort index subsets of a larger set (window, fillNulls) cannot
// use this — they extract keys once for every row and sort per group.
func (s orderSpec) sortPermRows(rows []dsl.Row) []int {
	perm := newPerm(len(rows))
	if len(s.fields) == 1 {
		field, desc := s.fields[0], s.desc[0]
		keys := make([]any, len(rows))
		for i, r := range rows {
			keys[i] = r[field]
		}
		sort.Slice(perm, func(a, b int) bool {
			ia, ib := perm[a], perm[b]
			c := compareValues(keys[ia], keys[ib])
			if c == 0 {
				return ia < ib
			}
			if desc {
				return c > 0
			}
			return c < 0
		})
		return perm
	}
	return s.sortPerm(s.keys(rows), len(rows))
}
