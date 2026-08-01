package planner

import "github.com/xraph/dql/scope"

// testScope is the partition model these tests were written against: a single
// always-present column that also scopes joins. A fixture, not a default.
func testScope() scope.Scope {
	return scope.Scope{{Name: "workspace_id", Required: true, ScopeJoins: true}}
}
