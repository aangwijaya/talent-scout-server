package domain

// MachineNodeIDs returns the ordered list of graph nodes that emit node events.
// recruiter_gate is a human gate outside the runner and emits none, so it is
// excluded. The graph runner and the session sweeper share this list to avoid a
// package cycle (graph imports session).
func MachineNodeIDs() []string {
	return []string{
		"planner", "plan_check", "interviewer", "judge", "guard", "scorer", "evidence_check",
	}
}
