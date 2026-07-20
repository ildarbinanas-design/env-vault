package actionsartifact

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAuthorizedManifestLoaderRequiresExactCanonicalBytes(t *testing.T) {
	manifest := deletionTestManifest(t, 1)
	canonical, err := MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "authorized.json")
	if err := os.WriteFile(path, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAuthorizedDecisionManifestFile(path); err != nil {
		t.Fatalf("canonical manifest rejected: %v", err)
	}
	if err := os.WriteFile(path, append(canonical, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAuthorizedDecisionManifestFile(path); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("non-canonical manifest error=%v", err)
	}
}

func TestDeletionPreparationRejectsAuthorityBatchAndCurrentStateDrift(t *testing.T) {
	manifest := deletionTestManifest(t, 2)
	deleteIDs := deletionIDs(manifest)
	base := deletionTestPreparation(manifest, deleteIDs[:1])

	tests := []struct {
		name    string
		mutate  func(*DeletionPreparation)
		message string
	}{
		{"confirmation", func(value *DeletionPreparation) { value.Confirmation += "\n" }, "confirmation"},
		{"manifest digest", func(value *DeletionPreparation) { value.AuthorizedManifestSHA256 = strings.Repeat("0", 64) }, "SHA-256"},
		{"delete count", func(value *DeletionPreparation) { value.AuthorizedDeleteCount++ }, "count"},
		{"delete bytes", func(value *DeletionPreparation) { value.AuthorizedDeleteBytes++ }, "bytes"},
		{"duplicate batch", func(value *DeletionPreparation) { value.Batch.ArtifactIDs = []int64{deleteIDs[0], deleteIDs[0]} }, "unique"},
		{"oversized batch", func(value *DeletionPreparation) {
			value.Batch.ArtifactIDs = make([]int64, MaxDeletionBatchSize+1)
			for index := range value.Batch.ArtifactIDs {
				value.Batch.ArtifactIDs[index] = int64(index + 1)
			}
		}, "1..500"},
		{"nonauthorized batch", func(value *DeletionPreparation) { value.Batch.ArtifactIDs = []int64{999999} }, "not present"},
		{"keep loss", func(value *DeletionPreparation) {
			for index, record := range value.CurrentManifest.Records {
				if record.Decision == DecisionKeep {
					value.CurrentManifest.Records = append(value.CurrentManifest.Records[:index], value.CurrentManifest.Records[index+1:]...)
					break
				}
			}
			rebuildDeletionManifest(t, &value.CurrentManifest)
		}, "keep artifact"},
		{"keep lineage drift", func(value *DeletionPreparation) {
			for index := range value.CurrentManifest.Records {
				if value.CurrentManifest.Records[index].Decision == DecisionKeep {
					value.CurrentManifest.Records[index].HeadSHA = strings.Repeat("9", 40)
					break
				}
			}
			rebuildDeletionManifest(t, &value.CurrentManifest)
		}, "keep artifact"},
		{"target drift", func(value *DeletionPreparation) {
			for index := range value.CurrentManifest.Records {
				if value.CurrentManifest.Records[index].ArtifactID == deleteIDs[0] {
					value.CurrentManifest.Records[index].SizeInBytes++
				}
			}
			rebuildDeletionManifest(t, &value.CurrentManifest)
		}, "exact DELETE_SUPERSEDED"},
		{"nonbatch delete content drift", func(value *DeletionPreparation) {
			for index := range value.CurrentManifest.Records {
				if value.CurrentManifest.Records[index].ArtifactID == deleteIDs[1] {
					value.CurrentManifest.Records[index].ArtifactDigest = "sha256:" + strings.Repeat("9", 64)
				}
			}
			rebuildDeletionManifest(t, &value.CurrentManifest)
		}, "exact DELETE_SUPERSEDED"},
		{"target absent without prior", func(value *DeletionPreparation) {
			removeDeletionRecord(t, &value.CurrentManifest, deleteIDs[0])
		}, "lacks one accepted prior"},
		{"policy drift", func(value *DeletionPreparation) {
			value.CurrentManifest.PolicySemanticSHA256 = strings.Repeat("9", 64)
			rebuildDeletionManifest(t, &value.CurrentManifest)
		}, "different Actions artifact policy"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cloneDeletionPreparation(t, base)
			test.mutate(&value)
			_, err := PrepareDeletionPlan(value)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v want substring %q", err, test.message)
			}
		})
	}

	withNew := cloneDeletionPreparation(t, base)
	newRecord := withNew.CurrentManifest.Records[len(withNew.CurrentManifest.Records)-1]
	newRecord.ArtifactID += 1000
	withNew.CurrentManifest.Records = append(withNew.CurrentManifest.Records, newRecord)
	rebuildDeletionManifest(t, &withNew.CurrentManifest)
	if _, err := PrepareDeletionPlan(withNew); err != nil {
		t.Fatalf("new current artifact must be preserved without widening deletion authority: %v", err)
	}

	decisionShift := cloneDeletionPreparation(t, base)
	shiftedKeepID := int64(0)
	for index := range decisionShift.CurrentManifest.Records {
		record := &decisionShift.CurrentManifest.Records[index]
		if decisionShift.AuthorizedManifest.Records[index].Decision == DecisionKeep && record.Lifecycle == LifecycleSupersededEligible {
			shiftedKeepID = record.ArtifactID
			record.Decision, record.ReasonCode = DecisionDelete, ReasonDeleteSuperseded
			break
		}
	}
	if shiftedKeepID == 0 {
		t.Fatal("fixture has no superseded-eligible authorized keep")
	}
	rebuildDeletionManifest(t, &decisionShift.CurrentManifest)
	if _, err := PrepareDeletionPlan(decisionShift); err != nil {
		t.Fatalf("dynamic decision/reason shift changed immutable keep authority: %v", err)
	}
	decisionShift.Batch.ArtifactIDs = []int64{shiftedKeepID}
	if _, err := PrepareDeletionPlan(decisionShift); err == nil || !strings.Contains(err.Error(), "not present under exact current delete authority") {
		t.Fatalf("authorized keep was selectable after decision shift: %v", err)
	}
}

