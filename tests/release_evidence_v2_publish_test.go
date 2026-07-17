package tests

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releaseevidence"
)

type evidenceV2Fixture struct {
	bundleDirectory string
	indexPath       string
	comparisonPath  string
	markdownPath    string
	storagePath     string
	parityPath      string
	genesisPath     string
	objectBytes     []byte
}

func TestPublishReleaseEvidenceV2ParentlessGenesisIsBash3SafeAndRaceReconciled(t *testing.T) {
	tests := []struct {
		name             string
		mode             string
		wantStatus       int
		wantOutput       string
		wantState        string
		wantSourceProbes int
		wantBlobPosts    int
		wantRefPosts     int
	}{
		{name: "exact absent ref creates evidence-only parentless ledger", mode: "success", wantState: "created", wantSourceProbes: 12, wantBlobPosts: 8, wantRefPosts: 1},
		{name: "ambiguous ref create reconciles without replay", mode: "ref-ambiguous", wantState: "reconciled", wantSourceProbes: 12, wantBlobPosts: 8, wantRefPosts: 1},
		{name: "exact concurrent ref create reconciles without replay", mode: "ref-race", wantState: "reconciled", wantSourceProbes: 12, wantBlobPosts: 8, wantRefPosts: 1},
		{name: "conflicting concurrent ref fails closed", mode: "ref-conflict", wantStatus: 1, wantOutput: "GitHub API request failed", wantSourceProbes: 12, wantBlobPosts: 8, wantRefPosts: 1},
		{name: "forbidden ref observation is not absence", mode: "probe-403", wantStatus: 1, wantOutput: "GitHub reference query failed", wantSourceProbes: 1},
		{name: "rate-limited ref observation is not absence", mode: "probe-429", wantStatus: 1, wantOutput: "GitHub reference query failed", wantSourceProbes: 1},
		{name: "source disappearance between probes prevents mutation", mode: "source-disappears", wantStatus: 1, wantOutput: "GitHub API request failed", wantSourceProbes: 2},
		{name: "late source disappearance blocks the next mutation", mode: "source-disappears-late", wantStatus: 1, wantOutput: "GitHub API request failed", wantSourceProbes: 6, wantBlobPosts: 4},
		{name: "wrong source response identity blocks all mutation", mode: "wrong-source", wantStatus: 1, wantOutput: "GitHub returned invalid commit data", wantSourceProbes: 1},
		{name: "wrong returned tree identity blocks commit and ref", mode: "wrong-tree-sha", wantStatus: 1, wantOutput: "GitHub returned an invalid or truncated tree", wantSourceProbes: 10, wantBlobPosts: 8},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			fixture := writeEvidenceV2Fixture(t, filepath.Join(root, "candidate"))
			binDirectory := filepath.Join(root, "bin")
			remoteDirectory := filepath.Join(root, "remote")
			payloadDirectory := filepath.Join(root, "payloads")
			for _, directory := range []string{binDirectory, remoteDirectory, payloadDirectory} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			installEvidenceV2FakeGH(t, binDirectory)
			checker := installEvidenceV2FakeChecker(t, binDirectory)
			callLog := filepath.Join(root, "calls.log")
			refState := filepath.Join(root, "ref-state")
			sourceCount := filepath.Join(root, "source-count")
			blobCount := filepath.Join(root, "blob-count")
			treeState := filepath.Join(root, "tree.json")
			commitState := filepath.Join(root, "commit.json")
			sourceTree := writeGenesisSourceTreeFixture(t, root)

			args := []string{
				"../scripts/release/publish-release-evidence.sh", "--format", "v2",
				releaseTestVersion, evidenceSourceSHA, releaseTestRepository,
				fixture.bundleDirectory, fixture.indexPath, fixture.comparisonPath, fixture.markdownPath,
				fixture.storagePath, fixture.parityPath, fixture.genesisPath,
			}
			command := exec.Command("/bin/bash", args...)
			command.Env = environmentWithOverrides(map[string]string{
				"EVIDENCE_PUBLISHER_CHECK": checker,
				"FAKE_V2_MODE":             test.mode,
				"FAKE_V2_CALL_LOG":         callLog,
				"FAKE_V2_REMOTE_DIR":       remoteDirectory,
				"FAKE_V2_PAYLOAD_DIR":      payloadDirectory,
				"FAKE_V2_REF_STATE":        refState,
				"FAKE_V2_SOURCE_COUNT":     sourceCount,
				"FAKE_V2_BLOB_COUNT":       blobCount,
				"FAKE_V2_TREE_STATE":       treeState,
				"FAKE_V2_COMMIT_STATE":     commitState,
				"FAKE_V2_SOURCE_TREE_FILE": sourceTree,
				"PATH":                     binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
				"TMPDIR":                   root,
			})
			outputBytes, runErr := command.CombinedOutput()
			status := 0
			if runErr != nil {
				exitError, ok := runErr.(*exec.ExitError)
				if !ok {
					t.Fatalf("run v2 publisher: %v\n%s", runErr, outputBytes)
				}
				status = exitError.ExitCode()
			}
			output := string(outputBytes)
			if status != test.wantStatus {
				t.Fatalf("status=%d want=%d\n%s", status, test.wantStatus, output)
			}
			if test.wantStatus == 0 {
				for _, fragment := range []string{
					"commit_sha=5555555555555555555555555555555555555555\n",
					"state=" + test.wantState + "\n", "format=v2\n", "ledger_mode=evidence-only-parentless-v1\n",
					"path_prefix=evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2\n",
				} {
					if !strings.Contains(output, fragment) {
						t.Fatalf("successful output lacks %q:\n%s", fragment, output)
					}
				}
			} else if !strings.Contains(output, test.wantOutput) {
				t.Fatalf("failure output lacks %q:\n%s", test.wantOutput, output)
			}

			if got := readIntFile(t, sourceCount); got != test.wantSourceProbes {
				t.Fatalf("fresh source probes=%d want=%d\n%s", got, test.wantSourceProbes, output)
			}
			calls := readOptionalFile(t, callLog)
			if got := strings.Count(calls, "POST repos/example/env-vault/git/blobs\n"); got != test.wantBlobPosts {
				t.Fatalf("blob creates=%d want=%d\n%s", got, test.wantBlobPosts, calls)
			}
			if got := strings.Count(calls, "POST repos/example/env-vault/git/refs\n"); got != test.wantRefPosts {
				t.Fatalf("ref creates=%d want=%d\n%s", got, test.wantRefPosts, calls)
			}
			if strings.Contains(calls, "force=true") || strings.Contains(calls, "DELETE ") {
				t.Fatalf("destructive mutation observed:\n%s", calls)
			}
			if strings.Contains(calls, "GET repos/example/env-vault/git/trees/1010101010101010101010101010101010101010") {
				t.Fatalf("parentless genesis read or inherited the source tree:\n%s", calls)
			}
			assertGenesisMutationSourceOrder(t, calls)
			if test.wantStatus != 0 {
				if test.wantRefPosts == 0 && test.wantBlobPosts == 0 && strings.Contains(calls, "POST ") {
					t.Fatalf("precondition failure reached mutation:\n%s", calls)
				}
				return
			}

			assertEvidenceV2BinaryRoundTrip(t, payloadDirectory, remoteDirectory, fixture.objectBytes)
			assertEvidenceV2ParentlessRequests(t, payloadDirectory)
		})
	}
}

func writeGenesisSourceTreeFixture(t *testing.T, root string) string {
	t.Helper()
	entries := evidenceV2TreeClosure(map[string]string{
		"README.md":                     strings.Repeat("9", 40),
		".github/workflows/release.yml": strings.Repeat("7", 40),
	})
	path := filepath.Join(root, "genesis-source-tree.json")
	writeFile(t, path, marshalStandardJSON(t, map[string]any{
		"sha": "1010101010101010101010101010101010101010", "truncated": false, "tree": entries,
	}))
	return path
}

