package actionsartifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

type rawArtifact struct {
	RepositoryID     int64
	HeadRepositoryID int64
	ArtifactID       int64
	Name             string
	Digest           string
	SizeInBytes      int64
	CreatedAt        string
	UpdatedAt        string
	ExpiresAt        string
	Expired          bool
	ProducerRunID    int64
	HeadSHA          string
	HeadBranch       string
}

// AssembleSnapshot treats the collector directory as untrusted input. It
// rejects unexpected files, re-reads and hashes every page, replays all joins,
// resolves attempt intervals, and returns only the canonical typed projection.
func AssembleSnapshot(collectionDirectory string, policy Policy) (Snapshot, error) {
	if err := policy.Validate(); err != nil {
		return Snapshot{}, err
	}
	root, err := validateCollectionRoot(collectionDirectory)
	if err != nil {
		return Snapshot{}, err
	}
	budget := &rawReadBudget{}
	collectionData, err := budget.read(filepath.Join(root, "collection.json"), MaxRawPageBytes)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read raw collection index: %w", err)
	}
	var collection RawCollection
	if err := strictjson.Decode(collectionData, MaxRawPageBytes, &collection); err != nil {
		return Snapshot{}, fmt.Errorf("decode raw collection index: %w", err)
	}
	if err := collection.Validate(); err != nil {
		return Snapshot{}, err
	}
	if err := requireDirectoryEntries(root, expectedRootEntries(collection)); err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		SchemaID: SnapshotSchemaID, SchemaVersion: SnapshotSchemaVersion,
		ObservedStartedAt: collection.ObservedStartedAt, ObservedFinishedAt: collection.ObservedFinishedAt,
	}
	for _, repository := range collection.Repositories {
		repositoryRoot := filepath.Join(root, repository.Directory)
		if err := requireDirectoryEntries(repositoryRoot, map[string]bool{
			"repository.json": false, "artifacts": true, "runs": true, "attempts": true,
		}); err != nil {
			return Snapshot{}, err
		}
		metadata, err := budget.read(filepath.Join(repositoryRoot, "repository.json"), MaxRawPageBytes)
		if err != nil {
			return Snapshot{}, fmt.Errorf("repository %q metadata: %w", repository.Repository, err)
		}
		metadataID, metadataName, err := parseRepositoryDocument(metadata)
		if err != nil || metadataID != repository.RepositoryID || metadataName != repository.Repository {
			return Snapshot{}, fmt.Errorf("repository %q metadata identity mismatch: %w", repository.Repository, err)
		}

		artifactMessages, err := readRawPages(budget, repositoryRoot, "artifacts", "artifacts", repository.Artifacts)
		if err != nil {
			return Snapshot{}, fmt.Errorf("repository %q artifacts: %w", repository.Repository, err)
		}
		runMessages, err := readRawPages(budget, repositoryRoot, "runs", "workflow_runs", repository.Runs)
		if err != nil {
			return Snapshot{}, fmt.Errorf("repository %q runs: %w", repository.Repository, err)
		}
		if err := requireDirectoryEntries(filepath.Join(repositoryRoot, "attempts"), expectedAttemptEntries(repository.AttemptDocuments)); err != nil {
			return Snapshot{}, fmt.Errorf("repository %q attempts: %w", repository.Repository, err)
		}

		repositoryRuns := make([]SnapshotRun, 0, len(runMessages))
		for index, message := range runMessages {
			run, rawRepositoryID, err := parseRunDocument(message, repository.Repository)
			if err != nil || rawRepositoryID != repository.RepositoryID {
				return Snapshot{}, fmt.Errorf("repository %q runs[%d] identity: %w", repository.Repository, index, err)
			}
			repositoryRuns = append(repositoryRuns, run)
		}
		snapshot.Runs = append(snapshot.Runs, repositoryRuns...)

		repositoryAttempts := make([]SnapshotAttempt, 0, len(repository.AttemptDocuments))
		for index, document := range repository.AttemptDocuments {
			filename := filepath.Join(repositoryRoot, "attempts", attemptFilename(document.RunID, document.RunAttempt))
			data, err := budget.read(filename, MaxRawPageBytes)
			if err != nil {
				return Snapshot{}, fmt.Errorf("repository %q attempt document %d: %w", repository.Repository, index, err)
			}
			attempt, rawRepositoryID, err := parseAttemptDocument(data, repository.Repository)
			if err != nil || rawRepositoryID != repository.RepositoryID || attempt.RunID != document.RunID || attempt.RunAttempt != document.RunAttempt {
				return Snapshot{}, fmt.Errorf("repository %q attempt document %d identity mismatch: %w", repository.Repository, index, err)
			}
			repositoryAttempts = append(repositoryAttempts, attempt)
		}
		snapshot.Attempts = append(snapshot.Attempts, repositoryAttempts...)

		runsByID := make(map[int64]SnapshotRun, len(repositoryRuns))
		for _, run := range repositoryRuns {
			runsByID[run.RunID] = run
		}
		attemptsByRun := make(map[int64][]SnapshotAttempt)
		for _, attempt := range repositoryAttempts {
			attemptsByRun[attempt.RunID] = append(attemptsByRun[attempt.RunID], attempt)
		}
		for runID := range attemptsByRun {
			sort.Slice(attemptsByRun[runID], func(i, j int) bool {
				return attemptsByRun[runID][i].RunAttempt < attemptsByRun[runID][j].RunAttempt
			})
		}

		var repositoryBytes int64
		for index, message := range artifactMessages {
			raw, err := parseArtifactDocument(message)
			if err != nil {
				return Snapshot{}, fmt.Errorf("repository %q artifacts[%d]: %w", repository.Repository, index, err)
			}
			if raw.SizeInBytes < 0 {
				return Snapshot{}, fmt.Errorf("repository %q artifacts[%d] has negative size", repository.Repository, index)
			}
			run, ok := runsByID[raw.ProducerRunID]
			if !ok || raw.RepositoryID != repository.RepositoryID || raw.HeadRepositoryID != run.HeadRepositoryID || raw.HeadSHA != run.HeadSHA || raw.HeadBranch != run.HeadBranch {
				return Snapshot{}, fmt.Errorf("repository %q artifacts[%d] producer repository/run/head mismatch", repository.Repository, index)
			}
			created, createdErr := parseCanonicalTime(raw.CreatedAt)
			updated, updatedErr := parseCanonicalTime(raw.UpdatedAt)
			if createdErr != nil || updatedErr != nil {
				return Snapshot{}, fmt.Errorf("repository %q artifacts[%d] has invalid timestamps", repository.Repository, index)
			}
			match, err := matchNameValidated(policy, run.WorkflowPath, raw.Name)
			if err != nil {
				return Snapshot{}, fmt.Errorf("repository %q artifacts[%d]: %w", repository.Repository, index, err)
			}
			resolvedAttempt, err := resolveAttemptByIntervals(attemptsByRun[run.RunID], created, updated)
			if err != nil || (match.Attempt != 0 && match.Attempt != resolvedAttempt) {
				return Snapshot{}, fmt.Errorf("repository %q artifacts[%d] attempt resolution: %w", repository.Repository, index, err)
			}
			snapshot.Artifacts = append(snapshot.Artifacts, SnapshotArtifact{
				Repository: repository.Repository, ArtifactID: raw.ArtifactID, Name: raw.Name, Digest: raw.Digest,
				SizeInBytes: raw.SizeInBytes, CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt, ExpiresAt: raw.ExpiresAt, Expired: raw.Expired,
				ProducerRunID: raw.ProducerRunID, ProducerRunAttempt: resolvedAttempt, WorkflowPath: run.WorkflowPath,
				HeadSHA: raw.HeadSHA, HeadBranch: raw.HeadBranch, HeadRepositoryID: raw.HeadRepositoryID,
				ReferencedVersion: match.ReferencedVersion, ReferencedSourceSHA: match.ReferencedSourceSHA,
				PolicyPattern: match.Pattern.ID, Class: match.Pattern.Class, Lifecycle: match.Pattern.Lifecycle,
				DependencyRationale: match.Pattern.DependencyRepairRationale,
			})
			if repositoryBytes > int64(^uint64(0)>>1)-raw.SizeInBytes {
				return Snapshot{}, errors.New("repository artifact byte total overflow")
			}
			repositoryBytes += raw.SizeInBytes
		}
		snapshot.Repositories = append(snapshot.Repositories, SnapshotRepository{
			Repository: repository.Repository, RepositoryID: repository.RepositoryID,
			Artifacts: repository.Artifacts, Runs: repository.Runs, AttemptDocuments: len(repository.AttemptDocuments),
			ArtifactCount: len(artifactMessages), ArtifactBytes: repositoryBytes,
			RunCount: len(runMessages), AttemptCount: len(repository.AttemptDocuments),
		})
	}
	sortSnapshot(&snapshot)
	snapshot.ArtifactCount = len(snapshot.Artifacts)
	snapshot.RunCount = len(snapshot.Runs)
	snapshot.AttemptCount = len(snapshot.Attempts)
	for _, repository := range snapshot.Repositories {
		if snapshot.ArtifactBytes > int64(^uint64(0)>>1)-repository.ArtifactBytes {
			return Snapshot{}, errors.New("snapshot artifact byte total overflow")
		}
		snapshot.ArtifactBytes += repository.ArtifactBytes
	}
	finished, _ := parseCanonicalTime(snapshot.ObservedFinishedAt)
	if err := ValidateSnapshot(snapshot, policy, finished, time.Nanosecond); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

