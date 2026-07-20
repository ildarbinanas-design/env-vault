package actionsartifact

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SnapshotSchemaID      = "env-vault.actions-artifact-snapshot.v1"
	SnapshotSchemaVersion = 1
	MaxRepositories       = 8
	MaxPagesPerResource   = 100
	MaxItemsPerPage       = 100
	MaxRunAttempts        = 5
	MaxSnapshotAge        = time.Hour
	collectionTimeLimit   = 30 * time.Minute
	attemptCreatedAtSkew  = 2 * time.Second
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	sha256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	dynamicPath       = regexp.MustCompile(`^dynamic/[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`)
)

// Snapshot is the strict, canonical offline projection of a complete raw
// collection. Artifacts are already bound to one reviewed lineage and exact
// producer attempt, but no keep/delete decision is present.
type Snapshot struct {
	SchemaID           string               `json:"schema_id"`
	SchemaVersion      int                  `json:"schema_version"`
	ObservedStartedAt  string               `json:"observed_started_at"`
	ObservedFinishedAt string               `json:"observed_finished_at"`
	Repositories       []SnapshotRepository `json:"repositories"`
	Artifacts          []SnapshotArtifact   `json:"artifacts"`
	Runs               []SnapshotRun        `json:"runs"`
	Attempts           []SnapshotAttempt    `json:"attempts"`
	ArtifactCount      int                  `json:"artifact_count"`
	ArtifactBytes      int64                `json:"artifact_bytes"`
	RunCount           int                  `json:"run_count"`
	AttemptCount       int                  `json:"attempt_count"`
}

type SnapshotRepository struct {
	Repository       string          `json:"repository"`
	RepositoryID     int64           `json:"repository_id"`
	Artifacts        PaginationProof `json:"artifacts"`
	Runs             PaginationProof `json:"runs"`
	AttemptDocuments int             `json:"attempt_documents"`
	ArtifactCount    int             `json:"artifact_count"`
	ArtifactBytes    int64           `json:"artifact_bytes"`
	RunCount         int             `json:"run_count"`
	AttemptCount     int             `json:"attempt_count"`
}

type PaginationProof struct {
	TotalCount      int      `json:"total_count"`
	PageCount       int      `json:"page_count"`
	PageItemCounts  []int    `json:"page_item_counts"`
	PageSHA256      []string `json:"page_sha256"`
	FinalPageSHA256 []string `json:"final_page_sha256"`
}

