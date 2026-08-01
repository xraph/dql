package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/xraph/dql/dsl"
)

// TopPerGroupConfig keeps the top N rows per partition by an order-by clause.
//
//	n:           rows to keep per group (required)
//	by:          order-by spec (required)
//	partitionBy: optional grouping keys; empty = single global group (= top N overall)
type TopPerGroupConfig struct {
	N           int                 `json:"n"`
	By          []dsl.OrderByClause `json:"by"`
	PartitionBy []string            `json:"partitionBy,omitempty"`
}

type topPerGroupOp struct {
	cfg TopPerGroupConfig
}

func (o *topPerGroupOp) Name() string     { return "topPerGroup" }
func (o *topPerGroupOp) IsLiveSafe() bool { return true }

func (o *topPerGroupOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if o.cfg.N <= 0 || len(in) == 0 {
		return in[:0:0], nil
	}
	groups := make(map[string][]dsl.Row)
	order := make([]string, 0)
	for _, row := range in {
		k := groupKey(o.cfg.PartitionBy, row)
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], row)
	}
	out := make([]dsl.Row, 0, len(in))
	for _, k := range order {
		bucket := groups[k]
		sort.SliceStable(bucket, func(i, j int) bool {
			return rowsLess(bucket[i], bucket[j], o.cfg.By)
		})
		n := o.cfg.N
		if n > len(bucket) {
			n = len(bucket)
		}
		out = append(out, bucket[:n]...)
	}
	return out, nil
}

func topPerGroupFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg TopPerGroupConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("topPerGroup: decode config: %w", err)
	}
	if cfg.N <= 0 {
		return nil, fmt.Errorf("topPerGroup: n must be positive")
	}
	if len(cfg.By) == 0 {
		return nil, fmt.Errorf("topPerGroup: by is required")
	}
	for i, ob := range cfg.By {
		if ob.Field == "" {
			return nil, fmt.Errorf("topPerGroup.by[%d]: field is required", i)
		}
	}
	return &topPerGroupOp{cfg: cfg}, nil
}

func init() { Register("topPerGroup", topPerGroupFactory) }
