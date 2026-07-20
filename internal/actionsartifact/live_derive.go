package actionsartifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

// DeriveLiveDecisionScope is the single offline Stage 3 replay. It distrusts
// the live collection directory, reconstructs the authoritative observation,
// proves that the operational recovery is closed, and derives both immutable
// keep authority and positive exact supersession authority.
func DeriveLiveDecisionScope(collectionDirectory string, snapshot Snapshot, policy Policy, now time.Time, maxAge time.Duration) (LiveObservation, RepairProof, DecisionScope, error) {
	if err := ValidateSnapshot(snapshot, policy, now, maxAge); err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, err
	}
	root, err := validateCollectionRoot(collectionDirectory)
	if err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, err
	}
	indexBudget := &rawReadBudget{}
	indexData, err := indexBudget.read(filepath.Join(root, "collection.json"), MaxRawPageBytes)
	if err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("read live collection index: %w", err)
	}
	var collection LiveRawCollection
	if err := strictjson.Decode(indexData, MaxRawPageBytes, &collection); err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("decode live collection index: %w", err)
	}
	if err := collection.Validate(); err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, err
	}
	var rawBytes int64
	for _, proof := range collection.Files {
		rawBytes += proof.Bytes
	}
	if err := ValidateLiveCollectionByteBudget(rawBytes, int64(len(indexData))); err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, err
	}
	snapshotDigest, err := snapshotSemanticSHA256(snapshot)
	if err != nil || collection.SnapshotSemanticSHA256 != snapshotDigest {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, errors.New("live collection snapshot digest mismatch")
	}
	started, _ := parseCanonicalTime(collection.ObservedStartedAt)
	finished, _ := parseCanonicalTime(collection.ObservedFinishedAt)
	snapshotFinished, _ := parseCanonicalTime(snapshot.ObservedFinishedAt)
	if started.Before(snapshotFinished) || finished.After(now) || now.Sub(finished) > maxAge {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, errors.New("live collection is stale, future-dated, or began before snapshot completion")
	}
	if len(collection.Repositories) != len(snapshot.Repositories) {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, errors.New("live collection repositories do not exactly cover the snapshot")
	}
	for index := range collection.Repositories {
		if collection.Repositories[index].Repository != snapshot.Repositories[index].Repository || collection.Repositories[index].RepositoryID != snapshot.Repositories[index].RepositoryID {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, errors.New("live collection repository identity differs from the snapshot")
		}
	}
	expectedFiles := expectedLiveRawFiles(collection)
	if err := validateLiveRawTree(root, collection, expectedFiles); err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, err
	}
	reader := newLiveRawReader(root, collection.Files)
	reader.bytes = int64(len(indexData))

	collectionDigest, err := semanticSHA256(collection)
	if err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, err
	}
	observation := LiveObservation{
		SchemaID: LiveObservationSchemaID, SchemaVersion: LiveObservationSchemaVersion,
		ObservedStartedAt: collection.ObservedStartedAt, ObservedFinishedAt: collection.ObservedFinishedAt,
		SnapshotSemanticSHA256: snapshotDigest, RawCollectionSemanticSHA256: collectionDigest,
		ReleaseRepository: collection.ReleaseRepository,
	}

	snapshotAttempts := make(map[string]SnapshotAttempt, len(snapshot.Attempts))
	for _, attempt := range snapshot.Attempts {
		snapshotAttempts[attemptKey(attempt.Repository, attempt.RunID, attempt.RunAttempt)] = attempt
	}
	snapshotRuns := make(map[string]SnapshotRun, len(snapshot.Runs))
	for _, run := range snapshot.Runs {
		snapshotRuns[producerKey(run.Repository, run.RunID)] = run
	}

	var contract releasecontract.Contract
	contractLoaded := false
	for _, repository := range collection.Repositories {
		prefix := repository.Directory
		metadataData, err := reader.readStablePair(prefix+"/repository.json", prefix+"/repository-final.json")
		if err != nil {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q metadata: %w", repository.Repository, err)
		}
		metadata, err := InspectLiveRepositoryResponse(metadataData)
		if err != nil || metadata.RepositoryID != repository.RepositoryID || metadata.FullName != repository.Repository || metadata.DefaultBranch != repository.DefaultBranch {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q metadata identity mismatch", repository.Repository)
		}
		refData, err := reader.readStablePair(prefix+"/default-ref.json", prefix+"/default-ref-final.json")
		if err != nil {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q default ref: %w", repository.Repository, err)
		}
		ref, err := InspectGitRefResponse(refData)
		if err != nil || ref.Ref != "refs/heads/"+repository.DefaultBranch || ref.ObjectType != "commit" || !shaPattern.MatchString(ref.ObjectSHA) {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q default ref identity mismatch", repository.Repository)
		}
		branchData, err := reader.readStablePair(prefix+"/default-branch.json", prefix+"/default-branch-final.json")
		if err != nil {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q default branch: %w", repository.Repository, err)
		}
		branch, err := InspectBranchResponse(branchData)
		if err != nil || branch.Name != repository.DefaultBranch || branch.CommitSHA != ref.ObjectSHA || !branch.Protected {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q default ref/branch mismatch or branch is unprotected", repository.Repository)
		}

		liveRepository := LiveRepositoryObservation{
			Repository: repository.Repository, RepositoryID: repository.RepositoryID,
			DefaultBranch: repository.DefaultBranch, ProtectedDefaultBranchSHA: ref.ObjectSHA,
			OpenPullRequests: make([]LivePullRequest, 0), Runs: make([]SnapshotRun, 0), Attempts: make([]SnapshotAttempt, 0),
		}

		pullRequestsByNumber := make(map[int64]LivePullRequest, repository.PullRequests.ItemCount)
		for page := 1; page <= repository.PullRequests.PageCount; page++ {
			original := fmt.Sprintf("%s/pull-requests/pages/page-%03d.json", prefix, page)
			final := fmt.Sprintf("%s/pull-requests/pages/page-%03d-final.json", prefix, page)
			data, err := reader.readStablePair(original, final)
			if err != nil {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q pull-request page %d: %w", repository.Repository, page, err)
			}
			if digestBytes(data) != repository.PullRequests.PageSHA256[page-1] || repository.PullRequests.PageSHA256[page-1] != repository.PullRequests.FinalPageSHA256[page-1] {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q pull-request page %d digest mismatch", repository.Repository, page)
			}
			values, err := InspectPullRequestPage(data, repository.Repository, repository.RepositoryID)
			if err != nil || len(values) != repository.PullRequests.PageItemCounts[page-1] {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q pull-request page %d is malformed", repository.Repository, page)
			}
			for _, pullRequest := range values {
				if _, exists := pullRequestsByNumber[pullRequest.Number]; exists {
					return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q has duplicate open pull request %d", repository.Repository, pullRequest.Number)
				}
				pullRequestsByNumber[pullRequest.Number] = pullRequest
			}
		}
		if len(pullRequestsByNumber) != repository.PullRequests.ItemCount {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q open pull-request count does not reconcile", repository.Repository)
		}
		for _, number := range repository.PullRequestNumbers {
			listed, exists := pullRequestsByNumber[number]
			if !exists {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q exact pull request %d is missing from complete pages", repository.Repository, number)
			}
			original := fmt.Sprintf("%s/pull-requests/exact/pr-%019d.json", prefix, number)
			final := fmt.Sprintf("%s/pull-requests/exact/pr-%019d-final.json", prefix, number)
			data, err := reader.readStablePair(original, final)
			if err != nil {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q exact pull request %d: %w", repository.Repository, number, err)
			}
			exact, err := InspectExactPullRequest(data, repository.Repository, repository.RepositoryID, number)
			if err != nil || !reflect.DeepEqual(listed, exact) {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q exact pull request %d differs from its complete-list projection", repository.Repository, number)
			}
			liveRepository.OpenPullRequests = append(liveRepository.OpenPullRequests, exact)
		}

		liveRunsByID := make(map[int64]SnapshotRun, repository.Runs.TotalCount)
		for page := 1; page <= repository.Runs.PageCount; page++ {
			original := fmt.Sprintf("%s/runs/page-%03d.json", prefix, page)
			final := fmt.Sprintf("%s/runs/page-%03d-final.json", prefix, page)
			data, err := reader.readStablePair(original, final)
			if err != nil {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q run page %d: %w", repository.Repository, page, err)
			}
			if digestBytes(data) != repository.Runs.PageSHA256[page-1] || repository.Runs.PageSHA256[page-1] != repository.Runs.FinalPageSHA256[page-1] {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q run page %d digest mismatch", repository.Repository, page)
			}
			total, messages, err := parsePageEnvelope(data, "workflow_runs")
			if err != nil || total != repository.Runs.TotalCount || len(messages) != repository.Runs.PageItemCounts[page-1] {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q run page %d is malformed or incomplete", repository.Repository, page)
			}
			for runIndex, message := range messages {
				run, observedRepositoryID, err := parseRunDocument(message, repository.Repository)
				if err != nil || observedRepositoryID != repository.RepositoryID {
					return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q run page %d item %d identity mismatch", repository.Repository, page, runIndex)
				}
				if run.Status != "completed" || strings.TrimSpace(run.Conclusion) == "" {
					return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q has active or incomplete run %d", repository.Repository, run.RunID)
				}
				if _, exists := liveRunsByID[run.RunID]; exists {
					return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q has duplicate run %d", repository.Repository, run.RunID)
				}
				liveRunsByID[run.RunID] = run
				liveRepository.Runs = append(liveRepository.Runs, run)
			}
		}
		if len(liveRunsByID) != repository.Runs.TotalCount {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q run count does not reconcile", repository.Repository)
		}
		sort.Slice(liveRepository.Runs, func(i, j int) bool { return liveRepository.Runs[i].RunID < liveRepository.Runs[j].RunID })

		expectedDocuments := make([]CollectedAttemptDocument, 0)
		for _, attempt := range snapshot.Attempts {
			if attempt.Repository == repository.Repository {
				expectedDocuments = append(expectedDocuments, CollectedAttemptDocument{RunID: attempt.RunID, RunAttempt: attempt.RunAttempt})
			}
		}
		if !reflect.DeepEqual(repository.AttemptDocuments, expectedDocuments) {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q live attempt list does not exactly cover snapshot producer attempts", repository.Repository)
		}
		for _, document := range repository.AttemptDocuments {
			original := fmt.Sprintf("%s/attempts/run-%019d-attempt-%02d.json", prefix, document.RunID, document.RunAttempt)
			final := fmt.Sprintf("%s/attempts/run-%019d-attempt-%02d-final.json", prefix, document.RunID, document.RunAttempt)
			data, err := reader.readStablePair(original, final)
			if err != nil {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q run %d attempt %d: %w", repository.Repository, document.RunID, document.RunAttempt, err)
			}
			attempt, observedRepositoryID, err := parseAttemptDocument(data, repository.Repository)
			expected := snapshotAttempts[attemptKey(repository.Repository, document.RunID, document.RunAttempt)]
			if err != nil || observedRepositoryID != repository.RepositoryID || !reflect.DeepEqual(attempt, expected) {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q run %d attempt %d differs from the snapshot exact attempt", repository.Repository, document.RunID, document.RunAttempt)
			}
			liveRepository.Attempts = append(liveRepository.Attempts, attempt)
		}
		expectedRunCount := 0
		for _, snapshotRun := range snapshot.Runs {
			if snapshotRun.Repository == repository.Repository {
				expectedRunCount++
			}
		}
		if len(liveRunsByID) != expectedRunCount {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q complete live run set differs from the snapshot", repository.Repository)
		}
		for runKey, snapshotRun := range snapshotRuns {
			if snapshotRun.Repository != repository.Repository {
				continue
			}
			liveRun, exists := liveRunsByID[snapshotRun.RunID]
			if !exists || !reflect.DeepEqual(liveRun, snapshotRun) {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("snapshot producer/current run %q changed or disappeared", runKey)
			}
		}

		if repository.Repository == collection.ReleaseRepository {
			contractData, err := reader.readStablePair(prefix+"/contract-content.json", prefix+"/contract-content-final.json")
			if err != nil {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q operational contract: %w", repository.Repository, err)
			}
			contractContent, err := InspectContractContentsResponse(contractData, releasecontract.CanonicalPath)
			if err != nil {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q operational contract response: %w", repository.Repository, err)
			}
			contract, err = releasecontract.LoadBytes(contractContent.Content)
			if err != nil {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q protected-main operational contract: %w", repository.Repository, err)
			}
			contractSemantic, err := releasecontract.SemanticSHA256(contract)
			if err != nil {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, err
			}
			workflowIDs := append([]string(nil), contract.Concurrency.Release.Workflows...)
			actionIDs := make([]string, 0, len(contract.AllowedRepairActions))
			for _, action := range contract.AllowedRepairActions {
				actionIDs = append(actionIDs, action.ID)
			}
			observation.OperationalContract = OperationalContractProof{
				Repository: repository.Repository, SourceSHA: ref.ObjectSHA, Path: releasecontract.CanonicalPath,
				BlobSHA: contractContent.BlobSHA, FileSHA256: contract.FileSHA256(), SemanticSHA256: contractSemantic,
				RecoveryState:    contract.VersionPolicy.ReleasePleaseRecovery.State,
				SourceRepository: contract.Repositories.Source.FullName, TapRepository: contract.Repositories.HomebrewTap.FullName,
				ReleaseConcurrencyWorkflows: workflowIDs, AllowedRepairActionIDs: actionIDs,
			}
			contractLoaded = true
		}
		observation.Repositories = append(observation.Repositories, liveRepository)
	}
	if !contractLoaded {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, errors.New("protected-main operational contract was not collected")
	}
	if contract.Repositories.Source.FullName != collection.ReleaseRepository {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, errors.New("designated release repository differs from the protected-main operational contract")
	}
	contractRepositories := map[string]string{
		contract.Repositories.Source.FullName:      contract.Repositories.Source.DefaultBranch,
		contract.Repositories.HomebrewTap.FullName: contract.Repositories.HomebrewTap.DefaultBranch,
	}
	if len(contractRepositories) != len(observation.Repositories) {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, errors.New("live repositories do not exactly match the protected-main operational contract")
	}
	for index := range observation.Repositories {
		repository := &observation.Repositories[index]
		defaultBranch, exists := contractRepositories[repository.Repository]
		if !exists || repository.DefaultBranch != defaultBranch {
			return LiveObservation{}, RepairProof{}, DecisionScope{}, fmt.Errorf("repository %q default branch differs from the protected-main operational contract", repository.Repository)
		}
		rawRepository := collection.Repositories[index]
		if repository.Repository == collection.ReleaseRepository {
			stable, err := assembleStableRelease(reader, rawRepository, repository.ProtectedDefaultBranchSHA, contract)
			if err != nil {
				return LiveObservation{}, RepairProof{}, DecisionScope{}, err
			}
			repository.StableRelease = stable
		} else {
			repository.StableRelease = LiveStableRelease{
				Designated: false, Enabled: false,
				Absence: &StableReleaseAbsenceProof{SchemaID: "env-vault.stable-release-not-designated.v1", SchemaVersion: 1, Repository: repository.Repository, ReasonCode: "NOT_DESIGNATED_RELEASE_REPOSITORY"},
				Assets:  make([]ReleaseAssetProjection, 0),
			}
		}
	}
	if reader.bytes > MaxLiveCollectionBytes {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, errors.New("live collection replay exceeded its byte budget")
	}
	observationDigest, err := liveObservationSemanticSHA256(observation)
	if err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, err
	}
	observation.SemanticSHA256 = observationDigest

	if observation.OperationalContract.RecoveryState != "complete" {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, errors.New("protected-main release recovery is not closed")
	}
	closed := true
	repair := RepairProof{
		SchemaID: RepairProofSchemaID, SchemaVersion: RepairProofSchemaVersion,
		ObservedAt: observation.ObservedFinishedAt, LiveObservationSemanticSHA256: observation.SemanticSHA256,
		ContractFileSHA256:     observation.OperationalContract.FileSHA256,
		ContractSemanticSHA256: observation.OperationalContract.SemanticSHA256,
		ContractSourceSHA:      observation.OperationalContract.SourceSHA,
		RecoveryState:          observation.OperationalContract.RecoveryState, Closed: &closed,
		ActiveRunIdentities: make([]ExactKeepIdentity, 0), Identities: make([]ExactKeepIdentity, 0),
		ReasonCode: "RECOVERY_COMPLETE_NO_ACTIVE_RUNS",
	}
	repairDigest, err := repairProofSemanticSHA256(repair)
	if err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, err
	}
	repair.SemanticSHA256 = repairDigest

	scope, err := deriveDecisionScope(snapshot, policy, observation, repair, now, maxAge)
	if err != nil {
		return LiveObservation{}, RepairProof{}, DecisionScope{}, err
	}
	return observation, repair, scope, nil
}

