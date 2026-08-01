package pipe

import (
	"context"
	"reflect"
	"testing"

	"github.com/xraph/dql/dsl"
)

// Phase 5 parity contract.
//
// The pipeline extension's FunctionStage and this package's callFunction op
// are intended to share the same row-merge semantics. They cannot share Go
// code today because pipe lives under extensions/query/internal/ and the
// pipeline extension cannot import internal packages. This test file
// replicates pipeline's mergeResult inline and asserts that the two
// implementations produce byte-identical row maps for representative inputs.
//
// If the parity ever drifts, this test fails — which means either:
//   - we intentionally diverged and should update the test, OR
//   - we accidentally diverged and should restore parity.
//
// The test uses NewCallFunctionOp directly to mirror what an external
// consumer (post-internal-promotion) would write.

// pipelineMergeResult mirrors extensions/pipeline/internal/services/stages/
// function_stage.go's mergeResult.
//
// Source as of Phase 5 baseline:
//
//	func mergeResult(row dsl.Row, result any) dsl.Row {
//	    out := make(dsl.Row, len(row))
//	    for k, v := range row { out[k] = v }
//	    switch r := result.(type) {
//	    case map[string]any:
//	        for k, v := range r { out[k] = v }
//	    default:
//	        out["_result"] = result
//	    }
//	    return out
//	}
func pipelineMergeResult(row dsl.Row, result any) dsl.Row {
	out := make(dsl.Row, len(row))
	for k, v := range row {
		out[k] = v
	}
	switch r := result.(type) {
	case map[string]any:
		for k, v := range r {
			out[k] = v
		}
	default:
		out["_result"] = result
	}
	return out
}

// stubReg returns a fixed result and ignores args.
type stubReg struct {
	out any
}

func (s *stubReg) Execute(_ context.Context, _ string, _ map[string]any) (any, error) {
	return s.out, nil
}

func TestParity_callFunction_mapResultSpread(t *testing.T) {
	row := dsl.Row{"id": 1, "name": "alpha"}
	result := map[string]any{"country": "US", "city": "NYC"}

	expected := pipelineMergeResult(row, result)

	op, err := NewCallFunctionOp(CallFunctionConfig{
		Name:              "geo::lookup",
		Mode:              "perRow",
		DisableRefResolve: true, // pipeline does not $-resolve parameters
	}, &stubReg{out: result})
	if err != nil {
		t.Fatalf("NewCallFunctionOp: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{row})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !reflect.DeepEqual(out[0], expected) {
		t.Fatalf("parity broken:\n  pipe op:  %+v\n  pipeline: %+v", out[0], expected)
	}
}

func TestParity_callFunction_scalarUnderResultKey(t *testing.T) {
	row := dsl.Row{"id": 1}
	result := 42

	expected := pipelineMergeResult(row, result)

	op, err := NewCallFunctionOp(CallFunctionConfig{
		Name:              "math::answer",
		Mode:              "perRow",
		DisableRefResolve: true,
	}, &stubReg{out: result})
	if err != nil {
		t.Fatalf("NewCallFunctionOp: %v", err)
	}
	out, _ := op.Apply(context.Background(), []dsl.Row{row})
	if !reflect.DeepEqual(out[0], expected) {
		t.Fatalf("scalar parity broken:\n  pipe op:  %+v\n  pipeline: %+v", out[0], expected)
	}
}

// TestParity_disableRefResolve_preservesLiteralDollarValues asserts that
// when DisableRefResolve is true (pipeline mode) literal "$foo" strings in
// args pass through untouched — which is what pipeline's per-row arg merge
// does today. This is the key behaviour difference between the two callers
// and the test pins it down so we don't regress.
func TestParity_disableRefResolve_preservesLiteralDollarValues(t *testing.T) {
	reg := &capturingReg{}
	op, err := NewCallFunctionOp(CallFunctionConfig{
		Name:              "noop",
		Mode:              "perRow",
		Args:              map[string]any{"hint": "$ip"}, // literal "$ip", not a ref
		DisableRefResolve: true,
	}, reg)
	if err != nil {
		t.Fatalf("NewCallFunctionOp: %v", err)
	}
	_, err = op.Apply(context.Background(), []dsl.Row{{"ip": "1.2.3.4"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if reg.lastArgs["hint"] != "$ip" {
		t.Fatalf("DisableRefResolve must preserve $ literals, got %v", reg.lastArgs["hint"])
	}
}

func TestParity_refResolveDefault_resolvesDollarValues(t *testing.T) {
	reg := &capturingReg{}
	op, err := NewCallFunctionOp(CallFunctionConfig{
		Name: "noop",
		Mode: "perRow",
		Args: map[string]any{"hint": "$ip"},
		// DisableRefResolve omitted → $-refs resolve.
	}, reg)
	if err != nil {
		t.Fatalf("NewCallFunctionOp: %v", err)
	}
	_, err = op.Apply(context.Background(), []dsl.Row{{"ip": "1.2.3.4"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if reg.lastArgs["hint"] != "1.2.3.4" {
		t.Fatalf("default mode should resolve $-refs, got %v", reg.lastArgs["hint"])
	}
}

type capturingReg struct {
	lastArgs map[string]any
}

func (c *capturingReg) Execute(_ context.Context, _ string, args map[string]any) (any, error) {
	c.lastArgs = make(map[string]any, len(args))
	for k, v := range args {
		c.lastArgs[k] = v
	}
	return nil, nil
}
