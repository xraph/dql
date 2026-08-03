package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/internal/rowops"
)

type stubEval struct{}

func (stubEval) Eval(context.Context, string, map[string]any) (any, error) { return nil, nil }

type stubAlgos struct{}

func (stubAlgos) LiveSafe(string) (bool, bool) { return true, true }
func (stubAlgos) Execute(context.Context, string, map[string]any, []rowops.Row) ([]rowops.Row, error) {
	return nil, nil
}

// fullyWired is what a host looks like with every service supplied. Requirement
// checks should find nothing missing under it.
func fullyWired() *OpContext {
	return &OpContext{
		Eval:       stubEval{},
		Registry:   &stubRegistry{},
		AppCaller:  &stubAppCaller{},
		Formula:    &stubFormula{},
		Classic:    &stubClassic{},
		Algorithms: stubAlgos{},
	}
}

func TestAvailable_selfContainedOpsNeedNothing(t *testing.T) {
	// The point of declaring requirements is that most operators have none.
	// If a pure operator reported unavailable under a nil context, an editor
	// with no host attached would offer nothing at all.
	idx := CatalogIndex()
	for _, name := range []string{"project", "sort", "limit", "distinct", "pivot", "cast"} {
		m, ok := idx[name]
		if !ok {
			t.Fatalf("%s missing from the catalog", name)
		}
		if !m.Available(nil) {
			t.Errorf("%s should work with no host services, needs %v", name, m.Missing(nil))
		}
	}
}

func TestAvailable_hostBackedOpsReportWhatTheyLack(t *testing.T) {
	idx := CatalogIndex()
	for name, want := range map[string]Requirement{
		"callApp":      ReqAppCaller,
		"callFunction": ReqFunctionRegistry,
		"algo":         ReqAlgorithms,
		"lookup":       ReqClassic,
		"branch":       ReqEval,
	} {
		m, ok := idx[name]
		if !ok {
			t.Fatalf("%s missing from the catalog", name)
		}
		if m.Available(nil) {
			t.Errorf("%s claims to work with no host services", name)
			continue
		}
		var found bool
		for _, r := range m.Missing(nil) {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: missing %v, expected it to name %q", name, m.Missing(nil), want)
		}
	}
}

func TestAvailable_becomesTrueOnceTheServiceIsWired(t *testing.T) {
	branch := CatalogIndex()["branch"]

	// The case that actually happens: a host passes a real OpContext with some
	// services wired and others not. A nil context short-circuits before any
	// per-service check, so it proves nothing about the checks themselves.
	partial := &OpContext{Classic: &stubClassic{}}
	if branch.Available(partial) {
		t.Error("branch needs an evaluator, but a context supplying only a classic executor satisfied it")
	}

	octx := &OpContext{Eval: stubEval{}}
	if !branch.Available(octx) {
		t.Errorf("branch should be available once an evaluator is supplied, still missing %v", branch.Missing(octx))
	}
}

// Each requirement must be satisfied by its own field and no other. Wiring one
// service should never make an operator that needs a different one look ready.
func TestHas_eachRequirementReadsItsOwnField(t *testing.T) {
	byReq := map[Requirement]func(*OpContext){
		ReqEval:             func(o *OpContext) { o.Eval = stubEval{} },
		ReqFunctionRegistry: func(o *OpContext) { o.Registry = &stubRegistry{} },
		ReqAppCaller:        func(o *OpContext) { o.AppCaller = &stubAppCaller{} },
		ReqFormula:          func(o *OpContext) { o.Formula = &stubFormula{} },
		ReqClassic:          func(o *OpContext) { o.Classic = &stubClassic{} },
		ReqAlgorithms:       func(o *OpContext) { o.Algorithms = stubAlgos{} },
	}

	for req, wire := range byReq {
		octx := &OpContext{}
		if octx.has(req) {
			t.Errorf("%q is satisfied by an empty context", req)
		}
		wire(octx)
		if !octx.has(req) {
			t.Errorf("%q is not satisfied by wiring its own service", req)
		}
		for other := range byReq {
			if other != req && octx.has(other) {
				t.Errorf("wiring %q also satisfied %q", req, other)
			}
		}
	}
}

func TestAvailableOps_narrowsToWhatCanRun(t *testing.T) {
	all := len(Catalog())
	bare := len(AvailableOps(nil))
	withEval := len(AvailableOps(&OpContext{Eval: stubEval{}}))

	if bare >= all {
		t.Errorf("a bare context should offer fewer than all %d operators, got %d", all, bare)
	}
	if withEval <= bare {
		t.Errorf("wiring an evaluator should unlock operators: bare=%d withEval=%d", bare, withEval)
	}
	if got := len(AvailableOps(fullyWired())); got != all {
		t.Errorf("a fully wired context should offer every operator: got %d of %d", got, all)
	}
	// Whatever is offered must actually be usable — that is the whole contract.
	for _, m := range AvailableOps(nil) {
		if !m.Available(nil) {
			t.Errorf("%s was offered but is not available", m.Name)
		}
	}
}

func TestMissingRequirements_isEmptyWhenFullyWired(t *testing.T) {
	if missing := MissingRequirements(fullyWired()); len(missing) > 0 {
		t.Errorf("a fully wired context should have no gaps, got %v", missing)
	}

	// With nothing wired, the operators that need a host show up — this is what
	// a host would log at start-up rather than discover one failed query later.
	missing := MissingRequirements(nil)
	if len(missing) == 0 {
		t.Fatal("a bare context should report the operators it cannot run")
	}
	if _, ok := missing["callApp"]; !ok {
		t.Errorf("callApp needs an app caller but was not reported: %v", missing)
	}
	if _, ok := missing["project"]; ok {
		t.Error("project is self-contained and should never be reported as missing something")
	}
}

// Every requirement a catalog entry declares must be one has() knows about. A
// new constant added to the catalog but not to the switch would make its
// operators permanently unavailable, silently.
func TestRequirements_areAllRecognised(t *testing.T) {
	full := fullyWired()
	for _, m := range Catalog() {
		for _, r := range m.Requires {
			if !full.has(r) {
				t.Errorf("%s declares requirement %q that a fully wired context does not satisfy — missing a case in has()", m.Name, r)
			}
		}
	}
}

// A requirement is only meaningful for an operator that can actually be built.
// Declaring one on a name with no factory would mislead a caller into wiring a
// service that nothing consumes.
func TestRequires_onlyOnOpsWithFactories(t *testing.T) {
	for _, m := range Catalog() {
		if len(m.Requires) > 0 && !Known(m.Name) {
			t.Errorf("%s declares requirements %v but has no registered factory", m.Name, m.Requires)
		}
	}
}
