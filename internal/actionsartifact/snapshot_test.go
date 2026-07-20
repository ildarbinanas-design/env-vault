package actionsartifact

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSnapshotClassifierMultiAttemptLifecycleAndCanonicalReplay(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	snapshot := validClassifierSnapshot(t, policy)
	now := mustTime(t, "2026-01-01T00:25:00Z")
	if err := ValidateSnapshot(snapshot, policy, now, time.Hour); err != nil {
		t.Fatal(err)
	}
	closed := false
	enabled := true
	scope := DecisionScope{
		SchemaID: DecisionScopeSchemaID, SchemaVersion: DecisionScopeSchemaVersion, ObservedAt: "2026-01-01T00:24:00Z",
		Repositories: []RepositoryDecisionScope{{
			Repository: "example/env-vault", ProtectedMainSHA: strings.Repeat("c", 40),
			StableRelease:    StableReleaseIdentity{Enabled: &enabled, Version: "v1.2.3", SourceSHA: strings.Repeat("b", 40)},
			OpenPullRequests: []PullRequestHead{},
			RepairBoundary: RepairBoundary{Closed: &closed, Identities: []ExactKeepIdentity{{
				ProducerRunID: 100, ProducerRunAttempt: 2, WorkflowPath: ".github/workflows/ci.yml", HeadSHA: strings.Repeat("a", 40),
			}}},
			AdditionalKeepIdentities: []ExactKeepIdentity{{
				ProducerRunID: 100, ProducerRunAttempt: 3, WorkflowPath: ".github/workflows/ci.yml", HeadSHA: strings.Repeat("a", 40),
			}},
			DeleteEligibleIdentities: []ExactKeepIdentity{{
				ProducerRunID: 100, ProducerRunAttempt: 1, WorkflowPath: ".github/workflows/ci.yml", HeadSHA: strings.Repeat("a", 40),
			}},
		}},
	}
	bindTestScope(t, &scope, snapshot, policy)
	manifest, err := Classify(snapshot, scope, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Totals.Before != (ArtifactTotals{Count: 5, Bytes: 150}) ||
		manifest.Totals.Keep != (ArtifactTotals{Count: 4, Bytes: 140}) ||
		manifest.Totals.Delete != (ArtifactTotals{Count: 1, Bytes: 10}) ||
		manifest.Totals.ExpectedAfter != manifest.Totals.Keep {
		t.Fatalf("totals=%+v", manifest.Totals)
	}
	wantReasons := []string{ReasonDeleteSuperseded, ReasonKeepRepairInput, ReasonKeepAdditional, ReasonKeepDurable, ReasonKeepSystemManaged}
	for index, want := range wantReasons {
		if manifest.Records[index].ReasonCode != want {
			t.Fatalf("records[%d].reason=%q want=%q", index, manifest.Records[index].ReasonCode, want)
		}
	}
	second, err := Classify(snapshot, scope, policy, now, time.Hour)
	if err != nil || !reflect.DeepEqual(manifest, second) || len(manifest.SemanticSHA256) != 64 {
		t.Fatalf("nondeterministic manifest err=%v\nfirst=%+v\nsecond=%+v", err, manifest, second)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var reformatted DecisionManifest
	if err := json.Unmarshal(data, &reformatted); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDecisionManifest(reformatted, snapshot, scope, policy, now, time.Hour); err != nil {
		t.Fatalf("whitespace-independent replay: %v", err)
	}
}

func TestSnapshotAndManifestRejectAdversarialDrift(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	canonical := validClassifierSnapshot(t, policy)
	now := mustTime(t, "2026-01-01T00:25:00Z")
	tests := []struct {
		name    string
		mutate  func(*Snapshot)
		message string
		now     time.Time
	}{
		{"stale", func(*Snapshot) {}, "stale", mustTime(t, "2026-01-02T00:25:00Z")},
		{"active run", func(value *Snapshot) { value.Runs[0].Status = "in_progress" }, "active or incomplete", now},
		{"missing attempt", func(value *Snapshot) {
			value.Attempts = value.Attempts[1:]
			value.AttemptCount--
			value.Repositories[0].AttemptCount--
			value.Repositories[0].AttemptDocuments--
		}, "missing exact producer attempt", now},
		{"duplicate artifact id", func(value *Snapshot) { value.Artifacts[1].ArtifactID = value.Artifacts[0].ArtifactID }, "sorted and unique", now},
		{"producer path mismatch", func(value *Snapshot) { value.Artifacts[0].WorkflowPath = ".github/workflows/build-binaries.yml" }, "producer run/path/head mismatch", now},
		{"unknown lineage", func(value *Snapshot) { value.Artifacts[0].Name = "env-vault-not-reviewed-attempt-1" }, "unknown artifact lineage", now},
		{"outside attempt", func(value *Snapshot) {
			value.Artifacts[0].CreatedAt = "2026-01-01T00:04:00Z"
			value.Artifacts[0].UpdatedAt = "2026-01-01T00:04:01Z"
		}, "no producer attempt interval", now},
		{"negative size", func(value *Snapshot) { value.Artifacts[0].SizeInBytes = -1 }, "invalid repository/id/size", now},
		{"byte overflow", func(value *Snapshot) {
			value.Artifacts[0].SizeInBytes = math.MaxInt64
			value.Artifacts[1].SizeInBytes = 1
		}, "byte total overflow", now},
		{"pagination max int", func(value *Snapshot) { value.Repositories[0].Artifacts.TotalCount = math.MaxInt }, "bound", now},
		{"count drift", func(value *Snapshot) { value.Repositories[0].Artifacts.TotalCount++ }, "item count", now},
		{"expiry drift", func(value *Snapshot) { value.Artifacts[0].Expired = true }, "expired state", now},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cloneSnapshot(t, canonical)
			test.mutate(&value)
			err := ValidateSnapshot(value, policy, test.now, time.Hour)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v want substring %q", err, test.message)
			}
		})
	}
	if err := ValidateSnapshot(canonical, policy, now, MaxSnapshotAge+time.Nanosecond); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("oversized freshness window error=%v", err)
	}

	scope := validDecisionScope(t, canonical, policy)
	manifest, err := Classify(canonical, scope, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	overlap := manifest.Records[0]
	overlap.Decision = DecisionKeep
	manifest.Records = append(manifest.Records, overlap)
	if err := ValidateDecisionManifest(manifest, canonical, scope, policy, now, time.Hour); err == nil || !strings.Contains(err.Error(), "both keep and delete") {
		t.Fatalf("overlap error=%v", err)
	}
}