func TestReplayCurrentDeletionManifestRejectsStaleAndForgedLiveScope(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	snapshot := liveFixtureSnapshot(t, policy)
	root, _ := writeLiveDeriveFixture(t, snapshot)
	now := mustTime(t, "2026-01-01T00:25:00Z")
	_, _, scope, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReplayCurrentDeletionManifest(root, snapshot, scope, policy, now, time.Hour); err != nil {
		t.Fatal(err)
	}
	forged := cloneScope(t, scope)
	forged.Repositories[0].ProtectedMainSHA = strings.Repeat("9", 40)
	if _, err := ReplayCurrentDeletionManifest(root, snapshot, forged, policy, now, time.Hour); err == nil || !strings.Contains(err.Error(), "does not equal replay") {
		t.Fatalf("forged scope error=%v", err)
	}
	if _, err := ReplayCurrentDeletionManifest(root, snapshot, scope, policy, now.Add(2*time.Hour), time.Hour); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale replay error=%v", err)
	}
}

func TestDeletionExecutorPersistsIntentAndOutcomeInExactOrder(t *testing.T) {
	manifest := deletionTestManifest(t, 3)
	ids := deletionIDs(manifest)
	plan, err := PrepareDeletionPlan(deletionTestPreparation(manifest, []int64{ids[2], ids[0], ids[1]}))
	if err != nil {
		t.Fatal(err)
	}
	reader := &fakeDeletionReader{responses: repeatRead(len(plan.Records), CheckedArtifactRead{Outcome: ReadPresent, Exact: true})}
	deleter := &fakeArtifactDeleter{responses: repeatMutation(len(plan.Records), ArtifactMutation{Outcome: MutationSuccess, HTTPStatus: 204})}
	resultPath := filepath.Join(t.TempDir(), "result.jsonl")
	clock := deletionTestClock()
	result, err := ExecuteDeletionBatch(context.Background(), plan, reader, deleter, resultPath, clock)
	if err != nil || result.Status != "complete" || result.AttemptCount != 3 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if !reflect.DeepEqual(deleter.ids, []int64{ids[2], ids[0], ids[1]}) || len(reader.ids) != 3 {
		t.Fatalf("delete order=%v read order=%v", deleter.ids, reader.ids)
	}
	prior, err := LoadPriorDeletionResultFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(prior.Intents) != 3 || len(prior.Records) != 3 || prior.Footer.Status != "complete" {
		t.Fatalf("prior result=%+v", prior)
	}
	for index, artifactID := range []int64{ids[2], ids[0], ids[1]} {
		if prior.Intents[index].ArtifactID != artifactID || prior.Records[index].ArtifactID != artifactID || prior.Records[index].TerminalState != TerminalDeleted {
			t.Fatalf("journal order mismatch at %d: %+v %+v", index, prior.Intents[index], prior.Records[index])
		}
	}
	before, _ := os.ReadFile(resultPath)
	reader.responses = repeatRead(3, CheckedArtifactRead{Outcome: ReadPresent, Exact: true})
	if _, err := ExecuteDeletionBatch(context.Background(), plan, reader, deleter, resultPath, clock); err == nil {
		t.Fatal("executor clobbered an existing result")
	}
	after, _ := os.ReadFile(resultPath)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("no-clobber failure changed result bytes")
	}
}

