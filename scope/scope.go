package scope

// Tenant scoping used to be hardcoded here: the planner looked for a column
// literally named "workspace_id", and the generator emitted "workspace_id" and
// "project_id" predicates directly. That works for exactly one host's data
// model and makes the query engine unusable to anyone whose tables are
// partitioned differently — or not at all.
//
// A Scope is the host's answer to "what partitions every row here". The engine
// applies it; it never decides what it is.

// ScopeColumn is one partition column the host requires on every query.
type ScopeColumn struct {
	// Name is the column, e.g. "workspace_id".
	Name string

	// Value is the predicate's right-hand side.
	Value any

	// Required emits the predicate on the base table even when the dataset does
	// not declare the column. Optional columns are emitted only when the
	// dataset actually carries them — which is how a partition column that some
	// system-owned sources lack is handled.
	Required bool

	// ScopeJoins additionally scopes joined tables that carry this column. The
	// predicate goes in the ON clause rather than WHERE, so an out-of-scope row
	// fails the join instead of NULL-padding through a LEFT join.
	ScopeJoins bool
}

// Scope is an ordered set of partition columns. Order is significant: it fixes
// the order predicates and their placeholders are emitted in, so it must stay
// stable for a given host.
type Scope []ScopeColumn

// JoinScoped returns the columns that also scope joined tables.
func (s Scope) JoinScoped() []ScopeColumn {
	out := make([]ScopeColumn, 0, len(s))
	for _, c := range s {
		if c.ScopeJoins {
			out = append(out, c)
		}
	}
	return out
}

// Names returns every column name in the scope, in order.
func (s Scope) Names() []string {
	out := make([]string, 0, len(s))
	for _, c := range s {
		out = append(out, c.Name)
	}
	return out
}

// Has reports whether name is part of this scope.
func (s Scope) Has(name string) bool {
	for _, c := range s {
		if c.Name == name {
			return true
		}
	}
	return false
}
