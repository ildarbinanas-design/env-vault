package main

// Reporting-tool selection is intentionally isolated from the semantic E2E
// runner hash. Phase 2 may need a newer reporter solely for target-Go
// compatibility; that must not pretend the scenarios, normalization, report
// validation, or comparison logic changed.
const (
	gotestsumModuleVersion = "gotest.tools/gotestsum@v1.12.2"
	gotestsumVersion       = "v1.12.2"
)
