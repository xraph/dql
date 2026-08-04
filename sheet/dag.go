package sheet

import (
	"fmt"
	"sort"
	"strings"
)

// topoSort orders formulas so each runs after everything it references.
//
// Only references naming another formula become edges. A reference to anything
// else is presumed to be a source column, which is available before any formula
// runs and so constrains nothing; whether such a column actually exists is
// settled when the sheet meets its input, because the schema is not known here.
//
// Declaration order is preserved among formulas that do not constrain each
// other, so a sheet's output column order is predictable.
func topoSort(formulas []Formula, refs map[string][]string) ([]Formula, error) {
	index := make(map[string]int, len(formulas))
	for i, f := range formulas {
		index[f.As] = i
	}

	seenEdge := make(map[string]map[string]bool, len(formulas))
	dependents := make(map[string][]string, len(formulas))
	inDegree := make(map[string]int, len(formulas))

	for _, f := range formulas {
		seenEdge[f.As] = map[string]bool{}
		inDegree[f.As] = 0
	}

	for _, f := range formulas {
		for _, ref := range refs[f.As] {
			// A self-reference is a cycle of length one. Recorded as its own
			// edge so the check below reports it rather than dropping it.
			if _, isFormula := index[ref]; !isFormula {
				continue
			}
			if seenEdge[f.As][ref] {
				continue
			}
			seenEdge[f.As][ref] = true
			dependents[ref] = append(dependents[ref], f.As)
			inDegree[f.As]++
		}
	}

	ready := make([]int, 0, len(formulas))
	for _, f := range formulas {
		if inDegree[f.As] == 0 {
			ready = append(ready, index[f.As])
		}
	}
	sort.Ints(ready)

	out := make([]Formula, 0, len(formulas))
	for len(ready) > 0 {
		i := ready[0]
		ready = ready[1:]
		f := formulas[i]
		out = append(out, f)

		var freed []int
		for _, dep := range dependents[f.As] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				freed = append(freed, index[dep])
			}
		}
		if len(freed) > 0 {
			ready = append(ready, freed...)
			sort.Ints(ready)
		}
	}

	if len(out) != len(formulas) {
		stuck := make([]string, 0, len(formulas)-len(out))
		for _, f := range formulas {
			if inDegree[f.As] > 0 {
				stuck = append(stuck, f.As)
			}
		}
		sort.Strings(stuck)
		return nil, fmt.Errorf("sheet: circular dependency among formulas: %s", strings.Join(stuck, ", "))
	}
	return out, nil
}
