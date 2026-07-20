package actionsartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

const (
	DeletionBatchSchemaID       = "env-vault.actions-artifact-deletion-batch.v1"
	DeletionBatchSchemaVersion  = 1
	DeletionResultSchemaID      = "env-vault.actions-artifact-deletion-result.v1"
	DeletionResultSchemaVersion = 1
	MaxDeletionBatchSize        = 500

	ReadPresent = "present"
	ReadAbsent  = "absent"
	ReadUnknown = "unknown"

	MutationSuccess   = "success"
	MutationHTTPError = "http_error"
	MutationAmbiguous = "ambiguous"

	TerminalDeleted              = "deleted"
	TerminalAbsentAfterAmbiguous = "absent_after_ambiguous"
	TerminalUncertain            = "uncertain"
)

const maxDeletionResultBytes = 8 << 20

var ErrDeletionBatchStopped = errors.New("artifact deletion batch stopped after the first non-success mutation")

type DeletionBatch struct {
	SchemaID      string  `json:"schema_id"`
	SchemaVersion int     `json:"schema_version"`
	ArtifactIDs   []int64 `json:"artifact_ids"`
}

type PriorDeletionResult struct {
	SHA256     string
	Header     DeletionResultHeader
	Intents    []DeletionResultIntent
	Records    []DeletionResultRecord
	Footer     DeletionResultFooter
	Incomplete bool
}

type DeletionPreparation struct {
	AuthorizedManifest       DecisionManifest
	AuthorizedManifestSHA256 string
	AuthorizedDeleteCount    int
	AuthorizedDeleteBytes    int64
	Confirmation             string
	Batch                    DeletionBatch
	CurrentManifest          DecisionManifest
	PriorResults             []PriorDeletionResult
	ValidatedAt              time.Time
	ProofExpiresAt           time.Time
}

type DeletionPlan struct {
	AuthorizedManifestSHA256 string
	AuthorizedDeleteCount    int
	AuthorizedDeleteBytes    int64
	Records                  []DecisionRecord
	PriorResultSHA256s       []string
	ProofExpiresAt           time.Time
}

type CheckedArtifactRead struct {
	Outcome   string
	Exact     bool
	ErrorCode string
}

type ArtifactMutation struct {
	Outcome    string
	HTTPStatus int
	ErrorCode  string
}

type CheckedArtifactReader interface {
	ReadArtifact(context.Context, DecisionRecord) CheckedArtifactRead
}

type ArtifactDeleter interface {
	DeleteArtifact(context.Context, DecisionRecord) ArtifactMutation
}

type DeletionResultHeader struct {
	SchemaID                 string   `json:"schema_id"`
	SchemaVersion            int      `json:"schema_version"`
	RecordType               string   `json:"record_type"`
	AuthorizedManifestSHA256 string   `json:"authorized_manifest_sha256"`
	AuthorizedDeleteCount    int      `json:"authorized_delete_count"`
	AuthorizedDeleteBytes    int64    `json:"authorized_delete_bytes"`
	BatchArtifactIDs         []int64  `json:"batch_artifact_ids"`
	PriorResultSHA256s       []string `json:"prior_result_sha256s"`
	StartedAt                string   `json:"started_at"`
	ProofExpiresAt           string   `json:"proof_expires_at"`
}

type DeletionResultRecord struct {
	SchemaID           string `json:"schema_id"`
	SchemaVersion      int    `json:"schema_version"`
	RecordType         string `json:"record_type"`
	ArtifactID         int64  `json:"artifact_id"`
	Repository         string `json:"repository"`
	ProducerRunID      int64  `json:"producer_run_id"`
	ProducerRunAttempt int    `json:"producer_run_attempt"`
	WorkflowPath       string `json:"workflow_path"`
	HeadSHA            string `json:"head_sha"`
	SizeInBytes        int64  `json:"size_in_bytes"`
	ReasonCode         string `json:"reason_code"`
	AttemptedAt        string `json:"attempted_at"`
	MutationOutcome    string `json:"mutation_outcome"`
	MutationHTTPStatus int    `json:"mutation_http_status"`
	MutationErrorCode  string `json:"mutation_error_code"`
	ReadAfterOutcome   string `json:"read_after_outcome"`
	ReadAfterErrorCode string `json:"read_after_error_code"`
	TerminalState      string `json:"terminal_state"`
}

type DeletionResultIntent struct {
	SchemaID           string `json:"schema_id"`
	SchemaVersion      int    `json:"schema_version"`
	RecordType         string `json:"record_type"`
	ArtifactID         int64  `json:"artifact_id"`
	Repository         string `json:"repository"`
	ProducerRunID      int64  `json:"producer_run_id"`
	ProducerRunAttempt int    `json:"producer_run_attempt"`
	WorkflowPath       string `json:"workflow_path"`
	HeadSHA            string `json:"head_sha"`
	SizeInBytes        int64  `json:"size_in_bytes"`
	ReasonCode         string `json:"reason_code"`
	IntendedAt         string `json:"intended_at"`
}