func TestPublishReleaseEvidenceV2PreservesLegacyLineageAndUpdatesByFastForward(t *testing.T) {
	tests := []struct {
		name          string
		mode          string
		includeTarget bool
		wantStatus    int
		wantState     string
		wantOutput    string
		wantBlobs     int
		wantTrees     int
		wantCommits   int
		wantUpdates   int
		ledgerDepth   int
	}{
		{name: "legacy-compatible append fast-forwards", mode: "legacy-append", wantState: "updated", wantBlobs: 7, wantTrees: 1, wantCommits: 1, wantUpdates: 1},
		{name: "ambiguous update reconciles exact descendant", mode: "legacy-update-ambiguous", wantState: "reconciled", wantBlobs: 7, wantTrees: 1, wantCommits: 1, wantUpdates: 1},
		{name: "exact update race reconciles exact descendant", mode: "legacy-update-race", wantState: "reconciled", wantBlobs: 7, wantTrees: 1, wantCommits: 1, wantUpdates: 1},
		{name: "conflicting update race fails closed", mode: "legacy-update-conflict", wantStatus: 1, wantOutput: "GitHub API request failed", wantBlobs: 7, wantTrees: 1, wantCommits: 1, wantUpdates: 1},
		{name: "existing exact tuple is a no-op", mode: "legacy-noop", includeTarget: true, wantState: "unchanged"},
		{name: "depth 64 exact tuple remains readable", mode: "legacy-depth64-noop", includeTarget: true, wantState: "unchanged", ledgerDepth: 64},
		{name: "depth 64 missing tuple fails before mutation", mode: "legacy-depth64-missing", wantStatus: 1, wantOutput: "64-commit validation bound", ledgerDepth: 64},
		{name: "depth 63 permits the final bounded append", mode: "legacy-depth63-append", wantState: "updated", wantBlobs: 7, wantTrees: 1, wantCommits: 1, wantUpdates: 1, ledgerDepth: 63},
		{name: "returned tree cannot drop older evidence before commit", mode: "legacy-dropped-history-tree", wantStatus: 1, wantOutput: "rewrote or removed an earlier controlled blob", wantBlobs: 7, wantTrees: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			fixture := writeEvidenceV2Fixture(t, filepath.Join(root, "candidate"))
			binDirectory := filepath.Join(root, "bin")
			remoteDirectory := filepath.Join(root, "remote")
			payloadDirectory := filepath.Join(root, "payloads")
			for _, directory := range []string{binDirectory, remoteDirectory, payloadDirectory} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			baseTree, sourceTree := writeLegacyEvidenceTreeFixtures(t, root, remoteDirectory, fixture, test.includeTarget)
			chainFile := writeLegacyEvidenceChainFixture(t, root, test.ledgerDepth)
			installEvidenceV2FakeGH(t, binDirectory)
			checker := installEvidenceV2FakeChecker(t, binDirectory)
			callLog := filepath.Join(root, "calls.log")
			args := []string{
				"../scripts/release/publish-release-evidence.sh", "--format", "v2",
				releaseTestVersion, evidenceSourceSHA, releaseTestRepository,
				fixture.bundleDirectory, fixture.indexPath, fixture.comparisonPath, fixture.markdownPath,
				fixture.storagePath, fixture.parityPath, fixture.genesisPath,
			}
			command := exec.Command("/bin/bash", args...)
			command.Env = environmentWithOverrides(map[string]string{
				"EVIDENCE_PUBLISHER_CHECK": checker,
				"FAKE_V2_MODE":             test.mode,
				"FAKE_V2_CALL_LOG":         callLog,
				"FAKE_V2_REMOTE_DIR":       remoteDirectory,
				"FAKE_V2_PAYLOAD_DIR":      payloadDirectory,
				"FAKE_V2_REF_STATE":        filepath.Join(root, "ref-state"),
				"FAKE_V2_SOURCE_COUNT":     filepath.Join(root, "source-count"),
				"FAKE_V2_BLOB_COUNT":       filepath.Join(root, "blob-count"),
				"FAKE_V2_TREE_STATE":       filepath.Join(root, "tree.json"),
				"FAKE_V2_COMMIT_STATE":     filepath.Join(root, "commit.json"),
				"FAKE_V2_BASE_TREE_FILE":   baseTree,
				"FAKE_V2_SOURCE_TREE_FILE": sourceTree,
				"FAKE_V2_CHAIN_FILE":       chainFile,
				"PATH":                     binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
				"TMPDIR":                   root,
			})
			outputBytes, runErr := command.CombinedOutput()
			status := 0
			if runErr != nil {
				status = runErr.(*exec.ExitError).ExitCode()
			}
			output := string(outputBytes)
			if status != test.wantStatus {
				t.Fatalf("status=%d want=%d\n%s", status, test.wantStatus, output)
			}
			if test.wantStatus == 0 {
				for _, fragment := range []string{"state=" + test.wantState + "\n", "format=v2\n", "ledger_mode=legacy-compatible\n"} {
					if !strings.Contains(output, fragment) {
						t.Fatalf("output lacks %q:\n%s", fragment, output)
					}
				}
			} else if !strings.Contains(output, test.wantOutput) {
				t.Fatalf("failure output lacks %q:\n%s", test.wantOutput, output)
			}
			calls := readOptionalFile(t, callLog)
			if got := strings.Count(calls, "POST repos/example/env-vault/git/blobs\n"); got != test.wantBlobs {
				t.Fatalf("blob posts=%d want=%d\n%s", got, test.wantBlobs, calls)
			}
			if got := strings.Count(calls, "POST repos/example/env-vault/git/trees\n"); got != test.wantTrees {
				t.Fatalf("tree posts=%d want=%d\n%s", got, test.wantTrees, calls)
			}
			if got := strings.Count(calls, "POST repos/example/env-vault/git/commits\n"); got != test.wantCommits {
				t.Fatalf("commit posts=%d want=%d\n%s", got, test.wantCommits, calls)
			}
			if got := strings.Count(calls, "PATCH repos/example/env-vault/git/refs/heads/release-evidence\n"); got != test.wantUpdates {
				t.Fatalf("ref updates=%d want=%d\n%s", got, test.wantUpdates, calls)
			}
			if test.wantUpdates == 0 {
				return
			}
			treeRequest, err := os.ReadFile(filepath.Join(payloadDirectory, "tree-request.json"))
			if err != nil {
				t.Fatal(err)
			}
			var tree struct {
				BaseTree string `json:"base_tree"`
			}
			if err := json.Unmarshal(treeRequest, &tree); err != nil || tree.BaseTree != "3333333333333333333333333333333333333333" {
				t.Fatalf("append base_tree=%q error=%v", tree.BaseTree, err)
			}
			createdTreeBytes, err := os.ReadFile(filepath.Join(root, "tree.json"))
			if err != nil {
				t.Fatal(err)
			}
			var createdTree struct {
				Tree []evidenceV2TreeEntry `json:"tree"`
			}
			if err := json.Unmarshal(createdTreeBytes, &createdTree); err != nil {
				t.Fatal(err)
			}
			workflowPreserved := false
			for _, entry := range createdTree.Tree {
				if entry.Path == ".github/workflows/release.yml" && entry.Type == "blob" && entry.Mode == "100644" && entry.SHA == strings.Repeat("7", 40) {
					workflowPreserved = true
				}
			}
			if !workflowPreserved {
				t.Fatal("legacy-compatible append did not preserve the inherited workflow blob exactly")
			}
			commitRequest, _ := os.ReadFile(filepath.Join(payloadDirectory, "commit-request.json"))
			var commit struct {
				Parents []string `json:"parents"`
			}
			if err := json.Unmarshal(commitRequest, &commit); err != nil || fmt.Sprint(commit.Parents) != "[2222222222222222222222222222222222222222]" {
				t.Fatalf("append parents=%v error=%v", commit.Parents, err)
			}
			updateRequest, _ := os.ReadFile(filepath.Join(payloadDirectory, "update-ref-request.json"))
			var update struct {
				Force bool `json:"force"`
			}
			if err := json.Unmarshal(updateRequest, &update); err != nil || update.Force {
				t.Fatalf("ref update force=%t error=%v", update.Force, err)
			}
		})
	}
}

