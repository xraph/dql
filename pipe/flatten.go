package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// FlattenConfig expands an array column into one row per element.
//
//	Field        — required, the array column to flatten.
//	As           — optional output key. When set, the element value is stored
//	               under row[As] and the original Field column is preserved.
//	               When empty, the original Field column is overwritten with
//	               the element value.
//	IndexAs      — optional; records the element's index within its source array.
//	PreserveEmpty — when true, rows with an empty / missing / non-array value
//	               in Field pass through unchanged. Default false drops them.
type FlattenConfig struct {
	Field         string `json:"field"`
	As            string `json:"as,omitempty"`
	IndexAs       string `json:"indexAs,omitempty"`
	PreserveEmpty bool   `json:"preserveEmpty,omitempty"`
}

type flattenOp struct {
	cfg FlattenConfig
}

func (o *flattenOp) Name() string     { return "flatten" }
func (o *flattenOp) IsLiveSafe() bool { return true }

func (o *flattenOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	out := make([]dsl.Row, 0, len(in))
	for _, row := range in {
		raw, ok := row[o.cfg.Field]
		items, convOk := flattenItems(raw)
		if !ok || !convOk || len(items) == 0 {
			if o.cfg.PreserveEmpty {
				out = append(out, row)
			}
			continue
		}
		for idx, item := range items {
			dup := make(dsl.Row, len(row)+1)
			for k, v := range row {
				dup[k] = v
			}
			if o.cfg.As != "" {
				dup[o.cfg.As] = item
			} else {
				dup[o.cfg.Field] = item
			}
			if o.cfg.IndexAs != "" {
				dup[o.cfg.IndexAs] = idx
			}
			out = append(out, dup)
		}
	}
	return out, nil
}

// flattenItems coerces a value into a slice of element values. Returns the
// slice and a bool indicating whether the value was recognised as an array.
// Scalar values return (nil, false).
func flattenItems(v any) ([]any, bool) {
	switch s := v.(type) {
	case []any:
		return s, true
	case []string:
		out := make([]any, len(s))
		for i, x := range s {
			out[i] = x
		}
		return out, true
	case []int:
		out := make([]any, len(s))
		for i, x := range s {
			out[i] = x
		}
		return out, true
	case []float64:
		out := make([]any, len(s))
		for i, x := range s {
			out[i] = x
		}
		return out, true
	case []map[string]any:
		out := make([]any, len(s))
		for i, x := range s {
			out[i] = x
		}
		return out, true
	default:
		return nil, false
	}
}

func flattenFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg FlattenConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("flatten: decode config: %w", err)
	}
	if cfg.Field == "" {
		return nil, fmt.Errorf("flatten: field is required")
	}
	return &flattenOp{cfg: cfg}, nil
}

func init() { Register("flatten", flattenFactory) }