func assembleStableRelease(reader *liveRawReader, repository LiveRawCollectionRepository, protectedMainSHA string, contract releasecontract.Contract) (LiveStableRelease, error) {
	prefix := repository.Directory + "/release"
	latestEndpoint := "repos/" + repository.Repository + "/releases/latest"
	if repository.StableRelease.State == StableReleaseAbsent {
		data, err := reader.readStablePair(prefix+"/latest.json", prefix+"/latest-final.json")
		if err != nil {
			return LiveStableRelease{}, fmt.Errorf("repository %q latest stable absence: %w", repository.Repository, err)
		}
		absence, err := ParseStableReleaseAbsenceProof(data, repository.Repository, latestEndpoint)
		if err != nil {
			return LiveStableRelease{}, fmt.Errorf("repository %q latest stable absence: %w", repository.Repository, err)
		}
		return LiveStableRelease{Designated: true, Enabled: false, Absence: &absence, Assets: make([]ReleaseAssetProjection, 0)}, nil
	}
	latestData, err := reader.readStablePair(prefix+"/latest.json", prefix+"/latest-final.json")
	if err != nil {
		return LiveStableRelease{}, fmt.Errorf("repository %q latest stable Release: %w", repository.Repository, err)
	}
	latest, err := InspectReleaseResponse(latestData)
	if err != nil || latest.Draft || latest.Prerelease || latest.Version != repository.StableRelease.Version {
		return LiveStableRelease{}, fmt.Errorf("repository %q latest Release is malformed, draft, prerelease, or version-drifted", repository.Repository)
	}
	exactData, err := reader.readStablePair(prefix+"/exact.json", prefix+"/exact-final.json")
	if err != nil {
		return LiveStableRelease{}, fmt.Errorf("repository %q exact stable Release: %w", repository.Repository, err)
	}
	exact, err := InspectReleaseResponse(exactData)
	if err != nil || !releasesEqual(latest, exact) {
		return LiveStableRelease{}, fmt.Errorf("repository %q latest and exact stable Release projections differ", repository.Repository)
	}
	expectedAssets := append([]string(nil), contract.Assets...)
	observedAssets := make([]string, 0, len(latest.Assets))
	for _, asset := range latest.Assets {
		observedAssets = append(observedAssets, asset.Name)
	}
	sort.Strings(expectedAssets)
	sort.Strings(observedAssets)
	if !reflect.DeepEqual(expectedAssets, observedAssets) {
		return LiveStableRelease{}, fmt.Errorf("repository %q stable Release asset names differ from the protected-main contract", repository.Repository)
	}
	tagRefData, err := reader.readStablePair(prefix+"/tag-ref.json", prefix+"/tag-ref-final.json")
	if err != nil {
		return LiveStableRelease{}, fmt.Errorf("repository %q stable tag ref: %w", repository.Repository, err)
	}
	tagRef, err := InspectGitRefResponse(tagRefData)
	if err != nil || tagRef.Ref != "refs/tags/"+latest.Version {
		return LiveStableRelease{}, fmt.Errorf("repository %q stable tag ref/version mismatch", repository.Repository)
	}
	objectType, objectSHA := tagRef.ObjectType, tagRef.ObjectSHA
	seen := make(map[string]bool)
	depth := 0
	for objectType == "tag" {
		depth++
		if depth > MaxTagPeelDepth || depth > repository.StableRelease.TagObjectDocuments || seen[objectSHA] {
			return LiveStableRelease{}, fmt.Errorf("repository %q stable tag peel is cyclic, too deep, or incomplete", repository.Repository)
		}
		seen[objectSHA] = true
		original := fmt.Sprintf("%s/tag-object-%02d.json", prefix, depth)
		final := fmt.Sprintf("%s/tag-object-%02d-final.json", prefix, depth)
		data, err := reader.readStablePair(original, final)
		if err != nil {
			return LiveStableRelease{}, fmt.Errorf("repository %q stable tag object %d: %w", repository.Repository, depth, err)
		}
		requestedTagSHA := objectSHA
		object, err := InspectTagObjectResponse(data, requestedTagSHA)
		if err != nil {
			return LiveStableRelease{}, fmt.Errorf("repository %q stable tag object %d: %w", repository.Repository, depth, err)
		}
		objectType, objectSHA = object.ObjectType, object.ObjectSHA
	}
	if objectType != "commit" || !shaPattern.MatchString(objectSHA) || depth != repository.StableRelease.TagObjectDocuments {
		return LiveStableRelease{}, fmt.Errorf("repository %q stable tag does not fully peel to one commit", repository.Repository)
	}
	if shaPattern.MatchString(latest.TargetCommitish) && latest.TargetCommitish != objectSHA {
		return LiveStableRelease{}, fmt.Errorf("repository %q stable Release target/source mismatch", repository.Repository)
	}
	if !shaPattern.MatchString(latest.TargetCommitish) && latest.TargetCommitish != repository.DefaultBranch {
		return LiveStableRelease{}, fmt.Errorf("repository %q stable Release target is neither its source nor protected default branch", repository.Repository)
	}
	_ = protectedMainSHA // target may name the branch; its exact live SHA is independently bound above.
	return LiveStableRelease{
		Designated: true, Enabled: true, Absence: nil,
		ReleaseID: latest.ReleaseID, Version: latest.Version, SourceSHA: objectSHA,
		TargetCommitish: latest.TargetCommitish, PublishedAt: latest.PublishedAt,
		TagRefObjectType: tagRef.ObjectType, TagRefObjectSHA: tagRef.ObjectSHA, TagPeelDepth: depth,
		Assets: latest.Assets,
	}, nil
}

