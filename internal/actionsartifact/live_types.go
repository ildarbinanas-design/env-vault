package actionsartifact

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	LiveCollectionSchemaID       = "env-vault.actions-artifact-live-collection.v1"
	LiveCollectionSchemaVersion  = 1
	LiveObservationSchemaID      = "env-vault.actions-artifact-live-observation.v1"
	LiveObservationSchemaVersion = 1
	RepairProofSchemaID          = "env-vault.actions-artifact-repair-proof.v1"
	RepairProofSchemaVersion     = 1

	StableReleasePresent = "present"
	StableReleaseAbsent  = "absent"

	MaxLiveCollectionBytes = 192 << 20
	MaxTagPeelDepth        = 16
)

// ValidateLiveCollectionByteBudget binds the canonical collection index and
// all indexed raw files to one aggregate byte ceiling.
func ValidateLiveCollectionByteBudget(rawBytes, indexBytes int64) error {
	if rawBytes < 0 || indexBytes < 1 || indexBytes > MaxRawPageBytes || rawBytes > MaxLiveCollectionBytes-indexBytes {
		return errors.New("live collection including its index exceeds its byte budget")
	}
	return nil
}

// LiveRawCollection is the no-clobber index published by the checked live
// collector only after one global final reread. Files binds every saved vendor
// response (and an explicit typed 404 proof when latest stable is absent) by
// relative path, exact byte count, and SHA-256.
type LiveRawCollection struct {
	SchemaID               string                        `json:"schema_id"`
	SchemaVersion          int                           `json:"schema_version"`
	ObservedStartedAt      string                        `json:"observed_started_at"`
	ObservedFinishedAt     string                        `json:"observed_finished_at"`
	SnapshotSemanticSHA256 string                        `json:"snapshot_semantic_sha256"`
	ReleaseRepository      string                        `json:"release_repository"`
	Repositories           []LiveRawCollectionRepository `json:"repositories"`
	Files                  []RawFileProof                `json:"files"`
}

type LiveRawCollectionRepository struct {
	Repository         string                     `json:"repository"`
	RepositoryID       int64                      `json:"repository_id"`
	Directory          string                     `json:"directory"`
	DefaultBranch      string                     `json:"default_branch"`
	PullRequests       ArrayPaginationProof       `json:"pull_requests"`
	Runs               PaginationProof            `json:"runs"`
	PullRequestNumbers []int64                    `json:"pull_request_numbers"`
	AttemptDocuments   []CollectedAttemptDocument `json:"attempt_documents"`
	StableRelease      RawStableReleaseCollection `json:"stable_release"`
}

type RawStableReleaseCollection struct {
	Designated         bool   `json:"designated"`
	State              string `json:"state"`
	Version            string `json:"version"`
	TagObjectDocuments int    `json:"tag_object_documents"`
}

type RawFileProof struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// ArrayPaginationProof is used for REST arrays, such as open pull requests,
// which do not carry total_count. Completeness requires a final page shorter
// than per_page; therefore exactly 100 items uses two pages (100, 0).
type ArrayPaginationProof struct {
	ItemCount       int      `json:"item_count"`
	PageCount       int      `json:"page_count"`
	PageItemCounts  []int    `json:"page_item_counts"`
	PageSHA256      []string `json:"page_sha256"`
	FinalPageSHA256 []string `json:"final_page_sha256"`
}

type LiveObservation struct {
	SchemaID                    string                      `json:"schema_id"`
	SchemaVersion               int                         `json:"schema_version"`
	ObservedStartedAt           string                      `json:"observed_started_at"`
	ObservedFinishedAt          string                      `json:"observed_finished_at"`
	SnapshotSemanticSHA256      string                      `json:"snapshot_semantic_sha256"`
	RawCollectionSemanticSHA256 string                      `json:"raw_collection_semantic_sha256"`
	ReleaseRepository           string                      `json:"release_repository"`
	Repositories                []LiveRepositoryObservation `json:"repositories"`
	OperationalContract         OperationalContractProof    `json:"operational_contract"`
	SemanticSHA256              string                      `json:"semantic_sha256"`
}

