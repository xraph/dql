package sheet

import (
	"context"
	"strings"
	"testing"

	"github.com/xraph/dql/internal/rowops"
)

// p95Kernel is the shape a host would register: an aggregate its expression
// language already has, given a typed scan and a name the source understands.
type p95Kernel struct {
	push  string
	calls int
}

func (k *p95Kernel) Name() string         { return "p95" }
func (k *p95Kernel) PushdownName() string { return k.push }

func (k *p95Kernel) Reduce(col Column) (any, error) {
	k.calls++
	var vals []float64
	eachFloat(col, func(f float64) { vals = append(vals, f) })
	if len(vals) == 0 {
		return nil, nil
	}
	// Nearest-rank, which is enough to tell this apart from a mean.
	idx := (len(vals)*95 + 99) / 100
	if idx > len(vals) {
		idx = len(vals)
	}
	sortFloats(vals)
	return vals[idx-1], nil
}

func sortFloats(v []float64) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1] > v[j]; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}

func registryWith(t *testing.T, f ReduceFunc) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.RegisterReduce(f); err != nil {
		t.Fatalf("RegisterReduce: %v", err)
	}
	return r
}

func TestRegistry_refusesToShadowABuiltin(t *testing.T) {
	// Shadowing sum would make the same sheet mean different things on
	// different hosts, which is the one difference portability forbids.
	err := NewRegistry().RegisterReduce(kernel{name: "sum", push: "sum", fn: kSum})
	if err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("want a refusal naming the collision, got %v", err)
	}
}

func TestRegistry_refusesADuplicate(t *testing.T) {
	r := registryWith(t, &p95Kernel{})
	if err := r.RegisterReduce(&p95Kernel{}); err == nil {
		t.Fatal("registering the same name twice must be refused")
	}
}

func TestRegistry_refusesAnUnnamedKernel(t *testing.T) {
	if err := NewRegistry().RegisterReduce(kernel{}); err == nil {
		t.Fatal("a kernel with no name must be refused")
	}
}

func TestRegistry_nilIsUsableAndMeansBuiltinsOnly(t *testing.T) {
	var r *Registry
	if _, ok := r.LookupReduce("p95"); ok {
		t.Error("a nil registry must hold nothing")
	}
	if names := r.ReduceNames(); len(names) != 0 {
		t.Errorf("ReduceNames on nil = %v", names)
	}
	if err := r.RegisterReduce(&p95Kernel{}); err == nil {
		t.Error("registering into a nil registry must error rather than panic")
	}
}

func TestRegistry_hostKernelIsUsedForAReduce(t *testing.T) {
	k := &p95Kernel{}
	s, err := Compile(
		Config{Formulas: []Formula{{As: "tail", Reduce: "latency p95"}}},
		newFakeCompilerWith("p95"),
		WithRegistry(registryWith(t, k)),
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	rows := make([]rowops.Row, 100)
	for i := range rows {
		rows[i] = rowops.Row{"latency": float64(i + 1)}
	}
	res, err := s.Apply(context.Background(), rows)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if k.calls != 1 {
		t.Errorf("host kernel called %d times, want 1", k.calls)
	}
	if res.Scalars["tail"] != 95.0 {
		t.Errorf("tail = %v, want 95", res.Scalars["tail"])
	}
}

func TestRegistry_hostKernelMatchesThePrefixSpellingToo(t *testing.T) {
	k := &p95Kernel{}
	s, err := Compile(
		Config{Formulas: []Formula{{As: "tail", Reduce: "p95(latency)"}}},
		newFakeCompilerWith("p95"),
		WithRegistry(registryWith(t, k)),
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, col, ok := s.kernelFor(s.order[0]); !ok || col != "latency" {
		t.Fatalf("prefix spelling did not select the host kernel (col=%q ok=%v)", col, ok)
	}
}

func TestRegistry_withoutTheKernelTheReduceStillWorks(t *testing.T) {
	// The registry accelerates; it does not gate. An unregistered aggregate is
	// not an error — it is simply evaluated by the compiler.
	s, err := Compile(
		Config{Formulas: []Formula{{As: "total", Reduce: "v sum"}}},
		newFakeCompiler(),
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := s.Apply(context.Background(), []rowops.Row{{"v": 1.0}, {"v": 2.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Scalars["total"] != 3.0 {
		t.Errorf("total = %v, want 3", res.Scalars["total"])
	}
}

func TestRegistry_hostKernelIsDelegatedWhenItNamesAnAggregate(t *testing.T) {
	k := &p95Kernel{push: "percentile_cont"}
	s, err := Compile(
		Config{Formulas: []Formula{{As: "tail", Reduce: "latency p95"}}},
		newFakeCompilerWith("p95"),
		WithRegistry(registryWith(t, k)),
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	d := &recordingDelegate{answers: map[string]any{"tail": 42.0}}
	s.SetReduceDelegate(d)

	res, err := s.Apply(completeCtx(), []rowops.Row{{"latency": 1.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(d.got) != 1 || d.got[0].Fn != "percentile_cont" {
		t.Fatalf("delegated request = %+v, want the kernel's pushdown name", d.got)
	}
	if res.Scalars["tail"] != 42.0 {
		t.Errorf("tail = %v, want the delegated 42", res.Scalars["tail"])
	}
}

func TestRegistry_hostKernelWithoutAPushdownNameIsNotDelegated(t *testing.T) {
	// The default. A host that has not said its aggregate has a portable SQL
	// spelling must not have one guessed for it.
	k := &p95Kernel{}
	s, err := Compile(
		Config{Formulas: []Formula{{As: "tail", Reduce: "latency p95"}}},
		newFakeCompilerWith("p95"),
		WithRegistry(registryWith(t, k)),
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	d := &recordingDelegate{answers: map[string]any{"tail": 42.0}}
	s.SetReduceDelegate(d)

	res, err := s.Apply(completeCtx(), []rowops.Row{{"latency": 7.0}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(d.got) != 0 {
		t.Errorf("delegated %+v despite no pushdown name", d.got)
	}
	if res.Scalars["tail"] != 7.0 {
		t.Errorf("tail = %v, want the locally computed 7", res.Scalars["tail"])
	}
}

func TestRegistry_reduceNamesAreSorted(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"zeta", "alpha", "mid"} {
		if err := r.RegisterReduce(kernel{name: name, fn: kSum}); err != nil {
			t.Fatalf("RegisterReduce(%q): %v", name, err)
		}
	}
	got := r.ReduceNames()
	for i, want := range []string{"alpha", "mid", "zeta"} {
		if got[i] != want {
			t.Fatalf("ReduceNames = %v, want sorted", got)
		}
	}
}
