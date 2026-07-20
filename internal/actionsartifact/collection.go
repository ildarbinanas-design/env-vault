package actionsartifact

import (
	"errors"
	"fmt"
	"sort"
)

const (
	CollectionSchemaID      = "env-vault.actions-artifact-raw-collection.v1"
	CollectionSchemaVersion = 1
	MaxRawPageBytes         = 8 << 20
	MaxRawCollectionBytes   = 128 << 20
	RawResourceArtifacts    = "artifacts"
	RawResourceRuns         = "runs"
)

// RawCollection is the typed index published only after the collector has
// saved and checked every raw response. The offline assembler distrusts and
// replays every proof from the indexed files.
type RawCollection struct {
	SchemaID           string                    `json:"schema_id"`
	SchemaVersion      int                       `json:"schema_version"`
	ObservedStartedAt  string                    `json:"observed_started_at"`
	ObservedFinishedAt string                    `json:"observed_finished_at"`
	Repositories       []RawCollectionRepository `json:"repositories"`
}

type RawCollectionRepository struct {
	Repository       string                     `json:"repository"`
	RepositoryID     int64                      `json:"repository_id"`
	Directory        string                     `json:"directory"`
	Artifacts        PaginationProof            `json:"artifacts"`
	Runs             PaginationProof            `json:"runs"`
	AttemptDocuments []CollectedAttemptDocument `json:"attempt_documents"`
}

type CollectedAttemptDocument struct {
	RunID      int64 `json:"run_id"`
	RunAttempt int   `json:"run_attempt"`
}

type RawPageInspection struct {
	TotalCount int
	ItemCount  int
	IDs        []int64
	Artifacts  []RawArtifactInspection
	Runs       []RawRunInspection
}

type RawArtifactInspection struct {
	ArtifactID       int64
	ProducerRunID    int64
	RepositoryID     int64
	HeadRepositoryID int64
}

type RawRunInspection struct {
	RunID          int64
	CurrentAttempt int
	RepositoryID   int64
}

func ValidateRepositoryName(repository string) error {
	if !repositoryPattern.MatchString(repository) {
		return fmt.Errorf("invalid repository %q", repository)
	}
	return nil
}

func InspectRepositoryResponse(data []byte) (int64, string, error) {
	return parseRepositoryDocument(data)
}

func InspectRawResourcePage(data []byte, resource, repository string) (RawPageInspection, error) {
	arrayField := ""
	switch resource {
	case RawResourceArtifacts:
		arrayField = "artifacts"
	case RawResourceRuns:
		arrayField = "workflow_runs"
	default:
		return RawPageInspection{}, fmt.Errorf("unknown raw resource %q", resource)
	}
	total, messages, err := parsePageEnvelope(data, arrayField)
	if err != nil {
		return RawPageInspection{}, err
	}
	inspection := RawPageInspection{TotalCount: total, ItemCount: len(messages), IDs: make([]int64, 0, len(messages))}
	for index, message := range messages {
		if resource == RawResourceArtifacts {
			artifact, err := parseArtifactDocument(message)
			if err != nil {
				return RawPageInspection{}, fmt.Errorf("artifacts[%d]: %w", index, err)
			}
			inspection.IDs = append(inspection.IDs, artifact.ArtifactID)
			inspection.Artifacts = append(inspection.Artifacts, RawArtifactInspection{
				ArtifactID: artifact.ArtifactID, ProducerRunID: artifact.ProducerRunID,
				RepositoryID: artifact.RepositoryID, HeadRepositoryID: artifact.HeadRepositoryID,
			})
			continue
		}
		run, repositoryID, err := parseRunDocument(message, repository)
		if err != nil {
			return RawPageInspection{}, fmt.Errorf("workflow_runs[%d]: %w", index, err)
		}
		inspection.IDs = append(inspection.IDs, run.RunID)
		inspection.Runs = append(inspection.Runs, RawRunInspection{RunID: run.RunID, CurrentAttempt: run.CurrentAttempt, RepositoryID: repositoryID})
	}
	return inspection, nil
}