type DeletionResultFooter struct {
	SchemaID      string `json:"schema_id"`
	SchemaVersion int    `json:"schema_version"`
	RecordType    string `json:"record_type"`
	Status        string `json:"status"`
	AttemptCount  int    `json:"attempt_count"`
	FinishedAt    string `json:"finished_at"`
}

type DeletionExecutionResult struct {
	AttemptCount int
	Status       string
}

func ExactDeletionConfirmation(count int, bytes int64, manifestSHA256 string) string {
	return fmt.Sprintf("ПОДТВЕРЖДАЮ DELETE ACTIONS ARTIFACTS COUNT %d BYTES %d MANIFEST SHA256 %s", count, bytes, manifestSHA256)
}

// ReplayCurrentDeletionManifest distrusts the supplied scope, rebuilds it from
// the newly collected raw live fence, requires exact equality, and only then
// replays the classifier. It performs no network or mutation.
func ReplayCurrentDeletionManifest(liveCollectionDirectory string, snapshot Snapshot, suppliedScope DecisionScope, policy Policy, now time.Time, maxAge time.Duration) (DecisionManifest, error) {
	_, _, derivedScope, err := DeriveLiveDecisionScope(liveCollectionDirectory, snapshot, policy, now, maxAge)
	if err != nil {
		return DecisionManifest{}, err
	}
	if !reflect.DeepEqual(suppliedScope, derivedScope) {
		return DecisionManifest{}, errors.New("supplied current decision scope does not equal replay from the raw live collection")
	}
	return Classify(snapshot, derivedScope, policy, now, maxAge)
}

func DeletionProofExpiresAt(snapshot Snapshot, scope DecisionScope, maxAge time.Duration) (time.Time, error) {
	if maxAge <= 0 || maxAge > MaxSnapshotAge {
		return time.Time{}, errors.New("deletion proof freshness bound is invalid")
	}
	snapshotFinished, err := parseCanonicalTime(snapshot.ObservedFinishedAt)
	if err != nil {
		return time.Time{}, err
	}
	scopeObserved, err := parseCanonicalTime(scope.ObservedAt)
	if err != nil {
		return time.Time{}, err
	}
	expires := snapshotFinished.Add(maxAge)
	if candidate := scopeObserved.Add(maxAge); candidate.Before(expires) {
		expires = candidate
	}
	return expires, nil
}

func ValidateAuthorizedDecisionManifest(manifest DecisionManifest) error {
	if manifest.SchemaID != DecisionManifestSchemaID || manifest.SchemaVersion != DecisionManifestVersion {
		return fmt.Errorf("authorized manifest must be %s version %d", DecisionManifestSchemaID, DecisionManifestVersion)
	}
	if !sha256Pattern.MatchString(manifest.SnapshotSemanticSHA256) || !sha256Pattern.MatchString(manifest.ScopeSemanticSHA256) ||
		!sha256Pattern.MatchString(manifest.PolicySemanticSHA256) || !sha256Pattern.MatchString(manifest.LiveObservationSemanticSHA256) ||
		!sha256Pattern.MatchString(manifest.RepairProofSemanticSHA256) || !sha256Pattern.MatchString(manifest.SemanticSHA256) {
		return errors.New("authorized manifest digest bindings are malformed")
	}
	var totals DecisionTotals
	seenIDs := make(map[int64]bool, len(manifest.Records))
	for index, record := range manifest.Records {
		if err := validateDeletionDecisionRecord(record); err != nil {
			return fmt.Errorf("authorized manifest records[%d]: %w", index, err)
		}
		if seenIDs[record.ArtifactID] {
			return fmt.Errorf("authorized manifest has duplicate global artifact ID %d", record.ArtifactID)
		}
		seenIDs[record.ArtifactID] = true
		if index > 0 && compareRunIdentity(manifest.Records[index-1].Repository, manifest.Records[index-1].ArtifactID, record.Repository, record.ArtifactID) >= 0 {
			return errors.New("authorized manifest records must be sorted and unique")
		}
		if err := addTotals(&totals.Before, record.SizeInBytes); err != nil {
			return err
		}
		if record.Decision == DecisionKeep {
			if err := addTotals(&totals.Keep, record.SizeInBytes); err != nil {
				return err
			}
		} else {
			if err := addTotals(&totals.Delete, record.SizeInBytes); err != nil {
				return err
			}
		}
	}
	totals.ExpectedAfter = totals.Keep
	if manifest.Totals != totals {
		return errors.New("authorized manifest totals do not reconcile")
	}
	digest, err := decisionManifestSemanticSHA256(manifest)
	if err != nil || digest != manifest.SemanticSHA256 {
		return errors.New("authorized manifest semantic SHA-256 does not match its complete decision document")
	}
	return nil
}