func TestPublishReleaseEvidenceV2AnchoredAmbiguousUpdateReplaysBoundedRootThreeTimes(t *testing.T) {
	root := t.TempDir()
	fixture := writeEvidenceV2Fixture(t, filepath.Join(root, "candidate"))
	binDirectory := filepath.Join(root, "bin")
	remoteDirectory := filepath.Join(root, "remote")
	payloadDirectory := filepath.Join(root, "payloads")
	for _, directory := range []string{binDirectory, remoteDirectory, payloadDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	baseTree, sourceTree := writeAnchoredEvidenceTreeFixtures(t, root, remoteDirectory, fixture)
	var rootTree struct {
		Tree []evidenceV2TreeEntry `json:"tree"`
	}
	rootTreeBytes, err := os.ReadFile(baseTree)
	if err != nil || json.Unmarshal(rootTreeBytes, &rootTree) != nil {
		t.Fatalf("read anchored root tree: %v", err)
	}
	rootBlobSHAs := make(map[string]string)
	for _, entry := range rootTree.Tree {
		if entry.Path == "evidence/genesis.v1.json" || entry.Path == "evidence/releases/v1.2.2/release-evidence-bundle.json" {
			rootBlobSHAs[entry.Path] = entry.SHA
		}
	}
	if len(rootBlobSHAs) != 2 {
		t.Fatalf("anchored root identities=%v", rootBlobSHAs)
	}
	installEvidenceV2FakeGH(t, binDirectory)
	checker := installEvidenceV2FakeChecker(t, binDirectory)
	callLog := filepath.Join(root, "calls.log")
	command := exec.Command("/bin/bash",
		"../scripts/release/publish-release-evidence.sh", "--format", "v2",
		releaseTestVersion, evidenceSourceSHA, releaseTestRepository,
		fixture.bundleDirectory, fixture.indexPath, fixture.comparisonPath, fixture.markdownPath,
		fixture.storagePath, fixture.parityPath, fixture.genesisPath,
	)
	command.Env = environmentWithOverrides(map[string]string{
		"EVIDENCE_PUBLISHER_CHECK": checker,
		"FAKE_V2_MODE":             "anchored-update-ambiguous",
		"FAKE_V2_CALL_LOG":         callLog,
		"FAKE_V2_REMOTE_DIR":       remoteDirectory,
		"FAKE_V2_PAYLOAD_DIR":      payloadDirectory,
		"FAKE_V2_REF_STATE":        filepath.Join(root, "ref-state"),
		"FAKE_V2_SOURCE_COUNT":     filepath.Join(root, "source-count"),
		"FAKE_V2_BLOB_COUNT":       filepath.Join(root, "blob-count"),
		"FAKE_V2_TREE_STATE":       filepath.Join(root, "tree.json"),
		"FAKE_V2_COMMIT_STATE":     filepath.Join(root, "commit.json"),
		"FAKE_V2_BASE_TREE_FILE":   baseTree,
		"FAKE_V2_SOURCE_TREE_FILE": sourceTree,
		"PATH":                     binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR":                   root,
	})
	outputBytes, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("anchored ambiguous append failed: %v\n%s", err, outputBytes)
	}
	output := string(outputBytes)
	for _, fragment := range []string{"state=reconciled\n", "format=v2\n", "ledger_mode=evidence-only-parentless-v1\n"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("output lacks %q:\n%s", fragment, output)
		}
	}
	calls := readOptionalFile(t, callLog)
	for fragment, want := range map[string]int{
		"POST repos/example/env-vault/git/blobs\n":                        6,
		"POST repos/example/env-vault/git/trees\n":                        1,
		"POST repos/example/env-vault/git/commits\n":                      1,
		"PATCH repos/example/env-vault/git/refs/heads/release-evidence\n": 1,
	} {
		if got := strings.Count(calls, fragment); got != want {
			t.Fatalf("calls containing %q=%d want=%d\n%s", fragment, got, want, calls)
		}
	}
	if strings.Contains(calls, "/contents/") {
		t.Fatalf("anchored replay used the 64 MiB Contents transport instead of bounded blob reads:\n%s", calls)
	}
	for path, sha := range rootBlobSHAs {
		if got := strings.Count(calls, "GET repos/example/env-vault/git/blobs/"+sha+"\n"); got != 3 {
			t.Fatalf("anchored %s validation reads=%d want=3\n%s", path, got, calls)
		}
	}
}

func TestPublishReleaseEvidenceV2AnchoredRootRejectsAmplificationBeforeBlockedBlobRead(t *testing.T) {
	tests := []struct {
		mode       string
		wantOutput string
	}{
		{mode: "oversized-metadata-tree-size", wantOutput: "blob identity or declared size is invalid"},
		{mode: "object-tree-size-mismatch", wantOutput: "Git tree size differs from its descriptor"},
		{mode: "inventory-65", wantOutput: "object inventory is invalid"},
		{mode: "descriptor-oversized", wantOutput: "object inventory is invalid"},
		{mode: "descriptor-aggregate", wantOutput: "object inventory is invalid"},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			root := t.TempDir()
			fixture := writeEvidenceV2Fixture(t, filepath.Join(root, "candidate"))
			binDirectory := filepath.Join(root, "bin")
			remoteDirectory := filepath.Join(root, "remote")
			payloadDirectory := filepath.Join(root, "payloads")
			for _, directory := range []string{binDirectory, remoteDirectory, payloadDirectory} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			baseTree, sourceTree := writeAnchoredEvidenceTreeFixtures(t, root, remoteDirectory, fixture)
			blockedSHA := mutateAnchoredTreeAttack(t, baseTree, remoteDirectory, test.mode)
			installEvidenceV2FakeGH(t, binDirectory)
			checker := installEvidenceV2FakeChecker(t, binDirectory)
			callLog := filepath.Join(root, "calls.log")
			command := exec.Command("/bin/bash",
				"../scripts/release/publish-release-evidence.sh", "--format", "v2",
				releaseTestVersion, evidenceSourceSHA, releaseTestRepository,
				fixture.bundleDirectory, fixture.indexPath, fixture.comparisonPath, fixture.markdownPath,
				fixture.storagePath, fixture.parityPath, fixture.genesisPath,
			)
			command.Env = environmentWithOverrides(map[string]string{
				"EVIDENCE_PUBLISHER_CHECK": checker,
				"FAKE_V2_MODE":             "anchored-hostile",
				"FAKE_V2_CALL_LOG":         callLog,
				"FAKE_V2_REMOTE_DIR":       remoteDirectory,
				"FAKE_V2_PAYLOAD_DIR":      payloadDirectory,
				"FAKE_V2_REF_STATE":        filepath.Join(root, "ref-state"),
				"FAKE_V2_SOURCE_COUNT":     filepath.Join(root, "source-count"),
				"FAKE_V2_BLOB_COUNT":       filepath.Join(root, "blob-count"),
				"FAKE_V2_TREE_STATE":       filepath.Join(root, "tree.json"),
				"FAKE_V2_COMMIT_STATE":     filepath.Join(root, "commit.json"),
				"FAKE_V2_BASE_TREE_FILE":   baseTree,
				"FAKE_V2_SOURCE_TREE_FILE": sourceTree,
				"PATH":                     binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
				"TMPDIR":                   root,
			})
			outputBytes, runErr := command.CombinedOutput()
			if runErr == nil || !strings.Contains(string(outputBytes), test.wantOutput) {
				t.Fatalf("status=%v want failure containing %q\n%s", runErr, test.wantOutput, outputBytes)
			}
			calls := readOptionalFile(t, callLog)
			if strings.Contains(calls, "GET repos/example/env-vault/git/blobs/"+blockedSHA+"\n") {
				t.Fatalf("blocked oversized or inconsistent blob %s was read:\n%s", blockedSHA, calls)
			}
			if strings.Contains(calls, "POST ") || strings.Contains(calls, "PATCH ") {
				t.Fatalf("hostile anchored root reached mutation:\n%s", calls)
			}
		})
	}
}

