package actionsartifact

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	LifecycleSupersededEligible = "superseded-eligible"
	LifecycleDurable            = "durable"
	LifecycleSystemManaged      = "system-managed"

	AttemptFromName     = "name_capture"
	AttemptFromInterval = "attempt_interval"
)

var workflowPathPattern = regexp.MustCompile(`^\.github/workflows/[a-z0-9][a-z0-9-]*\.yml$`)

// NameMatch is a unique reviewed lineage match. Attempt is zero only when the
// exact producer attempt must be resolved from collected attempt intervals.
type NameMatch struct {
	Pattern             NamePattern
	Attempt             int
	ReferencedVersion   string
	ReferencedSourceSHA string
}

func validateNamePatterns(patterns []NamePattern) (map[string]NamePattern, error) {
	if len(patterns) == 0 || len(patterns) > 128 {
		return nil, errors.New("patterns must contain 1..128 reviewed lineages")
	}
	byID := make(map[string]NamePattern, len(patterns))
	compiled := make(map[string]*regexp.Regexp, len(patterns))
	for index, pattern := range patterns {
		if !policyIDPattern.MatchString(pattern.ID) {
			return nil, fmt.Errorf("patterns[%d]: invalid id %q", index, pattern.ID)
		}
		if _, exists := byID[pattern.ID]; exists {
			return nil, fmt.Errorf("duplicate pattern id %q", pattern.ID)
		}
		if index > 0 && patterns[index-1].ID >= pattern.ID {
			return nil, fmt.Errorf("patterns must be sorted and unique by id: %q", pattern.ID)
		}
		if !containsString(supportedClasses, pattern.Class) {
			return nil, fmt.Errorf("patterns[%d]: unknown artifact class %q", index, pattern.Class)
		}
		if pattern.Lifecycle != LifecycleSupersededEligible && pattern.Lifecycle != LifecycleDurable && pattern.Lifecycle != LifecycleSystemManaged {
			return nil, fmt.Errorf("patterns[%d]: unknown lifecycle %q", index, pattern.Lifecycle)
		}
		if len(pattern.WorkflowPaths) == 0 || len(pattern.WorkflowPaths) > 16 {
			return nil, fmt.Errorf("patterns[%d]: workflow_paths must contain 1..16 entries", index)
		}
		for pathIndex, path := range pattern.WorkflowPaths {
			if !workflowPathPattern.MatchString(path) && path != "dynamic/github-code-scanning/codeql" {
				return nil, fmt.Errorf("patterns[%d]: unsupported workflow path %q", index, path)
			}
			if filepath.Clean(path) != path {
				return nil, fmt.Errorf("patterns[%d]: non-canonical workflow path %q", index, path)
			}
			if pathIndex > 0 && pattern.WorkflowPaths[pathIndex-1] >= path {
				return nil, fmt.Errorf("patterns[%d]: workflow_paths must be sorted and unique", index)
			}
		}
		if len(pattern.NameRegex) < 3 || len(pattern.NameRegex) > 1024 || !strings.HasPrefix(pattern.NameRegex, "^") || !strings.HasSuffix(pattern.NameRegex, "$") {
			return nil, fmt.Errorf("patterns[%d]: name_regex must be a bounded anchored expression", index)
		}
		expression, err := regexp.Compile(pattern.NameRegex)
		if err != nil {
			return nil, fmt.Errorf("patterns[%d]: compile name_regex: %w", index, err)
		}
		switch pattern.AttemptResolution {
		case AttemptFromName:
			if pattern.AttemptCapture < 1 || pattern.AttemptCapture > expression.NumSubexp() {
				return nil, fmt.Errorf("patterns[%d]: attempt_capture must name one existing positive capture", index)
			}
		case AttemptFromInterval:
			if pattern.AttemptCapture != 0 {
				return nil, fmt.Errorf("patterns[%d]: interval resolution must use attempt_capture 0", index)
			}
		default:
			return nil, fmt.Errorf("patterns[%d]: unknown attempt_resolution %q", index, pattern.AttemptResolution)
		}
		if pattern.ReferencedVersionCapture < 0 || pattern.ReferencedVersionCapture > expression.NumSubexp() || pattern.ReferencedSourceSHACapture < 0 || pattern.ReferencedSourceSHACapture > expression.NumSubexp() {
			return nil, fmt.Errorf("patterns[%d]: referenced identity capture is outside the expression", index)
		}
		if pattern.ReferencedVersionCapture != 0 && pattern.ReferencedVersionCapture == pattern.ReferencedSourceSHACapture {
			return nil, fmt.Errorf("patterns[%d]: referenced version and source SHA captures must differ", index)
		}
		if len(pattern.Examples) == 0 || len(pattern.Examples) > 32 {
			return nil, fmt.Errorf("patterns[%d]: examples must contain 1..32 bounded values", index)
		}
		for exampleIndex, example := range pattern.Examples {
			if len(example) == 0 || len(example) > 512 || !expression.MatchString(example) {
				return nil, fmt.Errorf("patterns[%d]: example %q does not match its regex", index, example)
			}
			if exampleIndex > 0 && pattern.Examples[exampleIndex-1] >= example {
				return nil, fmt.Errorf("patterns[%d]: examples must be sorted and unique", index)
			}
			if pattern.AttemptResolution == AttemptFromName {
				matches := expression.FindStringSubmatch(example)
				attempt, err := strconv.Atoi(matches[pattern.AttemptCapture])
				if err != nil || attempt < 1 {
					return nil, fmt.Errorf("patterns[%d]: example %q has invalid attempt capture", index, example)
				}
			}
			matches := expression.FindStringSubmatch(example)
			if pattern.ReferencedVersionCapture > 0 && !releaseVersionPattern.MatchString(matches[pattern.ReferencedVersionCapture]) {
				return nil, fmt.Errorf("patterns[%d]: example %q has invalid referenced version capture", index, example)
			}
			if pattern.ReferencedSourceSHACapture > 0 && !shaPattern.MatchString(matches[pattern.ReferencedSourceSHACapture]) {
				return nil, fmt.Errorf("patterns[%d]: example %q has invalid referenced source SHA capture", index, example)
			}
		}
		if strings.TrimSpace(pattern.DependencyRepairRationale) != pattern.DependencyRepairRationale || pattern.DependencyRepairRationale == "" || len(pattern.DependencyRepairRationale) > 500 {
			return nil, fmt.Errorf("patterns[%d]: dependency_repair_rationale must be a non-empty canonical string", index)
		}
		byID[pattern.ID] = pattern
		compiled[pattern.ID] = expression
	}

	// Every reviewed example must resolve uniquely on every declared producer
	// path. Runtime classification repeats this check for every live name.
	for _, pattern := range patterns {
		for _, path := range pattern.WorkflowPaths {
			for _, example := range pattern.Examples {
				matches := 0
				for _, candidate := range patterns {
					if containsString(candidate.WorkflowPaths, path) && compiled[candidate.ID].MatchString(example) {
						matches++
					}
				}
				if matches != 1 {
					return nil, fmt.Errorf("pattern example %q on %q resolves to %d lineages", example, path, matches)
				}
			}
		}
	}
	return byID, nil
}