func TestDeletionExecutorNeverRetriesAndStopsOnFirstUncertainty(t *testing.T) {
	manifest := deletionTestManifest(t, 3)
	ids := deletionIDs(manifest)
	plan, err := PrepareDeletionPlan(deletionTestPreparation(manifest, ids[:3]))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		mutation  ArtifactMutation
		readAfter CheckedArtifactRead
		terminal  string
		status    string
	}{
		{"ambiguous absent", ArtifactMutation{Outcome: MutationAmbiguous, ErrorCode: "TRANSPORT_FAILED"}, CheckedArtifactRead{Outcome: ReadAbsent}, TerminalAbsentAfterAmbiguous, "stopped_after_absence"},
		{"ambiguous present", ArtifactMutation{Outcome: MutationAmbiguous, ErrorCode: "TRANSPORT_FAILED"}, CheckedArtifactRead{Outcome: ReadPresent, Exact: true}, TerminalUncertain, "stopped_uncertain"},
		{"ambiguous unknown", ArtifactMutation{Outcome: MutationAmbiguous, ErrorCode: "TRANSPORT_FAILED"}, CheckedArtifactRead{Outcome: ReadUnknown, ErrorCode: "READ_FAILED"}, TerminalUncertain, "stopped_uncertain"},
		{"http error absent", ArtifactMutation{Outcome: MutationHTTPError, HTTPStatus: 404, ErrorCode: "REMOTE_NOT_FOUND"}, CheckedArtifactRead{Outcome: ReadAbsent}, TerminalUncertain, "stopped_uncertain"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &fakeDeletionReader{responses: append(repeatRead(3, CheckedArtifactRead{Outcome: ReadPresent, Exact: true}), test.readAfter)}
			deleter := &fakeArtifactDeleter{responses: []ArtifactMutation{test.mutation, {Outcome: MutationSuccess, HTTPStatus: 204}}}
			path := filepath.Join(t.TempDir(), "result.jsonl")
			result, err := ExecuteDeletionBatch(context.Background(), plan, reader, deleter, path, deletionTestClock())
			if !errors.Is(err, ErrDeletionBatchStopped) || result.Status != test.status || result.AttemptCount != 1 {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if len(deleter.ids) != 1 || len(reader.ids) != 4 {
				t.Fatalf("mutation IDs=%v read IDs=%v", deleter.ids, reader.ids)
			}
			prior, loadErr := LoadPriorDeletionResultFile(path)
			if loadErr != nil || len(prior.Records) != 1 || prior.Records[0].TerminalState != test.terminal || prior.Records[0].ReadAfterOutcome != test.readAfter.Outcome {
				t.Fatalf("prior=%+v loadErr=%v", prior, loadErr)
			}
		})
	}
}

