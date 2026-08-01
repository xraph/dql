package pipe

import "fmt"

// Classification summarises a plan's safety for live subscriptions.
type Classification struct {
	// UnsafeStages lists the op names that are not live-safe. Empty when the
	// pipe is fully pure. The pushed prefix is always safe (SQL) so stages
	// here are drawn only from InMemoryOps.
	UnsafeStages []string
	// UnsafeIndices holds the corresponding InMemoryOps indices, useful for
	// precise error reporting.
	UnsafeIndices []int
}

// IsLiveSafe reports whether the whole pipe is safe for unmuted live replay.
func (c Classification) IsLiveSafe() bool { return len(c.UnsafeStages) == 0 }

// Classify inspects the plan and reports which in-memory stages contain
// side effects. Used by the live manager at subscribe time to decide
// whether to accept the subscription, reject it, or accept it under DryRun.
func Classify(plan *PipePlan) Classification {
	if plan == nil {
		return Classification{}
	}
	var c Classification
	for i, op := range plan.InMemoryOps {
		if !op.IsLiveSafe() {
			c.UnsafeStages = append(c.UnsafeStages, op.Name())
			c.UnsafeIndices = append(c.UnsafeIndices, i)
		}
	}
	return c
}

// RejectError formats a user-facing error listing the unsafe stages.
func (c Classification) RejectError() error {
	if c.IsLiveSafe() {
		return nil
	}
	if len(c.UnsafeStages) == 1 {
		return fmt.Errorf("pipe contains side-effecting stage %q; set dryRun=true to preview or remove the stage", c.UnsafeStages[0])
	}
	return fmt.Errorf("pipe contains side-effecting stages %v; set dryRun=true to preview or remove these stages", c.UnsafeStages)
}