// MatchName requires exactly one path-qualified pattern. It never falls back
// to a broad prefix or an unknown cleanup class.
func MatchName(policy Policy, workflowPath, artifactName string) (NameMatch, error) {
	if err := policy.Validate(); err != nil {
		return NameMatch{}, err
	}
	return matchNameValidated(policy, workflowPath, artifactName)
}

func matchNameValidated(policy Policy, workflowPath, artifactName string) (NameMatch, error) {
	var result NameMatch
	matchCount := 0
	for _, pattern := range policy.Patterns {
		if !containsString(pattern.WorkflowPaths, workflowPath) {
			continue
		}
		expression := regexp.MustCompile(pattern.NameRegex)
		matches := expression.FindStringSubmatch(artifactName)
		if matches == nil {
			continue
		}
		matchCount++
		result.Pattern = pattern
		if pattern.AttemptResolution == AttemptFromName {
			attempt, err := strconv.Atoi(matches[pattern.AttemptCapture])
			if err != nil || attempt < 1 {
				return NameMatch{}, fmt.Errorf("pattern %q produced an invalid attempt", pattern.ID)
			}
			result.Attempt = attempt
		}
		if pattern.ReferencedVersionCapture > 0 {
			result.ReferencedVersion = matches[pattern.ReferencedVersionCapture]
		}
		if pattern.ReferencedSourceSHACapture > 0 {
			result.ReferencedSourceSHA = matches[pattern.ReferencedSourceSHACapture]
		}
	}
	if matchCount == 0 {
		return NameMatch{}, fmt.Errorf("unknown artifact lineage for workflow %q name %q", workflowPath, artifactName)
	}
	if matchCount != 1 {
		return NameMatch{}, fmt.Errorf("ambiguous artifact lineage for workflow %q name %q: %d patterns", workflowPath, artifactName, matchCount)
	}
	return result, nil
}

func sortedPatternIDs(policy Policy) []string {
	ids := make([]string, 0, len(policy.Patterns))
	for _, pattern := range policy.Patterns {
		ids = append(ids, pattern.ID)
	}
	sort.Strings(ids)
	return ids
}