func TestDeletionExecutorRejectsAbsentOrDriftedPreflightWithoutMutation(t *testing.T) {
	manifest := deletionTestManifest(t, 1)
	id := deletionIDs(manifest)[0]
	plan, err := PrepareDeletionPlan(deletionTestPreparation(manifest, []int64{id}))
	if err != nil {
		t.Fatal(err)
	}
	for name, response := range map[string]CheckedArtifactRead{
		"absent":  {Outcome: ReadAbsent},
		"drifted": {Outcome: ReadPresent, Exact: false},
		"unknown": {Outcome: ReadUnknown, ErrorCode: "READ_FAILED"},
	} {
		t.Run(name, func(t *testing.T) {
			reader := &fakeDeletionReader{responses: []CheckedArtifactRead{response}}
			deleter := &fakeArtifactDeleter{}
			path := filepath.Join(t.TempDir(), "result.jsonl")
			if _, err := ExecuteDeletionBatch(context.Background(), plan, reader, deleter, path, deletionTestClock()); err == nil {
				t.Fatal("preflight failure was accepted")
			}
			if len(deleter.ids) != 0 {
				t.Fatalf("preflight failure mutated IDs %v", deleter.ids)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("preflight failure published a journal: %v", err)
			}
		})
	}
}

func TestDeletionProofCannotUseHistoricalValidationTimeOrExpireMidBatch(t *testing.T) {
	manifest := deletionTestManifest(t, 2)
	ids := deletionIDs(manifest)
	expired := deletionTestPreparation(manifest, ids[:1])
	expired.ValidatedAt = expired.ProofExpiresAt
	if _, err := PrepareDeletionPlan(expired); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("historical validation error=%v", err)
	}

	input := deletionTestPreparation(manifest, ids[:2])
	plan, err := PrepareDeletionPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	before := plan.ProofExpiresAt.Add(-time.Second)
	clockCalls := 0
	clock := func() time.Time {
		clockCalls++
		if clockCalls >= 9 {
			return plan.ProofExpiresAt
		}
		return before
	}
	reader := &fakeDeletionReader{responses: repeatRead(2, CheckedArtifactRead{Outcome: ReadPresent, Exact: true})}
	deleter := &fakeArtifactDeleter{responses: repeatMutation(2, ArtifactMutation{Outcome: MutationSuccess, HTTPStatus: 204})}
	result, err := ExecuteDeletionBatch(context.Background(), plan, reader, deleter, filepath.Join(t.TempDir(), "expires.jsonl"), clock)
	if err == nil || !strings.Contains(err.Error(), "expired") || result.Status != "proof_expired" || len(deleter.ids) != 1 {
		t.Fatalf("result=%+v error=%v mutation IDs=%v clockCalls=%d", result, err, deleter.ids, clockCalls)
	}
}

