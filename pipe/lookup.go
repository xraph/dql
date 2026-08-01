package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/xraph/dql/dsl"
)

// LookupConfig performs an in-memory hash join against another dataset.
//
//	Dataset     — required, the name of the dataset to look up in.
//	On.Left     — the column on the current row to match.
//	On.Right    — the column on the right-side (looked-up) row to match.
//	As          — optional prefix. When set, looked-up columns are stored as
//	              {As}_{col}. When empty, columns are merged directly (later
//	              matches may overwrite earlier ones).
//	Select      — optional list of right-side columns to keep (empty = keep all).
//	Mode        — "left" (default) or "inner". In "left" unmatched left rows
//	              pass through with right-side columns set to nil. In "inner"
//	              unmatched left rows are dropped.
//	Where       — optional filter applied to the right-side query before the
//	              join so we only fetch relevant rows.
//	Limit       — optional cap on how many right-side rows are fetched.
type LookupConfig struct {
	Dataset string           `json:"dataset"`
	On      LookupOn         `json:"on"`
	As      string           `json:"as,omitempty"`
	Select  []string         `json:"select,omitempty"`
	Mode    string           `json:"mode,omitempty"`
	Where   *dsl.WhereClause `json:"where,omitempty"`
	Limit   *int             `json:"limit,omitempty"`
	// CacheTTLMs caches the right-side query result for this many milliseconds
	// across Apply calls on the same op instance. 0 (default) disables caching
	// — every Apply re-runs the right-side query. Useful for live
	// subscriptions where the right side changes rarely (e.g. a sites/users
	// dimension table); set CacheTTLMs to avoid re-fetching on every event.
	// Stale data is possible during the TTL window.
	CacheTTLMs int `json:"cacheTtlMs,omitempty"`
}

// LookupOn names the join key on each side.
type LookupOn struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

type lookupOp struct {
	cfg     LookupConfig
	classic ClassicExecutor
	// Cache state (used only when cfg.CacheTTLMs > 0). The op is shared
	// across live-replay invocations, so the cache survives between calls.
	cacheMu     sync.Mutex
	cachedIndex map[string]dsl.Row
	cachedAt    time.Time
}

func (o *lookupOp) Name() string     { return "lookup" }
func (o *lookupOp) IsLiveSafe() bool { return true }

func (o *lookupOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	ws, proj := scopeFrom(ctx)
	if ws == "" {
		return nil, fmt.Errorf("lookup: workspace not set in context")
	}

	index, err := o.fetchIndex(ctx, ws, proj)
	if err != nil {
		return nil, err
	}

	inner := o.cfg.Mode == "inner"

	// For each left row, find the match and merge.
	out := make([]dsl.Row, 0, len(in))
	for _, left := range in {
		key := fmt.Sprintf("%v", left[o.cfg.On.Left])
		match, found := index[key]
		if !found {
			if inner {
				continue
			}
			// left join: pass left row through unchanged but add null columns if As is set.
			out = append(out, left)
			continue
		}
		out = append(out, mergeLookup(left, match, o.cfg))
	}
	return out, nil
}

// fetchIndex returns the right-side hash index, optionally serving from cache.
func (o *lookupOp) fetchIndex(ctx context.Context, ws, proj string) (map[string]dsl.Row, error) {
	if o.cfg.CacheTTLMs > 0 {
		o.cacheMu.Lock()
		if o.cachedIndex != nil && time.Since(o.cachedAt) < time.Duration(o.cfg.CacheTTLMs)*time.Millisecond {
			idx := o.cachedIndex
			o.cacheMu.Unlock()
			return idx, nil
		}
		o.cacheMu.Unlock()
	}

	rightQ := &dsl.QueryDSL{
		From:      dsl.FromClause{Dataset: o.cfg.Dataset},
		ProjectID: proj,
		Where:     o.cfg.Where,
		Limit:     o.cfg.Limit,
	}
	rightRes, err := o.classic.Execute(ctx, rightQ, ws, proj)
	if err != nil {
		return nil, fmt.Errorf("lookup %s: fetch right side: %w", o.cfg.Dataset, err)
	}

	index := make(map[string]dsl.Row, len(rightRes.Rows))
	for _, r := range rightRes.Rows {
		key := fmt.Sprintf("%v", r[o.cfg.On.Right])
		index[key] = r // last-write-wins — lookups expect uniqueness on the right key
	}

	if o.cfg.CacheTTLMs > 0 {
		o.cacheMu.Lock()
		o.cachedIndex = index
		o.cachedAt = time.Now()
		o.cacheMu.Unlock()
	}
	return index, nil
}

func mergeLookup(left, right dsl.Row, cfg LookupConfig) dsl.Row {
	// Decide which right-side columns contribute.
	cols := cfg.Select
	if len(cols) == 0 {
		cols = make([]string, 0, len(right))
		for k := range right {
			cols = append(cols, k)
		}
	}
	out := make(dsl.Row, len(left)+len(cols))
	for k, v := range left {
		out[k] = v
	}
	for _, c := range cols {
		key := c
		if cfg.As != "" {
			key = cfg.As + "_" + c
		}
		out[key] = right[c]
	}
	return out
}

func lookupFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg LookupConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("lookup: decode config: %w", err)
	}
	if cfg.Dataset == "" {
		return nil, fmt.Errorf("lookup: dataset is required")
	}
	if cfg.On.Left == "" || cfg.On.Right == "" {
		return nil, fmt.Errorf("lookup: on.left and on.right are required")
	}
	switch cfg.Mode {
	case "", "left", "inner":
	default:
		return nil, fmt.Errorf("lookup: unknown mode %q (want left or inner)", cfg.Mode)
	}
	if octx == nil || octx.Classic == nil {
		return nil, fmt.Errorf("lookup: classic executor not available")
	}
	return &lookupOp{cfg: cfg, classic: octx.Classic}, nil
}

func init() { Register("lookup", lookupFactory) }
