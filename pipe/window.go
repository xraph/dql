package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/xraph/dql/dsl"
)

// WindowConfig computes a per-row value within a partition, similar to SQL
// window functions. Supported functions:
//
//	row_number  — 1-based index within partition (no ties).
//	rank        — same OrderBy values get the same rank; gaps after ties.
//	dense_rank  — like rank but no gaps.
//	lag         — value of Field N rows back (Offset, default 1).
//	lead        — value of Field N rows ahead.
//	first_value — first row's Field value in the partition.
//	last_value  — last row's Field value in the partition.
//
// Rows are emitted in the original input order — the window function does
// not re-order rows. Use a subsequent sort op if a sorted output is desired.
type WindowConfig struct {
	Fn          string              `json:"fn"`
	PartitionBy []string            `json:"partitionBy,omitempty"`
	OrderBy     []dsl.OrderByClause `json:"orderBy,omitempty"`
	Field       string              `json:"field,omitempty"`
	Offset      int                 `json:"offset,omitempty"`
	As          string              `json:"as"`
	// Default supplies a value when lag/lead reaches off the partition edge.
	// nil → null/missing.
	Default any `json:"default,omitempty"`
}

type windowOp struct {
	cfg WindowConfig
}

func (o *windowOp) Name() string     { return "window" }
func (o *windowOp) IsLiveSafe() bool { return true }

func (o *windowOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(in) == 0 {
		return in, nil
	}
	// Wrap rows with their original index so we can re-emit in input order.
	type indexed struct {
		idx int
		row dsl.Row
	}
	wrapped := make([]indexed, len(in))
	for i, r := range in {
		wrapped[i] = indexed{idx: i, row: r}
	}

	// Group by partition key.
	groups := make(map[string][]int) // partition key → indices into wrapped
	keys := make([]string, 0)
	for i, w := range wrapped {
		k := groupKey(o.cfg.PartitionBy, w.row)
		if _, seen := groups[k]; !seen {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], i)
	}

	// Within each group, sort indices by OrderBy and compute the function.
	values := make([]any, len(in)) // value to assign to wrapped[i].row[As]

	// Read the sort fields out of every row once, rather than once per
	// comparison — see orderSpec.
	spec := newOrderSpec(o.cfg.OrderBy)
	sortKeys := spec.keys(in)

	for _, k := range keys {
		idxs := groups[k]
		// Ties fall back to the original index, so this matches the stable sort
		// this replaced.
		if !spec.empty() {
			sort.Slice(idxs, func(a, b int) bool {
				ia, ib := idxs[a], idxs[b]
				if c := spec.compare(sortKeys[ia], sortKeys[ib]); c != 0 {
					return c < 0
				}
				return ia < ib
			})
		}

		switch strings.ToLower(o.cfg.Fn) {
		case "row_number":
			for rank, idx := range idxs {
				values[idx] = rank + 1
			}
		case "rank":
			rank := 0
			lastKey := ""
			for i, idx := range idxs {
				curKey := orderKey(wrapped[idx].row, o.cfg.OrderBy)
				if i == 0 || curKey != lastKey {
					rank = i + 1
					lastKey = curKey
				}
				values[idx] = rank
			}
		case "dense_rank":
			rank := 0
			lastKey := ""
			for i, idx := range idxs {
				curKey := orderKey(wrapped[idx].row, o.cfg.OrderBy)
				if i == 0 || curKey != lastKey {
					rank++
					lastKey = curKey
				}
				values[idx] = rank
			}
		case "lag":
			off := o.cfg.Offset
			if off <= 0 {
				off = 1
			}
			for i, idx := range idxs {
				if i-off >= 0 {
					values[idx] = wrapped[idxs[i-off]].row[o.cfg.Field]
				} else {
					values[idx] = o.cfg.Default
				}
			}
		case "lead":
			off := o.cfg.Offset
			if off <= 0 {
				off = 1
			}
			for i, idx := range idxs {
				if i+off < len(idxs) {
					values[idx] = wrapped[idxs[i+off]].row[o.cfg.Field]
				} else {
					values[idx] = o.cfg.Default
				}
			}
		case "first_value":
			if len(idxs) > 0 {
				v := wrapped[idxs[0]].row[o.cfg.Field]
				for _, idx := range idxs {
					values[idx] = v
				}
			}
		case "last_value":
			if len(idxs) > 0 {
				v := wrapped[idxs[len(idxs)-1]].row[o.cfg.Field]
				for _, idx := range idxs {
					values[idx] = v
				}
			}
		default:
			return nil, fmt.Errorf("window: unknown function %q", o.cfg.Fn)
		}
	}

	// Write computed values back into rows — re-using the input slice, in
	// original order.
	for i, w := range wrapped {
		w.row[o.cfg.As] = values[i]
	}
	return in, nil
}

// orderKey returns a stable string key for an OrderBy tuple — used by rank
// and dense_rank to detect ties.
func orderKey(r dsl.Row, order []dsl.OrderByClause) string {
	parts := make([]string, 0, len(order))
	for _, ob := range order {
		if ob.Field == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%v", r[ob.Field]))
	}
	return strings.Join(parts, "\x00")
}

func windowFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg WindowConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("window: decode config: %w", err)
	}
	if cfg.As == "" {
		return nil, fmt.Errorf("window: as is required")
	}
	switch strings.ToLower(cfg.Fn) {
	case "row_number", "rank", "dense_rank":
		if len(cfg.OrderBy) == 0 {
			return nil, fmt.Errorf("window %s: orderBy is required", cfg.Fn)
		}
	case "lag", "lead":
		if cfg.Field == "" {
			return nil, fmt.Errorf("window %s: field is required", cfg.Fn)
		}
		if len(cfg.OrderBy) == 0 {
			return nil, fmt.Errorf("window %s: orderBy is required", cfg.Fn)
		}
	case "first_value", "last_value":
		if cfg.Field == "" {
			return nil, fmt.Errorf("window %s: field is required", cfg.Fn)
		}
	case "":
		return nil, fmt.Errorf("window: fn is required")
	default:
		return nil, fmt.Errorf("window: unknown function %q", cfg.Fn)
	}
	return &windowOp{cfg: cfg}, nil
}

func init() { Register("window", windowFactory) }