func deriveDecisionScope(snapshot Snapshot, policy Policy, observation LiveObservation, repair RepairProof, now time.Time, maxAge time.Duration) (DecisionScope, error) {
	policyDigest, err := CanonicalSHA256(policy)
	if err != nil {
		return DecisionScope{}, err
	}
	scope := DecisionScope{
		SchemaID: DecisionScopeSchemaID, SchemaVersion: DecisionScopeSchemaVersion,
		ObservedAt:             observation.ObservedFinishedAt,
		SnapshotSemanticSHA256: observation.SnapshotSemanticSHA256, PolicySemanticSHA256: policyDigest,
		LiveObservationSemanticSHA256: observation.SemanticSHA256, RepairProofSemanticSHA256: repair.SemanticSHA256,
	}
	for _, repository := range observation.Repositories {
		enabled := repository.StableRelease.Enabled
		repositoryScope := RepositoryDecisionScope{
			Repository: repository.Repository, ProtectedMainSHA: repository.ProtectedDefaultBranchSHA,
			StableRelease:            StableReleaseIdentity{Enabled: &enabled},
			OpenPullRequests:         make([]PullRequestHead, 0, len(repository.OpenPullRequests)),
			RepairBoundary:           RepairBoundary{Closed: repair.Closed, Identities: make([]ExactKeepIdentity, 0)},
			AdditionalKeepIdentities: make([]ExactKeepIdentity, 0), DeleteEligibleIdentities: make([]ExactKeepIdentity, 0),
			ImmutableKeepArtifactIDs: make([]int64, 0),
		}
		if enabled {
			repositoryScope.StableRelease.Version = repository.StableRelease.Version
			repositoryScope.StableRelease.SourceSHA = repository.StableRelease.SourceSHA
		}
		for _, pullRequest := range repository.OpenPullRequests {
			repositoryScope.OpenPullRequests = append(repositoryScope.OpenPullRequests, PullRequestHead{Number: pullRequest.Number, HeadSHA: pullRequest.HeadSHA})
		}
		if repository.Repository == observation.ReleaseRepository {
			repositoryScope.RepairBoundary.Identities = append(repositoryScope.RepairBoundary.Identities, repair.Identities...)
		}
		scope.Repositories = append(scope.Repositories, repositoryScope)
	}
	sortDecisionScope(&scope)
	for repositoryIndex := range scope.Repositories {
		repositoryScope := &scope.Repositories[repositoryIndex]
		openHeads := make(map[string]bool, len(repositoryScope.OpenPullRequests))
		for _, pullRequest := range repositoryScope.OpenPullRequests {
			openHeads[pullRequest.HeadSHA] = true
		}
		artifactsByAttempt := make(map[string][]SnapshotArtifact)
		for _, artifact := range snapshot.Artifacts {
			if artifact.Repository == repositoryScope.Repository {
				key := attemptKey(artifact.Repository, artifact.ProducerRunID, artifact.ProducerRunAttempt)
				artifactsByAttempt[key] = append(artifactsByAttempt[key], artifact)
			}
		}
		for _, attempt := range snapshot.Attempts {
			if attempt.Repository != repositoryScope.Repository {
				continue
			}
			artifacts := artifactsByAttempt[attemptKey(attempt.Repository, attempt.RunID, attempt.RunAttempt)]
			if len(artifacts) == 0 || attempt.Status != "completed" || strings.TrimSpace(attempt.Conclusion) == "" || attempt.HeadSHA == repositoryScope.ProtectedMainSHA || openHeads[attempt.HeadSHA] || (*repositoryScope.StableRelease.Enabled && attempt.HeadSHA == repositoryScope.StableRelease.SourceSHA) {
				continue
			}
			hasSuperseded, conflictsWithReferencedStable := false, false
			for _, artifact := range artifacts {
				if artifact.Lifecycle == LifecycleSupersededEligible {
					hasSuperseded = true
				}
				if *repositoryScope.StableRelease.Enabled && artifact.ReferencedSourceSHA == repositoryScope.StableRelease.SourceSHA {
					conflictsWithReferencedStable = true
				}
			}
			if hasSuperseded && !conflictsWithReferencedStable {
				repositoryScope.DeleteEligibleIdentities = append(repositoryScope.DeleteEligibleIdentities, ExactKeepIdentity{
					ProducerRunID: attempt.RunID, ProducerRunAttempt: attempt.RunAttempt,
					WorkflowPath: attempt.WorkflowPath, HeadSHA: attempt.HeadSHA,
				})
			}
		}
		sort.Slice(repositoryScope.DeleteEligibleIdentities, func(i, j int) bool {
			return compareExactKeepIdentity(repositoryScope.DeleteEligibleIdentities[i], repositoryScope.DeleteEligibleIdentities[j]) < 0
		})
		repositoryScope.ImmutableKeepArtifactIDs = immutableKeepArtifactIDs(snapshot, *repositoryScope)
	}
	if err := ValidateDecisionScope(scope, snapshot, now, maxAge); err != nil {
		return DecisionScope{}, err
	}
	return scope, nil
}

