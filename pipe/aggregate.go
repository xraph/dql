package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xraph/dql/dsl"
)

// GroupByConfig records the grouping keys for the immediately-following
// aggregate stage. On its own groupBy is a pass-through — it reshapes no rows.
// The pipe executor pairs each groupBy with the next aggregate.
type GroupByConfig struct {
	Keys []string `json:"keys"`
}

type groupByOp struct {
	keys []string
}

func (o *groupByOp) Name() string     { return "groupBy" }
func (o *groupByOp) IsLiveSafe() bool { return true }

// Keys returns the configured grouping keys.
func (o *groupByOp) Keys() []string { return o.keys }

// Apply is a pass-through. The downstream aggregate op consumes the keys via
// the planner's stage fusion logic (see planner.go).
func (o *groupByOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	return in, nil
}

func groupByFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg GroupByConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("groupBy: decode config: %w", err)
	}
	if len(cfg.Keys) == 0 {
		return nil, fmt.Errorf("groupBy: keys is required and must be non-empty")
	}
	return &groupByOp{keys: cfg.Keys}, nil
}

// AggregateConfig folds groups into aggregated rows. When no prior groupBy op
// has set keys, aggregation collapses the input to a single row.
type AggregateConfig struct {
	Keys []string              `json:"keys,omitempty"`
	Aggs []dsl.AggregateClause `json:"aggs"`
}

type aggregateOp struct {
	cfg AggregateConfig
}

func (o *aggregateOp) Name() string     { return "aggregate" }
func (o *aggregateOp) IsLiveSafe() bool { return true }

// WithKeys returns a clone of the op with the given keys attached. Used by the
// planner when a preceding groupBy supplies the keys.
func (o *aggregateOp) WithKeys(keys []string) *aggregateOp {
	clone := *o
	clone.cfg.Keys = keys
	return &clone
}

func (o *aggregateOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(o.cfg.Keys) == 0 {
		result := make(dsl.Row, len(o.cfg.Aggs))
		for _, agg := range o.cfg.Aggs {
			result[agg.As] = computeAggregate(agg, in)
		}
		return []dsl.Row{result}, nil
	}

	groups := make(map[string][]dsl.Row)
	order := make([]string, 0)
	for _, row := range in {
		k := groupKey(o.cfg.Keys, row)
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], row)
	}
	out := make([]dsl.Row, 0, len(groups))
	for _, k := range order {
		gr := groups[k]
		row := make(dsl.Row, len(o.cfg.Keys)+len(o.cfg.Aggs))
		for _, key := range o.cfg.Keys {
			row[key] = gr[0][key]
		}
		for _, agg := range o.cfg.Aggs {
			row[agg.As] = computeAggregate(agg, gr)
		}
		out = append(out, row)
	}
	return out, nil
}

func aggregateFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg AggregateConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("aggregate: decode config: %w", err)
	}
	if len(cfg.Aggs) == 0 {
		return nil, fmt.Errorf("aggregate: aggs is required and must be non-empty")
	}
	for i, a := range cfg.Aggs {
		if a.Fn == "" {
			return nil, fmt.Errorf("aggregate.aggs[%d]: fn is required", i)
		}
		fn := strings.ToUpper(a.Fn)
		if !dsl.ValidAggregateFns[fn] {
			return nil, fmt.Errorf("aggregate.aggs[%d]: unknown function %q", i, a.Fn)
		}
		if a.As == "" {
			return nil, fmt.Errorf("aggregate.aggs[%d]: as is required", i)
		}
		cfg.Aggs[i].Fn = fn
	}
	return &aggregateOp{cfg: cfg}, nil
}

func init() {
	Register("groupBy", groupByFactory)
	Register("aggregate", aggregateFactory)
}

// computeAggregate runs a single aggregate function over a row group. It mirrors
// the classic processor's computeAggregate to keep semantics identical.
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
		m := toFloat(rows[0][agg.Field])
		for _, row := range rows[1:] {
			v := toFloat(row[agg.Field])
			if v < m {
				m = v
			}
		}
		return m
	case "MAX":
		if len(rows) == 0 {
			return nil
		}
		m := toFloat(rows[0][agg.Field])
		for _, row := range rows[1:] {
			v := toFloat(row[agg.Field])
			if v > m {
				m = v
			}
		}
		return m
	default:
		return nil
	}
}