func validateDeletionDecisionRecord(record DecisionRecord) error {
	if ValidateRepositoryName(record.Repository) != nil || record.ArtifactID < 1 || record.ProducerRunID < 1 ||
		record.ProducerRunAttempt < 1 || record.ProducerRunAttempt > MaxRunAttempts || validateWorkflowPath(record.WorkflowPath) != nil ||
		!shaPattern.MatchString(record.HeadSHA) || record.SizeInBytes < 0 || strings.TrimSpace(record.Name) != record.Name ||
		record.Name == "" || len(record.Name) > 512 || !digestPattern.MatchString(record.ArtifactDigest) {
		return errors.New("artifact identity, lineage, or size is malformed")
	}
	created, createdErr := parseCanonicalTime(record.CreatedAt)
	updated, updatedErr := parseCanonicalTime(record.UpdatedAt)
	expires, expiresErr := parseCanonicalTime(record.ExpiresAt)
	if createdErr != nil || updatedErr != nil || expiresErr != nil || updated.Before(created) || !expires.After(updated) {
		return errors.New("artifact timestamps are malformed")
	}
	switch record.Decision {
	case DecisionDelete:
		if record.ReasonCode != ReasonDeleteSuperseded || record.Lifecycle != LifecycleSupersededEligible {
			return errors.New("delete record is not exact DELETE_SUPERSEDED authority")
		}
	case DecisionKeep:
		if record.ReasonCode == "" || record.ReasonCode == ReasonDeleteSuperseded {
			return errors.New("keep record has an invalid reason")
		}
	default:
		return errors.New("decision is neither keep nor delete")
	}
	return nil
}

func PrepareDeletionPlan(input DeletionPreparation) (DeletionPlan, error) {
	if input.ValidatedAt.IsZero() || input.ValidatedAt.Location() != time.UTC || input.ProofExpiresAt.IsZero() ||
		input.ProofExpiresAt.Location() != time.UTC || !input.ValidatedAt.Before(input.ProofExpiresAt) {
		return DeletionPlan{}, errors.New("current deletion proof is expired or is not bound to an actual UTC validation time")
	}
	if err := ValidateAuthorizedDecisionManifest(input.AuthorizedManifest); err != nil {
		return DeletionPlan{}, err
	}
	manifest := input.AuthorizedManifest
	if input.AuthorizedManifestSHA256 != manifest.SemanticSHA256 || input.AuthorizedDeleteCount != manifest.Totals.Delete.Count ||
		input.AuthorizedDeleteBytes != manifest.Totals.Delete.Bytes {
		return DeletionPlan{}, errors.New("authorized manifest SHA-256, delete count, or delete bytes mismatch")
	}
	if input.Confirmation != ExactDeletionConfirmation(input.AuthorizedDeleteCount, input.AuthorizedDeleteBytes, input.AuthorizedManifestSHA256) {
		return DeletionPlan{}, errors.New("deletion confirmation does not byte-match the exact authorized manifest totals")
	}
	if err := ValidateDeletionBatch(input.Batch); err != nil {
		return DeletionPlan{}, err
	}
	if err := ValidateAuthorizedDecisionManifest(input.CurrentManifest); err != nil {
		return DeletionPlan{}, fmt.Errorf("current replayed manifest: %w", err)
	}
	if input.CurrentManifest.PolicySemanticSHA256 != manifest.PolicySemanticSHA256 {
		return DeletionPlan{}, errors.New("current replay uses a different Actions artifact policy")
	}

	authorized := make(map[int64]DecisionRecord, len(manifest.Records))
	current := make(map[int64]DecisionRecord, len(input.CurrentManifest.Records))
	for _, record := range manifest.Records {
		authorized[record.ArtifactID] = record
	}
	for _, record := range input.CurrentManifest.Records {
		current[record.ArtifactID] = record
	}

	priorTerminal := make(map[int64]DeletionResultRecord)
	priorDigests := make([]string, 0, len(input.PriorResults))
	for resultIndex, result := range input.PriorResults {
		expectedChain := append([]string{}, priorDigests...)
		sort.Strings(expectedChain)
		if !reflect.DeepEqual(result.Header.PriorResultSHA256s, expectedChain) {
			return DeletionPlan{}, fmt.Errorf("prior deletion result %d does not extend the exact cumulative result chain", resultIndex+1)
		}
		if err := validatePriorDeletionResult(result, manifest); err != nil {
			return DeletionPlan{}, fmt.Errorf("prior deletion result %d: %w", resultIndex+1, err)
		}
		priorDigests = append(priorDigests, result.SHA256)
		for _, record := range result.Records {
			if record.TerminalState == TerminalUncertain {
				return DeletionPlan{}, fmt.Errorf("prior deletion result %d contains unresolved artifact %d", resultIndex+1, record.ArtifactID)
			}
			if _, duplicate := priorTerminal[record.ArtifactID]; duplicate {
				return DeletionPlan{}, fmt.Errorf("artifact %d has duplicate prior terminal records", record.ArtifactID)
			}
			priorTerminal[record.ArtifactID] = record
		}
	}
	canonicalPriorDigests := append([]string(nil), priorDigests...)
	sort.Strings(canonicalPriorDigests)
	for index := 1; index < len(canonicalPriorDigests); index++ {
		if canonicalPriorDigests[index-1] == canonicalPriorDigests[index] {
			return DeletionPlan{}, errors.New("prior deletion results must be unique")
		}
	}

	for _, record := range manifest.Records {
		currentRecord, present := current[record.ArtifactID]
		priorRecord, previouslyTerminal := priorTerminal[record.ArtifactID]
		if record.Decision == DecisionKeep {
			if !present || !sameImmutableArtifactTuple(record, currentRecord) {
				return DeletionPlan{}, fmt.Errorf("authorized keep artifact %d is absent or drifted", record.ArtifactID)
			}
			if previouslyTerminal {
				return DeletionPlan{}, fmt.Errorf("authorized keep artifact %d appears in a prior deletion result", record.ArtifactID)
			}
			continue
		}
		if present {
			if previouslyTerminal {
				return DeletionPlan{}, fmt.Errorf("prior terminal artifact %d is still present", record.ArtifactID)
			}
			if !sameDeleteTuple(record, currentRecord) {
				return DeletionPlan{}, fmt.Errorf("authorized delete artifact %d is no longer the exact DELETE_SUPERSEDED tuple", record.ArtifactID)
			}
			continue
		}
		if !previouslyTerminal || !sameResultTuple(record, priorRecord) {
			return DeletionPlan{}, fmt.Errorf("absent authorized delete artifact %d lacks one accepted prior terminal record", record.ArtifactID)
		}
	}
	for artifactID, record := range priorTerminal {
		authorizedRecord, exists := authorized[artifactID]
		if !exists || authorizedRecord.Decision != DecisionDelete || !sameResultTuple(authorizedRecord, record) {
			return DeletionPlan{}, fmt.Errorf("prior terminal artifact %d is outside exact delete authority", artifactID)
		}
		if _, present := current[artifactID]; present {
			return DeletionPlan{}, fmt.Errorf("prior terminal artifact %d must now be absent", artifactID)
		}
	}

	plan := DeletionPlan{
		AuthorizedManifestSHA256: input.AuthorizedManifestSHA256,
		AuthorizedDeleteCount:    input.AuthorizedDeleteCount, AuthorizedDeleteBytes: input.AuthorizedDeleteBytes,
		PriorResultSHA256s: append([]string{}, canonicalPriorDigests...),
		Records:            make([]DecisionRecord, 0, len(input.Batch.ArtifactIDs)),
		ProofExpiresAt:     input.ProofExpiresAt,
	}
	for _, artifactID := range input.Batch.ArtifactIDs {
		authorizedRecord, exists := authorized[artifactID]
		currentRecord, currentPresent := current[artifactID]
		if !exists || authorizedRecord.Decision != DecisionDelete || !currentPresent || !sameDeleteTuple(authorizedRecord, currentRecord) {
			return DeletionPlan{}, fmt.Errorf("batch artifact %d is not present under exact current delete authority", artifactID)
		}
		plan.Records = append(plan.Records, authorizedRecord)
	}
	return plan, nil
}