func InspectAttemptResponse(data []byte, repository string, repositoryID, runID int64, runAttempt int) error {
	attempt, observedRepositoryID, err := parseAttemptDocument(data, repository)
	if err != nil {
		return err
	}
	if observedRepositoryID != repositoryID || attempt.RunID != runID || attempt.RunAttempt != runAttempt {
		return errors.New("attempt response identity mismatch")
	}
	return nil
}

func (collection RawCollection) Validate() error {
	if collection.SchemaID != CollectionSchemaID || collection.SchemaVersion != CollectionSchemaVersion {
		return fmt.Errorf("raw collection must be %s version %d", CollectionSchemaID, CollectionSchemaVersion)
	}
	started, err := parseCanonicalTime(collection.ObservedStartedAt)
	if err != nil {
		return fmt.Errorf("raw collection observed_started_at: %w", err)
	}
	finished, err := parseCanonicalTime(collection.ObservedFinishedAt)
	if err != nil {
		return fmt.Errorf("raw collection observed_finished_at: %w", err)
	}
	if finished.Before(started) || finished.Sub(started) > collectionTimeLimit {
		return errors.New("raw collection interval is invalid or exceeds its limit")
	}
	if len(collection.Repositories) == 0 || len(collection.Repositories) > MaxRepositories {
		return fmt.Errorf("raw collection repositories must contain 1..%d entries", MaxRepositories)
	}
	seenRepositoryIDs := make(map[int64]bool, len(collection.Repositories))
	for index, repository := range collection.Repositories {
		if !repositoryPattern.MatchString(repository.Repository) || repository.RepositoryID < 1 || repository.Directory != fmt.Sprintf("repository-%03d", index+1) {
			return fmt.Errorf("repositories[%d] has invalid identity or directory", index)
		}
		if index > 0 && collection.Repositories[index-1].Repository >= repository.Repository {
			return errors.New("raw collection repositories must be sorted and unique")
		}
		if seenRepositoryIDs[repository.RepositoryID] {
			return fmt.Errorf("duplicate raw collection repository_id %d", repository.RepositoryID)
		}
		seenRepositoryIDs[repository.RepositoryID] = true
		if err := validatePaginationProof(repository.Artifacts); err != nil {
			return fmt.Errorf("repository %q artifact pagination: %w", repository.Repository, err)
		}
		if err := validatePaginationProof(repository.Runs); err != nil {
			return fmt.Errorf("repository %q run pagination: %w", repository.Repository, err)
		}
		if len(repository.AttemptDocuments) > MaxPagesPerResource*MaxItemsPerPage*MaxRunAttempts {
			return fmt.Errorf("repository %q has too many attempt documents", repository.Repository)
		}
		for attemptIndex, attempt := range repository.AttemptDocuments {
			if attempt.RunID < 1 || attempt.RunAttempt < 1 || attempt.RunAttempt > MaxRunAttempts {
				return fmt.Errorf("repository %q attempt_documents[%d] is invalid", repository.Repository, attemptIndex)
			}
			if attemptIndex > 0 && compareCollectedAttempt(repository.AttemptDocuments[attemptIndex-1], attempt) >= 0 {
				return fmt.Errorf("repository %q attempt_documents must be sorted and unique", repository.Repository)
			}
		}
	}
	return nil
}

func compareCollectedAttempt(left, right CollectedAttemptDocument) int {
	if left.RunID != right.RunID {
		if left.RunID < right.RunID {
			return -1
		}
		return 1
	}
	return left.RunAttempt - right.RunAttempt
}

func sortRawCollection(collection *RawCollection) {
	sort.Slice(collection.Repositories, func(i, j int) bool {
		return collection.Repositories[i].Repository < collection.Repositories[j].Repository
	})
	for index := range collection.Repositories {
		collection.Repositories[index].Directory = fmt.Sprintf("repository-%03d", index+1)
		sort.Slice(collection.Repositories[index].AttemptDocuments, func(i, j int) bool {
			return compareCollectedAttempt(collection.Repositories[index].AttemptDocuments[i], collection.Repositories[index].AttemptDocuments[j]) < 0
		})
	}
}
