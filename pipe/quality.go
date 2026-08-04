package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xraph/dql/dsl"
)

// --- dropNulls ---

// DropNullsConfig drops rows containing nulls in named columns.
//
//	columns: which to check (empty = all columns of the row)
//	any:     drop when ANY listed column is null (default true).
//	         When false, drop only when EVERY listed column is null.
type DropNullsConfig struct {
	Columns []string `json:"columns,omitempty"`
	Any     *bool    `json:"any,omitempty"`
}

type dropNullsOp struct {
	cfg DropNullsConfig
}

func (o *dropNullsOp) Name() string     { return "dropNulls" }
func (o *dropNullsOp) IsLiveSafe() bool { return true }

func (o *dropNullsOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	any := true
	if o.cfg.Any != nil {
		any = *o.cfg.Any
	}
	out := make([]dsl.Row, 0, len(in))
	for _, row := range in {
		cols := o.cfg.Columns
		if len(cols) == 0 {
			cols = make([]string, 0, len(row))
			for k := range row {
				cols = append(cols, k)
			}
		}
		nullCount := 0
		for _, c := range cols {
			if v, exists := row[c]; !exists || v == nil {
				nullCount++
			}
		}
		if any && nullCount > 0 {
			continue
		}
		if !any && nullCount == len(cols) {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func dropNullsFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg DropNullsConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("dropNulls: decode config: %w", err)
	}
	return &dropNullsOp{cfg: cfg}, nil
}

// --- fillNulls ---

// FillNullsConfig replaces nulls in named columns. Methods:
//
//	value      — set to Value (per-column or scalar)
//	zero       — set to 0
//	lastValue  — carry the previous non-null value (within partition)
//	nextValue  — carry the next non-null value (within partition)
//	mean       — replace with the mean of non-null values (numeric only)
//
// Forward/backward fill require partitionBy + orderBy for deterministic
// behaviour; without ordering they fall back to input row order.
type FillNullsConfig struct {
	Columns     []string            `json:"columns,omitempty"`
	Method      string              `json:"method,omitempty"`
	Value       any                 `json:"value,omitempty"`
	PartitionBy []string            `json:"partitionBy,omitempty"`
	OrderBy     []dsl.OrderByClause `json:"orderBy,omitempty"`
}

type fillNullsOp struct {
	cfg FillNullsConfig
}

func (o *fillNullsOp) Name() string     { return "fillNulls" }
func (o *fillNullsOp) IsLiveSafe() bool { return true }

func (o *fillNullsOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	method := o.cfg.Method
	if method == "" {
		method = "value"
	}
	cols := o.cfg.Columns
	if len(cols) == 0 && len(in) > 0 {
		cols = make([]string, 0, len(in[0]))
		for k := range in[0] {
			cols = append(cols, k)
		}
	}

	switch strings.ToLower(method) {
	case "value":
		for _, row := range in {
			for _, c := range cols {
				if v, exists := row[c]; !exists || v == nil {
					row[c] = o.cfg.Value
				}
			}
		}
		return in, nil
	case "zero":
		for _, row := range in {
			for _, c := range cols {
				if v, exists := row[c]; !exists || v == nil {
					row[c] = 0
				}
			}
		}
		return in, nil
	case "mean":
		means := make(map[string]float64, len(cols))
		counts := make(map[string]int, len(cols))
		for _, row := range in {
			for _, c := range cols {
				if v, exists := row[c]; exists && v != nil && isNumeric(v) {
					means[c] += toFloat(v)
					counts[c]++
				}
			}
		}
		for _, row := range in {
			for _, c := range cols {
				if v, exists := row[c]; !exists || v == nil {
					if counts[c] > 0 {
						row[c] = means[c] / float64(counts[c])
					}
				}
			}
		}
		return in, nil
	case "lastvalue", "nextvalue":
		return o.fillCarry(in, cols, strings.ToLower(method) == "lastvalue")
	default:
		return nil, fmt.Errorf("fillNulls: unknown method %q", o.cfg.Method)
	}
}

func (o *fillNullsOp) fillCarry(in []dsl.Row, cols []string, forward bool) ([]dsl.Row, error) {
	groups := make(map[string][]int)
	order := make([]string, 0)
	for i, row := range in {
		k := groupKey(o.cfg.PartitionBy, row)
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}
	spec := newOrderSpec(o.cfg.OrderBy)
	sortKeys := spec.keys(in)
	for _, k := range order {
		idxs := groups[k]
		if !spec.empty() {
			// Ties fall back to the original index, matching the stable sort
			// this replaced.
			sort.Slice(idxs, func(a, b int) bool {
				ia, ib := idxs[a], idxs[b]
				if c := spec.compare(sortKeys[ia], sortKeys[ib]); c != 0 {
					return c < 0
				}
				return ia < ib
			})
		}
		walk := idxs
		if !forward {
			// reverse for nextValue
			walk = make([]int, len(idxs))
			for i, v := range idxs {
				walk[len(idxs)-1-i] = v
			}
		}
		last := make(map[string]any, len(cols))
		for _, idx := range walk {
			row := in[idx]
			for _, c := range cols {
				v, exists := row[c]
				if !exists || v == nil {
					if prev, ok := last[c]; ok {
						row[c] = prev
					}
				} else {
					last[c] = v
				}
			}
		}
	}
	return in, nil
}

func fillNullsFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg FillNullsConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("fillNulls: decode config: %w", err)
	}
	if cfg.Method != "" {
		switch strings.ToLower(cfg.Method) {
		case "value", "zero", "mean", "lastvalue", "nextvalue":
		default:
			return nil, fmt.Errorf("fillNulls: unknown method %q", cfg.Method)
		}
	}
	return &fillNullsOp{cfg: cfg}, nil
}