func sameImmutableArtifactTuple(authorized, current DecisionRecord) bool {
	authorized.Decision, authorized.ReasonCode = "", ""
	current.Decision, current.ReasonCode = "", ""
	return reflect.DeepEqual(authorized, current)
}

func ValidateDeletionBatch(batch DeletionBatch) error {
	if batch.SchemaID != DeletionBatchSchemaID || batch.SchemaVersion != DeletionBatchSchemaVersion {
		return fmt.Errorf("deletion batch must be %s version %d", DeletionBatchSchemaID, DeletionBatchSchemaVersion)
	}
	if len(batch.ArtifactIDs) == 0 || len(batch.ArtifactIDs) > MaxDeletionBatchSize {
		return fmt.Errorf("deletion batch must contain 1..%d explicit artifact IDs", MaxDeletionBatchSize)
	}
	seen := make(map[int64]bool, len(batch.ArtifactIDs))
	for _, artifactID := range batch.ArtifactIDs {
		if artifactID < 1 || seen[artifactID] {
			return errors.New("deletion batch artifact IDs must be positive and unique")
		}
		seen[artifactID] = true
	}
	return nil
}

func sameDeleteTuple(authorized, current DecisionRecord) bool {
	return sameImmutableArtifactTuple(authorized, current) &&
		authorized.Decision == DecisionDelete && authorized.ReasonCode == ReasonDeleteSuperseded && authorized.Lifecycle == LifecycleSupersededEligible &&
		current.Decision == DecisionDelete && current.ReasonCode == ReasonDeleteSuperseded && current.Lifecycle == LifecycleSupersededEligible
}

func sameResultTuple(authorized DecisionRecord, result DeletionResultRecord) bool {
	return authorized.Repository == result.Repository && authorized.ArtifactID == result.ArtifactID &&
		authorized.ProducerRunID == result.ProducerRunID && authorized.ProducerRunAttempt == result.ProducerRunAttempt &&
		authorized.WorkflowPath == result.WorkflowPath && authorized.HeadSHA == result.HeadSHA &&
		authorized.SizeInBytes == result.SizeInBytes && result.ReasonCode == ReasonDeleteSuperseded
}

