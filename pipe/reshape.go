package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/xraph/dql/dsl"
)

// --- pivot ---

// PivotConfig turns rows into columns. Each unique value of ColumnKey
// becomes a column, and ValueField is aggregated (when multiple rows
// share the same RowKeys × ColumnKey value) using Aggregate.
//
//	rowKeys:    keys identifying output rows
//	columnKey:  column whose distinct values become column names
//	valueField: column whose values populate the cells
//	aggregate:  sum | avg | count | min | max | first | last
//	fillValue:  value for cells with no source row
//	prefix:     optional column-name prefix
type PivotConfig struct {
	RowKeys    []string `json:"rowKeys"`
	ColumnKey  string   `json:"columnKey"`
	ValueField string   `json:"valueField"`
	Aggregate  string   `json:"aggregate,omitempty"`
	FillValue  any      `json:"fillValue,omitempty"`
	Prefix     string   `json:"prefix,omitempty"`
}

type pivotOp struct {
	cfg PivotConfig
}

func (o *pivotOp) Name() string     { return "pivot" }
func (o *pivotOp) IsLiveSafe() bool { return true }

func (o *pivotOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(in) == 0 {
		return in, nil
	}

	// Pass 1: collect output row keys (preserving first-seen order) and
	// the set of distinct column-key values.
	type rowCells struct {
		rowKeyVals map[string]any
		cells      map[string][]any // colKey value -> values to aggregate
	}
	rowOrder := make([]string, 0)
	rowMap := make(map[string]*rowCells)
	colSet := make(map[string]struct{})
	colOrder := make([]string, 0)

	for _, row := range in {
		rkVals := make(map[string]any, len(o.cfg.RowKeys))
		keyParts := make([]string, len(o.cfg.RowKeys))
		for i, k := range o.cfg.RowKeys {
			rkVals[k] = row[k]
			keyParts[i] = fmt.Sprintf("%v", row[k])
		}
		rk := strings.Join(keyParts, "\x00")
		if _, exists := rowMap[rk]; !exists {
			rowOrder = append(rowOrder, rk)
			rowMap[rk] = &rowCells{rowKeyVals: rkVals, cells: map[string][]any{}}
		}
		ck := fmt.Sprintf("%v", row[o.cfg.ColumnKey])
		if _, seen := colSet[ck]; !seen {
			colSet[ck] = struct{}{}
			colOrder = append(colOrder, ck)
		}
		rowMap[rk].cells[ck] = append(rowMap[rk].cells[ck], row[o.cfg.ValueField])
	}
	sort.Strings(colOrder)

	agg := o.cfg.Aggregate
	if agg == "" {
		agg = "first"
	}

	out := make([]dsl.Row, 0, len(rowOrder))
	for _, rk := range rowOrder {
		rc := rowMap[rk]
		row := make(dsl.Row, len(o.cfg.RowKeys)+len(colOrder))
		for k, v := range rc.rowKeyVals {
			row[k] = v
		}
		for _, ck := range colOrder {
			colName := o.cfg.Prefix + ck
			if values, ok := rc.cells[ck]; ok && len(values) > 0 {
				row[colName] = aggregateValues(agg, values)
			} else {
				row[colName] = o.cfg.FillValue
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func aggregateValues(fn string, values []any) any {
	switch strings.ToLower(fn) {
	case "first":
		return values[0]
	case "last":
		return values[len(values)-1]
	case "count":
		return len(values)
	case "sum":
		s := 0.0
		for _, v := range values {
			s += toFloat(v)
		}
		return s
	case "avg":
		if len(values) == 0 {
			return nil
		}
		s := 0.0
		for _, v := range values {
			s += toFloat(v)
		}
		return s / float64(len(values))
	case "min":
		m := toFloat(values[0])
		for _, v := range values[1:] {
			f := toFloat(v)
			if f < m {
				m = f
			}
		}
		return m
	case "max":
		m := toFloat(values[0])
		for _, v := range values[1:] {
			f := toFloat(v)
			if f > m {
				m = f
			}
		}
		return m
	default:
		return values[0]
	}
}

func pivotFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg PivotConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("pivot: decode config: %w", err)
	}
	if cfg.ColumnKey == "" {
		return nil, fmt.Errorf("pivot: columnKey is required")
	}
	if cfg.ValueField == "" {
		return nil, fmt.Errorf("pivot: valueField is required")
	}
	if cfg.Aggregate != "" {
		switch strings.ToLower(cfg.Aggregate) {
		case "first", "last", "count", "sum", "avg", "min", "max":
		default:
			return nil, fmt.Errorf("pivot: unknown aggregate %q", cfg.Aggregate)
		}
	}
	return &pivotOp{cfg: cfg}, nil
}

// --- unpivot ---

// UnpivotConfig melts named columns into name/value pairs. One output row
// per (input row × value column).
//
//	idCols:    columns kept as-is on every output row
//	valueCols: columns to melt (when empty, every non-idCols column is melted)
//	nameAs:    output column for the source column name
//	valueAs:   output column for the value
type UnpivotConfig struct {
	IDCols    []string `json:"idCols,omitempty"`
	ValueCols []string `json:"valueCols,omitempty"`
	NameAs    string   `json:"nameAs"`
	ValueAs   string   `json:"valueAs"`
}

type unpivotOp struct {
	cfg UnpivotConfig
}

func (o *unpivotOp) Name() string     { return "unpivot" }
func (o *unpivotOp) IsLiveSafe() bool { return true }

func (o *unpivotOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	out := make([]dsl.Row, 0, len(in)*max(1, len(o.cfg.ValueCols)))
	for _, row := range in {
		valueCols := o.cfg.ValueCols
		if len(valueCols) == 0 {
			// All non-ID columns become value columns.
			idSet := make(map[string]struct{}, len(o.cfg.IDCols))
			for _, k := range o.cfg.IDCols {
				idSet[k] = struct{}{}
			}
			valueCols = make([]string, 0, len(row))
			for k := range row {
				if _, isID := idSet[k]; !isID {
					valueCols = append(valueCols, k)
				}
			}
			sort.Strings(valueCols)
		}
		for _, vc := range valueCols {
			outRow := make(dsl.Row, len(o.cfg.IDCols)+2)
			for _, k := range o.cfg.IDCols {
				outRow[k] = row[k]
			}
			outRow[o.cfg.NameAs] = vc
			outRow[o.cfg.ValueAs] = row[vc]
			out = append(out, outRow)
		}
	}
	return out, nil
}

func unpivotFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg UnpivotConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("unpivot: decode config: %w", err)
	}
	if cfg.NameAs == "" || cfg.ValueAs == "" {
		return nil, fmt.Errorf("unpivot: nameAs and valueAs are required")
	}
	return &unpivotOp{cfg: cfg}, nil
}