type LiveRepositoryObservation struct {
	Repository                string            `json:"repository"`
	RepositoryID              int64             `json:"repository_id"`
	DefaultBranch             string            `json:"default_branch"`
	ProtectedDefaultBranchSHA string            `json:"protected_default_branch_sha"`
	OpenPullRequests          []LivePullRequest `json:"open_pull_requests"`
	Runs                      []SnapshotRun     `json:"runs"`
	Attempts                  []SnapshotAttempt `json:"attempts"`
	StableRelease             LiveStableRelease `json:"stable_release"`
}

type LivePullRequest struct {
	Number  int64  `json:"number"`
	Draft   bool   `json:"draft"`
	BaseRef string `json:"base_ref"`
	HeadRef string `json:"head_ref"`
	HeadSHA string `json:"head_sha"`
}

type LiveStableRelease struct {
	Designated       bool                       `json:"designated"`
	Enabled          bool                       `json:"enabled"`
	Absence          *StableReleaseAbsenceProof `json:"absence"`
	ReleaseID        int64                      `json:"release_id"`
	Version          string                     `json:"version"`
	SourceSHA        string                     `json:"source_sha"`
	TargetCommitish  string                     `json:"target_commitish"`
	PublishedAt      string                     `json:"published_at"`
	TagRefObjectType string                     `json:"tag_ref_object_type"`
	TagRefObjectSHA  string                     `json:"tag_ref_object_sha"`
	TagPeelDepth     int                        `json:"tag_peel_depth"`
	Assets           []ReleaseAssetProjection   `json:"assets"`
}

type StableReleaseAbsenceProof struct {
	SchemaID      string `json:"schema_id"`
	SchemaVersion int    `json:"schema_version"`
	Repository    string `json:"repository"`
	Endpoint      string `json:"endpoint"`
	ReasonCode    string `json:"reason_code"`
	TransportExit int    `json:"transport_exit"`
}