func ExecuteDeletionBatch(ctx context.Context, plan DeletionPlan, reader CheckedArtifactReader, deleter ArtifactDeleter, resultPath string, now func() time.Time) (DeletionExecutionResult, error) {
	if reader == nil || deleter == nil || now == nil || len(plan.Records) == 0 || plan.ProofExpiresAt.IsZero() || plan.ProofExpiresAt.Location() != time.UTC {
		return DeletionExecutionResult{}, errors.New("deletion executor dependencies and nonempty plan are required")
	}
	for _, record := range plan.Records {
		if err := ctx.Err(); err != nil {
			return DeletionExecutionResult{}, err
		}
		if err := requireFreshDeletionProof(now, plan.ProofExpiresAt); err != nil {
			return DeletionExecutionResult{}, err
		}
		observation := reader.ReadArtifact(ctx, record)
		if observation.Outcome != ReadPresent || !observation.Exact || observation.ErrorCode != "" {
			return DeletionExecutionResult{}, fmt.Errorf("pre-delete checked read rejected artifact %d as %s", record.ArtifactID, observation.Outcome)
		}
	}
	if err := requireFreshDeletionProof(now, plan.ProofExpiresAt); err != nil {
		return DeletionExecutionResult{}, err
	}
	journal, err := openDeletionJournal(resultPath)
	if err != nil {
		return DeletionExecutionResult{}, err
	}
	defer journal.Close()
	batchIDs := make([]int64, len(plan.Records))
	for index, record := range plan.Records {
		batchIDs[index] = record.ArtifactID
	}
	header := DeletionResultHeader{
		SchemaID: DeletionResultSchemaID, SchemaVersion: DeletionResultSchemaVersion, RecordType: "header",
		AuthorizedManifestSHA256: plan.AuthorizedManifestSHA256, AuthorizedDeleteCount: plan.AuthorizedDeleteCount,
		AuthorizedDeleteBytes: plan.AuthorizedDeleteBytes, BatchArtifactIDs: batchIDs,
		PriorResultSHA256s: append([]string{}, plan.PriorResultSHA256s...), StartedAt: canonicalNow(now),
		ProofExpiresAt: plan.ProofExpiresAt.Format(time.RFC3339Nano),
	}
	if err := journal.Append(header); err != nil {
		return DeletionExecutionResult{}, err
	}
	for index, record := range plan.Records {
		if err := ctx.Err(); err != nil {
			return DeletionExecutionResult{AttemptCount: index, Status: "cancelled"}, err
		}
		if err := requireFreshDeletionProof(now, plan.ProofExpiresAt); err != nil {
			return DeletionExecutionResult{AttemptCount: index, Status: "proof_expired"}, err
		}
		intent := DeletionResultIntent{
			SchemaID: DeletionResultSchemaID, SchemaVersion: DeletionResultSchemaVersion, RecordType: "intent",
			ArtifactID: record.ArtifactID, Repository: record.Repository, ProducerRunID: record.ProducerRunID,
			ProducerRunAttempt: record.ProducerRunAttempt, WorkflowPath: record.WorkflowPath, HeadSHA: record.HeadSHA,
			SizeInBytes: record.SizeInBytes, ReasonCode: record.ReasonCode, IntendedAt: canonicalNow(now),
		}
		if err := journal.Append(intent); err != nil {
			return DeletionExecutionResult{AttemptCount: index, Status: "journal_failed"}, err
		}
		if err := ctx.Err(); err != nil {
			return DeletionExecutionResult{AttemptCount: index, Status: "cancelled_with_intent"}, err
		}
		if err := requireFreshDeletionProof(now, plan.ProofExpiresAt); err != nil {
			return DeletionExecutionResult{AttemptCount: index, Status: "proof_expired_with_intent"}, err
		}
		mutation := deleter.DeleteArtifact(ctx, record)
		resultRecord := DeletionResultRecord{
			SchemaID: DeletionResultSchemaID, SchemaVersion: DeletionResultSchemaVersion, RecordType: "attempt",
			ArtifactID: record.ArtifactID, Repository: record.Repository, ProducerRunID: record.ProducerRunID,
			ProducerRunAttempt: record.ProducerRunAttempt, WorkflowPath: record.WorkflowPath, HeadSHA: record.HeadSHA,
			SizeInBytes: record.SizeInBytes, ReasonCode: record.ReasonCode, AttemptedAt: canonicalNow(now),
			MutationOutcome: mutation.Outcome, MutationHTTPStatus: mutation.HTTPStatus, MutationErrorCode: mutation.ErrorCode,
			ReadAfterOutcome: "not_checked",
		}
		if mutation.Outcome == MutationSuccess && mutation.HTTPStatus == 204 && mutation.ErrorCode == "" {
			resultRecord.TerminalState = TerminalDeleted
		} else {
			if err := ctx.Err(); err != nil {
				resultRecord.ReadAfterOutcome = ReadUnknown
				resultRecord.ReadAfterErrorCode = "CONTEXT_CANCELLED"
				resultRecord.TerminalState = TerminalUncertain
				if appendErr := journal.Append(resultRecord); appendErr != nil {
					return DeletionExecutionResult{AttemptCount: index + 1, Status: "journal_failed"}, appendErr
				}
				if appendErr := journal.Append(resultFooter("stopped_uncertain", index+1, now)); appendErr != nil {
					return DeletionExecutionResult{AttemptCount: index + 1, Status: "journal_failed"}, appendErr
				}
				return DeletionExecutionResult{AttemptCount: index + 1, Status: "stopped_uncertain"}, err
			}
			readAfter := reader.ReadArtifact(ctx, record)
			resultRecord.ReadAfterOutcome = readAfter.Outcome
			resultRecord.ReadAfterErrorCode = readAfter.ErrorCode
			if mutation.Outcome == MutationAmbiguous && readAfter.Outcome == ReadAbsent && !readAfter.Exact && readAfter.ErrorCode == "" {
				resultRecord.TerminalState = TerminalAbsentAfterAmbiguous
			} else {
				resultRecord.TerminalState = TerminalUncertain
			}
		}
		if err := journal.Append(resultRecord); err != nil {
			return DeletionExecutionResult{AttemptCount: index + 1, Status: "journal_failed"}, err
		}
		if resultRecord.TerminalState != TerminalDeleted {
			status := "stopped_uncertain"
			if resultRecord.TerminalState == TerminalAbsentAfterAmbiguous {
				status = "stopped_after_absence"
			}
			if err := journal.Append(resultFooter(status, index+1, now)); err != nil {
				return DeletionExecutionResult{AttemptCount: index + 1, Status: "journal_failed"}, err
			}
			return DeletionExecutionResult{AttemptCount: index + 1, Status: status}, ErrDeletionBatchStopped
		}
	}
	if err := journal.Append(resultFooter("complete", len(plan.Records), now)); err != nil {
		return DeletionExecutionResult{AttemptCount: len(plan.Records), Status: "journal_failed"}, err
	}
	return DeletionExecutionResult{AttemptCount: len(plan.Records), Status: "complete"}, nil
}

