package sqlgen

import "github.com/xraph/dql/scope"

// testScope mirrors the partition model these tests were written against: one
// always-present column that also scopes joins, and one optional column
// emitted only when a value is supplied and the dataset carries it.
//
// It is a fixture, not a default. The generator has no opinion about what
// partitions a caller's data — see package scope.
func testScope(workspaceID, projectID string) scope.Scope {
	s := scope.Scope{
		{Name: "workspace_id", Value: workspaceID, Required: true, ScopeJoins: true},
	}
	if projectID != "" {
		s = append(s, scope.ScopeColumn{Name: "project_id", Value: projectID})
	}
	return s
}
