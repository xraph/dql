package sheet

import (
	"fmt"
	"sort"
)

// ReduceFunc computes a scalar from a whole column.
//
// Kernels must match SQL's aggregate semantics exactly: nulls are skipped, and
// an aggregate over no surviving values is NULL except for count, which is 0. A
// kernel that disagreed would make pushing an aggregate down to the database a
// semantic change rather than an optimisation.
type ReduceFunc interface {
	Name() string
	Reduce(col Column) (any, error)
	// PushdownName is the SQL aggregate this delegates to, or "" when it
	// cannot be computed by a database.
	PushdownName() string
}

var builtinReduces = map[string]ReduceFunc{}

func registerReduce(f ReduceFunc) {
	if _, dup := builtinReduces[f.Name()]; dup {
		panic(fmt.Sprintf("sheet: reduce %q already registered", f.Name()))
	}
	builtinReduces[f.Name()] = f
}

// LookupReduce finds a built-in kernel by name.
func LookupReduce(name string) (ReduceFunc, bool) {
	f, ok := builtinReduces[name]
	return f, ok
}

// reduceNames lists the registered kernels in a stable order, so textual
// kernel matching does not depend on map iteration.
func reduceNames() []string {
	out := make([]string, 0, len(builtinReduces))
	for name := range builtinReduces {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func init() {
	registerReduce(kernel{name: "sum", push: "sum", fn: kSum})
	registerReduce(kernel{name: "avg", push: "avg", fn: kAvg})
	registerReduce(kernel{name: "min", push: "min", fn: kMin})
	registerReduce(kernel{name: "max", push: "max", fn: kMax})
	registerReduce(kernel{name: "count", push: "count", fn: kCount})
}

type kernel struct {
	name string
	push string
	fn   func(Column) (any, error)
}

func (k kernel) Name() string                 { return k.name }
func (k kernel) PushdownName() string         { return k.push }
func (k kernel) Reduce(c Column) (any, error) { return k.fn(c) }

// eachFloat calls visit for every non-null value that is numeric.
//
// Non-numeric values in an []any-backed column are skipped, matching how a
// database ignores what does not participate in a numeric aggregate.
func eachFloat(c Column, visit func(float64)) {
	if fc, ok := c.(*FloatColumn); ok {
		vals := fc.Floats()
		for i := range vals {
			if fc.IsNull(i) {
				continue
			}
			visit(vals[i])
		}
		return
	}
	for i, n := 0, c.Len(); i < n; i++ {
		if c.IsNull(i) {
			continue
		}
		if f, ok := toFloat(c.At(i)); ok {
			visit(f)
		}
	}
}

func kSum(c Column) (any, error) {
	var acc float64
	var n int
	eachFloat(c, func(f float64) { acc += f; n++ })
	if n == 0 {
		return nil, nil
	}
	return acc, nil
}

func kAvg(c Column) (any, error) {
	var acc float64
	var n int
	eachFloat(c, func(f float64) { acc += f; n++ })
	if n == 0 {
		return nil, nil
	}
	return acc / float64(n), nil
}

func kMin(c Column) (any, error) {
	var best float64
	var n int
	eachFloat(c, func(f float64) {
		if n == 0 || f < best {
			best = f
		}
		n++
	})
	if n == 0 {
		return nil, nil
	}
	return best, nil
}

func kMax(c Column) (any, error) {
	var best float64
	var n int
	eachFloat(c, func(f float64) {
		if n == 0 || f > best {
			best = f
		}
		n++
	})
	if n == 0 {
		return nil, nil
	}
	return best, nil
}

// kCount counts non-null slots, including non-numeric ones — counting a text
// column is a valid question, and SQL answers it.
func kCount(c Column) (any, error) {
	var n int64
	for i, ln := 0, c.Len(); i < ln; i++ {
		if !c.IsNull(i) {
			n++
		}
	}
	return n, nil
}
