package pipe

import (
	"fmt"

	"github.com/xraph/dql/dsl"
)

// ShapeError describes a pipe-stage validation error. Mirrored (not the same
// type as) the parser's ParseError so the parser can convert between them.
type ShapeError struct {
	StageIndex int
	Op         string
	Message    string
}

func (e ShapeError) Error() string {
	if e.Op != "" {
		return fmt.Sprintf("pipe[%d] %s: %s", e.StageIndex, e.Op, e.Message)
	}
	return fmt.Sprintf("pipe[%d]: %s", e.StageIndex, e.Message)
}

// ValidateShape performs structural validation of pipe stages: every stage
// must have an op, the op must be registered, and its JSON config must decode.
// It does NOT check service availability (expression evaluator, formula
// manager, app caller, ...) — those checks happen at Build time inside the
// executor, when the engine has wired the OpContext.
func ValidateShape(stages []dsl.PipeStage) []ShapeError {
	if len(stages) == 0 {
		return []ShapeError{{StageIndex: -1, Message: "pipe must contain at least one stage"}}
	}
	var errs []ShapeError
	for i, s := range stages {
		if s.Op == "" {
			errs = append(errs, ShapeError{StageIndex: i, Message: "op is required"})
			continue
		}
		if !Known(s.Op) {
			errs = append(errs, ShapeError{StageIndex: i, Op: s.Op, Message: "unknown op"})
			continue
		}
		// Build with a nil OpContext — factories that need services must tolerate
		// nil here by deferring the service check to Apply time. Any error here
		// is purely about config shape (missing required fields, wrong types).
		if _, err := Build(s, nil); err != nil {
			// Only surface errors that look like config shape problems — "not
			// available" errors are deferred.
			if !isServiceMissing(err) {
				errs = append(errs, ShapeError{StageIndex: i, Op: s.Op, Message: err.Error()})
			}
		}
	}
	return errs
}

// isServiceMissing detects factory errors that stem from a missing runtime
// service (e.g. the function extension not being registered). These are not
// shape errors — they're deferred to execute time.
func isServiceMissing(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "not available")
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
