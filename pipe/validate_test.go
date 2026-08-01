package pipe

import (
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestValidateShape_emptyPipeIsError(t *testing.T) {
	errs := ValidateShape(nil)
	if len(errs) == 0 {
		t.Fatalf("expected error for empty pipe")
	}
}

func TestValidateShape_unknownOp(t *testing.T) {
	errs := ValidateShape([]dsl.PipeStage{{Op: "nope"}})
	if len(errs) != 1 || errs[0].Op != "nope" {
		t.Fatalf("unexpected: %+v", errs)
	}
}

func TestValidateShape_missingRequiredField(t *testing.T) {
	// sort requires `by`
	stage := mustStage(t, "sort", map[string]any{})
	errs := ValidateShape([]dsl.PipeStage{stage})
	if len(errs) != 1 {
		t.Fatalf("expected one error, got %+v", errs)
	}
}

func TestValidateShape_serviceMissing_isDeferred(t *testing.T) {
	// filter with an expression WHERE needs eval, but at shape-check time we
	// should NOT surface this — the executor will.
	stage := mustStage(t, "filter", map[string]any{"where": map[string]any{"expr": "true"}})
	errs := ValidateShape([]dsl.PipeStage{stage})
	if len(errs) != 0 {
		t.Fatalf("service-missing error leaked into shape check: %+v", errs)
	}
}

func TestValidateShape_happyPath(t *testing.T) {
	stages := []dsl.PipeStage{
		mustStage(t, "filter", map[string]any{"where": map[string]any{"field": "x", "op": "==", "value": 1}}),
		mustStage(t, "limit", map[string]any{"n": 10}),
	}
	errs := ValidateShape(stages)
	if len(errs) != 0 {
		t.Fatalf("happy path errors: %+v", errs)
	}
}