type evidenceV2TreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size *int64 `json:"size,omitempty"`
}

type evidenceV2SizedBlob struct {
	sha  string
	size int64
}

func evidenceV2SizedTreeClosure(blobs map[string]evidenceV2SizedBlob) []evidenceV2TreeEntry {
	entries := make(map[string]evidenceV2TreeEntry)
	for path, blob := range blobs {
		size := blob.size
		entries[path] = evidenceV2TreeEntry{Path: path, Mode: "100644", Type: "blob", SHA: blob.sha, Size: &size}
		parent := path
		for strings.Contains(parent, "/") {
			parent = parent[:strings.LastIndex(parent, "/")]
			entries[parent] = evidenceV2TreeEntry{Path: parent, Mode: "040000", Type: "tree", SHA: strings.Repeat("0", 40)}
		}
	}
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]evidenceV2TreeEntry, 0, len(paths))
	for _, path := range paths {
		result = append(result, entries[path])
	}
	return result
}

func writeLegacyEvidenceTreeFixtures(t *testing.T, root, remoteDirectory string, fixture evidenceV2Fixture, includeTarget bool) (string, string) {
	t.Helper()
	baseBlobs := map[string]string{
		"README.md":                     strings.Repeat("9", 40),
		"evidence/README.md":            strings.Repeat("8", 40),
		".github/workflows/release.yml": strings.Repeat("7", 40),
	}
	legacyNames := []string{"release-evidence.json", "index.md", "metrics-comparison.json", "metrics-comparison.md"}
	for index, name := range legacyNames {
		sha := fmt.Sprintf("%040x", index+1)
		baseBlobs["evidence/releases/v1.2.2/"+name] = sha
		baseBlobs["evidence/releases/v1.2.2/publisher-runs/run-41/attempt-1/"+name] = sha
	}
	if includeTarget {
		candidate := []struct {
			name string
			path string
		}{
			{"release-evidence-bundle.json", filepath.Join(fixture.bundleDirectory, releaseevidence.BundleRootName)},
			{"index.md", fixture.indexPath}, {"metrics-comparison.json", fixture.comparisonPath},
			{"metrics-comparison.md", fixture.markdownPath}, {"storage-metrics.json", fixture.storagePath}, {"parity.json", fixture.parityPath},
		}
		for _, item := range candidate {
			data, err := os.ReadFile(item.path)
			if err != nil {
				t.Fatal(err)
			}
			sha := gitBlobSHAForV2(data)
			writeFile(t, filepath.Join(remoteDirectory, "blob-"+sha), data)
			baseBlobs["evidence/releases/v1.2.3/"+item.name] = sha
			baseBlobs["evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/"+item.name] = sha
		}
		bundleBytes, _ := os.ReadFile(filepath.Join(fixture.bundleDirectory, releaseevidence.BundleRootName))
		bundle, err := releaseevidence.ParseBundle(bundleBytes)
		if err != nil {
			t.Fatal(err)
		}
		for _, object := range bundle.Objects {
			data, err := os.ReadFile(filepath.Join(fixture.bundleDirectory, "objects", "sha256", object.SHA256+".gz"))
			if err != nil {
				t.Fatal(err)
			}
			sha := gitBlobSHAForV2(data)
			writeFile(t, filepath.Join(remoteDirectory, "blob-"+sha), data)
			baseBlobs["evidence/objects/sha256/"+object.SHA256+".gz"] = sha
		}
	}
	baseEntries := evidenceV2TreeClosure(baseBlobs)
	sourceEntries := evidenceV2TreeClosure(map[string]string{
		"README.md":                     baseBlobs["README.md"],
		"evidence/README.md":            baseBlobs["evidence/README.md"],
		".github/workflows/release.yml": baseBlobs[".github/workflows/release.yml"],
	})
	basePath := filepath.Join(root, "base-tree.json")
	sourcePath := filepath.Join(root, "source-tree.json")
	writeFile(t, basePath, marshalStandardJSON(t, map[string]any{"sha": strings.Repeat("3", 40), "truncated": false, "tree": baseEntries}))
	writeFile(t, sourcePath, marshalStandardJSON(t, map[string]any{"sha": "1010101010101010101010101010101010101010", "truncated": false, "tree": sourceEntries}))
	return basePath, sourcePath
}

func writeLegacyEvidenceChainFixture(t *testing.T, root string, depth int) string {
	t.Helper()
	if depth == 0 {
		return ""
	}
	if depth < 1 || depth > 64 {
		t.Fatalf("unsupported synthetic ledger depth %d", depth)
	}
	const baseCommit = "2222222222222222222222222222222222222222"
	parents := make(map[string]string, depth)
	cursor := baseCommit
	for index := 1; index < depth; index++ {
		next := fmt.Sprintf("%040x", 1000+index)
		parents[cursor] = next
		cursor = next
	}
	parents[cursor] = evidenceSourceSHA
	path := filepath.Join(root, "legacy-chain.json")
	writeFile(t, path, marshalStandardJSON(t, parents))
	return path
}