func resultFooter(status string, attempts int, now func() time.Time) DeletionResultFooter {
	return DeletionResultFooter{SchemaID: DeletionResultSchemaID, SchemaVersion: DeletionResultSchemaVersion, RecordType: "footer", Status: status, AttemptCount: attempts, FinishedAt: canonicalNow(now)}
}

func canonicalNow(now func() time.Time) string { return now().UTC().Format(time.RFC3339Nano) }

func requireFreshDeletionProof(now func() time.Time, expires time.Time) error {
	if !now().UTC().Before(expires) {
		return errors.New("current Actions artifact deletion proof expired before mutation")
	}
	return nil
}

func validatePriorDeletionResult(result PriorDeletionResult, manifest DecisionManifest) error {
	digest, err := priorDeletionResultSHA256(result)
	if err != nil || digest != result.SHA256 {
		return errors.New("result SHA-256 does not match its canonical JSONL records")
	}
	header := result.Header
	if !sha256Pattern.MatchString(result.SHA256) || header.SchemaID != DeletionResultSchemaID || header.SchemaVersion != DeletionResultSchemaVersion || header.RecordType != "header" ||
		header.AuthorizedManifestSHA256 != manifest.SemanticSHA256 || header.AuthorizedDeleteCount != manifest.Totals.Delete.Count ||
		header.AuthorizedDeleteBytes != manifest.Totals.Delete.Bytes || len(header.BatchArtifactIDs) == 0 || len(header.BatchArtifactIDs) > MaxDeletionBatchSize ||
		parseResultTime(header.StartedAt) != nil || parseResultTime(header.ProofExpiresAt) != nil {
		return errors.New("header is malformed or bound to different delete authority")
	}
	if !sort.StringsAreSorted(header.PriorResultSHA256s) {
		return errors.New("header prior-result SHA-256 list is not canonical")
	}
	for index, digest := range header.PriorResultSHA256s {
		if !sha256Pattern.MatchString(digest) || (index > 0 && header.PriorResultSHA256s[index-1] == digest) {
			return errors.New("header prior-result SHA-256 list is malformed or duplicate")
		}
	}
	seen := make(map[int64]bool, len(header.BatchArtifactIDs))
	for _, artifactID := range header.BatchArtifactIDs {
		if artifactID < 1 || seen[artifactID] {
			return errors.New("header batch IDs are invalid or duplicate")
		}
		seen[artifactID] = true
	}
	if result.Incomplete || len(result.Intents) != len(result.Records) || len(result.Records) == 0 || len(result.Records) > len(header.BatchArtifactIDs) {
		return errors.New("result has no completed attempts or exceeds its header batch")
	}
	for index, record := range result.Records {
		intent := result.Intents[index]
		if intent.SchemaID != DeletionResultSchemaID || intent.SchemaVersion != DeletionResultSchemaVersion || intent.RecordType != "intent" ||
			intent.ArtifactID != header.BatchArtifactIDs[index] || parseResultTime(intent.IntendedAt) != nil || !sameIntentResultTuple(intent, record) {
			return fmt.Errorf("intent record %d is malformed, unmatched, or out of order", index+1)
		}
		if record.SchemaID != DeletionResultSchemaID || record.SchemaVersion != DeletionResultSchemaVersion || record.RecordType != "attempt" ||
			record.ArtifactID != header.BatchArtifactIDs[index] || parseResultTime(record.AttemptedAt) != nil {
			return fmt.Errorf("attempt record %d is malformed or out of order", index+1)
		}
		if record.TerminalState == TerminalDeleted {
			if record.MutationOutcome != MutationSuccess || record.MutationHTTPStatus != 204 || record.MutationErrorCode != "" || record.ReadAfterOutcome != "not_checked" || record.ReadAfterErrorCode != "" {
				return fmt.Errorf("attempt record %d has forged successful terminal state", index+1)
			}
		} else if record.TerminalState == TerminalAbsentAfterAmbiguous {
			if record.MutationOutcome != MutationAmbiguous || record.ReadAfterOutcome != ReadAbsent || record.ReadAfterErrorCode != "" {
				return fmt.Errorf("attempt record %d has forged ambiguous-absence terminal state", index+1)
			}
		} else if record.TerminalState != TerminalUncertain {
			return fmt.Errorf("attempt record %d has unknown terminal state", index+1)
		}
		if index < len(result.Records)-1 && record.TerminalState != TerminalDeleted {
			return fmt.Errorf("attempt record %d has an impossible continuation after a stopping outcome", index+1)
		}
	}
	footer := result.Footer
	if footer.SchemaID != DeletionResultSchemaID || footer.SchemaVersion != DeletionResultSchemaVersion || footer.RecordType != "footer" || footer.AttemptCount != len(result.Records) || parseResultTime(footer.FinishedAt) != nil {
		return errors.New("footer is malformed or does not reconcile")
	}
	wantStatus := "complete"
	last := result.Records[len(result.Records)-1]
	if last.TerminalState == TerminalAbsentAfterAmbiguous {
		wantStatus = "stopped_after_absence"
	} else if last.TerminalState == TerminalUncertain {
		wantStatus = "stopped_uncertain"
	} else if len(result.Records) != len(header.BatchArtifactIDs) {
		return errors.New("successful result is an incomplete batch")
	}
	if footer.Status != wantStatus {
		return errors.New("footer status does not match terminal record")
	}
	return nil
}

