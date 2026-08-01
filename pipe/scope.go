package pipe

import "context"

// scopeKey is a private type for context values so we don't collide with
// anything else in the pipeline.
type scopeKey int

const (
	wsKey scopeKey = iota
	projKey
)

// withScope stores workspace and project IDs on the context. The pipe Executor
// sets these before running ops so in-memory operators (compute(formula),
// lookup, callApp) can retrieve them without each op's config carrying the scope.
func withScope(ctx context.Context, workspaceID, projectID string) context.Context {
	ctx = context.WithValue(ctx, wsKey, workspaceID)
	ctx = context.WithValue(ctx, projKey, projectID)
	return ctx
}

// scopeFrom extracts workspace and project IDs previously stored with withScope.
// Missing values return empty strings — callers that require scope should error
// on empty, not panic.
func scopeFrom(ctx context.Context) (workspaceID, projectID string) {
	if v, ok := ctx.Value(wsKey).(string); ok {
		workspaceID = v
	}
	if v, ok := ctx.Value(projKey).(string); ok {
		projectID = v
	}
	return
}