func TestDeletionPriorResultChainSupportsLaterBatchAndRejectsForkOrUnresolved(t *testing.T) {
	manifest := deletionTestManifest(t, 3)
	ids := deletionIDs(manifest)
	current := cloneDecisionManifest(t, manifest)

	first := executeDeletionTestResult(t, deletionTestPreparation(manifest, []int64{ids[0]}), nil, ArtifactMutation{Outcome: MutationSuccess, HTTPStatus: 204}, CheckedArtifactRead{})
	removeDeletionRecord(t, &current, ids[0])
	secondInput := deletionTestPreparation(manifest, []int64{ids[1]})
	secondInput.CurrentManifest = current
	secondInput.PriorResults = []PriorDeletionResult{first}
	second := executeDeletionTestResult(t, secondInput, []PriorDeletionResult{first}, ArtifactMutation{Outcome: MutationAmbiguous, ErrorCode: "TRANSPORT_FAILED"}, CheckedArtifactRead{Outcome: ReadAbsent})
	removeDeletionRecord(t, &current, ids[1])

	third := deletionTestPreparation(manifest, []int64{ids[2]})
	third.CurrentManifest = current
	third.PriorResults = []PriorDeletionResult{first, second}
	if _, err := PrepareDeletionPlan(third); err != nil {
		t.Fatalf("valid chained prior results rejected: %v", err)
	}

	fork := second
	fork.Header.PriorResultSHA256s = nil
	fork.SHA256 = mustPriorSHA(t, fork)
	third.PriorResults = []PriorDeletionResult{first, fork}
	if _, err := PrepareDeletionPlan(third); err == nil || !strings.Contains(err.Error(), "cumulative result chain") {
		t.Fatalf("forked chain error=%v", err)
	}

	unresolvedInput := deletionTestPreparation(manifest, []int64{ids[0]})
	unresolved := executeDeletionTestResult(t, unresolvedInput, nil, ArtifactMutation{Outcome: MutationAmbiguous, ErrorCode: "TRANSPORT_FAILED"}, CheckedArtifactRead{Outcome: ReadPresent, Exact: true})
	unresolvedCurrent := cloneDecisionManifest(t, manifest)
	third = deletionTestPreparation(manifest, []int64{ids[1]})
	third.CurrentManifest = unresolvedCurrent
	third.PriorResults = []PriorDeletionResult{unresolved}
	if _, err := PrepareDeletionPlan(third); err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("unresolved prior result error=%v", err)
	}
}

func TestDeletionPriorResultRejectsContinuationAfterAmbiguousAbsence(t *testing.T) {
	manifest := deletionTestManifest(t, 2)
	ids := deletionIDs(manifest)
	result := executeDeletionTestResult(t, deletionTestPreparation(manifest, ids[:2]), nil, ArtifactMutation{Outcome: MutationSuccess, HTTPStatus: 204}, CheckedArtifactRead{})
	result.Records[0].MutationOutcome = MutationAmbiguous
	result.Records[0].MutationHTTPStatus = 0
	result.Records[0].MutationErrorCode = "TRANSPORT_FAILED"
	result.Records[0].ReadAfterOutcome = ReadAbsent
	result.Records[0].TerminalState = TerminalAbsentAfterAmbiguous
	result.SHA256 = mustPriorSHA(t, result)
	if err := validatePriorDeletionResult(result, manifest); err == nil || !strings.Contains(err.Error(), "impossible continuation") {
		t.Fatalf("forged post-stop continuation error=%v", err)
	}
}

func TestDeletionPriorLoaderTreatsSyncedUnmatchedIntentAsIncomplete(t *testing.T) {
	manifest := deletionTestManifest(t, 1)
	id := deletionIDs(manifest)[0]
	plan, err := PrepareDeletionPlan(deletionTestPreparation(manifest, []int64{id}))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "crash.jsonl")
	journal, err := openDeletionJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	header := DeletionResultHeader{SchemaID: DeletionResultSchemaID, SchemaVersion: 1, RecordType: "header", AuthorizedManifestSHA256: plan.AuthorizedManifestSHA256, AuthorizedDeleteCount: plan.AuthorizedDeleteCount, AuthorizedDeleteBytes: plan.AuthorizedDeleteBytes, BatchArtifactIDs: []int64{id}, PriorResultSHA256s: []string{}, StartedAt: "2026-01-01T01:00:00Z", ProofExpiresAt: plan.ProofExpiresAt.Format(time.RFC3339Nano)}
	record := plan.Records[0]
	intent := DeletionResultIntent{SchemaID: DeletionResultSchemaID, SchemaVersion: 1, RecordType: "intent", ArtifactID: id, Repository: record.Repository, ProducerRunID: record.ProducerRunID, ProducerRunAttempt: record.ProducerRunAttempt, WorkflowPath: record.WorkflowPath, HeadSHA: record.HeadSHA, SizeInBytes: record.SizeInBytes, ReasonCode: record.ReasonCode, IntendedAt: "2026-01-01T01:00:01Z"}
	if err := journal.Append(header); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(intent); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	prior, err := LoadPriorDeletionResultFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !prior.Incomplete || len(prior.Intents) != 1 || len(prior.Records) != 0 {
		t.Fatalf("incomplete prior=%+v", prior)
	}
	input := deletionTestPreparation(manifest, []int64{id})
	input.PriorResults = []PriorDeletionResult{prior}
	if _, err := PrepareDeletionPlan(input); err == nil {
		t.Fatal("unmatched intent was accepted as prior terminal evidence")
	}
}