type rawReadBudget struct {
	bytes int64
}

func (budget *rawReadBudget) read(filename string, limit int) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("input must be an existing regular non-symlink file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > limit {
		return nil, fmt.Errorf("file size %d is outside 1..%d", len(data), limit)
	}
	if budget.bytes > MaxRawCollectionBytes-int64(len(data)) {
		return nil, errors.New("raw collection exceeds byte budget")
	}
	budget.bytes += int64(len(data))
	return data, nil
}

func validateCollectionRoot(directory string) (string, error) {
	if directory == "" {
		return "", errors.New("raw collection directory is required")
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("raw collection must be an existing non-symlink directory")
	}
	return abs, nil
}

func expectedRootEntries(collection RawCollection) map[string]bool {
	expected := map[string]bool{"collection.json": false}
	for _, repository := range collection.Repositories {
		expected[repository.Directory] = true
	}
	return expected
}

func expectedAttemptEntries(documents []CollectedAttemptDocument) map[string]bool {
	expected := make(map[string]bool, len(documents))
	for _, document := range documents {
		expected[attemptFilename(document.RunID, document.RunAttempt)] = false
	}
	return expected
}

func attemptFilename(runID int64, attempt int) string {
	return fmt.Sprintf("run-%019d-attempt-%02d.json", runID, attempt)
}

