package sheet

import (
	"strings"
	"testing"
)

func TestFormula_kindComesFromWhichKeyIsSet(t *testing.T) {
	tests := []struct {
		name string
		f    Formula
		want Kind
	}{
		{"expr only", Formula{As: "a", Expr: "x"}, KindColumn},
		{"reduce only", Formula{As: "a", Reduce: "x sum"}, KindReduce},
		{"neither", Formula{As: "a"}, KindInvalid},
		{"both", Formula{As: "a", Expr: "x", Reduce: "x sum"}, KindInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.f.Kind(); got != tt.want {
				t.Errorf("Kind() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormula_sourceFollowsTheKind(t *testing.T) {
	if got := (Formula{As: "a", Expr: "x"}).Source(); got != "x" {
		t.Errorf("column source = %q", got)
	}
	if got := (Formula{As: "a", Reduce: "x sum"}).Source(); got != "x sum" {
		t.Errorf("reduce source = %q", got)
	}
}

func TestValidateFormulas_rejectsDuplicateNames(t *testing.T) {
	err := validateFormulas([]Formula{
		{As: "total", Expr: "x"},
		{As: "total", Expr: "y"},
	})
	if err == nil || !strings.Contains(err.Error(), "total") {
		t.Fatalf("want an error naming the duplicate, got %v", err)
	}
}

func TestValidateFormulas_rejectsMissingName(t *testing.T) {
	if err := validateFormulas([]Formula{{Expr: "x"}}); err == nil {
		t.Fatal("want an error for a formula with no `as`")
	}
}

func TestValidateFormulas_rejectsBothKeys(t *testing.T) {
	err := validateFormulas([]Formula{{As: "a", Expr: "x", Reduce: "x sum"}})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("want an error explaining the two keys are exclusive, got %v", err)
	}
}

func TestValidateFormulas_rejectsAnEmptySheet(t *testing.T) {
	if err := validateFormulas(nil); err == nil {
		t.Fatal("a sheet with no formulas is meaningless and must be rejected")
	}
}

func TestParsePolicy(t *testing.T) {
	if p, err := ParsePolicy(""); err != nil || p != PolicyFail {
		t.Errorf("empty must default to fail, got %v %v", p, err)
	}
	if p, err := ParsePolicy("fail"); err != nil || p != PolicyFail {
		t.Errorf("fail: got %v %v", p, err)
	}
	if p, err := ParsePolicy("null"); err != nil || p != PolicyNull {
		t.Errorf("null: got %v %v", p, err)
	}
	if _, err := ParsePolicy("shrug"); err == nil {
		t.Error("unknown policy must error")
	}
}
