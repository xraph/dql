package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xraph/dql/dsl"
)

// --- timeBucket ---

// TimeBucketConfig groups rows into fixed time intervals. The bucket start
// time is written to a new column.
//
//	field:    timestamp column. Accepts time.Time, RFC3339 string, or epoch
//	          numeric (seconds or milliseconds — distinguished by magnitude).
//	interval: duration string ("5m", "1h", "1d"). Day buckets respect tz.
//	as:       output column for the bucket start (RFC3339 string).
//	tz:       optional timezone name (e.g. "America/New_York"). Defaults to UTC.
//	origin:   optional anchor time. Buckets are aligned to (origin + n*interval).
//	          Defaults to the Unix epoch (UTC). Useful for week/month buckets.
type TimeBucketConfig struct {
	Field    string `json:"field"`
	Interval string `json:"interval"`
	As       string `json:"as"`
	TZ       string `json:"tz,omitempty"`
	Origin   string `json:"origin,omitempty"`
}

type timeBucketOp struct {
	cfg      TimeBucketConfig
	dur      time.Duration
	loc      *time.Location
	originAt time.Time
}

func (o *timeBucketOp) Name() string     { return "timeBucket" }
func (o *timeBucketOp) IsLiveSafe() bool { return true }

func (o *timeBucketOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	for i, row := range in {
		t, err := parseRowTime(row[o.cfg.Field])
		if err != nil {
			return nil, fmt.Errorf("timeBucket row %d: %w", i, err)
		}
		bucket := bucketStart(t.In(o.loc), o.originAt, o.dur)
		row[o.cfg.As] = bucket.UTC().Format(time.RFC3339)
	}
	return in, nil
}

// bucketStart rounds t down to the nearest origin + n*interval.
func bucketStart(t, origin time.Time, interval time.Duration) time.Time {
	if interval <= 0 {
		return t
	}
	delta := t.Sub(origin)
	n := delta / interval
	if delta < 0 && delta%interval != 0 {
		n--
	}
	return origin.Add(n * interval)
}

func timeBucketFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg TimeBucketConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("timeBucket: decode config: %w", err)
	}
	if cfg.Field == "" {
		return nil, fmt.Errorf("timeBucket: field is required")
	}
	if cfg.Interval == "" {
		return nil, fmt.Errorf("timeBucket: interval is required")
	}
	if cfg.As == "" {
		return nil, fmt.Errorf("timeBucket: as is required")
	}
	dur, err := parseInterval(cfg.Interval)
	if err != nil {
		return nil, fmt.Errorf("timeBucket: %w", err)
	}
	loc := time.UTC
	if cfg.TZ != "" {
		l, err := time.LoadLocation(cfg.TZ)
		if err != nil {
			return nil, fmt.Errorf("timeBucket: tz %q: %w", cfg.TZ, err)
		}
		loc = l
	}
	origin := time.Unix(0, 0).In(loc)
	if cfg.Origin != "" {
		t, err := time.Parse(time.RFC3339, cfg.Origin)
		if err != nil {
			return nil, fmt.Errorf("timeBucket: origin: %w", err)
		}
		origin = t
	}
	return &timeBucketOp{cfg: cfg, dur: dur, loc: loc, originAt: origin}, nil
}

// --- gapfill ---

