package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// SortConfig orders rows by one or more fields.
type SortConfig struct {
	By []dsl.OrderByClause `json:"by"`
}

type sortOp struct {
	cfg SortConfig
}

func (o *sortOp) Name() string     { return "sort" }
func (o *sortOp) IsLiveSafe() bool { return true }

func (o *sortOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	spec := newOrderSpec(o.cfg.By)
	if spec.empty() || len(in) < 2 {
		return in, nil
	}
	// Read each row's sort fields once rather than on every comparison, and
	// order a permutation whose ties fall back to the original index — which
	// reproduces the stable sort this replaced. See orderSpec.
	perm := spec.sortPermRows(in)
	sorted := make([]dsl.Row, len(in))
	for i, p := range perm {
		sorted[i] = in[p]
	}
	// Write back into the caller's slice: sortOp has always reordered its input
	// in place and returned it, and callers rely on both.
	copy(in, sorted)
	return in, nil
}

func sortFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg SortConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("sort: decode config: %w", err)
	}
	if len(cfg.By) == 0 {
		return nil, fmt.Errorf("sort: by is required and must be non-empty")
	}
	for i, ob := range cfg.By {
		if ob.Field == "" && ob.Expr == "" {
			return nil, fmt.Errorf("sort.by[%d]: field or expr is required", i)
		}
	}
	return &sortOp{cfg: cfg}, nil
}

// LimitConfig caps the row count.
type LimitConfig struct {
	N int `json:"n"`
}

type limitOp struct {
	n int
}

func (o *limitOp) Name() string     { return "limit" }
func (o *limitOp) IsLiveSafe() bool { return true }

func (o *limitOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if o.n <= 0 || len(in) <= o.n {
		return in, nil
	}
	return in[:o.n], nil
}

func limitFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg LimitConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("limit: decode config: %w", err)
	}
	if cfg.N < 0 {
		return nil, fmt.Errorf("limit: n must be non-negative")
	}
	return &limitOp{n: cfg.N}, nil
}

// SkipConfig drops the first N rows.
type SkipConfig struct {
	N int `json:"n"`
}

type skipOp struct {
	n int
}

func (o *skipOp) Name() string     { return "skip" }
func (o *skipOp) IsLiveSafe() bool { return true }

func (o *skipOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if o.n <= 0 {
		return in, nil
	}
	if o.n >= len(in) {
		return nil, nil
	}
	return in[o.n:], nil
}

func skipFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg SkipConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("skip: decode config: %w", err)
	}
	if cfg.N < 0 {
		return nil, fmt.Errorf("skip: n must be non-negative")
	}
	return &skipOp{n: cfg.N}, nil
}

func init() {
	Register("sort", sortFactory)
	Register("limit", limitFactory)
	Register("skip", skipFactory)
}
