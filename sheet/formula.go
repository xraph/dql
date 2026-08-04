package sheet

import "fmt"

// Kind distinguishes the two formula shapes.
type Kind int

const (
	// KindInvalid is a formula that sets neither or both expression keys.
	KindInvalid Kind = iota
	// KindColumn evaluates once per row and produces a column.
	KindColumn
	// KindReduce evaluates once over whole columns and produces a scalar.
	KindReduce
)

func (k Kind) String() string {
	switch k {
	case KindColumn:
		return "expr"
	case KindReduce:
		return "reduce"
	case KindInvalid:
		return "invalid"
	}
	return "invalid"
}

// Formula is one named calculation.
//
// Exactly one of Expr and Reduce is set, and which one determines the kind.
// There is deliberately no separate scope annotation: an annotation could
// disagree with the expression's shape, and then one of them would be wrong.
type Formula struct {
	As     string `json:"as"`
	Expr   string `json:"expr,omitempty"`
	Reduce string `json:"reduce,omitempty"`
}

// Kind reports which of the two shapes this formula is.
func (f Formula) Kind() Kind {
	switch {
	case f.Expr != "" && f.Reduce == "":
		return KindColumn
	case f.Reduce != "" && f.Expr == "":
		return KindReduce
	}
	return KindInvalid
}

// Source returns whichever expression the formula carries.
func (f Formula) Source() string {
	if f.Kind() == KindReduce {
		return f.Reduce
	}
	return f.Expr
}

func validateFormulas(fs []Formula) error {
	if len(fs) == 0 {
		return fmt.Errorf("sheet: at least one formula is required")
	}
	seen := make(map[string]bool, len(fs))
	for i, f := range fs {
		if f.As == "" {
			return fmt.Errorf("sheet: formula %d has no `as` name", i)
		}
		if f.Kind() == KindInvalid {
			return fmt.Errorf("sheet: formula %q must set exactly one of `expr` or `reduce`", f.As)
		}
		if seen[f.As] {
			return fmt.Errorf("sheet: formula %q is defined more than once", f.As)
		}
		seen[f.As] = true
	}
	return nil
}
