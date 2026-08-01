package rowops

import (
	"context"
	"testing"
)

type passThrough struct{}

func (passThrough) Name() string                                     { return "passthrough" }
func (passThrough) Apply(_ context.Context, in []Row) ([]Row, error) { return in, nil }
func (passThrough) IsLiveSafe() bool                                 { return true }

func TestOperator_interfaceCompiles(t *testing.T) {
	var op Operator = passThrough{}
	if op.Name() != "passthrough" {
		t.Fatalf("name: got %q", op.Name())
	}
	if !op.IsLiveSafe() {
		t.Fatalf("expected live-safe")
	}
	out, err := op.Apply(context.Background(), []Row{{"a": 1}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 1 || out[0]["a"] != 1 {
		t.Fatalf("unexpected output: %+v", out)
	}
}