// GapfillConfig fills missing time intervals with synthetic rows.
//
//	field:       time column (already bucket-aligned, typically the output of timeBucket)
//	interval:    expected gap between rows
//	from / to:   inclusive bounds; when omitted, derived from the input
//	method:      zero | null | lastValue | value (uses Value)
//	value:       fallback value for method=value (applied to numeric columns only)
//	groupBy:     optional partition keys. Each partition is gap-filled independently.
//	carry:       columns whose values copy from the latest seen row in the partition
//	             (default behaviour for groupBy keys).
type GapfillConfig struct {
	Field    string   `json:"field"`
	Interval string   `json:"interval"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Method   string   `json:"method,omitempty"` // zero|null|lastValue|value
	Value    any      `json:"value,omitempty"`
	GroupBy  []string `json:"groupBy,omitempty"`
	Carry    []string `json:"carry,omitempty"`
}

type gapfillOp struct {
	cfg      GapfillConfig
	dur      time.Duration
	from, to *time.Time
}

func (o *gapfillOp) Name() string     { return "gapfill" }
func (o *gapfillOp) IsLiveSafe() bool { return true }

func (o *gapfillOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(in) == 0 {
		return in, nil
	}

	// Group input rows by partition key.
	groups := make(map[string][]int)
	order := make([]string, 0)
	for i, row := range in {
		k := groupKey(o.cfg.GroupBy, row)
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}

	out := make([]dsl.Row, 0, len(in))
	for _, k := range order {
		idxs := groups[k]
		// Sort by time within the group.
		sort.SliceStable(idxs, func(a, b int) bool {
			ta, _ := parseRowTime(in[idxs[a]][o.cfg.Field])
			tb, _ := parseRowTime(in[idxs[b]][o.cfg.Field])
			return ta.Before(tb)
		})

		// Determine gap range.
		first, _ := parseRowTime(in[idxs[0]][o.cfg.Field])
		last, _ := parseRowTime(in[idxs[len(idxs)-1]][o.cfg.Field])
		from := first
		to := last
		if o.from != nil {
			from = *o.from
		}
		if o.to != nil {
			to = *o.to
		}

		// Walk every expected bucket; emit either the original row or a synthetic.
		idx := 0
		var lastRow dsl.Row
		for cur := from; !cur.After(to); cur = cur.Add(o.dur) {
			// Match: does the next input row's time equal cur (within sub-second tolerance)?
			matched := false
			for idx < len(idxs) {
				rowTime, _ := parseRowTime(in[idxs[idx]][o.cfg.Field])
				if rowTime.Before(cur) {
					idx++
					continue
				}
				if rowTime.Equal(cur) || rowTime.Sub(cur).Abs() < o.dur/2 {
					row := in[idxs[idx]]
					out = append(out, row)
					lastRow = row
					idx++
					matched = true
				}
				break
			}
			if matched {
				continue
			}
			// Emit a synthetic row for cur.
			synth := make(dsl.Row, 4)
			synth[o.cfg.Field] = cur.UTC().Format(time.RFC3339)
			// Copy the partition-key values.
			for _, gk := range o.cfg.GroupBy {
				if lastRow != nil {
					synth[gk] = lastRow[gk]
				} else if len(idxs) > 0 {
					synth[gk] = in[idxs[0]][gk]
				}
			}
			// Carry columns.
			for _, c := range o.cfg.Carry {
				if lastRow != nil {
					synth[c] = lastRow[c]
				}
			}
			// Apply fill method to remaining numeric/scalar columns.
			fillSyntheticValues(synth, o.cfg, lastRow)
			out = append(out, synth)
		}
	}
	return out, nil
}

// fillSyntheticValues populates a synthetic row's columns according to the
// configured method. Operates on the union of columns seen in lastRow.
func fillSyntheticValues(synth dsl.Row, cfg GapfillConfig, lastRow dsl.Row) {
	if lastRow == nil {
		return
	}
	method := cfg.Method
	if method == "" {
		method = "null"
	}
	for k := range lastRow {
		if _, set := synth[k]; set {
			continue
		}
		switch method {
		case "zero":
			synth[k] = 0
		case "lastValue":
			synth[k] = lastRow[k]
		case "value":
			synth[k] = cfg.Value
		default: // null
			synth[k] = nil
		}
	}
}

func gapfillFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg GapfillConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("gapfill: decode config: %w", err)
	}
	if cfg.Field == "" {
		return nil, fmt.Errorf("gapfill: field is required")
	}
	if cfg.Interval == "" {
		return nil, fmt.Errorf("gapfill: interval is required")
	}
	dur, err := parseInterval(cfg.Interval)
	if err != nil {
		return nil, fmt.Errorf("gapfill: %w", err)
	}
	op := &gapfillOp{cfg: cfg, dur: dur}
	if cfg.From != "" {
		t, err := time.Parse(time.RFC3339, cfg.From)
		if err != nil {
			return nil, fmt.Errorf("gapfill: from: %w", err)
		}
		op.from = &t
	}
	if cfg.To != "" {
		t, err := time.Parse(time.RFC3339, cfg.To)
		if err != nil {
			return nil, fmt.Errorf("gapfill: to: %w", err)
		}
		op.to = &t
	}
	return op, nil
}

// --- asofJoin ---

// AsofJoinConfig joins each left row to the right row with the closest
// timestamp on a matching key, within an optional tolerance.
//
//	dataset:       right-side dataset
//	leftTime/rightTime: time columns
//	leftKey/rightKey:   optional matching keys (no key = global match)
//	tolerance:     duration string; matches outside this window are dropped
//	direction:     backward (right ≤ left, default) | forward (right ≥ left) | nearest
//	select / as:   like lookup
//	where / limit: like lookup
type AsofJoinConfig struct {
	Dataset   string           `json:"dataset"`
	LeftTime  string           `json:"leftTime"`
	RightTime string           `json:"rightTime"`
	LeftKey   string           `json:"leftKey,omitempty"`
	RightKey  string           `json:"rightKey,omitempty"`
	Tolerance string           `json:"tolerance,omitempty"`
	Direction string           `json:"direction,omitempty"`
	As        string           `json:"as,omitempty"`
	Select    []string         `json:"select,omitempty"`
	Where     *dsl.WhereClause `json:"where,omitempty"`
	Limit     *int             `json:"limit,omitempty"`
}

type asofJoinOp struct {
	cfg       AsofJoinConfig
	classic   ClassicExecutor
	tolerance time.Duration
}

func (o *asofJoinOp) Name() string     { return "asofJoin" }
func (o *asofJoinOp) IsLiveSafe() bool { return true }

func (o *asofJoinOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	ws, proj := scopeFrom(ctx)
	if ws == "" {
		return nil, fmt.Errorf("asofJoin: workspace not set in context")
	}
	rightQ := &dsl.QueryDSL{
		From:      dsl.FromClause{Dataset: o.cfg.Dataset},
		ProjectID: proj,
		Where:     o.cfg.Where,
		Limit:     o.cfg.Limit,
	}
	rightRes, err := o.classic.Execute(ctx, rightQ, ws, proj)
	if err != nil {
		return nil, fmt.Errorf("asofJoin %s: fetch right: %w", o.cfg.Dataset, err)
	}

	// Bucket right rows by key (or single bucket when no key configured)
	// and sort each bucket by time ascending.
	buckets := make(map[string][]dsl.Row)
	for _, r := range rightRes.Rows {
		k := ""
		if o.cfg.RightKey != "" {
			k = fmt.Sprintf("%v", r[o.cfg.RightKey])
		}
		buckets[k] = append(buckets[k], r)
	}
	for k := range buckets {
		sort.SliceStable(buckets[k], func(a, b int) bool {
			ta, _ := parseRowTime(buckets[k][a][o.cfg.RightTime])
			tb, _ := parseRowTime(buckets[k][b][o.cfg.RightTime])
			return ta.Before(tb)
		})
	}

	out := make([]dsl.Row, 0, len(in))
	plan := newMergePlan(LookupConfig{As: o.cfg.As, Select: o.cfg.Select})
	for _, left := range in {
		k := ""
		if o.cfg.LeftKey != "" {
			k = fmt.Sprintf("%v", left[o.cfg.LeftKey])
		}
		bucket := buckets[k]
		if len(bucket) == 0 {
			out = append(out, left)
			continue
		}
		leftT, err := parseRowTime(left[o.cfg.LeftTime])
		if err != nil {
			out = append(out, left)
			continue
		}
		match := o.findAsofMatch(bucket, leftT)
		if match == nil {
			out = append(out, left)
			continue
		}
		out = append(out, plan.merge(left, match))
	}
	return out, nil
}

// findAsofMatch returns the right-side row that satisfies direction +
// tolerance. Bucket is pre-sorted ascending by time.
func (o *asofJoinOp) findAsofMatch(bucket []dsl.Row, leftT time.Time) dsl.Row {
	dir := o.cfg.Direction
	if dir == "" {
		dir = "backward"
	}
	// Binary search: find first element with rightTime > leftT.
	idx := sort.Search(len(bucket), func(i int) bool {
		t, _ := parseRowTime(bucket[i][o.cfg.RightTime])
		return t.After(leftT)
	})
	var candidate dsl.Row
	switch dir {
	case "backward":
		if idx == 0 {
			return nil
		}
		candidate = bucket[idx-1]
	case "forward":
		if idx >= len(bucket) {
			return nil
		}
		candidate = bucket[idx]
	case "nearest":
		var before, after dsl.Row
		if idx > 0 {
			before = bucket[idx-1]
		}
		if idx < len(bucket) {
			after = bucket[idx]
		}
		switch {
		case before == nil:
			candidate = after
		case after == nil:
			candidate = before
		default:
			tb, _ := parseRowTime(before[o.cfg.RightTime])
			ta, _ := parseRowTime(after[o.cfg.RightTime])
			if leftT.Sub(tb).Abs() <= ta.Sub(leftT).Abs() {
				candidate = before
			} else {
				candidate = after
			}
		}
	}
	if candidate == nil {
		return nil
	}
	if o.tolerance > 0 {
		t, _ := parseRowTime(candidate[o.cfg.RightTime])
		if leftT.Sub(t).Abs() > o.tolerance {
			return nil
		}
	}
	return candidate
}

func asofJoinFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg AsofJoinConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("asofJoin: decode config: %w", err)
	}
	if cfg.Dataset == "" {
		return nil, fmt.Errorf("asofJoin: dataset is required")
	}
	if cfg.LeftTime == "" || cfg.RightTime == "" {
		return nil, fmt.Errorf("asofJoin: leftTime and rightTime are required")
	}
	if cfg.Direction != "" && cfg.Direction != "backward" && cfg.Direction != "forward" && cfg.Direction != "nearest" {
		return nil, fmt.Errorf("asofJoin: unknown direction %q", cfg.Direction)
	}
	if octx == nil || octx.Classic == nil {
		return nil, fmt.Errorf("asofJoin: classic executor not available")
	}
	op := &asofJoinOp{cfg: cfg, classic: octx.Classic}
	if cfg.Tolerance != "" {
		dur, err := parseInterval(cfg.Tolerance)
		if err != nil {
			return nil, fmt.Errorf("asofJoin: tolerance: %w", err)
		}
		op.tolerance = dur
	}
	return op, nil
}

// --- shared helpers ---

// parseInterval accepts Go duration strings ("5m", "1h30m") plus a few
// human-friendly forms ("1d" → 24h, "1w" → 168h).
func parseInterval(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("interval is empty")
	}
	// Expand human-friendly suffixes that Go's time.ParseDuration rejects.
	last := s[len(s)-1]
	if last == 'd' || last == 'w' {
		nStr := s[:len(s)-1]
		var n int
		if _, err := fmt.Sscanf(nStr, "%d", &n); err != nil {
			return 0, fmt.Errorf("invalid interval %q", s)
		}
		mult := time.Hour * 24
		if last == 'w' {
			mult = time.Hour * 24 * 7
		}
		return time.Duration(n) * mult, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid interval %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("interval must be positive")
	}
	return d, nil
}

// parseRowTime accepts time.Time, RFC3339 string, or numeric epoch.
func parseRowTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case string:
		// Try RFC3339 first, then RFC3339Nano.
		if pt, err := time.Parse(time.RFC3339, t); err == nil {
			return pt, nil
		}
		if pt, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return pt, nil
		}
		// Try epoch as string.
		var n int64
		if _, err := fmt.Sscanf(t, "%d", &n); err == nil {
			return epochToTime(n), nil
		}
		return time.Time{}, fmt.Errorf("unparseable time string %q", t)
	case int64:
		return epochToTime(t), nil
	case int:
		return epochToTime(int64(t)), nil
	case float64:
		return epochToTime(int64(t)), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported time value type %T", v)
	}
}

// epochToTime distinguishes seconds vs milliseconds vs microseconds vs
// nanoseconds by magnitude. After 2001 in seconds, magnitude crosses 10^9.
func epochToTime(n int64) time.Time {
	switch {
	case n >= 1e18: // ns
		return time.Unix(0, n)
	case n >= 1e15: // us
		return time.Unix(0, n*1e3)
	case n >= 1e12: // ms
		return time.Unix(0, n*1e6)
	default:
		return time.Unix(n, 0)
	}
}

func init() {
	Register("timeBucket", timeBucketFactory)
	Register("gapfill", gapfillFactory)
	Register("asofJoin", asofJoinFactory)
}
