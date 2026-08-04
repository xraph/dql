package sheet

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/xraph/dql/internal/rowops"
)

// Config is a sheet's declaration.
type Config struct {
	Formulas []Formula `json:"formulas"`
	// OnError is "fail" (default) or "null". See ErrorPolicy.
	OnError string `json:"onError,omitempty"`
}

// Sheet is a compiled, ordered set of formulas ready to run over rows.
type Sheet struct {
	order     []Formula
	compiled  map[string]CompiledExpr
	refs      map[string][]string
	isFormula map[string]bool
	policy    ErrorPolicy

	// disableKernels forces every reduce through the compiler. Set only by the
	// equivalence tests, which assert the two paths agree.
	disableKernels bool
}

// Compile validates a sheet, resolves its dependency order, and prepares every
// expression.
//
// Everything detectable without knowing the input's columns is settled here, so
// a malformed sheet fails while the query is being validated rather than
// partway through a scan. What is left for Apply is exactly what needs a
// schema: whether each referenced name is a real column.
func Compile(cfg Config, c ExprCompiler) (*Sheet, error) {
	if c == nil {
		return nil, fmt.Errorf("sheet: an expression compiler is required")
	}
	if err := validateFormulas(cfg.Formulas); err != nil {
		return nil, err
	}
	policy, err := ParsePolicy(cfg.OnError)
	if err != nil {
		return nil, err
	}

	refs := make(map[string][]string, len(cfg.Formulas))
	compiled := make(map[string]CompiledExpr, len(cfg.Formulas))
	isFormula := make(map[string]bool, len(cfg.Formulas))
	for _, f := range cfg.Formulas {
		isFormula[f.As] = true
	}

	for _, f := range cfg.Formulas {
		ids, err := c.FreeIdentifiers(f.Source())
		if err != nil {
			return nil, fmt.Errorf("sheet: formula %q: %w", f.As, err)
		}
		refs[f.As] = ids

		ce, err := c.Compile(f.Source())
		if err != nil {
			return nil, fmt.Errorf("sheet: formula %q: %w", f.As, err)
		}
		compiled[f.As] = ce
	}

	order, err := topoSort(cfg.Formulas, refs)
	if err != nil {
		return nil, err
	}

	return &Sheet{
		order:     order,
		compiled:  compiled,
		refs:      refs,
		isFormula: isFormula,
		policy:    policy,
	}, nil
}

// Names returns the formula names in evaluation order.
func (s *Sheet) Names() []string {
	out := make([]string, len(s.order))
	for i, f := range s.order {
		out[i] = f.As
	}
	return out
}

// Result is one sheet evaluation.
type Result struct {
	Rows    []rowops.Row
	Scalars map[string]any
	// Errors holds up to MaxRecordedErrors entries under PolicyNull.
	Errors []CellError
	// ErrorCount is the true total, which may exceed len(Errors).
	ErrorCount int
}

