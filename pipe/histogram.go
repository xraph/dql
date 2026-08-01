package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/xraph/dql/dsl"
)

// HistogramConfig collapses numeric values into fixed-width bins and emits
// one row per bin.
//
//	field:    numeric column (required)
//	bins:     bin count (required, ≥ 1)
//	min, max: explicit value range; when omitted, derived from input
//	asCount:  output column for the count (default "count")
//	asStart:  output column for bin start (default "binStart")
//	asEnd:    output column for bin end (default "binEnd")
type HistogramConfig struct {
	Field   string   `json:"field"`
	Bins    int      `json:"bins"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	AsCount string   `json:"asCount,omitempty"`
	AsStart string   `json:"asStart,omitempty"`
	AsEnd   string   `json:"asEnd,omitempty"`
}

type histogramOp struct {
	cfg HistogramConfig
}

func (o *histogramOp) Name() string     { return "histogram" }
func (o *histogramOp) IsLiveSafe() bool { return true }

func (o *histogramOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(in) == 0 || o.cfg.Bins <= 0 {
		return in[:0:0], nil
	}
	min, max := math.Inf(1), math.Inf(-1)
	if o.cfg.Min != nil {
		min = *o.cfg.Min
	}
	if o.cfg.Max != nil {
		max = *o.cfg.Max
	}
	if o.cfg.Min == nil || o.cfg.Max == nil {
		for _, row := range in {
			v, ok := row[o.cfg.Field]
			if !ok || v == nil || !isNumeric(v) {
				continue
			}
			f := toFloat(v)
			if o.cfg.Min == nil && f < min {
				min = f
			}
			if o.cfg.Max == nil && f > max {
				max = f
			}
		}
	}
	if math.IsInf(min, 1) || math.IsInf(max, -1) || max <= min {
		return nil, fmt.Errorf("histogram: cannot derive bin range (no numeric data?)")
	}
	width := (max - min) / float64(o.cfg.Bins)
	counts := make([]int, o.cfg.Bins)
	for _, row := range in {
		v, ok := row[o.cfg.Field]
		if !ok || v == nil || !isNumeric(v) {
			continue
		}
		f := toFloat(v)
		idx := int((f - min) / width)
		if idx == o.cfg.Bins { // include the right edge in the last bin
			idx = o.cfg.Bins - 1
		}
		if idx < 0 || idx >= o.cfg.Bins {
			continue
		}
		counts[idx]++
	}
	asCount := o.cfg.AsCount
	if asCount == "" {
		asCount = "count"
	}
	asStart := o.cfg.AsStart
	if asStart == "" {
		asStart = "binStart"
	}
	asEnd := o.cfg.AsEnd
	if asEnd == "" {
		asEnd = "binEnd"
	}
	out := make([]dsl.Row, o.cfg.Bins)
	for i := 0; i < o.cfg.Bins; i++ {
		out[i] = dsl.Row{
			asStart: min + float64(i)*width,
			asEnd:   min + float64(i+1)*width,
			asCount: counts[i],
		}
	}
	return out, nil
}

func histogramFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg HistogramConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("histogram: decode config: %w", err)
	}
	if cfg.Field == "" {
		return nil, fmt.Errorf("histogram: field is required")
	}
	if cfg.Bins <= 0 {
		return nil, fmt.Errorf("histogram: bins must be > 0")
	}
	return &histogramOp{cfg: cfg}, nil
}

func init() { Register("histogram", histogramFactory) }
