package releaseevidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

type fakeEvidenceRunner struct {
	responses map[string][]byte
	errors    map[string]error
	calls     [][]string
}

type pagedEvidenceRunner struct{ calls int }

type functionEvidenceRunner func([]string) ([]byte, error)

func (runner functionEvidenceRunner) Run(_ context.Context, args []string) ([]byte, error) {
	return runner(args)
}

func (runner *pagedEvidenceRunner) Run(_ context.Context, args []string) ([]byte, error) {
	runner.calls++
	page := ""
	for index, arg := range args {
		if arg == "--raw-field" && index+1 < len(args) && strings.HasPrefix(args[index+1], "page=") {
			page = strings.TrimPrefix(args[index+1], "page=")
		}
	}
	response := apiJobs{TotalCount: 101}
	switch page {
	case "1":
		for index := 1; index <= 100; index++ {
			response.Jobs = append(response.Jobs, apiJob{ID: int64(index), RunAttempt: 1})
		}
	case "2":
		response.Jobs = []apiJob{{ID: 101, RunAttempt: 1}}
	default:
		return nil, fmt.Errorf("unexpected page %q", page)
	}
	return json.Marshal(response)
}

func (runner *fakeEvidenceRunner) Run(_ context.Context, args []string) ([]byte, error) {
	runner.calls = append(runner.calls, append([]string(nil), args...))
	key := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "repos/") {
			key = arg
			break
		}
	}
	if err := runner.errors[key]; err != nil {
		return nil, err
	}
	data, ok := runner.responses[key]
	if !ok {
		return nil, fmt.Errorf("unexpected call %q", strings.Join(args, " "))
	}
	return data, nil
}

