package pipe

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/dql/dsl"
)

type stubRegistry struct {
	returns   any
	err       error
	lastName  string
	lastArgs  map[string]any
	callCount int
	responses []any // optional per-call overrides
}

func (s *stubRegistry) Execute(_ context.Context, fullName string, args map[string]any) (any, error) {
	s.lastName = fullName
	// Clone args so the caller can inspect them without races on iteration.
	s.lastArgs = make(map[string]any, len(args))
	for k, v := range args {
		s.lastArgs[k] = v
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.callCount < len(s.responses) {
		r := s.responses[s.callCount]
		s.callCount++
		return r, nil
	}
	s.callCount++
	return s.returns, nil
}

func TestCallFunction_perRow_spreadsMapResult(t *testing.T) {
	reg := &stubRegistry{returns: map[string]any{"country": "US", "city": "NYC"}}
	op, err := callFunctionFactory(stageJSON(t, map[string]any{
		"op":   "callFunction",
		"name": "geo::lookup",
	}), &OpContext{Registry: reg})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"ip": "1.1.1.1"}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("rows: %d", len(out))
	}
	if out[0]["country"] != "US" || out[0]["city"] != "NYC" {
		t.Fatalf("map result not spread: %+v", out[0])
	}
	if out[0]["ip"] != "1.1.1.1" {
		t.Fatalf("original row lost: %+v", out[0])
	}
}

func TestCallFunction_perRow_asKey(t *testing.T) {
	reg := &stubRegistry{returns: 42}
	op, err := callFunctionFactory(stageJSON(t, map[string]any{
		"op":   "callFunction",
		"name": "math::add",
		"as":   "sum",
	}), &OpContext{Registry: reg})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, _ := op.Apply(context.Background(), []dsl.Row{{"x": 1}})
	if out[0]["sum"] != 42 {
		t.Fatalf("expected sum=42, got %+v", out[0])
	}
}

func TestCallFunction_perRow_dollarRefResolvesFromRow(t *testing.T) {
	reg := &stubRegistry{returns: "ok"}
	op, err := callFunctionFactory(stageJSON(t, map[string]any{
		"op":   "callFunction",
		"name": "noop",
		"args": map[string]any{"target": "$ip", "static": "value"},
		"as":   "_",
	}), &OpContext{Registry: reg})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if _, err := op.Apply(context.Background(), []dsl.Row{{"ip": "9.9.9.9"}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if reg.lastArgs["target"] != "9.9.9.9" {
		t.Fatalf("$ref not resolved: %+v", reg.lastArgs)
	}
	if reg.lastArgs["static"] != "value" {
		t.Fatalf("static arg lost: %+v", reg.lastArgs)
	}
}

func TestCallFunction_batch_rowsArg(t *testing.T) {
	reg := &stubRegistry{returns: []dsl.Row{{"x": 1}, {"x": 2}}}
	op, err := callFunctionFactory(stageJSON(t, map[string]any{
		"op":   "callFunction",
		"name": "batch::fn",
		"mode": "batch",
	}), &OpContext{Registry: reg})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{{"x": 5}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 2 || out[1]["x"] != 2 {
		t.Fatalf("batch result not used: %+v", out)
	}
	if _, ok := reg.lastArgs["rows"]; !ok {
		t.Fatalf("batch mode did not pass rows arg: %+v", reg.lastArgs)
	}
}

func TestCallFunction_pureDefaultFalse(t *testing.T) {
	op, err := callFunctionFactory(stageJSON(t, map[string]any{
		"op":   "callFunction",
		"name": "x",
	}), &OpContext{Registry: &stubRegistry{}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if op.IsLiveSafe() {
		t.Fatalf("default pure should be false / not live-safe")
	}
}

func TestCallFunction_pureTrue_isLiveSafe(t *testing.T) {
	op, err := callFunctionFactory(stageJSON(t, map[string]any{
		"op":   "callFunction",
		"name": "x",
		"pure": true,
	}), &OpContext{Registry: &stubRegistry{}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if !op.IsLiveSafe() {
		t.Fatalf("pure=true should be live-safe")
	}
}

func TestCallFunction_missingRegistry_errorsAtBuild(t *testing.T) {
	_, err := callFunctionFactory(stageJSON(t, map[string]any{
		"op":   "callFunction",
		"name": "x",
	}), &OpContext{})
	if err == nil {
		t.Fatalf("expected error when registry is nil")
	}
}

func TestCallFunction_execError_wraps(t *testing.T) {
	reg := &stubRegistry{err: errors.New("boom")}
	op, err := callFunctionFactory(stageJSON(t, map[string]any{
		"op":   "callFunction",
		"name": "x",
	}), &OpContext{Registry: reg})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	_, err = op.Apply(context.Background(), []dsl.Row{{"a": 1}})
	if err == nil {
		t.Fatalf("expected wrapped error")
	}
}