func writeAnchoredEvidenceTreeFixtures(t *testing.T, root, remoteDirectory string, fixture evidenceV2Fixture) (string, string) {
	t.Helper()
	const firstVersion = "v1.2.2"
	const firstRun = int64(41)
	const firstAttempt = 1
	metadata := []struct {
		name string
		path string
	}{
		{"release-evidence-bundle.json", filepath.Join(fixture.bundleDirectory, releaseevidence.BundleRootName)},
		{"index.md", fixture.indexPath},
		{"metrics-comparison.json", fixture.comparisonPath},
		{"metrics-comparison.md", fixture.markdownPath},
		{"storage-metrics.json", fixture.storagePath},
		{"parity.json", fixture.parityPath},
	}
	blobs := make(map[string]evidenceV2SizedBlob)
	prefix := "evidence/releases/" + firstVersion
	tuple := prefix + "/publisher-runs/run-41/attempt-1"
	var baseBundle releaseevidence.Bundle
	for _, item := range metadata {
		data, err := os.ReadFile(item.path)
		if err != nil {
			t.Fatal(err)
		}
		if item.name == releaseevidence.BundleRootName {
			baseBundle, err = releaseevidence.ParseBundle(data)
			if err != nil {
				t.Fatal(err)
			}
			baseBundle.ReleaseVersion = firstVersion
			baseBundle.PublisherRunID = firstRun
			baseBundle.PublisherRunAttempt = firstAttempt
			data = marshalEvidenceV2JSON(t, baseBundle)
		}
		sha := gitBlobSHAForV2(data)
		writeFile(t, filepath.Join(remoteDirectory, "blob-"+sha), data)
		identity := evidenceV2SizedBlob{sha: sha, size: int64(len(data))}
		blobs[prefix+"/"+item.name] = identity
		blobs[tuple+"/"+item.name] = identity
	}

	genesisBytes, err := os.ReadFile(fixture.genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	var genesis releaseevidence.GenesisAnchor
	if err := json.Unmarshal(genesisBytes, &genesis); err != nil {
		t.Fatal(err)
	}
	genesis.FirstReleaseVersion = firstVersion
	genesis.PublisherRunID = firstRun
	genesis.PublisherRunAttempt = firstAttempt
	genesisBytes = marshalEvidenceV2JSON(t, genesis)
	genesisSHA := gitBlobSHAForV2(genesisBytes)
	writeFile(t, filepath.Join(remoteDirectory, "blob-"+genesisSHA), genesisBytes)
	blobs["evidence/genesis.v1.json"] = evidenceV2SizedBlob{sha: genesisSHA, size: int64(len(genesisBytes))}

	for _, object := range baseBundle.Objects {
		data, err := os.ReadFile(filepath.Join(fixture.bundleDirectory, "objects", "sha256", object.SHA256+".gz"))
		if err != nil {
			t.Fatal(err)
		}
		sha := gitBlobSHAForV2(data)
		writeFile(t, filepath.Join(remoteDirectory, "blob-"+sha), data)
		blobs["evidence/objects/sha256/"+object.SHA256+".gz"] = evidenceV2SizedBlob{sha: sha, size: int64(len(data))}
	}

	basePath := filepath.Join(root, "anchored-base-tree.json")
	writeFile(t, basePath, marshalStandardJSON(t, map[string]any{
		"sha": strings.Repeat("3", 40), "truncated": false, "tree": evidenceV2SizedTreeClosure(blobs),
	}))
	return basePath, writeGenesisSourceTreeFixture(t, root)
}

func mutateAnchoredTreeAttack(t *testing.T, baseTreePath, remoteDirectory, mode string) string {
	t.Helper()
	var tree struct {
		SHA       string                `json:"sha"`
		Truncated bool                  `json:"truncated"`
		Tree      []evidenceV2TreeEntry `json:"tree"`
	}
	data, err := os.ReadFile(baseTreePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}
	objectIndex := -1
	bundleIndexes := make([]int, 0, 2)
	for index := range tree.Tree {
		if strings.HasPrefix(tree.Tree[index].Path, "evidence/objects/sha256/") && tree.Tree[index].Type == "blob" {
			objectIndex = index
		}
		if strings.HasSuffix(tree.Tree[index].Path, "/release-evidence-bundle.json") && tree.Tree[index].Type == "blob" {
			bundleIndexes = append(bundleIndexes, index)
		}
	}
	if objectIndex < 0 || len(bundleIndexes) != 2 {
		t.Fatalf("anchored fixture lacks object or root/tuple bundle entries: object=%d bundles=%d", objectIndex, len(bundleIndexes))
	}
	blockedSHA := tree.Tree[objectIndex].SHA
	switch mode {
	case "oversized-metadata-tree-size":
		blockedSHA = tree.Tree[bundleIndexes[0]].SHA
		size := int64(releaseevidence.MaxBundleRootBytes + 1)
		for _, index := range bundleIndexes {
			tree.Tree[index].Size = &size
		}
	case "object-tree-size-mismatch":
		size := *tree.Tree[objectIndex].Size + 1
		tree.Tree[objectIndex].Size = &size
	case "inventory-65", "descriptor-oversized", "descriptor-aggregate":
		bundlePath := filepath.Join(remoteDirectory, "blob-"+tree.Tree[bundleIndexes[0]].SHA)
		bundleBytes, err := os.ReadFile(bundlePath)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := releaseevidence.ParseBundle(bundleBytes)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "descriptor-oversized" {
			bundle.Objects[0].CompressedSize = releaseevidence.MaxBundleObjectCompressed + 1
		} else {
			count := 5
			compressedSize := int64(releaseevidence.MaxBundleObjectCompressed)
			if mode == "inventory-65" {
				count = releaseevidence.MaxBundleObjects + 1
				compressedSize = 1
			}
			bundle.Objects = make([]releaseevidence.BundleObject, 0, count)
			for index := 0; index < count; index++ {
				digest := fmt.Sprintf("%064x", index+1)
				mediaType := releaseevidence.RawJSONMedia
				if index == 0 {
					mediaType = releaseevidence.EvidenceCoreMedia
					bundle.EvidenceCoreObjectSHA256 = digest
				}
				bundle.Objects = append(bundle.Objects, releaseevidence.BundleObject{
					SHA256: digest, MediaType: mediaType, Encoding: releaseevidence.BundleEncodingGZIP,
					UncompressedSize: 1, CompressedSize: compressedSize, CompressedSHA256: strings.Repeat("a", 64),
				})
			}
		}
		bundleBytes = marshalEvidenceV2JSON(t, bundle)
		newSHA := gitBlobSHAForV2(bundleBytes)
		writeFile(t, filepath.Join(remoteDirectory, "blob-"+newSHA), bundleBytes)
		size := int64(len(bundleBytes))
		for _, index := range bundleIndexes {
			tree.Tree[index].SHA = newSHA
			tree.Tree[index].Size = &size
		}
	default:
		t.Fatalf("unknown anchored attack mode %q", mode)
	}
	writeFile(t, baseTreePath, marshalStandardJSON(t, tree))
	return blockedSHA
}

func evidenceV2TreeClosure(blobs map[string]string) []evidenceV2TreeEntry {
	entries := make(map[string]evidenceV2TreeEntry)
	for path, sha := range blobs {
		entries[path] = evidenceV2TreeEntry{Path: path, Mode: "100644", Type: "blob", SHA: sha}
		parent := path
		for strings.Contains(parent, "/") {
			parent = parent[:strings.LastIndex(parent, "/")]
			entries[parent] = evidenceV2TreeEntry{Path: parent, Mode: "040000", Type: "tree", SHA: strings.Repeat("0", 40)}
		}
	}
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]evidenceV2TreeEntry, 0, len(paths))
	for _, path := range paths {
		result = append(result, entries[path])
	}
	return result
}

func marshalStandardJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func writeEvidenceV2Fixture(t *testing.T, directory string) evidenceV2Fixture {
	t.Helper()
	bundleDirectory := filepath.Join(directory, "bundle")
	objectDirectory := filepath.Join(bundleDirectory, "objects", "sha256")
	if err := os.MkdirAll(objectDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	rawObject := []byte{0x00, 0x80, 0xff}
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.NoCompression)
	if err != nil {
		t.Fatal(err)
	}
	writer.Header.ModTime = time.Unix(0, 0).UTC()
	writer.Header.OS = 255
	if _, err := writer.Write(rawObject); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	objectBytes := append([]byte(nil), compressed.Bytes()...)
	for _, value := range rawObject {
		if !bytes.Contains(objectBytes, []byte{value}) {
			t.Fatalf("binary gzip fixture does not retain byte %02x", value)
		}
	}
	rawDigest := sha256.Sum256(rawObject)
	objectDigest := hex.EncodeToString(rawDigest[:])
	compressedDigest := sha256.Sum256(objectBytes)
	bundle := releaseevidence.Bundle{
		SchemaID: releaseevidence.BundleSchemaID, SchemaVersion: releaseevidence.BundleSchemaVersion,
		Repository: releaseTestRepository, ReleaseVersion: releaseTestVersion, SourceSHA: evidenceSourceSHA,
		PublisherRepairMode: "none", PublisherRunID: evidenceRunID, PublisherRunAttempt: evidenceAttempt,
		EvidenceSchemaID: releaseevidence.SchemaID, EvidenceSchemaVersion: releaseevidence.SchemaVersion,
		LegacyEvidenceSHA256: strings.Repeat("e", 64), LegacyCanonicalJSONSHA256: strings.Repeat("d", 64), LegacyCanonicalJSONSize: 2_000_000,
		EvidenceCoreObjectSHA256: objectDigest,
		Objects: []releaseevidence.BundleObject{{
			SHA256: objectDigest, MediaType: releaseevidence.EvidenceCoreMedia, Encoding: releaseevidence.BundleEncodingGZIP,
			UncompressedSize: int64(len(rawObject)), CompressedSize: int64(len(objectBytes)), CompressedSHA256: hex.EncodeToString(compressedDigest[:]),
		}},
		Result: "pass", BundleSHA256: strings.Repeat("b", 64),
	}
	bundleBytes := marshalEvidenceV2JSON(t, bundle)
	writeFile(t, filepath.Join(bundleDirectory, releaseevidence.BundleRootName), bundleBytes)
	writeFile(t, filepath.Join(objectDirectory, objectDigest+".gz"), objectBytes)

	indexBytes := []byte(fmt.Sprintf("# env-vault %s release evidence\n\n- Result: `pass`\n- Source SHA: `%s`\n", releaseTestVersion, evidenceSourceSHA))
	comparisonBytes := []byte(fmt.Sprintf(`{"schema_id":"env-vault.release-metrics-comparison.v1","schema_version":1,"scenarios":[{"scenario":"main_ci","current_head_sha":"%s"},{"scenario":"pr_ci","current_head_sha":"9999999999999999999999999999999999999999"},{"scenario":"publisher","current_head_sha":"%s"}]}
`, evidenceSourceSHA, evidenceSourceSHA))
	markdownBytes := []byte("# Release pipeline metrics comparison\n\n| Scenario | Jobs |\n|---|---:|\n")
	parity := releaseevidence.ParityResult{
		SchemaID: releaseevidence.ParitySchemaID, SchemaVersion: releaseevidence.ParitySchemaVersion, OK: true,
		Repository: releaseTestRepository, ReleaseVersion: releaseTestVersion, SourceSHA: evidenceSourceSHA,
		LegacyDecision: "pass", BundleDecision: "pass", LegacyCanonicalJSONSHA256: bundle.LegacyCanonicalJSONSHA256,
		BundleSHA256: bundle.BundleSHA256, ReconstructedByteExact: true, Result: "pass",
	}
	parityBytes := marshalEvidenceV2JSON(t, parity)
	auxiliaryBytes := int64(len(indexBytes) + len(comparisonBytes) + len(markdownBytes))
	metrics := evidenceV2StorageFixedPoint(t, int64(len(bundleBytes)), auxiliaryBytes, int64(len(parityBytes)), int64(len(rawObject)), int64(len(objectBytes)))
	storageBytes := marshalEvidenceV2JSON(t, metrics)
	genesis := releaseevidence.GenesisAnchor{
		SchemaID: releaseevidence.GenesisSchemaID, SchemaVersion: releaseevidence.GenesisSchemaVersion, LedgerMode: releaseevidence.GenesisLedgerMode,
		Repository: releaseTestRepository, SourceSHA: evidenceSourceSHA, FirstReleaseVersion: releaseTestVersion,
		EvidenceFormatSchemaID: releaseevidence.BundleSchemaID, EvidenceFormatVersion: releaseevidence.BundleSchemaVersion,
		FirstBundleSHA256: bundle.BundleSHA256, PublisherRunID: evidenceRunID, PublisherRunAttempt: evidenceAttempt, PublisherRepairMode: "none",
		EvidenceWorkflowID: "release_evidence", EvidenceWorkflowName: "release-evidence", EvidenceWorkflowFile: "release-evidence.yml",
		EvidenceRunID: 333333, EvidenceRunAttempt: 1, AnchorSHA256: strings.Repeat("a", 64),
	}

	fixture := evidenceV2Fixture{
		bundleDirectory: bundleDirectory,
		indexPath:       filepath.Join(directory, "index.md"),
		comparisonPath:  filepath.Join(directory, "metrics-comparison.json"),
		markdownPath:    filepath.Join(directory, "metrics-comparison.md"),
		storagePath:     filepath.Join(directory, "storage-metrics.json"),
		parityPath:      filepath.Join(directory, "parity.json"),
		genesisPath:     filepath.Join(directory, "genesis.v1.json"),
		objectBytes:     objectBytes,
	}
	writeFile(t, fixture.indexPath, indexBytes)
	writeFile(t, fixture.comparisonPath, comparisonBytes)
	writeFile(t, fixture.markdownPath, markdownBytes)
	writeFile(t, fixture.storagePath, storageBytes)
	writeFile(t, fixture.parityPath, parityBytes)
	writeFile(t, fixture.genesisPath, marshalEvidenceV2JSON(t, genesis))
	return fixture
}

