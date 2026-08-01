package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// CallAppConfig invokes a managed app via the runtime extension.
//
// The payload shape matches what pipeline's app_stage sends so apps can be
// shared between pipelines and pipe queries without special handling:
//
//	{
//	  "capability":   <Capability or "pipe_query">,
//	  "rows":         <rows>,
//	  "workspace_id": <ws>,
//	  "project_id":   <proj>,
//	  "dataset":      <from.dataset>,
//	  "parameters":   <extra Payload fields>,
//	}
//
// callApp is always treated as live-unsafe — Phase 3's live subscription
// classifier rejects pipes containing this op unless the client opts into
// dryRun.
type CallAppConfig struct {
	AppID      string         `json:"appId"`
	Method     string         `json:"method,omitempty"`
	Capability string         `json:"capability,omitempty"`
	Batch      *bool          `json:"batch,omitempty"` // default true
	Payload    map[string]any `json:"payload,omitempty"`
	Dataset    string         `json:"dataset,omitempty"`
}

type callAppOp struct {
	cfg    CallAppConfig
	caller AppCaller
}

func (o *callAppOp) Name() string     { return "callApp" }
func (o *callAppOp) IsLiveSafe() bool { return false }

func (o *callAppOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	method := o.cfg.Method
	if method == "" {
		method = "transform"
	}
	capability := o.cfg.Capability
	if capability == "" {
		capability = "pipe_query"
	}
	batch := true
	if o.cfg.Batch != nil {
		batch = *o.cfg.Batch
	}

	if batch {
		return o.callBatch(ctx, method, capability, in)
	}
	return o.callPerRow(ctx, method, capability, in)
}

func (o *callAppOp) callBatch(ctx context.Context, method, capability string, in []dsl.Row) ([]dsl.Row, error) {
	payload := o.basePayload(capability, in)
	data, err := o.caller.CallApp(ctx, o.cfg.AppID, method, payload)
	if err != nil {
		return nil, fmt.Errorf("callApp %s: %w", o.cfg.AppID, err)
	}
	rows, err := extractAppRows(data, in)
	if err != nil {
		return nil, fmt.Errorf("callApp %s: %w", o.cfg.AppID, err)
	}
	return rows, nil
}

func (o *callAppOp) callPerRow(ctx context.Context, method, capability string, in []dsl.Row) ([]dsl.Row, error) {
	out := make([]dsl.Row, 0, len(in))
	for i, row := range in {
		payload := o.basePayload(capability, []dsl.Row{row})
		data, err := o.caller.CallApp(ctx, o.cfg.AppID, method, payload)
		if err != nil {
			return nil, fmt.Errorf("callApp %s: row %d: %w", o.cfg.AppID, i, err)
		}
		rows, err := extractAppRows(data, []dsl.Row{row})
		if err != nil {
			return nil, fmt.Errorf("callApp %s: row %d: %w", o.cfg.AppID, i, err)
		}
		out = append(out, rows...)
	}
	return out, nil
}

// basePayload produces the common envelope shared between batch and per-row
// calls. Workspace and project are unknown to the op itself; they are set in
// the payload via the parent executor through a context value. Rather than
// thread context values, we leave them empty here and let apps accept missing
// scope — this matches the read-path (apps invoked from queries) rather than
// the pipeline's write-path contract.
func (o *callAppOp) basePayload(capability string, rows []dsl.Row) map[string]any {
	p := map[string]any{
		"capability": capability,
		"rows":       rows,
	}
	if o.cfg.Dataset != "" {
		p["dataset"] = o.cfg.Dataset
	}
	if len(o.cfg.Payload) > 0 {
		p["parameters"] = o.cfg.Payload
	}
	return p
}

// extractAppRows pulls the "rows" key from the app response, falling back to
// the input rows when the response contains none. This mirrors the pipeline's
// behaviour for apps that only produce summaries.
func extractAppRows(data map[string]any, fallback []dsl.Row) ([]dsl.Row, error) {
	raw, ok := data["rows"]
	if !ok || raw == nil {
		return fallback, nil
	}
	switch v := raw.(type) {
	case []dsl.Row:
		return v, nil
	case []any:
		out := make([]dsl.Row, 0, len(v))
		for i, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("rows[%d]: expected map, got %T", i, item)
			}
			out = append(out, m)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("rows: expected array, got %T", raw)
	}
}

func callAppFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg CallAppConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("callApp: decode config: %w", err)
	}
	if octx == nil {
		return NewCallAppOp(cfg, nil)
	}
	return NewCallAppOp(cfg, octx.AppCaller)
}

// NewCallAppOp builds a callApp Operator from typed config.
//
// Exported so consumers outside the pipe package can reuse the same
// row-in/row-out behaviour. Note that callApp's payload envelope is
// pipe-mode-shaped (capability default "pipe_query") — the pipeline
// extension's AppStage uses a different envelope ("pipeline_hook" with
// workspace_id/project_id/dataset/parameters keys) and is intentionally
// not built on top of this op.
func NewCallAppOp(cfg CallAppConfig, caller AppCaller) (Operator, error) {
	if cfg.AppID == "" {
		return nil, fmt.Errorf("callApp: appId is required")
	}
	if caller == nil {
		return nil, fmt.Errorf("callApp: app caller not available (runtime extension missing)")
	}
	return &callAppOp{cfg: cfg, caller: caller}, nil
}

func init() { Register("callApp", callAppFactory) }
