package sheet

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// fakeCompiler implements ExprCompiler over a tiny space-separated postfix
// language, so these tests exercise real compile-once behaviour without
// depending on an expression language.
//
// Tokens: a number is a literal; + - * / are binary operators; sum avg min max
// count reduce a column argument; anything else is an identifier.
type fakeCompiler struct {
	compiles int
	// reduces is the set of names this grammar treats as aggregates rather
	// than as identifiers. A real host compiler knows its own language's
	// aggregates; this is how a test says which ones it is pretending to have.
	reduces map[string]bool
}

func newFakeCompiler() *fakeCompiler { return newFakeCompilerWith() }

func newFakeCompilerWith(extra ...string) *fakeCompiler {
	r := map[string]bool{"sum": true, "avg": true, "min": true, "max": true, "count": true}
	for _, name := range extra {
		r[name] = true
	}
	return &fakeCompiler{reduces: r}
}

func (c *fakeCompiler) FreeIdentifiers(expr string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, tok := range strings.Fields(expr) {
		// A prefix call reduces to its argument, the way a real parser would
		// report the free identifier rather than the whole call text.
		if _, arg, ok := c.splitCall(tok); ok {
			add(arg)
			continue
		}
		if !c.isIdentToken(tok) {
			continue
		}
		add(tok)
	}
	return out, nil
}

// splitCall recognises the prefix spelling `fn(arg)` for a known reduce.
func (c *fakeCompiler) splitCall(tok string) (fn, arg string, ok bool) {
	open := strings.IndexByte(tok, '(')
	if open <= 0 || !strings.HasSuffix(tok, ")") {
		return "", "", false
	}
	fn = tok[:open]
	arg = tok[open+1 : len(tok)-1]
	if arg == "" || !c.reduces[fn] {
		return "", "", false
	}
	return fn, arg, true
}

func (c *fakeCompiler) Compile(expr string) (CompiledExpr, error) {
	toks := strings.Fields(expr)
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	c.compiles++
	return &fakeExpr{toks: toks, reduces: c.reduces}, nil
}

func (c *fakeCompiler) isIdentToken(tok string) bool {
	switch tok {
	case "+", "-", "*", "/":
		return false
	}
	if c.reduces[tok] {
		return false
	}
	if _, err := strconv.ParseFloat(tok, 64); err == nil {
		return false
	}
	return true
}

type fakeExpr struct {
	toks    []string
	reduces map[string]bool
}

func (e *fakeExpr) Eval(_ context.Context, args map[string]any) (any, error) {
	var stack []any
	pop := func() (any, error) {
		if len(stack) == 0 {
			return nil, fmt.Errorf("stack underflow")
		}
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v, nil
	}

	for _, tok := range e.toks {
		switch tok {
		case "+", "-", "*", "/":
			b, err := pop()
			if err != nil {
				return nil, err
			}
			a, err := pop()
			if err != nil {
				return nil, err
			}
			af, aok := toFloat(a)
			bf, bok := toFloat(b)
			if !aok || !bok {
				return nil, fmt.Errorf("non-numeric operand for %q", tok)
			}
			switch tok {
			case "+":
				stack = append(stack, af+bf)
			case "-":
				stack = append(stack, af-bf)
			case "*":
				stack = append(stack, af*bf)
			case "/":
				if bf == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				stack = append(stack, af/bf)
			}
		default:
			if e.reduces[tok] {
				v, err := pop()
				if err != nil {
					return nil, err
				}
				r, err := fakeReduce(tok, v)
				if err != nil {
					return nil, err
				}
				stack = append(stack, r)
				continue
			}
			if fn, arg, ok := (&fakeCompiler{reduces: e.reduces}).splitCall(tok); ok {
				r, err := fakeReduce(fn, args[arg])
				if err != nil {
					return nil, err
				}
				stack = append(stack, r)
				continue
			}
			if f, err := strconv.ParseFloat(tok, 64); err == nil {
				stack = append(stack, f)
				continue
			}
			stack = append(stack, args[tok])
		}
	}
	if len(stack) != 1 {
		return nil, fmt.Errorf("expression left %d values on the stack", len(stack))
	}
	return stack[0], nil
}

// fakeReduce mirrors SQL's aggregate semantics so the equivalence tests
// compare the native kernels against something that already agrees with them.
func fakeReduce(fn string, v any) (any, error) {
	vals, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: want a column, got %T", fn, v)
	}
	var acc, mn, mx float64
	var n, nonNull int
	for _, x := range vals {
		if x != nil {
			nonNull++
		}
		f, ok := toFloat(x)
		if !ok {
			continue
		}
		if n == 0 {
			mn, mx = f, f
		}
		if f < mn {
			mn = f
		}
		if f > mx {
			mx = f
		}
		acc += f
		n++
	}
	switch fn {
	case "count":
		return int64(nonNull), nil
	case "sum":
		if n == 0 {
			return nil, nil
		}
		return acc, nil
	case "avg":
		if n == 0 {
			return nil, nil
		}
		return acc / float64(n), nil
	case "min":
		if n == 0 {
			return nil, nil
		}
		return mn, nil
	case "max":
		if n == 0 {
			return nil, nil
		}
		return mx, nil
	}
	return nil, fmt.Errorf("unknown reduce %q", fn)
}