type ReleaseAssetProjection struct {
	ID                 int64   `json:"id"`
	NodeID             string  `json:"node_id"`
	Name               string  `json:"name"`
	Label              *string `json:"label"`
	UploaderID         int64   `json:"uploader_id"`
	UploaderLogin      string  `json:"uploader_login"`
	ContentType        string  `json:"content_type"`
	State              string  `json:"state"`
	Size               int64   `json:"size"`
	Digest             *string `json:"digest"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
	BrowserDownloadURL string  `json:"browser_download_url"`
}

type OperationalContractProof struct {
	Repository                  string   `json:"repository"`
	SourceSHA                   string   `json:"source_sha"`
	Path                        string   `json:"path"`
	BlobSHA                     string   `json:"blob_sha"`
	FileSHA256                  string   `json:"file_sha256"`
	SemanticSHA256              string   `json:"semantic_sha256"`
	RecoveryState               string   `json:"recovery_state"`
	SourceRepository            string   `json:"source_repository"`
	TapRepository               string   `json:"tap_repository"`
	ReleaseConcurrencyWorkflows []string `json:"release_concurrency_workflows"`
	AllowedRepairActionIDs      []string `json:"allowed_repair_action_ids"`
}

type RepairProof struct {
	SchemaID                      string              `json:"schema_id"`
	SchemaVersion                 int                 `json:"schema_version"`
	ObservedAt                    string              `json:"observed_at"`
	LiveObservationSemanticSHA256 string              `json:"live_observation_semantic_sha256"`
	ContractFileSHA256            string              `json:"contract_file_sha256"`
	ContractSemanticSHA256        string              `json:"contract_semantic_sha256"`
	ContractSourceSHA             string              `json:"contract_source_sha"`
	RecoveryState                 string              `json:"recovery_state"`
	Closed                        *bool               `json:"closed"`
	ActiveRunIdentities           []ExactKeepIdentity `json:"active_run_identities"`
	Identities                    []ExactKeepIdentity `json:"identities"`
	ReasonCode                    string              `json:"reason_code"`
	SemanticSHA256                string              `json:"semantic_sha256"`
}

func (collection LiveRawCollection) Validate() error {
	if collection.SchemaID != LiveCollectionSchemaID || collection.SchemaVersion != LiveCollectionSchemaVersion {
		return fmt.Errorf("live collection must be %s version %d", LiveCollectionSchemaID, LiveCollectionSchemaVersion)
	}
	started, err := parseCanonicalTime(collection.ObservedStartedAt)
	if err != nil {
		return fmt.Errorf("live collection observed_started_at: %w", err)
	}
	finished, err := parseCanonicalTime(collection.ObservedFinishedAt)
	if err != nil {
		return fmt.Errorf("live collection observed_finished_at: %w", err)
	}
	if finished.Before(started) || finished.Sub(started) > collectionTimeLimit {
		return errors.New("live collection interval is invalid or exceeds its limit")
	}
	if !sha256Pattern.MatchString(collection.SnapshotSemanticSHA256) || ValidateRepositoryName(collection.ReleaseRepository) != nil {
		return errors.New("live collection has invalid snapshot or release-repository identity")
	}
	if len(collection.Repositories) == 0 || len(collection.Repositories) > MaxRepositories {
		return fmt.Errorf("live collection repositories must contain 1..%d entries", MaxRepositories)
	}
	releaseSeen := false
	repositoryIDs := make(map[int64]bool, len(collection.Repositories))
	for index, repository := range collection.Repositories {
		if ValidateRepositoryName(repository.Repository) != nil || repository.RepositoryID < 1 || repository.Directory != fmt.Sprintf("repository-%03d", index+1) || !validRefName(repository.DefaultBranch) {
			return fmt.Errorf("repositories[%d] has invalid identity, directory, or default branch", index)
		}
		if index > 0 && collection.Repositories[index-1].Repository >= repository.Repository {
			return errors.New("live collection repositories must be sorted and unique")
		}
		if repositoryIDs[repository.RepositoryID] {
			return fmt.Errorf("duplicate live repository_id %d", repository.RepositoryID)
		}
		repositoryIDs[repository.RepositoryID] = true
		if err := validateArrayPaginationProof(repository.PullRequests); err != nil {
			return fmt.Errorf("repository %q pull-request pagination: %w", repository.Repository, err)
		}
		if err := validatePaginationProof(repository.Runs); err != nil {
			return fmt.Errorf("repository %q run pagination: %w", repository.Repository, err)
		}
		if repository.PullRequestNumbers == nil || len(repository.PullRequestNumbers) != repository.PullRequests.ItemCount {
			return fmt.Errorf("repository %q pull-request identities are omitted or incomplete", repository.Repository)
		}
		for prIndex, number := range repository.PullRequestNumbers {
			if number < 1 || (prIndex > 0 && repository.PullRequestNumbers[prIndex-1] >= number) {
				return fmt.Errorf("repository %q pull-request identities must be sorted, positive, and unique", repository.Repository)
			}
		}
		if repository.AttemptDocuments == nil {
			return fmt.Errorf("repository %q attempt_documents must be explicit", repository.Repository)
		}
		for attemptIndex, attempt := range repository.AttemptDocuments {
			if attempt.RunID < 1 || attempt.RunAttempt < 1 || attempt.RunAttempt > MaxRunAttempts || (attemptIndex > 0 && compareCollectedAttempt(repository.AttemptDocuments[attemptIndex-1], attempt) >= 0) {
				return fmt.Errorf("repository %q attempt_documents must be sorted, unique, and valid", repository.Repository)
			}
		}
		stable := repository.StableRelease
		if repository.Repository == collection.ReleaseRepository {
			releaseSeen = true
			if !stable.Designated || (stable.State != StableReleasePresent && stable.State != StableReleaseAbsent) {
				return fmt.Errorf("release repository %q has invalid stable-release collection state", repository.Repository)
			}
			if stable.State == StableReleasePresent {
				if !releaseVersionPattern.MatchString(stable.Version) || stable.TagObjectDocuments < 0 || stable.TagObjectDocuments > MaxTagPeelDepth {
					return fmt.Errorf("release repository %q has invalid stable-release identity", repository.Repository)
				}
			} else if stable.Version != "" || stable.TagObjectDocuments != 0 {
				return fmt.Errorf("release repository %q absence state contains a fabricated release tuple", repository.Repository)
			}
		} else if stable.Designated || stable.State != "" || stable.Version != "" || stable.TagObjectDocuments != 0 {
			return fmt.Errorf("non-release repository %q contains stable-release state", repository.Repository)
		}
	}
	if !releaseSeen {
		return errors.New("release repository is absent from the live collection")
	}
	if collection.Files == nil || len(collection.Files) == 0 {
		return errors.New("live collection raw file proof list must be explicit and nonempty")
	}
	var totalBytes int64
	for index, proof := range collection.Files {
		clean := filepath.ToSlash(filepath.Clean(proof.Path))
		if proof.Path == "" || clean != proof.Path || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") || clean == "." || proof.Bytes < 1 || proof.Bytes > MaxRawPageBytes || !sha256Pattern.MatchString(proof.SHA256) {
			return fmt.Errorf("files[%d] is invalid", index)
		}
		if index > 0 && collection.Files[index-1].Path >= proof.Path {
			return errors.New("live collection raw file proofs must be sorted and unique")
		}
		if totalBytes > MaxLiveCollectionBytes-proof.Bytes {
			return errors.New("live collection exceeds its byte budget")
		}
		totalBytes += proof.Bytes
	}
	return nil
}

func validateArrayPaginationProof(proof ArrayPaginationProof) error {
	if proof.ItemCount < 0 || proof.PageCount < 1 || proof.PageCount > MaxPagesPerResource || len(proof.PageItemCounts) != proof.PageCount || len(proof.PageSHA256) != proof.PageCount || len(proof.FinalPageSHA256) != proof.PageCount {
		return errors.New("array pagination proof shape or bound is invalid")
	}
	maximumItems := (MaxPagesPerResource-1)*MaxItemsPerPage + (MaxItemsPerPage - 1)
	if proof.ItemCount > maximumItems {
		return errors.New("array pagination item count exceeds the complete terminal-page bound")
	}
	total := 0
	for index, count := range proof.PageItemCounts {
		if count < 0 || count > MaxItemsPerPage || !sha256Pattern.MatchString(proof.PageSHA256[index]) || proof.PageSHA256[index] != proof.FinalPageSHA256[index] {
			return fmt.Errorf("array page %d has invalid count or final digest", index+1)
		}
		if index < proof.PageCount-1 && count != MaxItemsPerPage {
			return fmt.Errorf("array page %d is short before the terminal page", index+1)
		}
		if index == proof.PageCount-1 && count == MaxItemsPerPage {
			return errors.New("array pagination is incomplete without a short terminal page")
		}
		total += count
	}
	if total != proof.ItemCount {
		return errors.New("array pagination item count does not reconcile")
	}
	return nil
}

func validRefName(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= 255 && !strings.ContainsAny(value, "\x00\r\n?&#") && !strings.HasPrefix(value, "/") && !strings.HasSuffix(value, "/") && !strings.Contains(value, "..")
}

func sortLiveRawCollection(collection *LiveRawCollection) {
	sort.Slice(collection.Repositories, func(i, j int) bool {
		return collection.Repositories[i].Repository < collection.Repositories[j].Repository
	})
	for index := range collection.Repositories {
		collection.Repositories[index].Directory = fmt.Sprintf("repository-%03d", index+1)
		sort.Slice(collection.Repositories[index].PullRequestNumbers, func(i, j int) bool {
			return collection.Repositories[index].PullRequestNumbers[i] < collection.Repositories[index].PullRequestNumbers[j]
		})
		sort.Slice(collection.Repositories[index].AttemptDocuments, func(i, j int) bool {
			return compareCollectedAttempt(collection.Repositories[index].AttemptDocuments[i], collection.Repositories[index].AttemptDocuments[j]) < 0
		})
	}
	sort.Slice(collection.Files, func(i, j int) bool { return collection.Files[i].Path < collection.Files[j].Path })
}