type SnapshotArtifact struct {
	Repository          string `json:"repository"`
	ArtifactID          int64  `json:"artifact_id"`
	Name                string `json:"name"`
	Digest              string `json:"digest"`
	SizeInBytes         int64  `json:"size_in_bytes"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
	ExpiresAt           string `json:"expires_at"`
	Expired             bool   `json:"expired"`
	ProducerRunID       int64  `json:"producer_run_id"`
	ProducerRunAttempt  int    `json:"producer_run_attempt"`
	WorkflowPath        string `json:"workflow_path"`
	HeadSHA             string `json:"head_sha"`
	HeadBranch          string `json:"head_branch"`
	HeadRepositoryID    int64  `json:"head_repository_id"`
	ReferencedVersion   string `json:"referenced_version"`
	ReferencedSourceSHA string `json:"referenced_source_sha"`
	PolicyPattern       string `json:"policy_pattern"`
	Class               string `json:"class"`
	Lifecycle           string `json:"lifecycle"`
	DependencyRationale string `json:"dependency_repair_rationale"`
}

type SnapshotRun struct {
	Repository       string `json:"repository"`
	RunID            int64  `json:"run_id"`
	CurrentAttempt   int    `json:"current_attempt"`
	WorkflowPath     string `json:"workflow_path"`
	HeadSHA          string `json:"head_sha"`
	HeadBranch       string `json:"head_branch"`
	HeadRepository   string `json:"head_repository"`
	HeadRepositoryID int64  `json:"head_repository_id"`
	Event            string `json:"event"`
	Status           string `json:"status"`
	Conclusion       string `json:"conclusion"`
	CreatedAt        string `json:"created_at"`
	RunStartedAt     string `json:"run_started_at"`
	UpdatedAt        string `json:"updated_at"`
}

type SnapshotAttempt struct {
	Repository       string `json:"repository"`
	RunID            int64  `json:"run_id"`
	RunAttempt       int    `json:"run_attempt"`
	WorkflowPath     string `json:"workflow_path"`
	HeadSHA          string `json:"head_sha"`
	HeadBranch       string `json:"head_branch"`
	HeadRepository   string `json:"head_repository"`
	HeadRepositoryID int64  `json:"head_repository_id"`
	Event            string `json:"event"`
	Status           string `json:"status"`
	Conclusion       string `json:"conclusion"`
	CreatedAt        string `json:"created_at"`
	RunStartedAt     string `json:"run_started_at"`
	UpdatedAt        string `json:"updated_at"`
}

// ValidateSnapshot replays every join, lineage match, attempt interval, and
// total. now and maxAge are explicit so offline freshness is deterministic.
func ValidateSnapshot(snapshot Snapshot, policy Policy, now time.Time, maxAge time.Duration) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if snapshot.SchemaID != SnapshotSchemaID || snapshot.SchemaVersion != SnapshotSchemaVersion {
		return fmt.Errorf("snapshot must be %s version %d", SnapshotSchemaID, SnapshotSchemaVersion)
	}
	started, err := parseCanonicalTime(snapshot.ObservedStartedAt)
	if err != nil {
		return fmt.Errorf("observed_started_at: %w", err)
	}
	finished, err := parseCanonicalTime(snapshot.ObservedFinishedAt)
	if err != nil {
		return fmt.Errorf("observed_finished_at: %w", err)
	}
	if finished.Before(started) || finished.Sub(started) > collectionTimeLimit {
		return errors.New("snapshot collection interval is invalid or exceeds 30 minutes")
	}
	if err := validateFreshnessWindow(now, maxAge); err != nil {
		return err
	}
	if now.Before(finished) || now.Sub(finished) > maxAge {
		return errors.New("snapshot is stale or validation time is invalid")
	}
	if len(snapshot.Repositories) == 0 || len(snapshot.Repositories) > MaxRepositories {
		return fmt.Errorf("snapshot repositories must contain 1..%d entries", MaxRepositories)
	}

	repositories := make(map[string]SnapshotRepository, len(snapshot.Repositories))
	repositoryIDs := make(map[int64]bool, len(snapshot.Repositories))
	for index, repository := range snapshot.Repositories {
		if !repositoryPattern.MatchString(repository.Repository) || repository.RepositoryID < 1 {
			return fmt.Errorf("repositories[%d] has invalid identity", index)
		}
		if index > 0 && snapshot.Repositories[index-1].Repository >= repository.Repository {
			return errors.New("repositories must be sorted and unique")
		}
		if repositoryIDs[repository.RepositoryID] {
			return fmt.Errorf("duplicate repository_id %d", repository.RepositoryID)
		}
		repositoryIDs[repository.RepositoryID] = true
		if err := validatePaginationProof(repository.Artifacts); err != nil {
			return fmt.Errorf("repository %q artifact pagination: %w", repository.Repository, err)
		}
		if err := validatePaginationProof(repository.Runs); err != nil {
			return fmt.Errorf("repository %q run pagination: %w", repository.Repository, err)
		}
		if repository.AttemptDocuments < 0 || repository.ArtifactCount < 0 || repository.ArtifactBytes < 0 || repository.RunCount < 0 || repository.AttemptCount < 0 {
			return fmt.Errorf("repository %q has negative totals", repository.Repository)
		}
		repositories[repository.Repository] = repository
	}

	runs := make(map[string]SnapshotRun, len(snapshot.Runs))
	runIDs := make(map[int64]bool, len(snapshot.Runs))
	runCounts := make(map[string]int, len(repositories))
	for index, run := range snapshot.Runs {
		if _, ok := repositories[run.Repository]; !ok || run.RunID < 1 || run.CurrentAttempt < 1 || run.CurrentAttempt > MaxRunAttempts {
			return fmt.Errorf("runs[%d] has invalid identity", index)
		}
		if index > 0 && compareRunIdentity(snapshot.Runs[index-1].Repository, snapshot.Runs[index-1].RunID, run.Repository, run.RunID) >= 0 {
			return errors.New("runs must be sorted and unique by repository/run_id")
		}
		if runIDs[run.RunID] {
			return fmt.Errorf("duplicate global run_id %d", run.RunID)
		}
		runIDs[run.RunID] = true
		if err := validateWorkflowPath(run.WorkflowPath); err != nil || !shaPattern.MatchString(run.HeadSHA) || strings.TrimSpace(run.Event) == "" || !repositoryPattern.MatchString(run.HeadRepository) || run.HeadRepositoryID < 1 {
			return fmt.Errorf("runs[%d] has invalid workflow/head/event identity", index)
		}
		if run.Status != "completed" || strings.TrimSpace(run.Conclusion) == "" {
			return fmt.Errorf("active or incomplete run %s/%d status=%q conclusion=%q", run.Repository, run.RunID, run.Status, run.Conclusion)
		}
		created, start, updated, err := validateRunTimes(run.CreatedAt, run.RunStartedAt, run.UpdatedAt)
		if err != nil || start.Before(created) || updated.Before(start) {
			return fmt.Errorf("runs[%d] has invalid timestamps", index)
		}
		key := producerKey(run.Repository, run.RunID)
		runs[key] = run
		runCounts[run.Repository]++
	}

	attempts := make(map[string]SnapshotAttempt, len(snapshot.Attempts))
	attemptsByRun := make(map[string][]SnapshotAttempt)
	attemptCounts := make(map[string]int, len(repositories))
	for index, attempt := range snapshot.Attempts {
		run, ok := runs[producerKey(attempt.Repository, attempt.RunID)]
		if !ok || attempt.RunAttempt < 1 || attempt.RunAttempt > run.CurrentAttempt {
			return fmt.Errorf("attempts[%d] has no matching current run", index)
		}
		if index > 0 && compareAttemptIdentity(snapshot.Attempts[index-1], attempt) >= 0 {
			return errors.New("attempts must be sorted and unique by repository/run_id/attempt")
		}
		if attempt.WorkflowPath != run.WorkflowPath || attempt.HeadSHA != run.HeadSHA || attempt.HeadBranch != run.HeadBranch || attempt.HeadRepository != run.HeadRepository || attempt.HeadRepositoryID != run.HeadRepositoryID || attempt.Event != run.Event {
			return fmt.Errorf("attempts[%d] path/head/ref/event mismatch", index)
		}
		if attempt.Status != "completed" || strings.TrimSpace(attempt.Conclusion) == "" {
			return fmt.Errorf("attempts[%d] is active or incomplete", index)
		}
		created, start, updated, err := validateRunTimes(attempt.CreatedAt, attempt.RunStartedAt, attempt.UpdatedAt)
		if err != nil || created.After(start.Add(attemptCreatedAtSkew)) || updated.Before(start) || updated.Before(created) {
			return fmt.Errorf("attempts[%d] has invalid timestamps", index)
		}
		key := attemptKey(attempt.Repository, attempt.RunID, attempt.RunAttempt)
		attempts[key] = attempt
		runKey := producerKey(attempt.Repository, attempt.RunID)
		attemptsByRun[runKey] = append(attemptsByRun[runKey], attempt)
		attemptCounts[attempt.Repository]++
	}
	for runKey, values := range attemptsByRun {
		for index := 1; index < len(values); index++ {
			previousEnd, _ := parseCanonicalTime(values[index-1].UpdatedAt)
			currentStart, _ := parseCanonicalTime(values[index].RunStartedAt)
			if !previousEnd.Before(currentStart) {
				return fmt.Errorf("run %q has overlapping or unordered attempt intervals", runKey)
			}
		}
	}

	artifactCounts := make(map[string]int, len(repositories))
	artifactIDs := make(map[int64]bool, len(snapshot.Artifacts))
	artifactBytes := make(map[string]int64, len(repositories))
	producerRuns := make(map[string]bool)
	var totalBytes int64
	for index, artifact := range snapshot.Artifacts {
		if _, ok := repositories[artifact.Repository]; !ok || artifact.ArtifactID < 1 || artifact.SizeInBytes < 0 {
			return fmt.Errorf("artifacts[%d] has invalid repository/id/size", index)
		}
		if index > 0 && compareRunIdentity(snapshot.Artifacts[index-1].Repository, snapshot.Artifacts[index-1].ArtifactID, artifact.Repository, artifact.ArtifactID) >= 0 {
			return errors.New("artifacts must be sorted and unique by repository/artifact_id")
		}
		if artifactIDs[artifact.ArtifactID] {
			return fmt.Errorf("duplicate global artifact_id %d", artifact.ArtifactID)
		}
		artifactIDs[artifact.ArtifactID] = true
		if strings.TrimSpace(artifact.Name) != artifact.Name || artifact.Name == "" || len(artifact.Name) > 512 || !digestPattern.MatchString(artifact.Digest) {
			return fmt.Errorf("artifacts[%d] has invalid name or digest", index)
		}
		runKey := producerKey(artifact.Repository, artifact.ProducerRunID)
		run, ok := runs[runKey]
		if !ok || artifact.WorkflowPath != run.WorkflowPath || artifact.HeadSHA != run.HeadSHA || artifact.HeadBranch != run.HeadBranch || artifact.HeadRepositoryID != run.HeadRepositoryID {
			return fmt.Errorf("artifacts[%d] producer run/path/head mismatch", index)
		}
		attempt, ok := attempts[attemptKey(artifact.Repository, artifact.ProducerRunID, artifact.ProducerRunAttempt)]
		if !ok {
			return fmt.Errorf("artifacts[%d] is missing exact producer attempt", index)
		}
		created, err := parseCanonicalTime(artifact.CreatedAt)
		if err != nil {
			return fmt.Errorf("artifacts[%d] created_at: %w", index, err)
		}
		updated, err := parseCanonicalTime(artifact.UpdatedAt)
		if err != nil {
			return fmt.Errorf("artifacts[%d] updated_at: %w", index, err)
		}
		expires, err := parseCanonicalTime(artifact.ExpiresAt)
		if err != nil || updated.Before(created) || !expires.After(updated) {
			return fmt.Errorf("artifacts[%d] has invalid timestamp/expiry order", index)
		}
		if artifact.Expired != !expires.After(finished) {
			return fmt.Errorf("artifacts[%d] expired state disagrees with observation time", index)
		}
		match, err := matchNameValidated(policy, artifact.WorkflowPath, artifact.Name)
		if err != nil {
			return fmt.Errorf("artifacts[%d]: %w", index, err)
		}
		if artifact.PolicyPattern != match.Pattern.ID || artifact.Class != match.Pattern.Class || artifact.Lifecycle != match.Pattern.Lifecycle || artifact.DependencyRationale != match.Pattern.DependencyRepairRationale || artifact.ReferencedVersion != match.ReferencedVersion || artifact.ReferencedSourceSHA != match.ReferencedSourceSHA {
			return fmt.Errorf("artifacts[%d] policy lineage projection mismatch", index)
		}
		resolved, err := resolveAttemptByIntervals(attemptsByRun[runKey], created, updated)
		if err != nil {
			return fmt.Errorf("artifacts[%d]: %w", index, err)
		}
		if resolved != artifact.ProducerRunAttempt || (match.Attempt != 0 && match.Attempt != resolved) || attempt.RunAttempt != resolved {
			return fmt.Errorf("artifacts[%d] name/interval/producer attempt mismatch", index)
		}
		producerRuns[runKey] = true
		artifactCounts[artifact.Repository]++
		if artifactBytes[artifact.Repository] > math.MaxInt64-artifact.SizeInBytes || totalBytes > math.MaxInt64-artifact.SizeInBytes {
			return errors.New("artifact byte total overflow")
		}
		artifactBytes[artifact.Repository] += artifact.SizeInBytes
		totalBytes += artifact.SizeInBytes
	}

	for runKey := range producerRuns {
		run := runs[runKey]
		values := attemptsByRun[runKey]
		if len(values) != run.CurrentAttempt {
			return fmt.Errorf("producer run %q has %d attempt documents, want %d", runKey, len(values), run.CurrentAttempt)
		}
		for attempt := 1; attempt <= run.CurrentAttempt; attempt++ {
			if _, ok := attempts[attemptKey(run.Repository, run.RunID, attempt)]; !ok {
				return fmt.Errorf("producer run %q is missing attempt %d", runKey, attempt)
			}
		}
	}

	for _, repository := range snapshot.Repositories {
		if repository.Artifacts.TotalCount != artifactCounts[repository.Repository] || repository.ArtifactCount != artifactCounts[repository.Repository] || repository.ArtifactBytes != artifactBytes[repository.Repository] {
			return fmt.Errorf("repository %q artifact totals do not reconcile", repository.Repository)
		}
		if repository.Runs.TotalCount != runCounts[repository.Repository] || repository.RunCount != runCounts[repository.Repository] {
			return fmt.Errorf("repository %q run totals do not reconcile", repository.Repository)
		}
		if repository.AttemptDocuments != attemptCounts[repository.Repository] || repository.AttemptCount != attemptCounts[repository.Repository] {
			return fmt.Errorf("repository %q attempt totals do not reconcile", repository.Repository)
		}
	}
	if snapshot.ArtifactCount != len(snapshot.Artifacts) || snapshot.ArtifactBytes != totalBytes || snapshot.RunCount != len(snapshot.Runs) || snapshot.AttemptCount != len(snapshot.Attempts) {
		return errors.New("snapshot aggregate totals do not reconcile")
	}
	return nil
}

func validateFreshnessWindow(now time.Time, maxAge time.Duration) error {
	if maxAge <= 0 || maxAge > MaxSnapshotAge || now.Location() != time.UTC {
		return fmt.Errorf("freshness window must be positive, at most %s, and use an explicit UTC validation time", MaxSnapshotAge)
	}
	return nil
}

func validatePaginationProof(proof PaginationProof) error {
	maximumItems := MaxPagesPerResource * MaxItemsPerPage
	if proof.TotalCount < 0 || proof.TotalCount > maximumItems || proof.PageCount < 1 || proof.PageCount > MaxPagesPerResource || len(proof.PageItemCounts) != proof.PageCount || len(proof.PageSHA256) != proof.PageCount || len(proof.FinalPageSHA256) != proof.PageCount {
		return errors.New("pagination proof shape, bound, or final reread is invalid")
	}
	wantPages := 1
	if proof.TotalCount > 0 {
		wantPages = (proof.TotalCount-1)/MaxItemsPerPage + 1
	}
	if proof.PageCount != wantPages {
		return fmt.Errorf("page_count=%d want=%d", proof.PageCount, wantPages)
	}
	remaining := proof.TotalCount
	for index, count := range proof.PageItemCounts {
		if !sha256Pattern.MatchString(proof.PageSHA256[index]) || proof.PageSHA256[index] != proof.FinalPageSHA256[index] {
			return fmt.Errorf("page %d final reread digest is invalid or drifted", index+1)
		}
		want := MaxItemsPerPage
		if remaining < want {
			want = remaining
		}
		if proof.TotalCount == 0 {
			want = 0
		}
		if count != want {
			return fmt.Errorf("page %d item count=%d want=%d", index+1, count, want)
		}
		remaining -= count
	}
	if remaining != 0 {
		return errors.New("pagination item counts are incomplete")
	}
	return nil
}

func resolveAttemptByIntervals(attempts []SnapshotAttempt, created, updated time.Time) (int, error) {
	resolved := 0
	for _, attempt := range attempts {
		start, err := parseCanonicalTime(attempt.RunStartedAt)
		if err != nil {
			return 0, err
		}
		end, err := parseCanonicalTime(attempt.UpdatedAt)
		if err != nil {
			return 0, err
		}
		if !created.Before(start) && !updated.After(end) {
			if resolved != 0 {
				return 0, errors.New("artifact timestamps match multiple producer attempt intervals")
			}
			resolved = attempt.RunAttempt
		}
	}
	if resolved == 0 {
		return 0, errors.New("artifact timestamps match no producer attempt interval")
	}
	return resolved, nil
}

func validateWorkflowPath(path string) error {
	if workflowPathPattern.MatchString(path) || dynamicPath.MatchString(path) {
		return nil
	}
	return fmt.Errorf("invalid workflow path %q", path)
}

func validateRunTimes(createdAt, runStartedAt, updatedAt string) (time.Time, time.Time, time.Time, error) {
	created, err := parseCanonicalTime(createdAt)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, err
	}
	started, err := parseCanonicalTime(runStartedAt)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, err
	}
	updated, err := parseCanonicalTime(updatedAt)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, err
	}
	return created, started, updated, nil
}

func parseCanonicalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("timestamp %q is not canonical UTC RFC3339", value)
	}
	return parsed, nil
}

func producerKey(repository string, runID int64) string {
	return repository + "\x00" + fmt.Sprint(runID)
}

func attemptKey(repository string, runID int64, attempt int) string {
	return producerKey(repository, runID) + "\x00" + fmt.Sprint(attempt)
}

func compareRunIdentity(leftRepository string, leftID int64, rightRepository string, rightID int64) int {
	if leftRepository != rightRepository {
		return strings.Compare(leftRepository, rightRepository)
	}
	if leftID < rightID {
		return -1
	}
	if leftID > rightID {
		return 1
	}
	return 0
}

func compareAttemptIdentity(left, right SnapshotAttempt) int {
	if compared := compareRunIdentity(left.Repository, left.RunID, right.Repository, right.RunID); compared != 0 {
		return compared
	}
	return left.RunAttempt - right.RunAttempt
}

func sortSnapshot(snapshot *Snapshot) {
	sort.Slice(snapshot.Repositories, func(i, j int) bool { return snapshot.Repositories[i].Repository < snapshot.Repositories[j].Repository })
	sort.Slice(snapshot.Artifacts, func(i, j int) bool {
		return compareRunIdentity(snapshot.Artifacts[i].Repository, snapshot.Artifacts[i].ArtifactID, snapshot.Artifacts[j].Repository, snapshot.Artifacts[j].ArtifactID) < 0
	})
	sort.Slice(snapshot.Runs, func(i, j int) bool {
		return compareRunIdentity(snapshot.Runs[i].Repository, snapshot.Runs[i].RunID, snapshot.Runs[j].Repository, snapshot.Runs[j].RunID) < 0
	})
	sort.Slice(snapshot.Attempts, func(i, j int) bool { return compareAttemptIdentity(snapshot.Attempts[i], snapshot.Attempts[j]) < 0 })
}