// --- unnestObject ---

// UnnestObjectConfig spreads the keys of a map-valued column into the root row.
//
//	field:  column whose value is a map
//	prefix: optional prefix added to each merged key
//	drop:   when true, the source field is removed
type UnnestObjectConfig struct {
	Field  string `json:"field"`
	Prefix string `json:"prefix,omitempty"`
	Drop   bool   `json:"drop,omitempty"`
}

type unnestObjectOp struct {
	cfg UnnestObjectConfig
}

func (o *unnestObjectOp) Name() string     { return "unnestObject" }
func (o *unnestObjectOp) IsLiveSafe() bool { return true }

func (o *unnestObjectOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	for _, row := range in {
		v, ok := row[o.cfg.Field]
		if !ok || v == nil {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for k, mv := range m {
			row[o.cfg.Prefix+k] = mv
		}
		if o.cfg.Drop {
			delete(row, o.cfg.Field)
		}
	}
	return in, nil
}

func unnestObjectFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg UnnestObjectConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("unnestObject: decode config: %w", err)
	}
	if cfg.Field == "" {
		return nil, fmt.Errorf("unnestObject: field is required")
	}
	return &unnestObjectOp{cfg: cfg}, nil
}

// --- nest ---

// NestConfig groups rows by `by` keys and collects the remaining (or
// `include`d) columns into an array under `into`.
//
//	by:       partition keys, kept on the output row
//	into:     output column name for the array
//	include:  optional list of columns to include in each nested record
//	          (default: every column not in `by`)
type NestConfig struct {
	By      []string `json:"by"`
	Into    string   `json:"into"`
	Include []string `json:"include,omitempty"`
}

type nestOp struct {
	cfg NestConfig
}

func (o *nestOp) Name() string     { return "nest" }
func (o *nestOp) IsLiveSafe() bool { return true }

func (o *nestOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(in) == 0 {
		return in, nil
	}
	groups := make(map[string][]dsl.Row)
	order := make([]string, 0)
	for _, row := range in {
		k := groupKey(o.cfg.By, row)
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], row)
	}
	out := make([]dsl.Row, 0, len(order))
	for _, k := range order {
		bucket := groups[k]
		row := make(dsl.Row, len(o.cfg.By)+1)
		for _, key := range o.cfg.By {
			row[key] = bucket[0][key]
		}
		nested := make([]map[string]any, 0, len(bucket))
		for _, b := range bucket {
			rec := make(map[string]any)
			if len(o.cfg.Include) > 0 {
				for _, c := range o.cfg.Include {
					if v, ok := b[c]; ok {
						rec[c] = v
					}
				}
			} else {
				bySet := make(map[string]struct{}, len(o.cfg.By))
				for _, k := range o.cfg.By {
					bySet[k] = struct{}{}
				}
				for c, v := range b {
					if _, isBy := bySet[c]; !isBy {
						rec[c] = v
					}
				}
			}
			nested = append(nested, rec)
		}
		row[o.cfg.Into] = nested
		out = append(out, row)
	}
	return out, nil
}

func nestFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg NestConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("nest: decode config: %w", err)
	}
	if len(cfg.By) == 0 {
		return nil, fmt.Errorf("nest: by is required")
	}
	if cfg.Into == "" {
		return nil, fmt.Errorf("nest: into is required")
	}
	return &nestOp{cfg: cfg}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func init() {
	Register("pivot", pivotFactory)
	Register("unpivot", unpivotFactory)
	Register("unnestObject", unnestObjectFactory)
	Register("nest", nestFactory)
}