func evidenceV2StorageFixedPoint(t *testing.T, rootBytes, auxiliaryBytes, parityBytes, objectRaw, objectCompressed int64) releaseevidence.StorageMetrics {
	t.Helper()
	var matches []releaseevidence.StorageMetrics
	for candidate := int64(1); candidate <= releaseevidence.MaxStorageMetricsBytes; candidate++ {
		metadata := rootBytes + auxiliaryBytes + parityBytes + candidate
		metrics := releaseevidence.StorageMetrics{
			SchemaID: releaseevidence.StorageMetricsID, SchemaVersion: 1,
			Repository: releaseTestRepository, ReleaseVersion: releaseTestVersion, SourceSHA: evidenceSourceSHA,
			LegacyRootJSONBytes: 2_000_000, AuxiliaryBytes: auxiliaryBytes, ParityBytes: parityBytes, StorageMetricsSelfBytes: candidate,
			LegacyDurableMetadataBytes: 2_000_000 + auxiliaryBytes, CompactDurableMetadataBytes: metadata,
			LogicalPathBytesScope: "git_blob_payload_bytes_per_ledger_path", LegacyRootAttemptLogicalBytes: 2 * (2_000_000 + auxiliaryBytes),
			UniqueGitBlobBytesScope: "git_blob_payload_bytes_after_object_id_deduplication", LegacyUniqueGitBlobBytes: 2_000_000 + auxiliaryBytes,
			CompactRootIndexBytes: rootBytes, CompactObjectCount: 1, CompactObjectUncompressedBytes: objectRaw, CompactObjectCompressedBytes: objectCompressed,
			CompactRootAttemptLogicalBytes: 2*metadata + objectCompressed, CompactUniqueGitBlobBytes: metadata + objectCompressed,
			OfflineReconstructedBytesScope: "durable_metadata_plus_gunzip_object_bytes", CompactOfflineReconstructedBytes: metadata + objectRaw,
			LogicalPayloadReductionPermille: 900, DeterministicExportScope: "excluding_storage_metrics_self_report",
			LegacyDeterministicExportTarGZIPBytes: 1_000_000, CompactDeterministicExportTarGZIPBytes: 100_000, DeterministicExportReductionPermille: 900,
			RootTargetBytes: releaseevidence.MaxBundleRootBytes, ReductionTargetPermille: 600, TargetsMet: true, Result: "pass",
		}
		if int64(len(marshalEvidenceV2JSON(t, metrics))) == candidate {
			matches = append(matches, metrics)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("storage metrics fixture fixed points=%d", len(matches))
	}
	return matches[0]
}

func marshalEvidenceV2JSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := releaseevidence.MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func installEvidenceV2FakeChecker(t *testing.T, binDirectory string) string {
	t.Helper()
	path := filepath.Join(binDirectory, "releasecheck")
	script := `#!/bin/sh
if [ "${1:-}" = --version ] && [ "${2:-}" = --json ]; then
  printf '%s\n' '{"supported_schema_versions":{"release_evidence_bundle":[2],"release_evidence_genesis":[1]}}'
  exit 0
fi
if [ "${1:-}" = evidence ] && { [ "${2:-}" = bundle-verify ] || [ "${2:-}" = genesis-verify ]; }; then exit 0; fi
exit 90
`
	writeExecutable(t, path, script)
	return path
}

func installEvidenceV2FakeGH(t *testing.T, binDirectory string) {
	t.Helper()
	path := filepath.Join(binDirectory, "gh")
	script := `#!/bin/bash
set -euo pipefail

if [[ ${1:-} == --version ]]; then printf 'gh version 2.96.0 (2026-07-02)\n'; exit 0; fi
if [[ ${1:-} == api && ${2:-} == --help ]]; then printf '%s\n' '--include --hostname --method --header --raw-field --input'; exit 0; fi

mode=${FAKE_V2_MODE:?}
call_log=${FAKE_V2_CALL_LOG:?}
remote_dir=${FAKE_V2_REMOTE_DIR:?}
payload_dir=${FAKE_V2_PAYLOAD_DIR:?}
ref_state=${FAKE_V2_REF_STATE:?}
source_count=${FAKE_V2_SOURCE_COUNT:?}
blob_count=${FAKE_V2_BLOB_COUNT:?}
tree_state=${FAKE_V2_TREE_STATE:?}
commit_state=${FAKE_V2_COMMIT_STATE:?}
base_tree_file=${FAKE_V2_BASE_TREE_FILE:-}
source_tree_file=${FAKE_V2_SOURCE_TREE_FILE:-}
chain_file=${FAKE_V2_CHAIN_FILE:-}

respond() {
  local status=$1 text=$2 exit_status=$3 body=$4 extra=${5:-}
  printf 'HTTP/2 %s %s\r\nContent-Type: application/vnd.github+json; charset=utf-8\r\nDate: Fri, 17 Jul 2026 12:00:00 GMT\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n%s\r\n%s' "$status" "$text" "$extra" "$body"
  exit "$exit_status"
}

[[ ${1:-} == api ]] || exit 90
shift
method=GET
endpoint=''
input=''
while (($#)); do
  case "$1" in
    --include) shift ;;
    --hostname|--header|--raw-field) shift 2 ;;
    --method) method=${2:-}; shift 2 ;;
    --input) input=${2:-}; shift 2 ;;
    -*) exit 91 ;;
    *) [[ -z $endpoint ]] || exit 91; endpoint=$1; shift ;;
  esac
done
printf '%s %s\n' "$method" "$endpoint" >>"$call_log"

source_sha=1111111111111111111111111111111111111111
source_tree=1010101010101010101010101010101010101010
new_tree=4444444444444444444444444444444444444444
new_commit=5555555555555555555555555555555555555555
conflict_commit=6666666666666666666666666666666666666666
base_commit=2222222222222222222222222222222222222222
base_tree=3333333333333333333333333333333333333333
full_ref=refs/heads/release-evidence

if [[ $method == GET && $endpoint == repos/example/env-vault/git/commits/$source_sha ]]; then
  count=0; [[ ! -f $source_count ]] || count=$(<"$source_count"); count=$((count + 1)); printf '%s\n' "$count" >"$source_count"
  if [[ $mode == source-disappears && $count -ge 2 ]]; then respond 404 'Not Found' 1 '{"message":"Not Found"}'; fi
  if [[ $mode == source-disappears-late && $count -ge 6 ]]; then respond 404 'Not Found' 1 '{"message":"Not Found"}'; fi
  response_sha=$source_sha; [[ $mode != wrong-source ]] || response_sha=9999999999999999999999999999999999999999
  body=$(jq -cn --arg sha "$response_sha" --arg tree "$source_tree" '{sha:$sha,tree:{sha:$tree},parents:[],message:"source"}')
  respond 200 OK 0 "$body"
fi

if [[ $method == GET && $endpoint == repos/example/env-vault/git/ref/heads/release-evidence ]]; then
  if [[ ! -f $ref_state ]]; then
    if [[ $mode == probe-403 ]]; then respond 403 Forbidden 1 '{"message":"Forbidden"}'; fi
    if [[ $mode == probe-429 ]]; then respond 429 'Too Many Requests' 1 '{"message":"rate limited"}' $'Retry-After: 0\r\n'; fi
    if [[ $mode == legacy-* || $mode == anchored-* ]]; then
      body=$(jq -cn --arg ref "$full_ref" --arg sha "$base_commit" '{ref:$ref,object:{type:"commit",sha:$sha}}')
      respond 200 OK 0 "$body"
    fi
    respond 404 'Not Found' 1 '{"message":"Not Found"}'
  fi
  ref_sha=$(<"$ref_state")
  body=$(jq -cn --arg ref "$full_ref" --arg sha "$ref_sha" '{ref:$ref,object:{type:"commit",sha:$sha}}')
  respond 200 OK 0 "$body"
fi

if [[ $method == GET && $endpoint == repos/example/env-vault/git/commits/$new_commit && -f $commit_state ]]; then
  respond 200 OK 0 "$(<"$commit_state")"
fi
if [[ $method == GET && $endpoint =~ ^repos/example/env-vault/git/commits/([0-9a-f]{40})$ && -n $chain_file && -f $chain_file ]]; then
  chain_commit=${BASH_REMATCH[1]}
  if jq -e --arg sha "$chain_commit" 'has($sha)' "$chain_file" >/dev/null; then
    chain_parent=$(jq -er --arg sha "$chain_commit" '.[$sha]' "$chain_file")
    body=$(jq -cn --arg sha "$chain_commit" --arg tree "$base_tree" --arg parent "$chain_parent" '{sha:$sha,tree:{sha:$tree},parents:[{sha:$parent}],message:"chore(evidence): legacy baseline"}')
    respond 200 OK 0 "$body"
  fi
fi
if [[ $method == GET && $endpoint == repos/example/env-vault/git/commits/$base_commit && $mode == anchored-* ]]; then
  body=$(jq -cn --arg sha "$base_commit" --arg tree "$base_tree" '{sha:$sha,tree:{sha:$tree},parents:[],message:"chore(evidence): create ledger at v1.2.2 from 1111111111111111111111111111111111111111"}')
  respond 200 OK 0 "$body"
fi
if [[ $method == GET && $endpoint == repos/example/env-vault/git/commits/$base_commit && $mode == legacy-* ]]; then
  body=$(jq -cn --arg sha "$base_commit" --arg tree "$base_tree" --arg parent "$source_sha" '{sha:$sha,tree:{sha:$tree},parents:[{sha:$parent}],message:"chore(evidence): legacy baseline"}')
  respond 200 OK 0 "$body"
fi
if [[ $method == GET && $endpoint == repos/example/env-vault/git/commits/$conflict_commit ]]; then
  respond 404 'Not Found' 1 '{"message":"Not Found"}'
fi
if [[ $method == GET && $endpoint == repos/example/env-vault/git/trees/$new_tree\?recursive=1 && -f $tree_state ]]; then
  if [[ $mode == wrong-tree-sha ]]; then
    respond 200 OK 0 "$(jq -c '.sha="7777777777777777777777777777777777777777"' "$tree_state")"
  fi
  respond 200 OK 0 "$(<"$tree_state")"
fi
if [[ $method == GET && $endpoint == repos/example/env-vault/git/trees/$base_tree\?recursive=1 && -n $base_tree_file ]]; then
  respond 200 OK 0 "$(<"$base_tree_file")"
fi
if [[ $method == GET && $endpoint == repos/example/env-vault/git/trees/$source_tree\?recursive=1 && -n $source_tree_file ]]; then
  respond 200 OK 0 "$(<"$source_tree_file")"
fi
if [[ $method == GET && $endpoint =~ ^repos/example/env-vault/git/blobs/([0-9a-f]{40})$ ]]; then
  sha=${BASH_REMATCH[1]}
  file="$remote_dir/blob-$sha"
  [[ -f $file ]] || respond 404 'Not Found' 1 '{"message":"Not Found"}'
  encoded="$remote_dir/encoded-$sha"; base64 <"$file" >"$encoded"; size=$(wc -c <"$file" | tr -d '[:space:]')
  body=$(jq -cn --arg sha "$sha" --argjson size "$size" --rawfile content "$encoded" '($content|gsub("[\\r\\n]";"")) as $encoded | {sha:$sha,encoding:"base64",size:$size,content:$encoded}')
  respond 200 OK 0 "$body"
fi

if [[ $method == POST && $endpoint == repos/example/env-vault/git/blobs && $input == - ]]; then
  count=0; [[ ! -f $blob_count ]] || count=$(<"$blob_count"); count=$((count + 1)); printf '%s\n' "$count" >"$blob_count"
  payload="$payload_dir/blob-$count.json"; cat >"$payload"
  encoded="$payload_dir/blob-$count.base64"; decoded="$payload_dir/blob-$count.bin"; jq -er '.content' "$payload" >"$encoded"
  if base64 --help 2>&1 | grep -q -- --decode; then base64 --decode <"$encoded" >"$decoded"; else base64 -D <"$encoded" >"$decoded"; fi
  sha=$(git hash-object -- "$decoded"); cp "$decoded" "$remote_dir/blob-$sha"
  respond 201 Created 0 "$(jq -cn --arg sha "$sha" '{sha:$sha}')"
fi
if [[ $method == POST && $endpoint == repos/example/env-vault/git/trees && $input == - ]]; then
  payload="$payload_dir/tree-request.json"; cat >"$payload"
  if [[ $mode == legacy-* || $mode == anchored-* ]]; then
    jq -c --arg sha "$new_tree" --slurpfile base "$base_tree_file" '
      .tree as $new |
      ([ $new[].path | split("/") as $parts | range(1;($parts|length)) as $n |
         {path:($parts[0:$n]|join("/")),mode:"040000",type:"tree",sha:"0000000000000000000000000000000000000000"}] | unique_by(.path)) as $dirs |
      {sha:$sha,truncated:false,tree:(($base[0].tree+$dirs+$new)|sort_by(.path)|unique_by(.path))}
    ' "$payload" >"$tree_state"
	if [[ $mode == legacy-dropped-history-tree ]]; then
	  filtered="$tree_state.filtered"
	  jq -c '.tree |= map(select(((.path == "evidence/releases/v1.2.2") or (.path | startswith("evidence/releases/v1.2.2/"))) | not))' "$tree_state" >"$filtered"
	  mv "$filtered" "$tree_state"
	fi
  else
    jq -c --arg sha "$new_tree" '
    .tree as $blobs |
    ([ $blobs[].path | split("/") as $parts | range(1;($parts|length)) as $n |
       {path:($parts[0:$n]|join("/")),mode:"040000",type:"tree",sha:"0000000000000000000000000000000000000000"}] | unique_by(.path)) as $dirs |
    {sha:$sha,truncated:false,tree:(($dirs+$blobs)|sort_by(.path))}
    ' "$payload" >"$tree_state"
  fi
  respond 201 Created 0 "$(jq -cn --arg sha "$new_tree" '{sha:$sha}')"
fi
if [[ $method == POST && $endpoint == repos/example/env-vault/git/commits && $input == - ]]; then
  payload="$payload_dir/commit-request.json"; cat >"$payload"
  jq -c --arg sha "$new_commit" '{sha:$sha,tree:{sha:.tree},parents:[.parents[]|{sha:.}],message:.message}' "$payload" >"$commit_state"
  respond 201 Created 0 "$(jq -cn --arg sha "$new_commit" '{sha:$sha}')"
fi
if [[ $method == POST && $endpoint == repos/example/env-vault/git/refs && $input == - ]]; then
  cat >"$payload_dir/ref-request.json"
  case $mode in
    ref-ambiguous) printf '%s\n' "$new_commit" >"$ref_state"; exit 1 ;;
    ref-race) printf '%s\n' "$new_commit" >"$ref_state"; respond 422 'Unprocessable Entity' 1 '{"message":"Reference already exists"}' ;;
    ref-conflict) printf '%s\n' "$conflict_commit" >"$ref_state"; respond 422 'Unprocessable Entity' 1 '{"message":"Reference already exists"}' ;;
    *) printf '%s\n' "$new_commit" >"$ref_state"; body=$(jq -cn --arg ref "$full_ref" --arg sha "$new_commit" '{ref:$ref,object:{type:"commit",sha:$sha}}'); respond 201 Created 0 "$body" ;;
  esac
fi
if [[ $method == PATCH && $endpoint == repos/example/env-vault/git/refs/heads/release-evidence && $input == - ]]; then
  cat >"$payload_dir/update-ref-request.json"
  case $mode in
    legacy-update-ambiguous) printf '%s\n' "$new_commit" >"$ref_state"; exit 1 ;;
	anchored-update-ambiguous) printf '%s\n' "$new_commit" >"$ref_state"; exit 1 ;;
    legacy-update-race) printf '%s\n' "$new_commit" >"$ref_state"; respond 422 'Unprocessable Entity' 1 '{"message":"Update is not a fast forward"}' ;;
    legacy-update-conflict) printf '%s\n' "$conflict_commit" >"$ref_state"; respond 422 'Unprocessable Entity' 1 '{"message":"Update is not a fast forward"}' ;;
    *) printf '%s\n' "$new_commit" >"$ref_state"; body=$(jq -cn --arg ref "$full_ref" --arg sha "$new_commit" '{ref:$ref,object:{type:"commit",sha:$sha}}'); respond 200 OK 0 "$body" ;;
  esac
fi
respond 404 'Not Found' 1 '{"message":"Not Found"}'
`
	writeExecutable(t, path, script)
}

func assertEvidenceV2BinaryRoundTrip(t *testing.T, payloadDirectory, remoteDirectory string, expected []byte) {
	t.Helper()
	expectedBase64 := base64.StdEncoding.EncodeToString(expected)
	found := false
	entries, err := os.ReadDir(payloadDirectory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "blob-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(payloadDirectory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Encoding string `json:"encoding"`
			Content  string `json:"content"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Encoding == "base64" && payload.Content == expectedBase64 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no mutation payload retained exact binary base64 %q", expectedBase64)
	}
	sha := gitBlobSHAForV2(expected)
	remote, err := os.ReadFile(filepath.Join(remoteDirectory, "blob-"+sha))
	if err != nil || !bytes.Equal(remote, expected) {
		t.Fatalf("remote binary roundtrip mismatch: size=%d err=%v", len(remote), err)
	}
}

func assertEvidenceV2ParentlessRequests(t *testing.T, payloadDirectory string) {
	t.Helper()
	treeData, err := os.ReadFile(filepath.Join(payloadDirectory, "tree-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]json.RawMessage
	if err := json.Unmarshal(treeData, &tree); err != nil {
		t.Fatal(err)
	}
	if _, inherited := tree["base_tree"]; inherited {
		t.Fatal("parentless genesis tree inherited a source/base tree")
	}
	var request struct {
		Tree []evidenceV2TreeEntry `json:"tree"`
	}
	if err := json.Unmarshal(treeData, &request); err != nil {
		t.Fatal(err)
	}
	for _, entry := range request.Tree {
		if !strings.HasPrefix(entry.Path, "evidence/") {
			t.Fatalf("parentless genesis request inherited source path %q", entry.Path)
		}
	}
	commitData, err := os.ReadFile(filepath.Join(payloadDirectory, "commit-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var commit struct {
		Parents []string `json:"parents"`
	}
	if err := json.Unmarshal(commitData, &commit); err != nil || len(commit.Parents) != 0 {
		t.Fatalf("genesis commit parents=%v error=%v", commit.Parents, err)
	}
}

func assertGenesisMutationSourceOrder(t *testing.T, calls string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(calls), "\n")
	const sourceRead = "GET repos/example/env-vault/git/commits/" + evidenceSourceSHA
	for index, line := range lines {
		if !strings.HasPrefix(line, "POST repos/example/env-vault/git/") {
			continue
		}
		if index == 0 || lines[index-1] != sourceRead {
			t.Fatalf("genesis mutation %q was not immediately preceded by a fresh exact source read:\n%s", line, calls)
		}
	}
}

func gitBlobSHAForV2(content []byte) string {
	object := append([]byte(fmt.Sprintf("blob %d\x00", len(content))), content...)
	digest := sha1.Sum(object)
	return hex.EncodeToString(digest[:])
}

func readIntFile(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	var value int
	if _, err := fmt.Sscanf(string(data), "%d", &value); err != nil {
		t.Fatal(err)
	}
	return value
}