func liveObservationSemanticSHA256(observation LiveObservation) (string, error) {
	payload := observation
	payload.SemanticSHA256 = ""
	return semanticSHA256(payload)
}

func repairProofSemanticSHA256(proof RepairProof) (string, error) {
	payload := proof
	payload.SemanticSHA256 = ""
	return semanticSHA256(payload)
}

func expectedLiveRawFiles(collection LiveRawCollection) map[string]bool {
	expected := make(map[string]bool)
	addPair := func(original, final string) {
		expected[original] = true
		expected[final] = true
	}
	for _, repository := range collection.Repositories {
		prefix := repository.Directory
		addPair(prefix+"/repository.json", prefix+"/repository-final.json")
		addPair(prefix+"/default-ref.json", prefix+"/default-ref-final.json")
		addPair(prefix+"/default-branch.json", prefix+"/default-branch-final.json")
		for page := 1; page <= repository.PullRequests.PageCount; page++ {
			addPair(fmt.Sprintf("%s/pull-requests/pages/page-%03d.json", prefix, page), fmt.Sprintf("%s/pull-requests/pages/page-%03d-final.json", prefix, page))
		}
		for _, number := range repository.PullRequestNumbers {
			addPair(fmt.Sprintf("%s/pull-requests/exact/pr-%019d.json", prefix, number), fmt.Sprintf("%s/pull-requests/exact/pr-%019d-final.json", prefix, number))
		}
		for page := 1; page <= repository.Runs.PageCount; page++ {
			addPair(fmt.Sprintf("%s/runs/page-%03d.json", prefix, page), fmt.Sprintf("%s/runs/page-%03d-final.json", prefix, page))
		}
		for _, document := range repository.AttemptDocuments {
			addPair(fmt.Sprintf("%s/attempts/run-%019d-attempt-%02d.json", prefix, document.RunID, document.RunAttempt), fmt.Sprintf("%s/attempts/run-%019d-attempt-%02d-final.json", prefix, document.RunID, document.RunAttempt))
		}
		if repository.StableRelease.Designated {
			addPair(prefix+"/contract-content.json", prefix+"/contract-content-final.json")
			addPair(prefix+"/release/latest.json", prefix+"/release/latest-final.json")
			if repository.StableRelease.State != StableReleaseAbsent {
				addPair(prefix+"/release/exact.json", prefix+"/release/exact-final.json")
				addPair(prefix+"/release/tag-ref.json", prefix+"/release/tag-ref-final.json")
				for depth := 1; depth <= repository.StableRelease.TagObjectDocuments; depth++ {
					addPair(fmt.Sprintf("%s/release/tag-object-%02d.json", prefix, depth), fmt.Sprintf("%s/release/tag-object-%02d-final.json", prefix, depth))
				}
			}
		}
	}
	return expected
}