func requireDirectoryEntries(directory string, expected map[string]bool) error {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%q must be an existing non-symlink directory", directory)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) != len(expected) {
		return fmt.Errorf("%q contains %d entries, want exactly %d", directory, len(entries), len(expected))
	}
	for _, entry := range entries {
		wantDirectory, ok := expected[entry.Name()]
		if !ok || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() != wantDirectory {
			return fmt.Errorf("%q contains unexpected or wrong-type entry %q", directory, entry.Name())
		}
		if !entry.IsDir() {
			entryInfo, err := entry.Info()
			if err != nil || !entryInfo.Mode().IsRegular() {
				return fmt.Errorf("%q entry %q is not a regular file", directory, entry.Name())
			}
		}
	}
	return nil
}

func readRawPages(budget *rawReadBudget, repositoryRoot, directory, arrayField string, proof PaginationProof) ([]json.RawMessage, error) {
	resourceRoot := filepath.Join(repositoryRoot, directory)
	expected := make(map[string]bool, proof.PageCount*2)
	for page := 1; page <= proof.PageCount; page++ {
		expected[fmt.Sprintf("page-%03d.json", page)] = false
		expected[fmt.Sprintf("page-%03d-final.json", page)] = false
	}
	if err := requireDirectoryEntries(resourceRoot, expected); err != nil {
		return nil, err
	}
	var all []json.RawMessage
	for page := 1; page <= proof.PageCount; page++ {
		data, err := budget.read(filepath.Join(resourceRoot, fmt.Sprintf("page-%03d.json", page)), MaxRawPageBytes)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != proof.PageSHA256[page-1] {
			return nil, fmt.Errorf("page %d digest does not match collection proof", page)
		}
		total, messages, err := parsePageEnvelope(data, arrayField)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		if total != proof.TotalCount || len(messages) != proof.PageItemCounts[page-1] {
			return nil, fmt.Errorf("page %d total_count or item count drift", page)
		}
		final, err := budget.read(filepath.Join(resourceRoot, fmt.Sprintf("page-%03d-final.json", page)), MaxRawPageBytes)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(data, final) {
			return nil, fmt.Errorf("page %d final reread differs byte-for-byte", page)
		}
		finalDigest := sha256.Sum256(final)
		if hex.EncodeToString(finalDigest[:]) != proof.FinalPageSHA256[page-1] {
			return nil, fmt.Errorf("page %d final reread digest does not match collection proof", page)
		}
		all = append(all, messages...)
	}
	return all, nil
}

