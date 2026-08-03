package pipe

import "sort"

// Available reports whether every service this operator needs is present in
// octx. A nil OpContext supplies nothing, so only self-contained operators are
// available under it.
func (m OpMetadata) Available(octx *OpContext) bool {
	return len(m.Missing(octx)) == 0
}

// Missing returns the services this operator needs that octx does not supply,
// in declaration order.
func (m OpMetadata) Missing(octx *OpContext) []Requirement {
	var out []Requirement
	for _, r := range m.Requires {
		if !octx.has(r) {
			out = append(out, r)
		}
	}
	return out
}

// has reports whether a service is wired. A nil receiver supplies nothing,
// which is the honest answer for a caller that passed no context at all.
func (o *OpContext) has(r Requirement) bool {
	if o == nil {
		return false
	}
	switch r {
	case ReqEval:
		return o.Eval != nil
	case ReqFunctionRegistry:
		return o.Registry != nil
	case ReqAppCaller:
		return o.AppCaller != nil
	case ReqFormula:
		return o.Formula != nil
	case ReqClassic:
		return o.Classic != nil
	case ReqAlgorithms:
		return o.Algorithms != nil
	default:
		// An unknown requirement is treated as unmet. A new one added to the
		// catalog without a case here should narrow what is offered, not
		// silently widen it.
		return false
	}
}

// AvailableOps returns the operators usable with octx, sorted by name.
//
// This is what a completion list should offer: a stage the caller's deployment
// cannot run is worse than no suggestion, because it looks supported until the
// query fails.
func AvailableOps(octx *OpContext) []OpMetadata {
	var out []OpMetadata
	for _, m := range Catalog() {
		if m.Available(octx) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MissingRequirements maps each unavailable operator to the services it lacks.
//
// Intended for start-up: a host can log or fail on a wiring gap once, rather
// than discovering it one failed query at a time in production. An empty result
// means every operator in the catalog is usable.
func MissingRequirements(octx *OpContext) map[string][]Requirement {
	out := map[string][]Requirement{}
	for _, m := range Catalog() {
		if missing := m.Missing(octx); len(missing) > 0 {
			out[m.Name] = missing
		}
	}
	return out
}