func validateLiveRawTree(root string, collection LiveRawCollection, expected map[string]bool) error {
	if len(expected) != len(collection.Files) {
		return fmt.Errorf("live collection files=%d want exactly %d", len(collection.Files), len(expected))
	}
	for _, proof := range collection.Files {
		if !expected[proof.Path] {
			return fmt.Errorf("live collection indexes unexpected raw file %q", proof.Path)
		}
	}
	seen := make(map[string]bool, len(expected)+1)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("live collection tree contains an unreadable entry or symlink")
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("live collection tree contains a non-regular file")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative != "collection.json" && !expected[relative] {
			return fmt.Errorf("live collection tree contains unexpected file %q", relative)
		}
		if seen[relative] {
			return fmt.Errorf("live collection tree contains duplicate path %q", relative)
		}
		seen[relative] = true
		return nil
	})
	if err != nil {
		return err
	}
	if !seen["collection.json"] || len(seen) != len(expected)+1 {
		return errors.New("live collection tree is incomplete")
	}
	return nil
}

type liveRawReader struct {
	root   string
	proofs map[string]RawFileProof
	bytes  int64
}

func newLiveRawReader(root string, proofs []RawFileProof) *liveRawReader {
	values := make(map[string]RawFileProof, len(proofs))
	for _, proof := range proofs {
		values[proof.Path] = proof
	}
	return &liveRawReader{root: root, proofs: values}
}