type fakeDeletionReader struct {
	responses []CheckedArtifactRead
	ids       []int64
}

func (fake *fakeDeletionReader) ReadArtifact(_ context.Context, record DecisionRecord) CheckedArtifactRead {
	fake.ids = append(fake.ids, record.ArtifactID)
	if len(fake.responses) == 0 {
		return CheckedArtifactRead{Outcome: ReadUnknown, ErrorCode: "UNSCRIPTED"}
	}
	response := fake.responses[0]
	fake.responses = fake.responses[1:]
	return response
}

type fakeArtifactDeleter struct {
	responses []ArtifactMutation
	ids       []int64
}

func (fake *fakeArtifactDeleter) DeleteArtifact(_ context.Context, record DecisionRecord) ArtifactMutation {
	fake.ids = append(fake.ids, record.ArtifactID)
	if len(fake.responses) == 0 {
		return ArtifactMutation{Outcome: MutationAmbiguous, ErrorCode: "UNSCRIPTED"}
	}
	response := fake.responses[0]
	fake.responses = fake.responses[1:]
	return response
}

func deletionTestManifest(t *testing.T, deleteCount int) DecisionManifest {
	t.Helper()
	policy := loadCanonicalPolicy(t)
	snapshot := validClassifierSnapshot(t, policy)
	now := mustTime(t, "2026-01-01T00:25:00Z")
	scope := validDecisionScope(t, snapshot, policy)
	manifest, err := Classify(snapshot, scope, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for index := range manifest.Records {
		if index < deleteCount {
			manifest.Records[index].Decision = DecisionDelete
			manifest.Records[index].ReasonCode = ReasonDeleteSuperseded
			manifest.Records[index].Lifecycle = LifecycleSupersededEligible
		} else {
			manifest.Records[index].Decision = DecisionKeep
			if manifest.Records[index].ReasonCode == ReasonDeleteSuperseded {
				manifest.Records[index].ReasonCode = ReasonKeepNotProven
			}
		}
	}
	rebuildDeletionManifest(t, &manifest)
	return manifest
}

func rebuildDeletionManifest(t *testing.T, manifest *DecisionManifest) {
	t.Helper()
	manifest.Totals = DecisionTotals{}
	for _, record := range manifest.Records {
		if err := addTotals(&manifest.Totals.Before, record.SizeInBytes); err != nil {
			t.Fatal(err)
		}
		if record.Decision == DecisionDelete {
			if err := addTotals(&manifest.Totals.Delete, record.SizeInBytes); err != nil {
				t.Fatal(err)
			}
		} else if err := addTotals(&manifest.Totals.Keep, record.SizeInBytes); err != nil {
			t.Fatal(err)
		}
	}
	manifest.Totals.ExpectedAfter = manifest.Totals.Keep
	digest, err := decisionManifestSemanticSHA256(*manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.SemanticSHA256 = digest
}

func deletionTestPreparation(manifest DecisionManifest, ids []int64) DeletionPreparation {
	return DeletionPreparation{
		AuthorizedManifest: cloneDecisionManifestWithoutTest(manifest), AuthorizedManifestSHA256: manifest.SemanticSHA256,
		AuthorizedDeleteCount: manifest.Totals.Delete.Count, AuthorizedDeleteBytes: manifest.Totals.Delete.Bytes,
		Confirmation:    ExactDeletionConfirmation(manifest.Totals.Delete.Count, manifest.Totals.Delete.Bytes, manifest.SemanticSHA256),
		Batch:           DeletionBatch{SchemaID: DeletionBatchSchemaID, SchemaVersion: DeletionBatchSchemaVersion, ArtifactIDs: append([]int64(nil), ids...)},
		CurrentManifest: cloneDecisionManifestWithoutTest(manifest),
		ValidatedAt:     mustTimeNoTest("2026-01-01T00:25:00Z"),
		ProofExpiresAt:  mustTimeNoTest("2026-01-01T02:00:00Z"),
	}
}

func deletionIDs(manifest DecisionManifest) []int64 {
	var ids []int64
	for _, record := range manifest.Records {
		if record.Decision == DecisionDelete {
			ids = append(ids, record.ArtifactID)
		}
	}
	return ids
}

func removeDeletionRecord(t *testing.T, manifest *DecisionManifest, artifactID int64) {
	t.Helper()
	for index, record := range manifest.Records {
		if record.ArtifactID == artifactID {
			manifest.Records = append(manifest.Records[:index], manifest.Records[index+1:]...)
			rebuildDeletionManifest(t, manifest)
			return
		}
	}
	t.Fatalf("artifact %d not found", artifactID)
}

func cloneDeletionPreparation(t *testing.T, value DeletionPreparation) DeletionPreparation {
	t.Helper()
	data, _ := json.Marshal(value)
	var clone DeletionPreparation
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func cloneDecisionManifest(t *testing.T, value DecisionManifest) DecisionManifest {
	t.Helper()
	return cloneDecisionManifestWithoutTest(value)
}

func cloneDecisionManifestWithoutTest(value DecisionManifest) DecisionManifest {
	data, _ := json.Marshal(value)
	var clone DecisionManifest
	_ = json.Unmarshal(data, &clone)
	return clone
}

func repeatRead(count int, value CheckedArtifactRead) []CheckedArtifactRead {
	result := make([]CheckedArtifactRead, count)
	for index := range result {
		result[index] = value
	}
	return result
}

func repeatMutation(count int, value ArtifactMutation) []ArtifactMutation {
	result := make([]ArtifactMutation, count)
	for index := range result {
		result[index] = value
	}
	return result
}

func deletionTestClock() func() time.Time {
	current := mustTimeNoTest("2026-01-01T01:00:00Z")
	return func() time.Time {
		result := current
		current = current.Add(time.Second)
		return result
	}
}

func mustTimeNoTest(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func executeDeletionTestResult(t *testing.T, input DeletionPreparation, prior []PriorDeletionResult, mutation ArtifactMutation, readAfter CheckedArtifactRead) PriorDeletionResult {
	t.Helper()
	input.PriorResults = prior
	plan, err := PrepareDeletionPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	reads := repeatRead(len(plan.Records), CheckedArtifactRead{Outcome: ReadPresent, Exact: true})
	if mutation.Outcome != MutationSuccess {
		reads = append(reads, readAfter)
	}
	path := filepath.Join(t.TempDir(), "result.jsonl")
	_, executionErr := ExecuteDeletionBatch(context.Background(), plan, &fakeDeletionReader{responses: reads}, &fakeArtifactDeleter{responses: repeatMutation(len(plan.Records), mutation)}, path, deletionTestClock())
	if mutation.Outcome == MutationSuccess && executionErr != nil {
		t.Fatal(executionErr)
	}
	if mutation.Outcome != MutationSuccess && !errors.Is(executionErr, ErrDeletionBatchStopped) {
		t.Fatalf("execution error=%v", executionErr)
	}
	result, err := LoadPriorDeletionResultFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustPriorSHA(t *testing.T, result PriorDeletionResult) string {
	t.Helper()
	digest, err := priorDeletionResultSHA256(result)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
