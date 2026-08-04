package sheet

import (
	"fmt"
	"sort"
)

// Registry holds a host's own reduce kernels.
//
// It does not define what a sheet may say. The expression language owns the
// vocabulary, so `reduce: "p95(latency)"` already works wherever the host's
// language has p95 — it is simply evaluated by the compiler over a boxed slice.
// What registering a kernel adds is a scan over the typed column instead, and
// the option of having the aggregate computed by the source. Both are
// optimisations, which is why an unregistered name is not an error.
//
// Per-OpContext rather than global, unlike the built-in table. Two hosts in one
// process — a test binary is the ordinary case — would otherwise collide, and
// "what does this deployment support" would stop being answerable per query.
type Registry struct {
	fns map[string]ReduceFunc
}

// NewRegistry returns an empty registry. A nil *Registry is usable and means
// built-ins only, so a host that registers nothing need not construct one.
func NewRegistry() *Registry { return &Registry{fns: map[string]ReduceFunc{}} }

// RegisterReduce adds a kernel.
//
// A name already taken by a built-in is refused rather than shadowed. Shadowing
// sum would make the same sheet mean different things on different hosts, which
// is the one outcome portability actually forbids — every other difference
// between deployments is visible in the query.
func (r *Registry) RegisterReduce(f ReduceFunc) error {
	if r == nil {
		return fmt.Errorf("sheet: register %q: nil registry", f.Name())
	}
	name := f.Name()
	if name == "" {
		return fmt.Errorf("sheet: a reduce kernel must have a name")
	}
	if _, isBuiltin := builtinReduces[name]; isBuiltin {
		return fmt.Errorf("sheet: %q is a built-in reduce and cannot be replaced", name)
	}
	if r.fns == nil {
		r.fns = map[string]ReduceFunc{}
	}
	if _, dup := r.fns[name]; dup {
		return fmt.Errorf("sheet: reduce %q is already registered", name)
	}
	r.fns[name] = f
	return nil
}

// LookupReduce finds a host kernel. Built-ins are not consulted here; see
// (*Sheet).lookupReduce for the combined view.
func (r *Registry) LookupReduce(name string) (ReduceFunc, bool) {
	if r == nil || r.fns == nil {
		return nil, false
	}
	f, ok := r.fns[name]
	return f, ok
}

// ReduceNames lists the host kernels, sorted. Useful for diagnostics and for a
// host reporting what its deployment accelerates.
func (r *Registry) ReduceNames() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.fns))
	for name := range r.fns {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Option configures a compiled sheet.
type Option func(*Sheet)

// WithRegistry attaches a host's reduce kernels.
func WithRegistry(r *Registry) Option {
	return func(s *Sheet) { s.registry = r }
}

// lookupReduce resolves a kernel from the host's registry first and the
// built-in table second. The two cannot collide — RegisterReduce refuses a
// built-in name — so the order is for clarity rather than precedence.
func (s *Sheet) lookupReduce(name string) (ReduceFunc, bool) {
	if f, ok := s.registry.LookupReduce(name); ok {
		return f, true
	}
	return LookupReduce(name)
}

// reduceCandidates lists every kernel name this sheet could match, sorted so
// textual matching does not depend on map iteration.
func (s *Sheet) reduceCandidates() []string {
	names := reduceNames()
	if hosted := s.registry.ReduceNames(); len(hosted) > 0 {
		names = append(names, hosted...)
		sort.Strings(names)
	}
	return names
}
