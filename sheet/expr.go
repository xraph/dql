package sheet

import "context"

// ExprCompiler prepares an expression once for repeated evaluation and reports
// what it references.
//
// Both halves live on the host because both require parsing, and parsing
// requires owning the grammar — which this package deliberately does not. A
// text-level approximation is not an option for FreeIdentifiers: it would match
// names inside string literals, so a formula named `status` and an expression
// containing "status" would yield an edge that does not exist, and a phantom
// edge surfaces as a circular-dependency error for an acyclic sheet.
type ExprCompiler interface {
	// FreeIdentifiers reports the unbound names the expression references, in
	// a stable order. Names bound within the expression (lambda parameters,
	// let bindings) and called function names must be excluded.
	FreeIdentifiers(expr string) ([]string, error)

	// Compile prepares an expression for evaluation, returning parse and
	// resolution errors here rather than deferring them to Eval.
	Compile(expr string) (CompiledExpr, error)
}

// CompiledExpr is one prepared expression.
type CompiledExpr interface {
	// Eval binds args and evaluates.
	//
	// Implementations must not retain args: the sheet reuses a single map
	// across every row of every formula, so a retained reference would be
	// overwritten underneath the holder.
	Eval(ctx context.Context, args map[string]any) (any, error)
}
