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
	// ColumnBudgetBytes caps the materialised columns held at once. Zero means
	// no cap, which is the default and right for the ordinary sheet: columns
	// are built only for reduces that take a native kernel, and delegated
	// reduces build none at all.
	//
	// It earns its place on the shape this does not cover — many reduces over
	// many distinct columns that cannot be delegated, where the cache would
	// otherwise grow for the whole evaluation and never release a column whose
	// last reader has already run.
	ColumnBudgetBytes int `json:"columnBudgetBytes,omitempty"`
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

	// delegate computes eligible aggregates elsewhere. See pushdown.go.
	delegate ReduceDelegate

	columnBudget int
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
		order:        order,
		compiled:     compiled,
		refs:         refs,
		isFormula:    isFormula,
		policy:       policy,
		columnBudget: cfg.ColumnBudgetBytes,
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
	// Complete reports whether the reduces saw every row that matched, rather
	// than a prefix cut short by a cap.
	//
	// Apply sets it true: a caller that handed over a slice had everything it
	// had. ApplyStream sets it from the source, and it is false when iteration
	// stopped early. Aggregate pushdown may only be considered when it is
	// true, since an aggregate computed by the database spans the whole match
	// and would otherwise disagree with the rows being evaluated.
	Complete bool
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
	// Absent means unknown, and unknown is not complete: a caller that handed
	// over a slice with no provenance has not established that these are every
	// matching row, and treating silence as a yes is how an aggregate ends up
	// delegated on an assumption.
	res.Complete, _ = sourceCompleteFrom(ctx)
	run := &runState{
		// One args map, reused for every row of every formula. CompiledExpr is
		// contractually forbidden from retaining it.
		args:      make(map[string]any, len(s.order)+8),
		columns:   make(map[string]Column, len(s.order)),
		delegated: map[string]any{},
	}

	defer run.close()

	// Asked before the walk so a delegated answer is already in hand when the
	// reduce that needs it comes round, and so one round trip covers them all
	// rather than one per reduce.
	s.delegateReduces(ctx, run, res)

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
	if v, ok := run.delegated[f.As]; ok {
		if err := checkDelegated(f.As, v); err != nil {
			return err
		}
		res.Scalars[f.As] = v
		return nil
	}
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
		run.touch(name)
		return col, nil
	}
	// Evicted earlier. Reading it back is roughly an order of magnitude
	// cheaper than another pass over every row map, which is the only reason
	// the spill file exists.
	if col, ok := run.spill.Get(name); ok {
		run.admit(name, col, s.columnBudget)
		return col, nil
	}

	b := NewColumnBuilder(len(in))
	for _, row := range in {
		b.Append(row[name])
	}
	col := b.Build()
	run.admit(name, col, s.columnBudget)
	return col, nil
}

// touch marks a column as most recently used.
func (r *runState) touch(name string) {
	for i, h := range r.held {
		if h == name {
			r.held = append(append(r.held[:i:i], r.held[i+1:]...), name)
			return
		}
	}
}

// admit caches a column, evicting older ones while the budget is exceeded.
//
// A budget of zero disables all of it: no accounting, no eviction, no spill
// file. That is the default, because the ordinary sheet holds one or two
// columns and paying for a policy it never triggers would be pure overhead.
func (r *runState) admit(name string, col Column, budget int) {
	r.columns[name] = col
	if budget <= 0 {
		return
	}
	r.touch(name)
	if !contains(r.held, name) {
		r.held = append(r.held, name)
	}
	r.bytes += columnBytes(col)

	// Never evict the column just admitted: the caller is about to read it,
	// and evicting it would spill and restore the same bytes for nothing.
	for r.bytes > budget && len(r.held) > 1 {
		victim := r.held[0]
		r.held = r.held[1:]
		evicted := r.columns[victim]
		delete(r.columns, victim)
		r.bytes -= columnBytes(evicted)

		if r.spill == nil {
			st, err := newSpillStore()
			if err != nil {
				// No file, no restore path. The column is simply gone and
				// will be rebuilt if it is wanted again.
				continue
			}
			r.spill = st
		}
		r.spill.Put(victim, evicted)
	}
}

// runState is the per-Apply scratch space: the argument map every expression
// binds into, the columns materialised so far, and any answers a delegate
// supplied.
type runState struct {
	args    map[string]any
	columns map[string]Column
	// delegated holds answers supplied by a ReduceDelegate, keyed by formula
	// name. A reduce absent from here is computed locally.
	delegated map[string]any

	// held tracks the resident columns in touch order, oldest first, and the
	// bytes they account for. Only used when a budget is set.
	held  []string
	bytes int
	spill *spillStore
}

// close releases anything the run acquired. Always safe to call.
func (r *runState) close() {
	if r.spill != nil {
		_ = r.spill.Close()
		r.spill = nil
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
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