func (reader *liveRawReader) read(relative string) ([]byte, error) {
	proof, exists := reader.proofs[relative]
	if !exists {
		return nil, fmt.Errorf("raw file %q is not indexed", relative)
	}
	filename := filepath.Join(reader.root, filepath.FromSlash(relative))
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != proof.Bytes {
		return nil, fmt.Errorf("raw file %q is missing, wrong-type, or size-drifted", relative)
	}
	if reader.bytes > MaxLiveCollectionBytes-proof.Bytes {
		return nil, errors.New("live raw replay exceeds byte budget")
	}
	data, err := os.ReadFile(filename)
	if err != nil || int64(len(data)) != proof.Bytes || digestBytes(data) != proof.SHA256 {
		return nil, fmt.Errorf("raw file %q digest or byte count mismatch", relative)
	}
	reader.bytes += proof.Bytes
	return data, nil
}

func (reader *liveRawReader) readStablePair(original, final string) ([]byte, error) {
	left, err := reader.read(original)
	if err != nil {
		return nil, err
	}
	right, err := reader.read(final)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(left, right) {
		return nil, errors.New("global final reread differs byte-for-byte")
	}
	return left, nil
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func MarshalLiveObservationCanonical(observation LiveObservation) ([]byte, error) {
	return MarshalCanonical(observation)
}

func MarshalRepairProofCanonical(proof RepairProof) ([]byte, error) {
	return MarshalCanonical(proof)
}