func TestValidateSnapshotAttemptTimestampSkewDoesNotWidenIntervals(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	canonical := validClassifierSnapshot(t, policy)
	now := mustTime(t, "2026-01-01T00:25:00Z")
	validate := func(t *testing.T, value Snapshot) error {
		t.Helper()
		return ValidateSnapshot(value, policy, now, time.Hour)
	}

	t.Run("attempt plus two second inversion accepted", func(t *testing.T) {
		value := cloneSnapshot(t, canonical)
		value.Attempts[0].CreatedAt = "2026-01-01T00:01:02Z"
		if err := validate(t, value); err != nil {
			t.Fatalf("exactly two seconds of attempt metadata skew was rejected: %v", err)
		}
	})

	t.Run("attempt beyond two second inversion rejected", func(t *testing.T) {
		value := cloneSnapshot(t, canonical)
		value.Attempts[0].CreatedAt = "2026-01-01T00:01:02.000000001Z"
		if err := validate(t, value); err == nil || !strings.Contains(err.Error(), "attempts[0] has invalid timestamps") {
			t.Fatalf("error=%v", err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{
			name: "run created after start",
			mutate: func(value *Snapshot) {
				value.Runs[0].CreatedAt = "2026-01-01T00:09:00.000000001Z"
			},
		},
		{
			name: "run start after updated",
			mutate: func(value *Snapshot) {
				value.Runs[0].RunStartedAt = "2026-01-01T00:11:00.000000001Z"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := cloneSnapshot(t, canonical)
			test.mutate(&value)
			if err := validate(t, value); err == nil || !strings.Contains(err.Error(), "runs[0] has invalid timestamps") {
				t.Fatalf("error=%v", err)
			}
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{
			name: "attempt created after updated",
			mutate: func(value *Snapshot) {
				value.Attempts[0].CreatedAt = "2026-01-01T00:01:02Z"
				value.Attempts[0].UpdatedAt = "2026-01-01T00:01:01Z"
			},
		},
		{
			name: "attempt start after updated",
			mutate: func(value *Snapshot) {
				value.Attempts[0].UpdatedAt = "2026-01-01T00:00:30Z"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := cloneSnapshot(t, canonical)
			test.mutate(&value)
			if err := validate(t, value); err == nil || !strings.Contains(err.Error(), "attempts[0] has invalid timestamps") {
				t.Fatalf("error=%v", err)
			}
		})
	}

	for _, test := range []struct {
		name  string
		start string
	}{
		{name: "equal attempt boundary", start: "2026-01-01T00:03:00Z"},
		{name: "overlapping attempt boundary", start: "2026-01-01T00:02:59.999999999Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := cloneSnapshot(t, canonical)
			value.Attempts[1].RunStartedAt = test.start
			if err := validate(t, value); err == nil || !strings.Contains(err.Error(), "overlapping or unordered attempt intervals") {
				t.Fatalf("error=%v", err)
			}
		})
	}

	t.Run("artifact before run started remains outside interval", func(t *testing.T) {
		value := cloneSnapshot(t, canonical)
		value.Attempts[0].CreatedAt = "2026-01-01T00:01:02Z"
		value.Artifacts[0].CreatedAt = "2026-01-01T00:00:59Z"
		value.Artifacts[0].UpdatedAt = "2026-01-01T00:01:01Z"
		if err := validate(t, value); err == nil || !strings.Contains(err.Error(), "no producer attempt interval") {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestDecisionScopeRequiresExplicitRepairBoundaryAndExactBinding(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	snapshot := validClassifierSnapshot(t, policy)
	valid := validDecisionScope(t, snapshot, policy)
	if err := ValidateDecisionScope(valid, snapshot, mustTime(t, "2026-01-01T00:25:00Z"), time.Hour); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		mutate  func(*DecisionScope)
		message string
	}{
		{"omitted closed", func(scope *DecisionScope) { scope.Repositories[0].RepairBoundary.Closed = nil }, "must be explicit"},
		{"omitted stable enable", func(scope *DecisionScope) { scope.Repositories[0].StableRelease.Enabled = nil }, "stable_release.enabled must be explicit"},
		{"omitted observation", func(scope *DecisionScope) { scope.ObservedAt = "" }, "observed_at"},
		{"scope predates snapshot", func(scope *DecisionScope) { scope.ObservedAt = "2026-01-01T00:20:29Z" }, "predates snapshot"},
		{"scope is future-dated", func(scope *DecisionScope) { scope.ObservedAt = "2026-01-01T00:25:01Z" }, "future-dated"},
		{"omitted open PR array", func(scope *DecisionScope) { scope.Repositories[0].OpenPullRequests = nil }, "open_pull_requests must be explicitly present"},
		{"omitted repair identities", func(scope *DecisionScope) { scope.Repositories[0].RepairBoundary.Identities = nil }, "repair_boundary.identities must be explicitly present"},
		{"omitted additional keep array", func(scope *DecisionScope) { scope.Repositories[0].AdditionalKeepIdentities = nil }, "additional_keep_identities must be explicitly present"},
		{"omitted delete authority", func(scope *DecisionScope) { scope.Repositories[0].DeleteEligibleIdentities = nil }, "delete_eligible_identities must be explicitly present"},
		{"disabled fake tuple", func(scope *DecisionScope) { disabled := false; scope.Repositories[0].StableRelease.Enabled = &disabled }, "must not contain a fake"},
		{"uncorroborated protected main", func(scope *DecisionScope) { scope.Repositories[0].ProtectedMainSHA = strings.Repeat("9", 40) }, "not corroborated"},
		{"uncorroborated stable source", func(scope *DecisionScope) { scope.Repositories[0].StableRelease.SourceSHA = strings.Repeat("9", 40) }, "corroborat"},
		{"open without identity", func(scope *DecisionScope) {
			open := false
			scope.Repositories[0].RepairBoundary.Closed = &open
			scope.Repositories[0].RepairBoundary.Identities = []ExactKeepIdentity{}
		}, "without exact identities"},
		{"closed with identity", func(scope *DecisionScope) {
			scope.Repositories[0].RepairBoundary.Identities = []ExactKeepIdentity{{ProducerRunID: 100, ProducerRunAttempt: 2, WorkflowPath: ".github/workflows/ci.yml", HeadSHA: strings.Repeat("a", 40)}}
		}, "closed repair boundary with identities"},
		{"delete overlaps additional keep", func(scope *DecisionScope) {
			scope.Repositories[0].AdditionalKeepIdentities = append([]ExactKeepIdentity(nil), scope.Repositories[0].DeleteEligibleIdentities...)
		}, "overlaps an exact keep authority"},
		{"delete overlaps repair keep", func(scope *DecisionScope) {
			open := false
			scope.Repositories[0].RepairBoundary.Closed = &open
			scope.Repositories[0].RepairBoundary.Identities = append([]ExactKeepIdentity(nil), scope.Repositories[0].DeleteEligibleIdentities...)
		}, "overlaps an exact keep authority"},
		{"delete conflicts with protected main", func(scope *DecisionScope) {
			scope.Repositories[0].ProtectedMainSHA = strings.Repeat("a", 40)
		}, "conflicts with protected-main keep authority"},
		{"delete conflicts with stable release", func(scope *DecisionScope) {
			scope.Repositories[0].StableRelease.SourceSHA = strings.Repeat("a", 40)
		}, "conflicts with stable-release keep authority"},
		{"delete conflicts with open PR", func(scope *DecisionScope) {
			scope.Repositories[0].OpenPullRequests = []PullRequestHead{{Number: 1, HeadSHA: strings.Repeat("a", 40)}}
		}, "conflicts with open-PR keep authority"},
		{"unsorted delete authority", func(scope *DecisionScope) {
			scope.Repositories[0].DeleteEligibleIdentities = []ExactKeepIdentity{
				{ProducerRunID: 100, ProducerRunAttempt: 2, WorkflowPath: ".github/workflows/ci.yml", HeadSHA: strings.Repeat("a", 40)},
				{ProducerRunID: 100, ProducerRunAttempt: 1, WorkflowPath: ".github/workflows/ci.yml", HeadSHA: strings.Repeat("a", 40)},
			}
		}, "canonically sorted and unique"},
		{"unknown attempt", func(scope *DecisionScope) { scope.Repositories[0].DeleteEligibleIdentities[0].ProducerRunID = 999 }, "does not bind"},
		{"path mismatch", func(scope *DecisionScope) {
			scope.Repositories[0].DeleteEligibleIdentities[0].WorkflowPath = ".github/workflows/build-binaries.yml"
		}, "does not corroborate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cloneScope(t, valid)
			test.mutate(&value)
			err := ValidateDecisionScope(value, snapshot, mustTime(t, "2026-01-01T00:25:00Z"), time.Hour)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v want substring %q", err, test.message)
			}
		})
	}
	if err := ValidateDecisionScope(valid, snapshot, mustTime(t, "2026-01-01T01:24:00.000000001Z"), time.Hour); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale decision scope error=%v", err)
	}
	if err := ValidateDecisionScope(valid, snapshot, mustTime(t, "2026-01-01T00:25:00Z"), MaxSnapshotAge+time.Nanosecond); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("oversized decision-scope freshness window error=%v", err)
	}
}

func TestDeleteRequiresPositiveExactAuthority(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	snapshot := validClassifierSnapshot(t, policy)
	now := mustTime(t, "2026-01-01T00:25:00Z")
	scope := validDecisionScope(t, snapshot, policy)
	scope.Repositories[0].DeleteEligibleIdentities = []ExactKeepIdentity{}
	manifest, err := Classify(snapshot, scope, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Records[0].Decision != DecisionKeep || manifest.Records[0].ReasonCode != ReasonKeepNotProven || manifest.Totals.Delete.Count != 0 {
		t.Fatalf("empty delete authority must preserve artifact: record=%+v totals=%+v", manifest.Records[0], manifest.Totals)
	}
	scope = validDecisionScope(t, snapshot, policy)
	manifest, err = Classify(snapshot, scope, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Records[0].Decision != DecisionDelete || manifest.Records[0].ReasonCode != ReasonDeleteSuperseded || manifest.Totals.Delete.Count != 1 {
		t.Fatalf("exact delete authority was not applied: record=%+v totals=%+v", manifest.Records[0], manifest.Totals)
	}
}

func TestReferencedStableSourceKeepsControlMainBootstrap(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	snapshot := validClassifierSnapshot(t, policy)
	run := SnapshotRun{
		Repository: "example/env-vault", RunID: 400, CurrentAttempt: 1,
		WorkflowPath: ".github/workflows/bootstrap-release-assets.yml", HeadSHA: strings.Repeat("c", 40), HeadBranch: "main",
		HeadRepository: "example/env-vault", HeadRepositoryID: 10, Event: "workflow_dispatch", Status: "completed", Conclusion: "success",
		CreatedAt: "2026-01-01T00:14:00Z", RunStartedAt: "2026-01-01T00:15:00Z", UpdatedAt: "2026-01-01T00:17:00Z",
	}
	snapshot.Runs = append(snapshot.Runs, run)
	snapshot.Attempts = append(snapshot.Attempts, attemptFromRun(run, 1, "2026-01-01T00:15:00Z", "2026-01-01T00:17:00Z"))
	snapshot.Artifacts = append(snapshot.Artifacts, fixtureArtifact(6, 60, "env-vault-release-assets-bootstrap-v1.2.3-"+strings.Repeat("b", 40)+"-101-attempt-1", run, 1, "2026-01-01T00:16:00Z", policy))
	snapshot.Repositories[0].Artifacts.TotalCount = 6
	snapshot.Repositories[0].Artifacts.PageItemCounts[0] = 6
	snapshot.Repositories[0].Runs.TotalCount = 4
	snapshot.Repositories[0].Runs.PageItemCounts[0] = 4
	snapshot.Repositories[0].AttemptDocuments = 6
	snapshot.Repositories[0].ArtifactCount = 6
	snapshot.Repositories[0].ArtifactBytes = 210
	snapshot.Repositories[0].RunCount = 4
	snapshot.Repositories[0].AttemptCount = 6
	snapshot.ArtifactCount = 6
	snapshot.ArtifactBytes = 210
	snapshot.RunCount = 4
	snapshot.AttemptCount = 6
	scope := validDecisionScope(t, snapshot, policy)
	manifest, err := Classify(snapshot, scope, policy, mustTime(t, "2026-01-01T00:25:00Z"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	record := manifest.Records[len(manifest.Records)-1]
	if record.ReasonCode != ReasonKeepStableRelease || record.ReferencedVersion != "v1.2.3" || record.ReferencedSourceSHA != strings.Repeat("b", 40) {
		t.Fatalf("bootstrap classification=%+v", record)
	}
	badScope := cloneScope(t, scope)
	badScope.Repositories[0].StableRelease.Version = "v1.2.4"
	if _, err := Classify(snapshot, badScope, policy, mustTime(t, "2026-01-01T00:25:00Z"), time.Hour); err == nil || !strings.Contains(err.Error(), "different version") {
		t.Fatalf("stable version/source mismatch error=%v", err)
	}
}

func validClassifierSnapshot(t *testing.T, policy Policy) Snapshot {
	t.Helper()
	repository := "example/env-vault"
	repositoryID := int64(10)
	headRepository := repository
	headRepositoryID := repositoryID
	shaA, shaB, shaC := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	runs := []SnapshotRun{
		{Repository: repository, RunID: 100, CurrentAttempt: 3, WorkflowPath: ".github/workflows/ci.yml", HeadSHA: shaA, HeadBranch: "main", HeadRepository: headRepository, HeadRepositoryID: headRepositoryID, Event: "push", Status: "completed", Conclusion: "success", CreatedAt: "2026-01-01T00:00:00Z", RunStartedAt: "2026-01-01T00:09:00Z", UpdatedAt: "2026-01-01T00:11:00Z"},
		{Repository: repository, RunID: 200, CurrentAttempt: 1, WorkflowPath: ".github/workflows/build-binaries.yml", HeadSHA: shaB, HeadBranch: "main", HeadRepository: headRepository, HeadRepositoryID: headRepositoryID, Event: "workflow_dispatch", Status: "completed", Conclusion: "success", CreatedAt: "2026-01-01T00:01:00Z", RunStartedAt: "2026-01-01T00:02:00Z", UpdatedAt: "2026-01-01T00:04:00Z"},
		{Repository: repository, RunID: 300, CurrentAttempt: 1, WorkflowPath: "dynamic/github-code-scanning/codeql", HeadSHA: shaC, HeadBranch: "main", HeadRepository: headRepository, HeadRepositoryID: headRepositoryID, Event: "push", Status: "completed", Conclusion: "success", CreatedAt: "2026-01-01T00:11:00Z", RunStartedAt: "2026-01-01T00:12:00Z", UpdatedAt: "2026-01-01T00:14:00Z"},
	}
	attempts := []SnapshotAttempt{
		attemptFromRun(runs[0], 1, "2026-01-01T00:01:00Z", "2026-01-01T00:03:00Z"),
		attemptFromRun(runs[0], 2, "2026-01-01T00:05:00Z", "2026-01-01T00:07:00Z"),
		attemptFromRun(runs[0], 3, "2026-01-01T00:09:00Z", "2026-01-01T00:11:00Z"),
		attemptFromRun(runs[1], 1, "2026-01-01T00:02:00Z", "2026-01-01T00:04:00Z"),
		attemptFromRun(runs[2], 1, "2026-01-01T00:12:00Z", "2026-01-01T00:14:00Z"),
	}
	artifacts := []SnapshotArtifact{
		fixtureArtifact(1, 10, "env-vault-linux-amd64", runs[0], 1, "2026-01-01T00:02:00Z", policy),
		fixtureArtifact(2, 20, "env-vault-release-linux-amd64-attempt-2", runs[0], 2, "2026-01-01T00:06:00Z", policy),
		fixtureArtifact(3, 30, "env-vault-release-linux-amd64-attempt-3", runs[0], 3, "2026-01-01T00:10:00Z", policy),
		fixtureArtifact(4, 40, "env-vault-release-observation-v1.2.3-attempt-1", runs[1], 1, "2026-01-01T00:03:00Z", policy),
		fixtureArtifact(5, 50, "sarif-artifact-go", runs[2], 1, "2026-01-01T00:13:00Z", policy),
	}
	return Snapshot{
		SchemaID: SnapshotSchemaID, SchemaVersion: SnapshotSchemaVersion,
		ObservedStartedAt: "2026-01-01T00:20:00Z", ObservedFinishedAt: "2026-01-01T00:20:30Z",
		Repositories: []SnapshotRepository{{
			Repository: repository, RepositoryID: repositoryID,
			Artifacts:        PaginationProof{TotalCount: 5, PageCount: 1, PageItemCounts: []int{5}, PageSHA256: []string{strings.Repeat("1", 64)}, FinalPageSHA256: []string{strings.Repeat("1", 64)}},
			Runs:             PaginationProof{TotalCount: 3, PageCount: 1, PageItemCounts: []int{3}, PageSHA256: []string{strings.Repeat("2", 64)}, FinalPageSHA256: []string{strings.Repeat("2", 64)}},
			AttemptDocuments: 5, ArtifactCount: 5, ArtifactBytes: 150, RunCount: 3, AttemptCount: 5,
		}},
		Artifacts: artifacts, Runs: runs, Attempts: attempts,
		ArtifactCount: 5, ArtifactBytes: 150, RunCount: 3, AttemptCount: 5,
	}
}

func attemptFromRun(run SnapshotRun, number int, started, updated string) SnapshotAttempt {
	return SnapshotAttempt{
		Repository: run.Repository, RunID: run.RunID, RunAttempt: number, WorkflowPath: run.WorkflowPath,
		HeadSHA: run.HeadSHA, HeadBranch: run.HeadBranch, HeadRepository: run.HeadRepository, HeadRepositoryID: run.HeadRepositoryID,
		Event: run.Event, Status: run.Status, Conclusion: run.Conclusion, CreatedAt: run.CreatedAt,
		RunStartedAt: started, UpdatedAt: updated,
	}
}

func fixtureArtifact(id, size int64, name string, run SnapshotRun, attempt int, timestamp string, policy Policy) SnapshotArtifact {
	match, err := matchNameValidated(policy, run.WorkflowPath, name)
	if err != nil {
		panic(err)
	}
	return SnapshotArtifact{
		Repository: run.Repository, ArtifactID: id, Name: name, Digest: "sha256:" + strings.Repeat("f", 64), SizeInBytes: size,
		CreatedAt: timestamp, UpdatedAt: timestamp, ExpiresAt: "2026-01-02T00:00:00Z", Expired: false,
		ProducerRunID: run.RunID, ProducerRunAttempt: attempt, WorkflowPath: run.WorkflowPath,
		HeadSHA: run.HeadSHA, HeadBranch: run.HeadBranch, HeadRepositoryID: run.HeadRepositoryID,
		ReferencedVersion: match.ReferencedVersion, ReferencedSourceSHA: match.ReferencedSourceSHA,
		PolicyPattern: match.Pattern.ID, Class: match.Pattern.Class, Lifecycle: match.Pattern.Lifecycle,
		DependencyRationale: match.Pattern.DependencyRepairRationale,
	}
}

func validDecisionScope(t *testing.T, snapshot Snapshot, policy Policy) DecisionScope {
	t.Helper()
	enabled, closed := true, true
	scope := DecisionScope{
		SchemaID: DecisionScopeSchemaID, SchemaVersion: DecisionScopeSchemaVersion, ObservedAt: "2026-01-01T00:24:00Z",
		Repositories: []RepositoryDecisionScope{{
			Repository: "example/env-vault", ProtectedMainSHA: strings.Repeat("c", 40),
			StableRelease:            StableReleaseIdentity{Enabled: &enabled, Version: "v1.2.3", SourceSHA: strings.Repeat("b", 40)},
			OpenPullRequests:         []PullRequestHead{},
			RepairBoundary:           RepairBoundary{Closed: &closed, Identities: []ExactKeepIdentity{}},
			AdditionalKeepIdentities: []ExactKeepIdentity{},
			DeleteEligibleIdentities: []ExactKeepIdentity{{
				ProducerRunID: 100, ProducerRunAttempt: 1, WorkflowPath: ".github/workflows/ci.yml", HeadSHA: strings.Repeat("a", 40),
			}},
		}},
	}
	bindTestScope(t, &scope, snapshot, policy)
	return scope
}

func bindTestScope(t *testing.T, scope *DecisionScope, snapshot Snapshot, policy Policy) {
	t.Helper()
	var err error
	scope.SnapshotSemanticSHA256, err = snapshotSemanticSHA256(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	scope.PolicySemanticSHA256, err = CanonicalSHA256(policy)
	if err != nil {
		t.Fatal(err)
	}
	scope.LiveObservationSemanticSHA256 = strings.Repeat("d", 64)
	scope.RepairProofSemanticSHA256 = strings.Repeat("e", 64)
	for index := range scope.Repositories {
		scope.Repositories[index].ImmutableKeepArtifactIDs = immutableKeepArtifactIDs(snapshot, scope.Repositories[index])
	}
}

func cloneSnapshot(t *testing.T, value Snapshot) Snapshot {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone Snapshot
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func cloneScope(t *testing.T, value DecisionScope) DecisionScope {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone DecisionScope
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
