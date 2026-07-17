package tests

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

type githubBoundaryRegistry struct {
	SchemaID      string                        `json:"schema_id"`
	SchemaVersion int                           `json:"schema_version"`
	Owner         string                        `json:"owner"`
	Entries       []githubBoundaryRegistryEntry `json:"entries"`
}

type githubBoundaryRegistryEntry struct {
	Path      string `json:"path"`
	Needle    string `json:"needle"`
	Count     int    `json:"count"`
	Category  string `json:"category"`
	Rationale string `json:"rationale"`
}

func TestGitHubTransportBoundaryRegistryIsExactAndComplete(t *testing.T) {
	registryData, err := os.ReadFile("../release/github-transport-boundary.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var registry githubBoundaryRegistry
	if err := strictjson.Decode(registryData, 128<<10, &registry); err != nil {
		t.Fatal(err)
	}
	if registry.SchemaID != "env-vault.github-transport-boundary.v1" || registry.SchemaVersion != 1 || registry.Owner == "" || len(registry.Entries) == 0 {
		t.Fatalf("invalid registry identity: %+v", registry)
	}

	root := filepath.Clean("..")
	commandPattern := regexp.MustCompile(`\bgh[[:space:]]+`)
	var observed, directMutations, graphqlObservations int
	for _, relativeRoot := range []string{".github/workflows", "scripts/release"} {
		err := filepath.WalkDir(filepath.Join(root, relativeRoot), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || (filepath.Ext(path) != ".sh" && filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml") {
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			scanner := bufio.NewScanner(file)
			lineNumber := 0
			for scanner.Scan() {
				lineNumber++
				line := scanner.Text()
				if !commandPattern.MatchString(line) || strings.Contains(line, "command -v gh") || strings.Contains(line, "for command in date gh ") {
					continue
				}
				observed++
				matches := 0
				for _, registered := range registry.Entries {
					if registered.Path == relative && strings.Contains(line, registered.Needle) {
						matches++
					}
				}
				if matches != 1 {
					t.Fatalf("GitHub command %s:%d matches %d registry entries: %s", relative, lineNumber, matches, strings.TrimSpace(line))
				}
			}
			return scanner.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	registeredCount := 0
	seen := map[string]bool{}
	for _, entry := range registry.Entries {
		key := entry.Path + "\x00" + entry.Needle
		if seen[key] || entry.Count <= 0 || len(entry.Rationale) < 20 {
			t.Fatalf("invalid or duplicate registry entry: %+v", entry)
		}
		seen[key] = true
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(contents), entry.Needle); got != entry.Count {
			t.Fatalf("registry count for %s %q=%d, want %d", entry.Path, entry.Needle, got, entry.Count)
		}
		registeredCount += entry.Count
		switch entry.Category {
		case "direct-mutation":
			directMutations += entry.Count
		case "graphql-observation":
			graphqlObservations += entry.Count
		case "high-level-observation", "metrics-observation", "high-level-mutation", "attestation-verification", "credential-setup":
		default:
			t.Fatalf("unknown registry category %q", entry.Category)
		}
	}
	if observed != registeredCount || directMutations != 8 || graphqlObservations != 1 {
		t.Fatalf("boundary counts observed=%d registered=%d direct_mutations=%d graphql=%d", observed, registeredCount, directMutations, graphqlObservations)
	}

	workflowData := readFile(t, "../.github/workflows/release-please.yml") +
		readFile(t, "../.github/workflows/build-binaries.yml") +
		readFile(t, "../.github/workflows/release-evidence.yml")
	scriptData := readFile(t, "../scripts/release/verify-release-proposal.sh") +
		readFile(t, "../scripts/release/verify-release-authorization.sh") +
		readFile(t, "../scripts/release/authorize-and-merge-release-pr.sh") +
		readFile(t, "../scripts/release/wait-tap-ci.sh")
	if got := strings.Count(workflowData+scriptData, "actions identity"); got < 10 {
		t.Fatalf("operational typed Actions identity call sites=%d, want at least 10", got)
	}
	if dynamic := `gh "${args[@]}"`; !commandPattern.MatchString(dynamic) || registryMatches(registry.Entries, "scripts/release/example.sh", dynamic) != 0 {
		t.Fatal("dynamic gh dispatch would escape the fail-closed registry detector")
	}
}

func registryMatches(entries []githubBoundaryRegistryEntry, path, line string) int {
	matches := 0
	for _, entry := range entries {
		if entry.Path == path && strings.Contains(line, entry.Needle) {
			matches++
		}
	}
	return matches
}