// --- cast ---

// CastConfig converts column values to a target type.
//
//	casts:   list of {field, to, onError}
//	onError: null | skip | fail (default null)
//	to:      int | float | bool | string | timestamp
type CastConfig struct {
	Casts []CastEntry `json:"casts"`
}

// CastEntry is one cast operation.
type CastEntry struct {
	Field   string `json:"field"`
	To      string `json:"to"`
	OnError string `json:"onError,omitempty"`
}

type castOp struct {
	cfg CastConfig
}

func (o *castOp) Name() string     { return "cast" }
func (o *castOp) IsLiveSafe() bool { return true }

func (o *castOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	out := make([]dsl.Row, 0, len(in))
	for i, row := range in {
		drop := false
		for _, c := range o.cfg.Casts {
			v, ok := row[c.Field]
			if !ok || v == nil {
				continue
			}
			converted, err := castValue(v, c.To)
			if err != nil {
				policy := c.OnError
				if policy == "" {
					policy = "null"
				}
				switch policy {
				case "null":
					row[c.Field] = nil
				case "skip":
					drop = true
				case "fail":
					return nil, fmt.Errorf("cast row %d field %q: %w", i, c.Field, err)
				default:
					return nil, fmt.Errorf("cast: unknown onError policy %q", policy)
				}
				if drop {
					break
				}
				continue
			}
			row[c.Field] = converted
		}
		if !drop {
			out = append(out, row)
		}
	}
	return out, nil
}

func castValue(v any, to string) (any, error) {
	switch strings.ToLower(to) {
	case "int":
		switch t := v.(type) {
		case int:
			return t, nil
		case int64:
			return t, nil
		case float64:
			return int64(t), nil
		case string:
			return strconv.ParseInt(t, 10, 64)
		case bool:
			if t {
				return int64(1), nil
			}
			return int64(0), nil
		}
	case "float":
		switch t := v.(type) {
		case float64:
			return t, nil
		case int:
			return float64(t), nil
		case int64:
			return float64(t), nil
		case string:
			return strconv.ParseFloat(t, 64)
		}
	case "bool":
		switch t := v.(type) {
		case bool:
			return t, nil
		case string:
			return strconv.ParseBool(t)
		case int:
			return t != 0, nil
		case int64:
			return t != 0, nil
		case float64:
			return t != 0, nil
		}
	case "string":
		return fmt.Sprintf("%v", v), nil
	case "timestamp":
		t, err := parseRowTime(v)
		if err != nil {
			return nil, err
		}
		return t.UTC().Format(time.RFC3339), nil
	}
	return nil, fmt.Errorf("cannot cast %T to %s", v, to)
}

func castFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg CastConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("cast: decode config: %w", err)
	}
	if len(cfg.Casts) == 0 {
		return nil, fmt.Errorf("cast: at least one cast entry required")
	}
	for i, c := range cfg.Casts {
		if c.Field == "" {
			return nil, fmt.Errorf("cast.casts[%d]: field is required", i)
		}
		switch strings.ToLower(c.To) {
		case "int", "float", "bool", "string", "timestamp":
		default:
			return nil, fmt.Errorf("cast.casts[%d]: unknown target type %q", i, c.To)
		}
	}
	return &castOp{cfg: cfg}, nil
}

// --- dedupe ---

// DedupeConfig keeps one row per identity key, picking first/last by an
// optional order. Different from `distinct`, which dedups by full-row
// equality.
//
//	by:      identity columns (required)
//	keep:    first | last (default first)
//	orderBy: optional ordering applied before picking
type DedupeConfig struct {
	By      []string            `json:"by"`
	Keep    string              `json:"keep,omitempty"`
	OrderBy []dsl.OrderByClause `json:"orderBy,omitempty"`
}

type dedupeOp struct {
	cfg DedupeConfig
}

func (o *dedupeOp) Name() string     { return "dedupe" }
func (o *dedupeOp) IsLiveSafe() bool { return true }

func (o *dedupeOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(in) == 0 {
		return in, nil
	}
	rows := in
	if spec := newOrderSpec(o.cfg.OrderBy); !spec.empty() {
		// Order a permutation, then materialise. Ties fall back to the original
		// index, matching the stable sort this replaced.
		perm := spec.sortPermRows(in)
		rows = make([]dsl.Row, len(in))
		for i, p := range perm {
			rows[i] = in[p]
		}
	}
	keepLast := strings.EqualFold(o.cfg.Keep, "last")
	seen := make(map[string]int, len(rows))
	out := make([]dsl.Row, 0, len(rows))
	for _, row := range rows {
		k := groupKey(o.cfg.By, row)
		if idx, exists := seen[k]; exists {
			if keepLast {
				out[idx] = row
			}
			continue
		}
		seen[k] = len(out)
		out = append(out, row)
	}
	return out, nil
}

func dedupeFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg DedupeConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("dedupe: decode config: %w", err)
	}
	if len(cfg.By) == 0 {
		return nil, fmt.Errorf("dedupe: by is required")
	}
	if cfg.Keep != "" && !strings.EqualFold(cfg.Keep, "first") && !strings.EqualFold(cfg.Keep, "last") {
		return nil, fmt.Errorf("dedupe: keep must be first or last")
	}
	return &dedupeOp{cfg: cfg}, nil
}

// --- sample ---

// SampleConfig draws a random subset of rows.
//
//	n:      target sample size (mutually exclusive with ratio)
//	ratio:  fraction in (0,1] (mutually exclusive with n)
//	seed:   optional seed for deterministic output (default time-based)
//	method: random (default) | systematic — every k-th row
type SampleConfig struct {
	N      int     `json:"n,omitempty"`
	Ratio  float64 `json:"ratio,omitempty"`
	Seed   int64   `json:"seed,omitempty"`
	Method string  `json:"method,omitempty"`
}

type sampleOp struct {
	cfg SampleConfig
}

func (o *sampleOp) Name() string     { return "sample" }
func (o *sampleOp) IsLiveSafe() bool { return true }

