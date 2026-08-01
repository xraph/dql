package pipe

import (
	"sort"

	"github.com/xraph/dql/dsl"
)

// deriveColumnsFromRows rebuilds a ColumnInfo slice from the actual returned
// rows. Names come from the union of row keys; types are inferred from the
// first non-null value seen for each name; Source is "computed" because the
// in-memory tail may have produced or mutated columns.
//
// Column ordering is alphabetical for determinism — Go map iteration is
// randomised, and threading author-intended order through every operator is
// out of scope for v1. Pipes that need a specific order can finish with a
// `project` stage and clients can read the project config back from the
// query plan.
func deriveColumnsFromRows(rows []dsl.Row) []dsl.ColumnInfo {
	if len(rows) == 0 {
		return nil
	}
	seen := make(map[string]any, len(rows[0]))
	for _, r := range rows {
		for k, v := range r {
			cur, ok := seen[k]
			if !ok || (cur == nil && v != nil) {
				seen[k] = v
			}
		}
	}
	names := make([]string, 0, len(seen))
	for k := range seen {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]dsl.ColumnInfo, 0, len(names))
	for _, name := range names {
		out = append(out, dsl.ColumnInfo{
			Name:   name,
			Type:   inferColumnType(seen[name]),
			Source: "computed",
		})
	}
	return out
}

// inferColumnType maps a Go runtime value to one of the ColumnInfo type
// strings used by the classic engine. Returns "" when the type can't be
// determined (e.g. nil, or a custom struct).
func inferColumnType(v any) string {
	switch v.(type) {
	case nil:
		return ""
	case bool:
		return "bool"
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return ""
	}
}
