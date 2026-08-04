package sheet

import "fmt"

// ErrorPolicy decides what an evaluation error does to the run.
type ErrorPolicy int

const (
	// PolicyFail aborts on the first evaluation error.
	//
	// The default, matching every other pipe operator: a stage that emits
	// partially-wrong rows is worse than one that stops, because downstream
	// stages compute on the damage and nothing surfaces it. A spreadsheet's
	// #DIV/0! model works because a human is looking at the cell; a query
	// pipeline has no such observer.
	PolicyFail ErrorPolicy = iota
	// PolicyNull writes null into the failing cell and continues, for the
	// imported-workbook case where a few bad rows should not fail a query.
	PolicyNull
)

// ParsePolicy reads the config spelling. The empty string is the default.
func ParsePolicy(s string) (ErrorPolicy, error) {
	switch s {
	case "", "fail":
		return PolicyFail, nil
	case "null":
		return PolicyNull, nil
	}
	return PolicyFail, fmt.Errorf("sheet: unknown onError policy %q (want %q or %q)", s, "fail", "null")
}

// MaxRecordedErrors bounds what PolicyNull retains. Collecting one entry per
// failing row over a ten-million-row sheet is its own failure mode.
const MaxRecordedErrors = 100

// CellError records one evaluation failure under PolicyNull. Row is -1 for a
// reduce, which has no row of its own.
type CellError struct {
	Formula string `json:"formula"`
	Row     int    `json:"row"`
	Message string `json:"message"`
}