func parsePageEnvelope(data []byte, arrayField string) (int, []json.RawMessage, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return 0, nil, err
	}
	var total int
	if err := decodeRequired(object, "total_count", &total); err != nil || total < 0 {
		return 0, nil, errors.New("page has invalid total_count")
	}
	var messages []json.RawMessage
	if err := decodeRequired(object, arrayField, &messages); err != nil || messages == nil || len(messages) > MaxItemsPerPage {
		return 0, nil, fmt.Errorf("page has invalid %s array", arrayField)
	}
	return total, messages, nil
}

func parseRepositoryDocument(data []byte) (int64, string, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return 0, "", err
	}
	var id int64
	var fullName string
	if err := decodeRequired(object, "id", &id); err != nil || id < 1 {
		return 0, "", errors.New("repository metadata has invalid id")
	}
	if err := decodeRequired(object, "full_name", &fullName); err != nil || !repositoryPattern.MatchString(fullName) {
		return 0, "", errors.New("repository metadata has invalid full_name")
	}
	return id, fullName, nil
}

func parseArtifactDocument(data []byte) (rawArtifact, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return rawArtifact{}, err
	}
	var artifact rawArtifact
	fields := []struct {
		name string
		dest any
	}{
		{"id", &artifact.ArtifactID}, {"name", &artifact.Name}, {"digest", &artifact.Digest},
		{"size_in_bytes", &artifact.SizeInBytes}, {"created_at", &artifact.CreatedAt}, {"updated_at", &artifact.UpdatedAt},
		{"expires_at", &artifact.ExpiresAt}, {"expired", &artifact.Expired},
	}
	for _, field := range fields {
		if err := decodeRequired(object, field.name, field.dest); err != nil {
			return rawArtifact{}, err
		}
	}
	var producer json.RawMessage
	if err := decodeRequired(object, "workflow_run", &producer); err != nil {
		return rawArtifact{}, err
	}
	producerObject, err := parseVendorObject(producer)
	if err != nil {
		return rawArtifact{}, err
	}
	producerFields := []struct {
		name string
		dest any
	}{
		{"id", &artifact.ProducerRunID}, {"repository_id", &artifact.RepositoryID}, {"head_repository_id", &artifact.HeadRepositoryID},
		{"head_sha", &artifact.HeadSHA}, {"head_branch", &artifact.HeadBranch},
	}
	for _, field := range producerFields {
		if err := decodeRequired(producerObject, field.name, field.dest); err != nil {
			return rawArtifact{}, err
		}
	}
	return artifact, nil
}

