package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xraph/dql/dsl"
)

// DistinctConfig deduplicates rows. When By is empty, uniqueness uses all
// columns; otherwise it uses only the named columns.
type DistinctConfig struct {
	By []string `json:"by,omitempty"`
}

type distinctOp struct {
	cfg DistinctConfig
}

func (o *distinctOp) Name() string     { return "distinct" }
func (o *distinctOp) IsLiveSafe() bool { return true }

func (o *distinctOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(in) == 0 {
		return in, nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]dsl.Row, 0, len(in))
	for _, row := range in {
		key := o.key(row)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
	}
	return out, nil
}

func (o *distinctOp) key(row dsl.Row) string {
	if len(o.cfg.By) > 0 {
		return groupKey(o.cfg.By, row)
	}
	// Stable key across all fields — sort column names so row-order doesn't affect hashing.
	cols := make([]string, 0, len(row))
	for c := range row {
		cols = append(cols, c)
	}
	// Manual insertion sort (rows are small; avoids a sort import here).
	for i := 1; i < len(cols); i++ {
		for j := i; j > 0 && cols[j-1] > cols[j]; j-- {
			cols[j-1], cols[j] = cols[j], cols[j-1]
		}
	}
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		parts = append(parts, c+"=")
		parts = append(parts, fmt.Sprintf("%v", row[c]))
	}
	return strings.Join(parts, "\x00")
}

func distinctFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg DistinctConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("distinct: decode config: %w", err)
	}
	return &distinctOp{cfg: cfg}, nil
}

func init() { Register("distinct", distinctFactory) }
