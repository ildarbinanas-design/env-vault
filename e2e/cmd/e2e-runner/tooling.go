package main

// Reporting-tool selection is intentionally isolated from the semantic E2E
// runner hash. Phase 2 may need a newer reporter solely for target-Go
// compatibility; that must not pretend the scenarios, normalization, report
// validation, or comparison logic changed.
const (
	gotestsumModuleVersion = "gotest.tools/gotestsum@v1.13.0"
	gotestsumVersion       = "v1.13.0"
	gotestsumModuleSum     = "h1:+Lh454O9mu9AMG1APV4o0y7oDYKyik/3kBOiCqiEpRo="
)
