package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// --- intersect ---

// IntersectConfig keeps rows present in EVERY source pipe, identified by
// the configured keys.
//
//	sources: list of sub-pipes (≥ 2)
//	by:      identity columns (when empty, full-row equality is used)
//
// The first source's rows are emitted (preserving order); rows whose key is
// missing from any other source are dropped.
type IntersectConfig struct {
	Sources []MergeSource `json:"sources"`
	By      []string      `json:"by,omitempty"`
}

type intersectOp struct {
	cfg    IntersectConfig
	subOps [][]Operator
}

func (o *intersectOp) Name() string { return "intersect" }

func (o *intersectOp) IsLiveSafe() bool {
	for _, sub := range o.subOps {
		for _, op := range sub {
			if !op.IsLiveSafe() {
				return false
			}
		}
	}
	return true
}

func (o *intersectOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(o.subOps) == 0 {
		return in, nil
	}
	results := make([][]dsl.Row, len(o.subOps))
	for i, sub := range o.subOps {
		input := cloneRows(in)
		rows, err := applyChain(ctx, sub, input)
		if err != nil {
			return nil, fmt.Errorf("intersect.sources[%d]: %w", i, err)
		}
		results[i] = rows
	}
	keysPerSource := make([]map[string]struct{}, len(results))
	for i, rs := range results {
		set := make(map[string]struct{}, len(rs))
		for _, r := range rs {
			set[intersectKey(r, o.cfg.By)] = struct{}{}
		}
		keysPerSource[i] = set
	}
	out := make([]dsl.Row, 0, len(results[0]))
	for _, row := range results[0] {
		k := intersectKey(row, o.cfg.By)
		present := true
		for i := 1; i < len(keysPerSource); i++ {
			if _, ok := keysPerSource[i][k]; !ok {
				present = false
				break
			}
		}
		if present {
			out = append(out, row)
		}
	}
	return out, nil
}

func intersectKey(row dsl.Row, by []string) string {
	if len(by) > 0 {
		return groupKey(by, row)
	}
	// Full-row equality: hash all key=value pairs in sorted key order.
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return groupKey(keys, row)
}

func intersectFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg IntersectConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("intersect: decode config: %w", err)
	}
	if len(cfg.Sources) < 2 {
		return nil, fmt.Errorf("intersect: at least two sources required")
	}
	subs := make([][]Operator, len(cfg.Sources))
	for i, src := range cfg.Sources {
		ops, err := buildOps(src.Pipe, octx)
		if err != nil {
			return nil, fmt.Errorf("intersect.sources[%d]: %w", i, err)
		}
		subs[i] = ops
	}
	return &intersectOp{cfg: cfg, subOps: subs}, nil
}

// --- except ---

// ExceptConfig emits rows from `left` whose key is not present in `right`.
//
//	left, right: sub-pipes
//	by:          identity columns (empty = full-row equality)
type ExceptConfig struct {
	Left  MergeSource `json:"left"`
	Right MergeSource `json:"right"`
	By    []string    `json:"by,omitempty"`
}

type exceptOp struct {
	cfg      ExceptConfig
	leftOps  []Operator
	rightOps []Operator
}

func (o *exceptOp) Name() string { return "except" }

func (o *exceptOp) IsLiveSafe() bool {
	for _, op := range o.leftOps {
		if !op.IsLiveSafe() {
			return false
		}
	}
	for _, op := range o.rightOps {
		if !op.IsLiveSafe() {
			return false
		}
	}
	return true
}

func (o *exceptOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	leftIn := cloneRows(in)
	left, err := applyChain(ctx, o.leftOps, leftIn)
	if err != nil {
		return nil, fmt.Errorf("except.left: %w", err)
	}
	rightIn := cloneRows(in)
	right, err := applyChain(ctx, o.rightOps, rightIn)
	if err != nil {
		return nil, fmt.Errorf("except.right: %w", err)
	}
	rightSet := make(map[string]struct{}, len(right))
	for _, r := range right {
		rightSet[intersectKey(r, o.cfg.By)] = struct{}{}
	}
	out := make([]dsl.Row, 0, len(left))
	for _, row := range left {
		if _, found := rightSet[intersectKey(row, o.cfg.By)]; !found {
			out = append(out, row)
		}
	}
	return out, nil
}

func exceptFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg ExceptConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("except: decode config: %w", err)
	}
	if len(cfg.Left.Pipe) == 0 || len(cfg.Right.Pipe) == 0 {
		return nil, fmt.Errorf("except: left.pipe and right.pipe are required")
	}
	leftOps, err := buildOps(cfg.Left.Pipe, octx)
	if err != nil {
		return nil, fmt.Errorf("except.left: %w", err)
	}
	rightOps, err := buildOps(cfg.Right.Pipe, octx)
	if err != nil {
		return nil, fmt.Errorf("except.right: %w", err)
	}
	return &exceptOp{cfg: cfg, leftOps: leftOps, rightOps: rightOps}, nil
}

// --- crossJoin ---

// CrossJoinConfig produces the cartesian product of the input rows with
// rows from another dataset. Use sparingly — output size is N×M.
//
//	dataset:    right-side dataset
//	as:         optional prefix for the right-side columns
//	select:     subset of right-side columns to include
//	where, limit: standard right-side filters
type CrossJoinConfig struct {
	Dataset string           `json:"dataset"`
	As      string           `json:"as,omitempty"`
	Select  []string         `json:"select,omitempty"`
	Where   *dsl.WhereClause `json:"where,omitempty"`
	Limit   *int             `json:"limit,omitempty"`
}

type crossJoinOp struct {
	cfg     CrossJoinConfig
	classic ClassicExecutor
}

func (o *crossJoinOp) Name() string     { return "crossJoin" }
func (o *crossJoinOp) IsLiveSafe() bool { return true }

func (o *crossJoinOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	ws, proj := scopeFrom(ctx)
	if ws == "" {
		return nil, fmt.Errorf("crossJoin: workspace not set in context")
	}
	rightQ := &dsl.QueryDSL{
		From:      dsl.FromClause{Dataset: o.cfg.Dataset},
		ProjectID: proj,
		Where:     o.cfg.Where,
		Limit:     o.cfg.Limit,
	}
	rightRes, err := o.classic.Execute(ctx, rightQ, ws, proj)
	if err != nil {
		return nil, fmt.Errorf("crossJoin %s: fetch right: %w", o.cfg.Dataset, err)
	}
	out := make([]dsl.Row, 0, len(in)*len(rightRes.Rows))
	for _, l := range in {
		for _, r := range rightRes.Rows {
			out = append(out, mergeLookup(l, r, LookupConfig{As: o.cfg.As, Select: o.cfg.Select}))
		}
	}
	return out, nil
}

func crossJoinFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg CrossJoinConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("crossJoin: decode config: %w", err)
	}
	if cfg.Dataset == "" {
		return nil, fmt.Errorf("crossJoin: dataset is required")
	}
	if octx == nil || octx.Classic == nil {
		return nil, fmt.Errorf("crossJoin: classic executor not available")
	}
	return &crossJoinOp{cfg: cfg, classic: octx.Classic}, nil
}

func init() {
	Register("intersect", intersectFactory)
	Register("except", exceptFactory)
	Register("crossJoin", crossJoinFactory)
}