func (o *sampleOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(in) == 0 {
		return in, nil
	}
	target := o.cfg.N
	if target == 0 && o.cfg.Ratio > 0 {
		target = int(float64(len(in)) * o.cfg.Ratio)
		if target == 0 {
			target = 1
		}
	}
	if target <= 0 || target >= len(in) {
		return in, nil
	}
	switch strings.ToLower(o.cfg.Method) {
	case "systematic":
		step := len(in) / target
		out := make([]dsl.Row, 0, target)
		for i := 0; i < len(in) && len(out) < target; i += step {
			out = append(out, in[i])
		}
		return out, nil
	default:
		seed := o.cfg.Seed
		if seed == 0 {
			seed = time.Now().UnixNano()
		}
		// #nosec G404 -- sampling rows for a query result, not generating a
		// secret. The seed is deliberately caller-supplied so a sample is
		// reproducible across runs; a cryptographic generator cannot be seeded
		// and would make the operator's Seed option meaningless.
		r := rand.New(rand.NewSource(seed))
		// Reservoir sampling.
		out := make([]dsl.Row, target)
		copy(out, in[:target])
		for i := target; i < len(in); i++ {
			j := r.Intn(i + 1)
			if j < target {
				out[j] = in[i]
			}
		}
		return out, nil
	}
}

func sampleFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg SampleConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("sample: decode config: %w", err)
	}
	if cfg.N == 0 && cfg.Ratio == 0 {
		return nil, fmt.Errorf("sample: either n or ratio is required")
	}
	if cfg.N != 0 && cfg.Ratio != 0 {
		return nil, fmt.Errorf("sample: n and ratio are mutually exclusive")
	}
	if cfg.Ratio < 0 || cfg.Ratio > 1 {
		return nil, fmt.Errorf("sample: ratio must be in (0,1]")
	}
	return &sampleOp{cfg: cfg}, nil
}

// --- assert ---

// AssertConfig fails the query if a DTL expression doesn't hold.
//
//	expr:    boolean DTL expression
//	scope:   row (evaluate against each row) | overall (evaluate once with
//	         row=null and total=count) — default row
//	message: optional message
type AssertConfig struct {
	Expr    string `json:"expr"`
	Scope   string `json:"scope,omitempty"`
	Message string `json:"message,omitempty"`
}

type assertOp struct {
	cfg  AssertConfig
	eval ExprEvaluator
}

func (o *assertOp) Name() string     { return "assert" }
func (o *assertOp) IsLiveSafe() bool { return true }

func (o *assertOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	scope := o.cfg.Scope
	if scope == "" {
		scope = "row"
	}
	switch scope {
	case "row":
		for i, row := range in {
			v, err := o.eval.Eval(ctx, o.cfg.Expr, row)
			if err != nil {
				return nil, fmt.Errorf("assert row %d: %w", i, err)
			}
			if !toBool(v) {
				msg := o.cfg.Message
				if msg == "" {
					msg = "assert failed"
				}
				return nil, fmt.Errorf("%s (row %d)", msg, i)
			}
		}
	case "overall":
		ctxRow := dsl.Row{"count": len(in)}
		v, err := o.eval.Eval(ctx, o.cfg.Expr, ctxRow)
		if err != nil {
			return nil, fmt.Errorf("assert overall: %w", err)
		}
		if !toBool(v) {
			msg := o.cfg.Message
			if msg == "" {
				msg = "assert failed"
			}
			return nil, fmt.Errorf("%s", msg)
		}
	default:
		return nil, fmt.Errorf("assert: unknown scope %q", scope)
	}
	return in, nil
}

func assertFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg AssertConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("assert: decode config: %w", err)
	}
	if cfg.Expr == "" {
		return nil, fmt.Errorf("assert: expr is required")
	}
	if octx == nil || octx.Eval == nil {
		return nil, fmt.Errorf("assert: expression evaluator not available")
	}
	return &assertOp{cfg: cfg, eval: octx.Eval}, nil
}

func init() {
	Register("dropNulls", dropNullsFactory)
	Register("fillNulls", fillNullsFactory)
	Register("cast", castFactory)
	Register("dedupe", dedupeFactory)
	Register("sample", sampleFactory)
	Register("assert", assertFactory)
}