func parseRunDocument(data []byte, repository string) (SnapshotRun, int64, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return SnapshotRun{}, 0, err
	}
	var run SnapshotRun
	run.Repository = repository
	fields := []struct {
		name string
		dest any
	}{
		{"id", &run.RunID}, {"run_attempt", &run.CurrentAttempt}, {"path", &run.WorkflowPath},
		{"head_sha", &run.HeadSHA}, {"head_branch", &run.HeadBranch}, {"event", &run.Event},
		{"status", &run.Status}, {"conclusion", &run.Conclusion}, {"created_at", &run.CreatedAt},
		{"run_started_at", &run.RunStartedAt}, {"updated_at", &run.UpdatedAt},
	}
	for _, field := range fields {
		if err := decodeRequired(object, field.name, field.dest); err != nil {
			return SnapshotRun{}, 0, err
		}
	}
	repositoryID, repositoryName, err := parseNestedRepository(object, "repository")
	if err != nil || repositoryName != repository {
		return SnapshotRun{}, 0, errors.New("run repository mismatch")
	}
	run.HeadRepositoryID, run.HeadRepository, err = parseNestedRepository(object, "head_repository")
	if err != nil {
		return SnapshotRun{}, 0, err
	}
	return run, repositoryID, nil
}

func parseAttemptDocument(data []byte, repository string) (SnapshotAttempt, int64, error) {
	run, repositoryID, err := parseRunDocument(data, repository)
	if err != nil {
		return SnapshotAttempt{}, 0, err
	}
	return SnapshotAttempt{
		Repository: run.Repository, RunID: run.RunID, RunAttempt: run.CurrentAttempt,
		WorkflowPath: run.WorkflowPath, HeadSHA: run.HeadSHA, HeadBranch: run.HeadBranch,
		HeadRepository: run.HeadRepository, HeadRepositoryID: run.HeadRepositoryID,
		Event: run.Event, Status: run.Status, Conclusion: run.Conclusion,
		CreatedAt: run.CreatedAt, RunStartedAt: run.RunStartedAt, UpdatedAt: run.UpdatedAt,
	}, repositoryID, nil
}

func parseNestedRepository(object map[string]json.RawMessage, field string) (int64, string, error) {
	var raw json.RawMessage
	if err := decodeRequired(object, field, &raw); err != nil {
		return 0, "", err
	}
	nested, err := parseVendorObject(raw)
	if err != nil {
		return 0, "", err
	}
	var id int64
	var name string
	if err := decodeRequired(nested, "id", &id); err != nil || id < 1 {
		return 0, "", fmt.Errorf("%s has invalid id", field)
	}
	if err := decodeRequired(nested, "full_name", &name); err != nil || !repositoryPattern.MatchString(name) {
		return 0, "", fmt.Errorf("%s has invalid full_name", field)
	}
	return id, name, nil
}

func parseVendorObject(data []byte) (map[string]json.RawMessage, error) {
	if err := strictjson.Validate(data, MaxRawPageBytes); err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, errors.New("vendor JSON value must be an object")
	}
	return object, nil
}

func decodeRequired(object map[string]json.RawMessage, name string, destination any) error {
	raw, ok := object[name]
	if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("vendor JSON is missing non-null field %q", name)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("vendor JSON field %q: %w", name, err)
	}
	return nil
}

func sanitizeEndpointRepository(repository string) string {
	return strings.TrimSpace(repository)
}