func priorDeletionResultSHA256(result PriorDeletionResult) (string, error) {
	var encoded []byte
	appendLine := func(value any) error {
		line, err := json.Marshal(value)
		if err != nil {
			return err
		}
		encoded = append(encoded, line...)
		encoded = append(encoded, '\n')
		return nil
	}
	if err := appendLine(result.Header); err != nil {
		return "", err
	}
	for index := range result.Records {
		if index >= len(result.Intents) {
			return "", errors.New("result has an unmatched attempt record")
		}
		if err := appendLine(result.Intents[index]); err != nil {
			return "", err
		}
		if err := appendLine(result.Records[index]); err != nil {
			return "", err
		}
	}
	if len(result.Intents) != len(result.Records) || result.Footer.RecordType == "" {
		return "", errors.New("result is incomplete")
	}
	if err := appendLine(result.Footer); err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func sameIntentResultTuple(intent DeletionResultIntent, result DeletionResultRecord) bool {
	return intent.ArtifactID == result.ArtifactID && intent.Repository == result.Repository && intent.ProducerRunID == result.ProducerRunID &&
		intent.ProducerRunAttempt == result.ProducerRunAttempt && intent.WorkflowPath == result.WorkflowPath && intent.HeadSHA == result.HeadSHA &&
		intent.SizeInBytes == result.SizeInBytes && intent.ReasonCode == result.ReasonCode
}

func parseResultTime(value string) error {
	_, err := parseCanonicalTime(value)
	return err
}

func LoadDeletionBatchFile(filename string) (DeletionBatch, error) {
	data, err := readStableDeletionFile(filename, MaxScopeBytes)
	if err != nil {
		return DeletionBatch{}, err
	}
	var batch DeletionBatch
	if err := strictjson.Decode(data, MaxScopeBytes, &batch); err != nil {
		return DeletionBatch{}, err
	}
	if err := ValidateDeletionBatch(batch); err != nil {
		return DeletionBatch{}, err
	}
	canonical, _ := MarshalCanonical(batch)
	if !bytes.Equal(data, canonical) {
		return DeletionBatch{}, errors.New("deletion batch JSON is not canonical")
	}
	return batch, nil
}

func LoadAuthorizedDecisionManifestFile(filename string) (DecisionManifest, error) {
	data, err := readStableDeletionFile(filename, MaxManifestBytes)
	if err != nil {
		return DecisionManifest{}, err
	}
	return decodeCanonicalAuthorizedManifest(data)
}

func LoadPriorDeletionResultFile(filename string) (PriorDeletionResult, error) {
	data, err := readStableDeletionFile(filename, maxDeletionResultBytes)
	if err != nil {
		return PriorDeletionResult{}, err
	}
	if data[len(data)-1] != '\n' {
		return PriorDeletionResult{}, errors.New("deletion result JSONL must end with a newline")
	}
	lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	if len(lines) < 2 || len(lines) > 2*MaxDeletionBatchSize+2 {
		return PriorDeletionResult{}, errors.New("deletion result JSONL has an invalid line count")
	}
	result := PriorDeletionResult{}
	for index, line := range lines {
		if len(line) == 0 || len(line) > 16<<10 || strictjson.Validate(line, 16<<10) != nil {
			return PriorDeletionResult{}, fmt.Errorf("deletion result line %d is malformed", index+1)
		}
		var envelope struct {
			RecordType string `json:"record_type"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			return PriorDeletionResult{}, err
		}
		switch envelope.RecordType {
		case "header":
			if index != 0 || strictjson.Decode(line, 16<<10, &result.Header) != nil || !canonicalLine(line, result.Header) {
				return PriorDeletionResult{}, errors.New("deletion result header is not strict canonical JSON")
			}
		case "attempt":
			var record DeletionResultRecord
			if index == 0 || index%2 != 0 || strictjson.Decode(line, 16<<10, &record) != nil || !canonicalLine(line, record) {
				return PriorDeletionResult{}, fmt.Errorf("deletion result attempt line %d is not strict canonical JSON", index+1)
			}
			result.Records = append(result.Records, record)
		case "intent":
			var intent DeletionResultIntent
			if index == 0 || index%2 != 1 || strictjson.Decode(line, 16<<10, &intent) != nil || !canonicalLine(line, intent) {
				return PriorDeletionResult{}, fmt.Errorf("deletion result intent line %d is not strict canonical JSON", index+1)
			}
			result.Intents = append(result.Intents, intent)
		case "footer":
			if index != len(lines)-1 || strictjson.Decode(line, 16<<10, &result.Footer) != nil || !canonicalLine(line, result.Footer) {
				return PriorDeletionResult{}, errors.New("deletion result footer is not strict canonical JSON")
			}
		default:
			return PriorDeletionResult{}, fmt.Errorf("deletion result line %d has unknown record_type", index+1)
		}
	}
	if result.Footer.RecordType == "" || len(result.Intents) != len(result.Records) {
		result.Incomplete = true
	}
	digest := sha256.Sum256(data)
	result.SHA256 = hex.EncodeToString(digest[:])
	return result, nil
}

func canonicalLine(line []byte, value any) bool {
	encoded, err := json.Marshal(value)
	return err == nil && bytes.Equal(line, encoded)
}

func readStableDeletionFile(filename string, limit int) ([]byte, error) {
	before, err := os.Lstat(filename)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > int64(limit) {
		return nil, errors.New("deletion input must be a bounded regular non-symlink file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() {
		return nil, errors.New("deletion input changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || int64(len(data)) != before.Size() {
		return nil, errors.New("deletion input could not be read stably")
	}
	return data, nil
}

type deletionJournal struct {
	file *os.File
}

func openDeletionJournal(filename string) (*deletionJournal, error) {
	if filename == "" {
		return nil, errors.New("deletion result path is required")
	}
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(absolute)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("deletion result parent must be a regular non-symlink directory")
	}
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	directory, err := os.Open(parent)
	if err != nil {
		_ = file.Close()
		return nil, errors.New("deletion result parent cannot be opened for sync")
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		_ = file.Close()
		return nil, errors.New("deletion result creation could not be synced")
	}
	return &deletionJournal{file: file}, nil
}

func (journal *deletionJournal) Append(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	written, err := journal.file.Write(encoded)
	if err != nil || written != len(encoded) {
		return errors.New("deletion result append failed")
	}
	return journal.file.Sync()
}

func (journal *deletionJournal) Close() error { return journal.file.Close() }

// InspectDeletionArtifactResponse verifies the exact fields returned by the
// single-artifact REST endpoint immediately before or after a deletion. The
// fresh full snapshot/scope replay separately binds attempt and workflow path.
func InspectDeletionArtifactResponse(data []byte, expected DecisionRecord) error {
	artifact, err := parseArtifactDocument(data)
	if err != nil {
		return err
	}
	if artifact.ArtifactID != expected.ArtifactID || artifact.Name != expected.Name || artifact.Digest != expected.ArtifactDigest ||
		artifact.SizeInBytes != expected.SizeInBytes || artifact.CreatedAt != expected.CreatedAt || artifact.UpdatedAt != expected.UpdatedAt ||
		artifact.ExpiresAt != expected.ExpiresAt || artifact.Expired != expected.Expired || artifact.ProducerRunID != expected.ProducerRunID ||
		artifact.HeadSHA != expected.HeadSHA {
		return errors.New("exact artifact response drifted from the current replayed decision tuple")
	}
	return nil
}
