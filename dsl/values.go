package dsl

// ToSlice normalizes the concrete slice types a decoded query document can
// yield into []any, reporting whether v was a slice at all.
//
// A document decoded from JSON gives []any, but one built in Go — or decoded
// from YAML — can give []string, []float64 or []int. Operators that accept a
// list of values need all of them to look the same.
func ToSlice(v any) ([]any, bool) {
	switch val := v.(type) {
	case []any:
		return val, true
	case []string:
		out := make([]any, len(val))
		for i, s := range val {
			out[i] = s
		}
		return out, true
	case []float64:
		out := make([]any, len(val))
		for i, f := range val {
			out[i] = f
		}
		return out, true
	case []int:
		out := make([]any, len(val))
		for i, n := range val {
			out[i] = n
		}
		return out, true
	default:
		return nil, false
	}
}
