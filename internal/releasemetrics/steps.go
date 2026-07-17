// Package releasemetrics defines the stable workflow-step classification used
// by both operator metrics and authoritative release evidence.
package releasemetrics

import "strings"

type StepKind uint8

const (
	StepOther StepKind = iota
	StepCache
	StepArtifact
)

var cacheSteps = map[string]struct{}{
	"Restore Go build and module caches": {},
	"Restore Go build cache":             {},
	"Set up Go":                          {},
	"Set up Go for safe extraction":      {},
}

var artifactSteps = map[string]struct{}{
	"Download canonical release assets":                            {},
	"Download exact main CI promotion manifest":                    {},
	"Download exact promotion manifest from CI":                    {},
	"Download exact promotion manifest from triggering CI attempt": {},
	"Download exact published release assets":                      {},
	"Download exact current-attempt promotion artifacts":           {},
	"Download five exact native release artifacts from CI":         {},
	"Download current-attempt E2E reports":                         {},
	"Download current-attempt platform promotion evidence":         {},
	"Download publisher-local verified promotion bundle":           {},
	"Download and verify published assets":                         {},
	"Upload authoritative machine evidence":                        {},
	"Upload durable baseline verification":                         {},
	"Upload E2E reports":                                           {},
	"Upload exact promotion manifest":                              {},
	"Upload native release artifact":                               {},
	"Upload platform quality evidence":                             {},
	"Upload publisher-local verified bundle":                       {},
	"Upload publisher-local verified promotion bundle":             {},
	"Upload SPDX SBOM workflow artifact":                           {},
}

// ClassifyStep returns a stable semantic kind for timing aggregation. Unnamed
// setup-go uses are represented by GitHub as "Run actions/setup-go@..."; the
// prefix is intentionally pinned while allowing the immutable action SHA to
// change independently.
func ClassifyStep(name string) StepKind {
	if _, ok := cacheSteps[name]; ok || strings.HasPrefix(name, "Run actions/setup-go@") {
		return StepCache
	}
	if _, ok := artifactSteps[name]; ok {
		return StepArtifact
	}
	return StepOther
}