// Apply evaluates every formula over in, in dependency order.
//
// Column formulas are evaluated one whole column at a time rather than one
// whole row at a time: each formula's expression is already compiled, and
// walking the column keeps that one expression and its argument map hot.
func (s *Sheet) Apply(ctx context.Context, in []rowops.Row) (*Result, error) {
	// With no rows there is no schema to check against, and an empty result
	// set is a normal thing for an upstream filter to produce. Reporting every
	// reference as unresolved there would turn a legitimate empty answer into
	// an error, so the check is skipped rather than failed.
	if len(in) > 0 {
		if err := s.checkReferences(columnsOf(in)); err != nil {
			return nil, err
		}
	}

	res := &Result{Rows: in, Scalars: make(map[string]any, len(s.order))}
	run := &runState{
		// One args map, reused for every row of every formula. CompiledExpr is
		// contractually forbidden from retaining it.
		args:    make(map[string]any, len(s.order)+8),
		columns: make(map[string]Column, len(s.order)),
	}

	for _, f := range s.order {
		// Checked per formula rather than per row: one column's evaluation is
		// already bounded work, and a select per row costs measurably on a
		// large scan for no gain in responsiveness.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		if f.Kind() == KindReduce {
			err = s.evalReduce(ctx, f, in, run, res)
		} else {
			err = s.evalColumn(ctx, f, in, run, res)
		}
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (s *Sheet) evalColumn(ctx context.Context, f Formula, in []rowops.Row, run *runState, res *Result) error {
	ce := s.compiled[f.As]
	refs := s.refs[f.As]
	args := run.args

	for i, row := range in {
		for _, name := range refs {
			if v, ok := res.Scalars[name]; ok {
				args[name] = v
				continue
			}
			args[name] = row[name]
		}

		val, err := ce.Eval(ctx, args)
		if err != nil {
			if s.policy == PolicyFail {
				return fmt.Errorf("sheet: formula %q, row %d: %w", f.As, i, err)
			}
			res.record(f.As, i, err)
			val = nil
		}
		row[f.As] = val
	}
	return nil
}

func (s *Sheet) evalReduce(ctx context.Context, f Formula, in []rowops.Row, run *runState, res *Result) error {
	args := run.args
	if k, colName, ok := s.kernelFor(f); ok {
		col, err := s.columnFor(colName, in, run, res)
		if err != nil {
			return err
		}
		val, kErr := k.Reduce(col)
		if kErr != nil {
			if s.policy == PolicyFail {
				return fmt.Errorf("sheet: reduce %q: %w", f.As, kErr)
			}
			res.record(f.As, -1, kErr)
			val = nil
		}
		res.Scalars[f.As] = val
		return nil
	}

	// A reduce reads whole columns: bind each reference to every row's value.
	for _, name := range s.refs[f.As] {
		if v, ok := res.Scalars[name]; ok {
			args[name] = v
			continue
		}
		vals := make([]any, len(in))
		for i, row := range in {
			vals[i] = row[name]
		}
		args[name] = vals
	}

	val, err := s.compiled[f.As].Eval(ctx, args)
	if err != nil {
		if s.policy == PolicyFail {
			return fmt.Errorf("sheet: reduce %q: %w", f.As, err)
		}
		res.record(f.As, -1, err)
		val = nil
	}
	res.Scalars[f.As] = val
	return nil
}

// kernelFor reports the native kernel for a reduce, when its source is exactly
// one registered aggregate applied to one column.
//
// The match is textual because recognising a call inside an arbitrary
// expression would mean parsing it, which is the host's job. Anything that does
// not match exactly goes to the compiler, so a miss costs performance and never
// correctness.
func (s *Sheet) kernelFor(f Formula) (ReduceFunc, string, bool) {
	if s.disableKernels || f.Kind() != KindReduce {
		return nil, "", false
	}
	refs := s.refs[f.As]
	if len(refs) != 1 {
		return nil, "", false
	}
	col := refs[0]
	src := strings.TrimSpace(f.Reduce)

	for _, name := range reduceNames() {
		if src == name+"("+col+")" || src == col+" "+name {
			k, _ := LookupReduce(name)
			return k, col, true
		}
	}
	return nil, "", false
}

// columnFor builds a typed Column from the named field of every row, caching
// it for the rest of the run.
//
// Without the cache a sheet with several reduces over the same column rebuilt
// it each time, and that construction cost dominated the scan the typed
// backing was meant to make cheap — the kernel path measured slower than the
// boxed one it replaced.
//
// The cache is keyed on the column not having changed since it was built,
// which holds because a column formula writes its output once, in topological
// order, before anything that reads it runs.
func (s *Sheet) columnFor(name string, in []rowops.Row, run *runState, res *Result) (Column, error) {
	if _, isScalar := res.Scalars[name]; isScalar {
		return nil, fmt.Errorf("sheet: %q is a scalar, not a column", name)
	}
	if col, ok := run.columns[name]; ok {
		return col, nil
	}
	b := NewColumnBuilder(len(in))
	for _, row := range in {
		b.Append(row[name])
	}
	col := b.Build()
	run.columns[name] = col
	return col, nil
}

// runState is the per-Apply scratch space: the argument map every expression
// binds into, and the columns materialised so far.
type runState struct {
	args    map[string]any
	columns map[string]Column
}

func (r *Result) record(formula string, row int, err error) {
	r.ErrorCount++
	if len(r.Errors) < MaxRecordedErrors {
		r.Errors = append(r.Errors, CellError{Formula: formula, Row: row, Message: err.Error()})
	}
}

// checkReferences reports identifiers that name neither a source column nor
// another formula. Deferred to Apply because the input's columns are not known
// while the sheet is being compiled.
func (s *Sheet) checkReferences(cols map[string]bool) error {
	var unresolved []string
	seen := map[string]bool{}
	for _, f := range s.order {
		for _, ref := range s.refs[f.As] {
			if cols[ref] || s.isFormula[ref] || seen[ref] {
				continue
			}
			seen[ref] = true
			unresolved = append(unresolved, ref)
		}
	}
	if len(unresolved) == 0 {
		return nil
	}
	sort.Strings(unresolved)
	return fmt.Errorf("sheet: unresolved reference(s): %s — not a column of the input nor a formula in this sheet",
		strings.Join(unresolved, ", "))
}

// columnsOf collects every key present across the input. Rows may be sparse, so
// a key missing from row 0 is still a column of the sheet.
func columnsOf(in []rowops.Row) map[string]bool {
	out := make(map[string]bool)
	for _, row := range in {
		for k := range row {
			out[k] = true
		}
	}
	return out
}
