package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xraph/dql/dsl"
)

// CallFunctionConfig invokes a registered DTL function.
//
//	Name           — required, e.g. "math::zscore" or "app:slack::post".
//	Mode           — "perRow" (default) calls the function for each row; "batch"
//	                 sends all rows in a single call under args["rows"].
//	Pure           — declares the function side-effect-free. When true the op is
//	                 live-safe and may run inside a live subscription's re-execute
//	                 loop. Defaults to false to be safe.
//	Args           — static args passed to each call. By default string values
//	                 starting with "$" are resolved per-row: "$ip" → row["ip"].
//	                 Set DisableRefResolve to pass them through verbatim — used
//	                 by the pipeline-extension function stage which spreads
//	                 raw parameters into args without ref-resolution.
//	DisableRefResolve — when true, $-refs in Args are NOT resolved. The flag
//	                    is JSON-serialised as "literalArgs" so external callers
//	                    can opt out without knowing the internal Go name.
//	As             — when set the function result is placed under row[As].
//	                 When unset, map results are spread into the row and scalar
//	                 results go under row["_result"] (matches the pipeline
//	                 function_stage merge behaviour).
type CallFunctionConfig struct {
	Name              string         `json:"name"`
	Mode              string         `json:"mode,omitempty"`
	Pure              bool           `json:"pure,omitempty"`
	Args              map[string]any `json:"args,omitempty"`
	As                string         `json:"as,omitempty"`
	DisableRefResolve bool           `json:"literalArgs,omitempty"`
}

type callFunctionOp struct {
	cfg CallFunctionConfig
	reg FunctionRegistry
}

func (o *callFunctionOp) Name() string     { return "callFunction" }
func (o *callFunctionOp) IsLiveSafe() bool { return o.cfg.Pure }

func (o *callFunctionOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	switch strings.ToLower(o.cfg.Mode) {
	case "", "perrow":
		return o.applyPerRow(ctx, in)
	case "batch":
		return o.applyBatch(ctx, in)
	default:
		return nil, fmt.Errorf("callFunction: unknown mode %q", o.cfg.Mode)
	}
}

func (o *callFunctionOp) applyPerRow(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	out := make([]dsl.Row, 0, len(in))
	for i, row := range in {
		args := o.perRowArgs(row)
		result, err := o.reg.Execute(ctx, o.cfg.Name, args)
		if err != nil {
			return nil, fmt.Errorf("callFunction %s: row %d: %w", o.cfg.Name, i, err)
		}
		out = append(out, mergeFunctionResult(row, result, o.cfg.As))
	}
	return out, nil
}

func (o *callFunctionOp) applyBatch(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	args := make(map[string]any, len(o.cfg.Args)+1)
	for k, v := range o.cfg.Args {
		args[k] = v
	}
	args["rows"] = in

	result, err := o.reg.Execute(ctx, o.cfg.Name, args)
	if err != nil {
		return nil, fmt.Errorf("callFunction %s: %w", o.cfg.Name, err)
	}
	if rows, ok := toRowSlice(result); ok {
		return rows, nil
	}
	// Scalar / map result wraps onto each row under As, falling back to _result.
	as := o.cfg.As
	if as == "" {
		as = "_result"
	}
	for _, row := range in {
		row[as] = result
	}
	return in, nil
}

// perRowArgs builds the arg map for a single row: row data first, then Args
// overrides. $-refs in Args are resolved against the row unless
// DisableRefResolve is set.
func (o *callFunctionOp) perRowArgs(row dsl.Row) map[string]any {
	// Start with the row so the function can see every column.
	out := make(map[string]any, len(row)+len(o.cfg.Args))
	for k, v := range row {
		out[k] = v
	}
	for k, v := range o.cfg.Args {
		if o.cfg.DisableRefResolve {
			out[k] = v
		} else {
			out[k] = resolveRef(v, row)
		}
	}
	return out
}

// resolveRef expands "$field" strings against the row. Non-string or
// non-$-prefixed values pass through unchanged.
func resolveRef(v any, row dsl.Row) any {
	s, ok := v.(string)
	if !ok || !strings.HasPrefix(s, "$") || len(s) < 2 {
		return v
	}
	if rv, exists := row[s[1:]]; exists {
		return rv
	}
	return nil
}

// mergeFunctionResult folds a function's return value back into a row. Follows
// pipeline function_stage semantics: if As is set the result goes under As,
// otherwise maps are spread and scalars go under "_result".
func mergeFunctionResult(row dsl.Row, result any, as string) dsl.Row {
	out := make(dsl.Row, len(row)+4)
	for k, v := range row {
		out[k] = v
	}
	if as != "" {
		out[as] = result
		return out
	}
	if m, ok := result.(map[string]any); ok {
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	out["_result"] = result
	return out
}

// toRowSlice interprets a value as a row slice. dsl.Row is an alias for
// map[string]any so we only need to handle the alias and the any-of-maps form.
func toRowSlice(v any) ([]dsl.Row, bool) {
	switch s := v.(type) {
	case []dsl.Row:
		return s, true
	case []any:
		out := make([]dsl.Row, 0, len(s))
		for _, item := range s {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			out = append(out, m)
		}
		return out, true
	default:
		return nil, false
	}
}

func callFunctionFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg CallFunctionConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("callFunction: decode config: %w", err)
	}
	if octx == nil {
		return NewCallFunctionOp(cfg, nil)
	}
	return NewCallFunctionOp(cfg, octx.Registry)
}

// NewCallFunctionOp builds a callFunction Operator from typed config.
//
// Exported so consumers outside the pipe package — notably the pipeline
// extension — can reuse the same row-transform behaviour. Returns an error
// when the config is invalid or reg is nil.
func NewCallFunctionOp(cfg CallFunctionConfig, reg FunctionRegistry) (Operator, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("callFunction: name is required")
	}
	switch strings.ToLower(cfg.Mode) {
	case "", "perrow", "batch":
	default:
		return nil, fmt.Errorf("callFunction: unknown mode %q", cfg.Mode)
	}
	if reg == nil {
		return nil, fmt.Errorf("callFunction: function registry not available")
	}
	return &callFunctionOp{cfg: cfg, reg: reg}, nil
}

func init() { Register("callFunction", callFunctionFactory) }