func TestCollectFailedPublisherEmitsExactAttemptMetricsWithoutReleaseGuessing(t *testing.T) {
	contract := testReleaseContract(t)
	runID := int64(42)
	run := apiRun{
		ID: runID, Name: "build-binaries", Path: ".github/workflows/build-binaries.yml", Event: "push",
		Status: "completed", Conclusion: "failure", HeadBranch: "v1.2.3", HeadSHA: testSHA,
		DisplayTitle: "env-vault-publication event=push version=v1.2.3 repair=none state=automatic",
		RunAttempt:   2, CreatedAt: time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC), RunStartedAt: time.Date(2026, 7, 16, 8, 0, 5, 0, time.UTC), UpdatedAt: time.Date(2026, 7, 16, 8, 0, 20, 0, time.UTC),
	}
	jobs := apiJobs{TotalCount: 1, Jobs: []apiJob{{
		ID: 7, Name: "release", Status: "completed", Conclusion: "failure", RunAttempt: 2,
		StartedAt: time.Date(2026, 7, 16, 8, 0, 5, 0, time.UTC), CompletedAt: time.Date(2026, 7, 16, 8, 0, 15, 0, time.UTC),
		Steps: []apiStep{
			{Name: "Restore Go build cache", StartedAt: time.Date(2026, 7, 16, 8, 0, 5, 0, time.UTC), CompletedAt: time.Date(2026, 7, 16, 8, 0, 8, 0, time.UTC)},
			{Name: "Upload native release artifact", StartedAt: time.Date(2026, 7, 16, 8, 0, 8, 0, time.UTC), CompletedAt: time.Date(2026, 7, 16, 8, 0, 10, 0, time.UTC)},
		},
	}}}
	allJobs := apiJobs{TotalCount: 2, Jobs: []apiJob{
		{
			ID: 6, Name: "release", Status: "completed", Conclusion: "failure", RunAttempt: 1,
			StartedAt: time.Date(2026, 7, 16, 7, 59, 0, 0, time.UTC), CompletedAt: time.Date(2026, 7, 16, 7, 59, 10, 0, time.UTC),
			Steps: []apiStep{
				{Name: "Run actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16", StartedAt: time.Date(2026, 7, 16, 7, 59, 0, 0, time.UTC), CompletedAt: time.Date(2026, 7, 16, 7, 59, 4, 0, time.UTC)},
				{Name: "Upload E2E reports", StartedAt: time.Date(2026, 7, 16, 7, 59, 4, 0, time.UTC), CompletedAt: time.Date(2026, 7, 16, 7, 59, 9, 0, time.UTC)},
			},
		},
		jobs.Jobs[0],
	}}
	runner := &fakeEvidenceRunner{responses: map[string][]byte{
		"repos/ildarbinanas-design/env-vault/actions/runs/42":                 mustJSON(t, run),
		"repos/ildarbinanas-design/env-vault/actions/runs/42/attempts/2/jobs": mustJSON(t, jobs),
		"repos/ildarbinanas-design/env-vault/actions/runs/42/jobs":            mustJSON(t, allJobs),
		"repos/ildarbinanas-design/env-vault/git/ref/tags/v1.2.3":             mustJSON(t, exactTagReference("v1.2.3", testSHA)),
	}}
	evidence, err := Collect(context.Background(), CollectOptions{
		Contract: contract, ContractPath: filepath.Join("..", "..", releasecontract.CanonicalPath), Repository: "ildarbinanas-design/env-vault", Version: "v1.2.3", SourceSHA: testSHA,
		PublisherRunID: runID, PublisherRunAttempt: 2,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Release.Status != "failed" || evidence.Release.GitHubRelease != "not_checked" || evidence.Release.TagSHA != testSHA || len(evidence.WorkflowRuns) != 1 || evidence.Release.Publication == nil {
		t.Fatalf("failed evidence=%+v", evidence)
	}
	if evidence.Release.Publication.GitHubRelease.State != "unknown" || evidence.Release.Publication.Homebrew.State != "unknown" {
		t.Fatalf("failed publication snapshot=%+v", evidence.Release.Publication)
	}
	metrics := evidence.WorkflowRuns[0]
	if metrics.RunAttempt != 2 || metrics.RetryCount != 1 || metrics.QueueSeconds != 5 || metrics.WallSeconds != 15 || metrics.AttemptRunnerSeconds != 10 || metrics.AggregateRunnerSeconds != 20 || metrics.CacheSeconds != 7 || metrics.ArtifactTransferSeconds != 7 || metrics.ExecutedJobCount != 2 || len(metrics.Attempts) != 2 || !metrics.TimingComplete {
		t.Fatalf("metrics=%+v", metrics)
	}
	for _, call := range runner.calls {
		joined := strings.Join(call, " ")
		if !strings.Contains(joined, "--method GET") || strings.Contains(joined, " POST ") || strings.Contains(joined, " PATCH ") || strings.Contains(joined, " PUT ") {
			t.Fatalf("collector issued a non-GET API call: %q", joined)
		}
	}
}

func TestFailedPublicationSnapshotDistinguishesAbsentIncompleteAndUnknown(t *testing.T) {
	contract := testReleaseContract(t)
	options := CollectOptions{Contract: contract, Repository: "ildarbinanas-design/env-vault", Version: "v1.2.3", SourceSHA: testSHA}
	releaseEndpoint := "repos/ildarbinanas-design/env-vault/releases/tags/v1.2.3"
	pullsEndpoint := "repos/ildarbinanas-design/homebrew-tap/pulls"

	absentRunner := &fakeEvidenceRunner{
		responses: map[string][]byte{pullsEndpoint: mustJSON(t, []apiPull{})},
		errors:    map[string]error{releaseEndpoint: &githubCommandError{Code: "NOT_FOUND", HTTPStatus: 404}},
	}
	absent, _, _, _ := collectFailedPublicationSnapshot(context.Background(), apiClient{runner: absentRunner}, options)
	if absent.GitHubRelease.State != "absent" || absent.Assets.State != "absent" || absent.Attestations.State != "unknown" || absent.Homebrew.State != "absent" {
		t.Fatalf("absent snapshot=%+v", absent)
	}

	authRunner := &fakeEvidenceRunner{responses: map[string][]byte{}, errors: map[string]error{
		releaseEndpoint: &githubCommandError{Code: "AUTH_FORBIDDEN", HTTPStatus: 403},
		pullsEndpoint:   &githubCommandError{Code: "AUTH_FORBIDDEN", HTTPStatus: 403},
	}}
	unknown, _, _, checks := collectFailedPublicationSnapshot(context.Background(), apiClient{runner: authRunner}, options)
	if unknown.GitHubRelease.State != "unknown" || unknown.Assets.State != "unknown" || unknown.Attestations.State != "unknown" || unknown.Homebrew.State != "unknown" {
		t.Fatalf("authentication failure was interpreted as absence: %+v", unknown)
	}
	for _, check := range checks {
		if check.Result != "unknown" {
			t.Fatalf("unknown remote state produced check=%+v", check)
		}
	}

	malformedRunner := &fakeEvidenceRunner{responses: map[string][]byte{
		releaseEndpoint: mustJSON(t, map[string]any{}),
		pullsEndpoint:   mustJSON(t, []apiPull{}),
	}}
	malformed, _, _, _ := collectFailedPublicationSnapshot(context.Background(), apiClient{runner: malformedRunner}, options)
	if malformed.GitHubRelease.State != "unknown" || malformed.Assets.State != "unknown" {
		t.Fatalf("schema failure was interpreted as known publication state: %+v", malformed)
	}

	partialRelease := apiRelease{TagName: "v1.2.3", Assets: []apiAsset{{Name: contract.Assets[0], Digest: "sha256:" + strings.Repeat("a", 64), State: "uploaded"}}}
	partialRunner := &fakeEvidenceRunner{responses: map[string][]byte{
		releaseEndpoint: mustJSON(t, partialRelease),
		pullsEndpoint:   mustJSON(t, []apiPull{}),
	}}
	partial, assets, _, _ := collectFailedPublicationSnapshot(context.Background(), apiClient{runner: partialRunner}, options)
	if partial.GitHubRelease.State != "present" || partial.Assets.State != "incomplete" || partial.Attestations.State != "incomplete" || partial.Homebrew.State != "absent" || len(assets) != 1 {
		t.Fatalf("partial snapshot=%+v assets=%+v", partial, assets)
	}
}

func TestResolveTriggerAcceptsExactTagRefRepairAndRejectsRunNameDrift(t *testing.T) {
	contract := testReleaseContract(t)
	state := strings.Repeat("a", 64)
	run := apiRun{
		ID: 42, Name: "build-binaries", Path: ".github/workflows/build-binaries.yml", Event: "workflow_dispatch",
		Status: "completed", Conclusion: "success", HeadBranch: "v1.2.3", HeadSHA: testSHA, RunAttempt: 1,
		DisplayTitle: "env-vault-publication event=workflow_dispatch version=v1.2.3 repair=homebrew state=" + state,
		CreatedAt:    time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 7, 16, 8, 1, 0, 0, time.UTC),
	}
	ci := apiRun{ID: 9, Name: "ci", Path: ".github/workflows/ci.yml", Event: "push", Status: "completed", Conclusion: "success", HeadBranch: "main", HeadSHA: testSHA, RunAttempt: 3}
	event := workflowRunEvent{Action: "completed", WorkflowRun: run}
	event.Repository.FullName = "ildarbinanas-design/env-vault"
	eventFile := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(eventFile, mustJSON(t, event), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeEvidenceRunner{responses: map[string][]byte{
		"repos/ildarbinanas-design/env-vault/actions/runs/42":               mustJSON(t, run),
		"repos/ildarbinanas-design/env-vault/git/ref/tags/v1.2.3":           mustJSON(t, exactTagReference("v1.2.3", testSHA)),
		"repos/ildarbinanas-design/env-vault/actions/workflows/ci.yml/runs": mustJSON(t, apiRuns{TotalCount: 1, WorkflowRuns: []apiRun{ci}}),
	}}
	identity, err := ResolveTrigger(context.Background(), eventFile, contract, runner)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Version != "v1.2.3" || identity.SourceSHA != testSHA || identity.RepairMode != "homebrew" || identity.RepairStateDigest != state || identity.CIRunID != 9 || identity.CIRunAttempt != 3 {
		t.Fatalf("identity=%+v", identity)
	}
	run.DisplayTitle += " extra"
	event.WorkflowRun = run
	if err := os.WriteFile(eventFile, mustJSON(t, event), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTrigger(context.Background(), eventFile, contract, runner); err == nil {
		t.Fatal("webhook/API run-name disagreement was accepted")
	}
	runner.responses["repos/ildarbinanas-design/env-vault/actions/runs/42"] = mustJSON(t, run)
	if _, err := ResolveTrigger(context.Background(), eventFile, contract, runner); err == nil {
		t.Fatal("authoritative API run-name drift was accepted")
	}
}

func exactTagReference(version, sha string) apiTagReference {
	return apiTagReference{Ref: "refs/tags/" + version, Object: apiGitObject{Type: "commit", SHA: sha}}
}

func TestExactTagResolutionUsesTagNamespaceAndDereferencesAnnotatedTags(t *testing.T) {
	annotatedSHA := strings.Repeat("a", 40)
	runner := &fakeEvidenceRunner{responses: map[string][]byte{
		"repos/ildarbinanas-design/env-vault/git/ref/tags/v1.2.3": mustJSON(t, apiTagReference{
			Ref: "refs/tags/v1.2.3", Object: apiGitObject{Type: "tag", SHA: annotatedSHA},
		}),
		"repos/ildarbinanas-design/env-vault/git/tags/" + annotatedSHA: mustJSON(t, apiAnnotatedTag{Object: apiGitObject{Type: "commit", SHA: testSHA}}),
	}}
	resolved, err := resolveExactTag(context.Background(), apiClient{runner: runner}, "ildarbinanas-design/env-vault", "v1.2.3")
	if err != nil || resolved != testSHA {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
	for _, call := range runner.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "/commits/v1.2.3") || !strings.Contains(joined, "--method GET") {
			t.Fatalf("ambiguous or mutating tag resolution call: %q", joined)
		}
	}

	runner.responses["repos/ildarbinanas-design/env-vault/git/ref/tags/v1.2.3"] = mustJSON(t, apiTagReference{
		Ref: "refs/heads/v1.2.3", Object: apiGitObject{Type: "commit", SHA: testSHA},
	})
	if _, err := resolveExactTag(context.Background(), apiClient{runner: runner}, "ildarbinanas-design/env-vault", "v1.2.3"); err == nil {
		t.Fatal("branch-like reference was accepted as the exact release tag")
	}
}

func TestSteadyEvidenceRejectsLegacyAndBlockedFailedTags(t *testing.T) {
	contract := testReleaseContract(t)
	if err := validateSteadyVersion(contract, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v0.0.7", "v0.0.8"} {
		if err := validateSteadyVersion(contract, version); err == nil {
			t.Fatalf("version %s entered steady release evidence", version)
		}
	}
}

func TestVerifyReleaseAssetsRequiresContractSetDigestsAndChecksumContent(t *testing.T) {
	contract := testReleaseContract(t)
	directory := t.TempDir()
	release := apiRelease{TagName: "v1.2.3", Assets: make([]apiAsset, 0, len(contract.Assets))}
	for _, platform := range contract.Platforms {
		archiveContent := []byte("archive:" + platform.ID)
		digest := sha256.Sum256(archiveContent)
		digestText := hex.EncodeToString(digest[:])
		if err := os.WriteFile(filepath.Join(directory, platform.Archive), archiveContent, 0o600); err != nil {
			t.Fatal(err)
		}
		checksumContent := []byte(digestText + "  " + platform.Archive + "\n")
		if err := os.WriteFile(filepath.Join(directory, platform.Checksum), checksumContent, 0o600); err != nil {
			t.Fatal(err)
		}
		checksumDigest := sha256.Sum256(checksumContent)
		release.Assets = append(release.Assets,
			apiAsset{Name: platform.Archive, Digest: "sha256:" + digestText, State: "uploaded"},
			apiAsset{Name: platform.Checksum, Digest: "sha256:" + hex.EncodeToString(checksumDigest[:]), State: "uploaded"},
		)
	}
	options := CollectOptions{Contract: contract, Version: "v1.2.3", SourceSHA: testSHA, AssetsDirectory: directory}
	assets, archives, err := verifyReleaseAssets(release, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 10 || len(archives) != 5 {
		t.Fatalf("assets=%d archives=%d", len(assets), len(archives))
	}
	first := contract.Platforms[0]
	if err := os.WriteFile(filepath.Join(directory, first.Checksum), []byte(archives[first.Archive]+" *"+first.Archive+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := verifyReleaseAssets(release, options); err == nil {
		t.Fatal("non-canonical checksum content was accepted")
	}
}

func TestParseAttestationBindsCertificateRunAndFiveContractArchives(t *testing.T) {
	contract := testReleaseContract(t)
	options := CollectOptions{Contract: contract, Repository: "ildarbinanas-design/env-vault", Version: "v1.2.3", SourceSHA: testSHA}
	workflow, _ := contract.WorkflowByID("publisher")
	archives := map[string]string{}
	entry := verificationEntry{}
	certificate := &entry.VerificationResult.Signature.Certificate
	certificate.GitHubWorkflowName = workflow.Name
	certificate.GitHubWorkflowRepository = options.Repository
	certificate.SourceRepositoryDigest = options.SourceSHA
	certificate.RunInvocationURI = "https://github.com/ildarbinanas-design/env-vault/actions/runs/123/attempts/4"
	entry.VerificationResult.Statement.PredicateType = "https://slsa.dev/provenance/v1"
	for index, platform := range contract.Platforms {
		digest := fmt.Sprintf("%064x", index+1)
		archives[platform.Archive] = digest
		subject := verificationSubject{Name: platform.Archive}
		subject.Digest.SHA256 = digest
		entry.VerificationResult.Statement.Subject = append(entry.VerificationResult.Statement.Subject, subject)
	}
	data := mustJSON(t, []verificationEntry{entry})
	attestation, err := parseAttestation(data, "provenance", "https://slsa.dev/provenance/v1", options, archives, workflow)
	if err != nil {
		t.Fatal(err)
	}
	if attestation.RunID != 123 || attestation.RunAttempt != 4 || len(attestation.SubjectSHA256s) != 5 {
		t.Fatalf("attestation=%+v", attestation)
	}
	extra := verificationSubject{Name: contract.Platforms[0].Checksum}
	extra.Digest.SHA256 = strings.Repeat("f", 64)
	entry.VerificationResult.Statement.Subject = append(entry.VerificationResult.Statement.Subject, extra)
	if _, err := parseAttestation(mustJSON(t, []verificationEntry{entry}), "provenance", "https://slsa.dev/provenance/v1", options, archives, workflow); err == nil {
		t.Fatal("checksum subject was accepted as part of the five-archive attestation contract")
	}
}

func TestTapFormulaBindsFourExactContractArchives(t *testing.T) {
	contract := testReleaseContract(t)
	directory := t.TempDir()
	var formula strings.Builder
	formula.WriteString("class EnvVault < Formula\n  version \"1.2.3\"\n")
	for _, platform := range contract.Platforms {
		content := []byte("archive:" + platform.ID)
		if err := os.WriteFile(filepath.Join(directory, platform.Archive), content, 0o600); err != nil {
			t.Fatal(err)
		}
		if platform.GOOS == "windows" {
			continue
		}
		digest := sha256.Sum256(content)
		fmt.Fprintf(&formula, "      url \"https://github.com/ildarbinanas-design/env-vault/releases/download/v1.2.3/%s\"\n      sha256 \"%s\"\n", platform.Archive, hex.EncodeToString(digest[:]))
	}
	options := CollectOptions{Contract: contract, Repository: "ildarbinanas-design/env-vault", Version: "v1.2.3", AssetsDirectory: directory}
	if err := validateTapFormulaAssets([]byte(formula.String()), options, "ildarbinanas-design/homebrew-tap"); err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(formula.String(), "sha256 \"", "sha256 \"0", 1)
	if err := validateTapFormulaAssets([]byte(drifted), options, "ildarbinanas-design/homebrew-tap"); err == nil {
		t.Fatal("formula checksum drift was accepted")
	}
}

func TestTapFormulaLineageBindsMarkerToExactPRHeadAndPostMergeBytes(t *testing.T) {
	content := []byte("class EnvVault < Formula\n  version \"1.2.3\"\nend\n")
	digest := sha256.Sum256(content)
	digestText := hex.EncodeToString(digest[:])
	head := tapFormula{Version: "1.2.3", SHA256: digestText, Content: append([]byte(nil), content...)}
	merge := tapFormula{Version: "1.2.3", SHA256: digestText, Content: append([]byte(nil), content...)}
	if err := validateTapFormulaLineage(head, merge, "1.2.3", digestText); err != nil {
		t.Fatal(err)
	}

	drifted := append([]byte(nil), content...)
	drifted = append(drifted, []byte("# merge-only change\n")...)
	driftedDigest := sha256.Sum256(drifted)
	merge.Content = drifted
	merge.SHA256 = hex.EncodeToString(driftedDigest[:])
	if err := validateTapFormulaLineage(head, merge, "1.2.3", digestText); err == nil {
		t.Fatal("post-merge formula drift from the exact PR head was accepted")
	}

	merge = head
	if err := validateTapFormulaLineage(head, merge, "1.2.3", strings.Repeat("f", 64)); err == nil {
		t.Fatal("PR marker that did not bind the exact PR-head formula was accepted")
	}
}

func TestWorkflowMetricsReportMissingTimestampsWithoutEstimating(t *testing.T) {
	run := apiRun{ID: 1, RunAttempt: 1, HeadSHA: testSHA, Conclusion: "success"}
	job := apiJob{ID: 2, Name: "quality", RunAttempt: 1}
	metrics := workflowRunEvidence("ci", run, []apiJob{job}, []apiJob{job})
	if metrics.TimingComplete || metrics.WallSeconds != 0 || metrics.AggregateRunnerSeconds != 0 || len(metrics.UnavailableReasonCodes) != 3 {
		t.Fatalf("metrics=%+v", metrics)
	}
}

func TestWorkflowMetricsReportMissingClassifiedStepTimestamps(t *testing.T) {
	base := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	run := apiRun{ID: 1, Event: "push", RunAttempt: 1, HeadSHA: testSHA, Conclusion: "success", CreatedAt: base, RunStartedAt: base.Add(time.Second)}
	job := apiJob{
		ID: 2, Name: "quality", RunAttempt: 1, StartedAt: base.Add(time.Second), CompletedAt: base.Add(10 * time.Second),
		Steps: []apiStep{{Name: "Download current-attempt platform promotion evidence"}},
	}
	metrics := workflowRunEvidence("ci", run, []apiJob{job}, []apiJob{job})
	if metrics.TimingComplete || !containsEvidenceString(metrics.UnavailableReasonCodes, "artifact_step_timestamps_unavailable") {
		t.Fatalf("missing classified step timestamp was silently accepted: %+v", metrics)
	}
}

func containsEvidenceString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestJobCollectionPaginatesWithoutDroppingExecutedJobs(t *testing.T) {
	runner := &pagedEvidenceRunner{}
	jobs, err := getAllJobPages(context.Background(), apiClient{runner: runner}, "repos/owner/repo/actions/runs/1/jobs", map[string]string{"filter": "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 101 || runner.calls != 2 || jobs[100].ID != 101 {
		t.Fatalf("jobs=%d calls=%d last=%+v", len(jobs), runner.calls, jobs[len(jobs)-1])
	}
}

func TestPublisherLineageIncludesPriorTagFailureAndExactStateRepair(t *testing.T) {
	contract := testReleaseContract(t)
	state := strings.Repeat("b", 64)
	prior := apiRun{ID: 1, Name: "build-binaries", Path: ".github/workflows/build-binaries.yml", Event: "push", Status: "completed", Conclusion: "failure", HeadBranch: "v1.2.3", HeadSHA: testSHA, RunAttempt: 1, DisplayTitle: "env-vault-publication event=push version=v1.2.3 repair=none state=automatic", CreatedAt: time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 7, 16, 8, 1, 0, 0, time.UTC)}
	currentRun := apiRun{ID: 2, Name: "build-binaries", Path: ".github/workflows/build-binaries.yml", Event: "workflow_dispatch", Status: "completed", Conclusion: "success", HeadBranch: "v1.2.3", HeadSHA: testSHA, RunAttempt: 1, DisplayTitle: "env-vault-publication event=workflow_dispatch version=v1.2.3 repair=release-assets state=" + state, CreatedAt: time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 7, 16, 9, 1, 0, 0, time.UTC)}
	future := currentRun
	future.ID, future.CreatedAt, future.UpdatedAt = 3, time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC), time.Date(2026, 7, 16, 10, 1, 0, 0, time.UTC)
	job := apiJob{ID: 10, Name: "release", RunAttempt: 1, StartedAt: time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC), CompletedAt: time.Date(2026, 7, 16, 8, 0, 10, 0, time.UTC)}
	runner := functionEvidenceRunner(func(args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "actions/workflows/build-binaries.yml/runs") && strings.Contains(joined, "event=push"):
			return mustJSON(t, apiRuns{TotalCount: 1, WorkflowRuns: []apiRun{prior}}), nil
		case strings.Contains(joined, "actions/workflows/build-binaries.yml/runs") && strings.Contains(joined, "event=workflow_dispatch"):
			return mustJSON(t, apiRuns{TotalCount: 2, WorkflowRuns: []apiRun{currentRun, future}}), nil
		case strings.Contains(joined, "actions/runs/1/attempts/1/jobs") || strings.Contains(joined, "actions/runs/1/jobs"):
			return mustJSON(t, apiJobs{TotalCount: 1, Jobs: []apiJob{job}}), nil
		case strings.Contains(joined, "actions/runs/1"):
			return mustJSON(t, prior), nil
		default:
			return nil, fmt.Errorf("unexpected call %q", joined)
		}
	})
	currentJob := job
	currentJob.ID, currentJob.StartedAt, currentJob.CompletedAt = 20, time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC), time.Date(2026, 7, 16, 9, 0, 10, 0, time.UTC)
	current := workflowRunEvidence("publisher", currentRun, []apiJob{currentJob}, []apiJob{currentJob})
	lineage, err := collectPublisherLineage(context.Background(), apiClient{runner: runner}, CollectOptions{Contract: contract, Repository: "ildarbinanas-design/env-vault", Version: "v1.2.3", SourceSHA: testSHA}, currentRun, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(lineage) != 2 || lineage[0].RunID != 1 || lineage[0].Conclusion != "failure" || lineage[1].RunID != 2 || lineage[1].RepairMode != "release-assets" || lineage[1].RepairStateDigest != state {
		t.Fatalf("lineage=%+v", lineage)
	}
}

func testReleaseContract(t *testing.T) releasecontract.Contract {
	t.Helper()
	contract, err := releasecontract.LoadCanonical(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
