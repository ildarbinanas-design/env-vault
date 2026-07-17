package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"sort"
	"testing"
)

type refactorBaseline struct {
	SchemaID                 string                       `json:"schema_id"`
	SchemaVersion            int                          `json:"schema_version"`
	MeasurementDate          string                       `json:"measurement_date"`
	Repositories             baselineRepositories         `json:"repositories"`
	ImmutableReleaseHistory  baselineImmutableHistory     `json:"immutable_release_history"`
	MeasurementDefinitions   map[string]string            `json:"measurement_definitions"`
	ReleaseContractInventory baselineContractInventory    `json:"release_contract_inventory"`
	ProductPathNoChangeGate  baselineProductPathGate      `json:"product_path_no_change_gate"`
	StaticSourceSurface      baselineStaticSourceSurface  `json:"static_source_surface"`
	RunMeasurements          baselineRunMeasurements      `json:"run_measurements"`
	EvidenceSnapshot         baselineEvidenceSnapshot     `json:"evidence_snapshot"`
	HomebrewSnapshot         baselineHomebrewSnapshot     `json:"homebrew_snapshot"`
	DuplicationInventory     baselineDuplicationInventory `json:"duplication_inventory"`
}

type baselineRepository struct {
	Name      string `json:"name"`
	SourceSHA string `json:"source_sha"`
}

type baselineRepositories struct {
	Release     baselineRepository `json:"release"`
	HomebrewTap baselineRepository `json:"homebrew_tap"`
}

type baselineImmutableHistory struct {
	PublishedVersion           string                           `json:"published_version"`
	HistoricalVersionStates    []baselineHistoricalVersionState `json:"historical_version_states"`
	ReleaseSourceSHA           string                           `json:"release_source_sha"`
	PublisherRunID             int64                            `json:"publisher_run_id"`
	PublisherRunAttempt        int                              `json:"publisher_run_attempt"`
	EvidenceRunID              int64                            `json:"evidence_run_id"`
	EvidenceRunAttempt         int                              `json:"evidence_run_attempt"`
	EvidenceCommitSHA          string                           `json:"evidence_commit_sha"`
	EvidenceParentSHA          string                           `json:"evidence_parent_sha"`
	HomebrewPullRequestNumber  int                              `json:"homebrew_pull_request_number"`
	HomebrewPullRequestHeadSHA string                           `json:"homebrew_pull_request_head_sha"`
	HomebrewMergeSHA           string                           `json:"homebrew_merge_sha"`
}

type baselineHistoricalVersionState struct {
	Version string `json:"version"`
	State   string `json:"state"`
}

type baselineContractInventory struct {
	ContractPath           string   `json:"contract_path"`
	PlatformIDs            []string `json:"platform_ids"`
	PlatformCount          int      `json:"platform_count"`
	ReleaseAssetCount      int      `json:"release_asset_count"`
	WorkflowIdentityCount  int      `json:"workflow_identity_count"`
	ReleaseRepository      string   `json:"release_repository"`
	HomebrewRepository     string   `json:"homebrew_repository"`
	HomebrewFormulaPath    string   `json:"homebrew_formula_path"`
	HomebrewCIWorkflowFile string   `json:"homebrew_ci_workflow_file"`
}

type baselineProductPathGate struct {
	BaseSHA                  string   `json:"base_sha"`
	Paths                    []string `json:"paths"`
	ExpectedChangedPathCount int      `json:"expected_changed_path_count"`
}

type baselineSourceSurface struct {
	Scope                string `json:"scope"`
	FileCount            int    `json:"file_count"`
	StaticJobDefinitions int    `json:"static_job_definitions"`
	PhysicalLines        int    `json:"physical_lines"`
	NonblankLines        int    `json:"nonblank_lines"`
}

type baselineCombinedSurface struct {
	FileCount            int `json:"file_count"`
	StaticJobDefinitions int `json:"static_job_definitions"`
	PhysicalLines        int `json:"physical_lines"`
	NonblankLines        int `json:"nonblank_lines"`
}

type baselineReleaseScripts struct {
	Scope          string `json:"scope"`
	ShellFileCount int    `json:"shell_file_count"`
	JQFileCount    int    `json:"jq_file_count"`
	FileCount      int    `json:"file_count"`
	PhysicalLines  int    `json:"physical_lines"`
	NonblankLines  int    `json:"nonblank_lines"`
}

type baselineReleaseGoCore struct {
	Scope                 []string `json:"scope"`
	GoFileCount           int      `json:"go_file_count"`
	PhysicalLines         int      `json:"physical_lines"`
	NonblankLines         int      `json:"nonblank_lines"`
	NonTestPhysicalLines  int      `json:"non_test_physical_lines"`
	UnitTestPhysicalLines int      `json:"unit_test_physical_lines"`
}

type baselineGoTestSurface struct {
	Scope         string `json:"scope"`
	GoFileCount   int    `json:"go_file_count"`
	PhysicalLines int    `json:"physical_lines"`
}

type baselineAdjacentSurface struct {
	Scope          []string `json:"scope"`
	Classification string   `json:"classification"`
	GoFileCount    int      `json:"go_file_count"`
	PhysicalLines  int      `json:"physical_lines"`
	NonblankLines  int      `json:"nonblank_lines"`
}

type baselineWorkflowStaticJobs struct {
	Repository           string `json:"repository"`
	File                 string `json:"file"`
	StaticJobDefinitions int    `json:"static_job_definitions"`
}

type baselineStaticSourceSurface struct {
	EnvVaultWorkflows               baselineSourceSurface        `json:"env_vault_workflows"`
	HomebrewTapWorkflows            baselineSourceSurface        `json:"homebrew_tap_workflows"`
	CombinedWorkflows               baselineCombinedSurface      `json:"combined_workflows"`
	ReleaseScripts                  baselineReleaseScripts       `json:"release_scripts"`
	ReleaseGoCore                   baselineReleaseGoCore        `json:"release_go_core"`
	ReleaseOperatorIntegrationTests baselineGoTestSurface        `json:"release_operator_integration_tests"`
	AdjacentE2EBaseline             baselineAdjacentSurface      `json:"adjacent_e2e_baseline"`
	WorkflowStaticJobs              []baselineWorkflowStaticJobs `json:"workflow_static_jobs"`
}

type baselineRunMeasurement struct {
	Scenario               string `json:"scenario"`
	RunID                  int64  `json:"run_id"`
	RunAttempt             int    `json:"run_attempt"`
	HeadSHA                string `json:"head_sha,omitempty"`
	JobCount               int    `json:"job_count"`
	WallSeconds            int    `json:"wall_seconds"`
	AggregateRunnerSeconds int    `json:"aggregate_runner_seconds"`
}

type baselineRunTotals struct {
	JobCount               int `json:"job_count"`
	WallSeconds            int `json:"wall_seconds"`
	AggregateRunnerSeconds int `json:"aggregate_runner_seconds"`
}

type baselineRunSet struct {
	Source       string                   `json:"source"`
	Measurements []baselineRunMeasurement `json:"measurements"`
	Totals       baselineRunTotals        `json:"totals"`
}

type baselineRunDelta struct {
	Scenario                     string `json:"scenario"`
	JobCountChange               int    `json:"job_count_change"`
	WallSecondsChange            int    `json:"wall_seconds_change"`
	AggregateRunnerSecondsChange int    `json:"aggregate_runner_seconds_change"`
}

type baselineRunDeltaTotals struct {
	JobCountChange               int `json:"job_count_change"`
	WallSecondsChange            int `json:"wall_seconds_change"`
	AggregateRunnerSecondsChange int `json:"aggregate_runner_seconds_change"`
}

type baselineRunDeltaSet struct {
	Measurements []baselineRunDelta     `json:"measurements"`
	Totals       baselineRunDeltaTotals `json:"totals"`
}

type baselineEvidenceTiming struct {
	RunID                           int64  `json:"run_id"`
	RunAttempt                      int    `json:"run_attempt"`
	JobCount                        int    `json:"job_count"`
	WorkflowStartedToUpdatedSeconds int    `json:"workflow_started_to_updated_seconds"`
	SummedJobActiveSpanSeconds      int    `json:"summed_job_active_span_seconds"`
	TimingNote                      string `json:"timing_note"`
}

type baselineTapRun struct {
	RunID      int64  `json:"run_id"`
	RunAttempt int    `json:"run_attempt"`
	HeadSHA    string `json:"head_sha"`
	Event      string `json:"event"`
	Conclusion string `json:"conclusion"`
}

type baselineHomebrewRuns struct {
	PullRequestHeadCI     baselineTapRun `json:"pull_request_head_ci"`
	PostMergeCI           baselineTapRun `json:"post_merge_ci"`
	RuntimeJobCountPerRun int            `json:"runtime_job_count_per_run"`
}

type baselineRunMeasurements struct {
	CheckedInHistoricalComparator baselineRunSet         `json:"checked_in_historical_comparator"`
	CurrentV0015                  baselineRunSet         `json:"current_v0_0_15"`
	CurrentMinusHistorical        baselineRunDeltaSet    `json:"current_minus_historical"`
	EvidenceWorkflowDerivedTiming baselineEvidenceTiming `json:"evidence_workflow_derived_timing"`
	HomebrewV0015                 baselineHomebrewRuns   `json:"homebrew_v0_0_15"`
}

type baselineGitProjection struct {
	PathCount                      int `json:"path_count"`
	LogicalPathBytes               int `json:"logical_path_bytes"`
	UniqueGitBlobCount             int `json:"unique_git_blob_count"`
	UniqueGitBlobUncompressedBytes int `json:"unique_git_blob_uncompressed_bytes"`
}

type baselineAttestationContent struct {
	VerificationEntryCount                 int `json:"verification_entry_count"`
	UniqueDocumentCount                    int `json:"unique_document_count"`
	DocumentBytesCountingEachEntry         int `json:"document_bytes_counting_each_entry"`
	DocumentBytesAfterContentDeduplication int `json:"document_bytes_after_content_deduplication"`
	DuplicateDocumentBytes                 int `json:"duplicate_document_bytes"`
	DuplicateFractionBasisPoints           int `json:"duplicate_fraction_basis_points"`
}

type baselineActionsArtifact struct {
	Role                 string `json:"role"`
	ArtifactID           int64  `json:"artifact_id"`
	Name                 string `json:"name"`
	ReportedArchiveBytes int    `json:"reported_archive_bytes"`
}

type baselineActionsArtifactArchives struct {
	MeasurementSource string                    `json:"measurement_source"`
	ReportedSizeField string                    `json:"reported_size_field"`
	Interpretation    string                    `json:"interpretation"`
	Artifacts         []baselineActionsArtifact `json:"artifacts"`
}

type baselineEvidenceSnapshot struct {
	GitCommitSHA                  string                          `json:"git_commit_sha"`
	GitParentSHA                  string                          `json:"git_parent_sha"`
	ReleaseEvidenceJSONBytes      int                             `json:"release_evidence_json_bytes"`
	V0015RootAndAttemptProjection baselineGitProjection           `json:"v0_0_15_root_and_attempt_projection"`
	FullEvidenceNamespace         baselineGitProjection           `json:"full_evidence_namespace"`
	AttestationDocumentContent    baselineAttestationContent      `json:"attestation_document_content"`
	ActionsArtifactArchives       baselineActionsArtifactArchives `json:"actions_artifact_archives"`
	OfflineReplayResult           string                          `json:"offline_replay_result"`
}

type baselineHomebrewSnapshot struct {
	FormulaPath        string `json:"formula_path"`
	FormulaSHA256      string `json:"formula_sha256"`
	PullRequestNumber  int    `json:"pull_request_number"`
	PullRequestHeadSHA string `json:"pull_request_head_sha"`
	MergeSHA           string `json:"merge_sha"`
	TapSHA             string `json:"tap_sha"`
}

type baselineTransportInventory struct {
	DirectGHAPICallSites                  int `json:"direct_gh_api_call_sites"`
	DirectGHAPIFiles                      int `json:"direct_gh_api_files"`
	DirectGHAPIReadSites                  int `json:"direct_gh_api_read_sites"`
	DirectGHAPIMutationSites              int `json:"direct_gh_api_mutation_sites"`
	DirectReadSitesOutsideGHAPIReadHelper int `json:"direct_read_sites_outside_gh_api_read_helper"`
	GHAPIReadHelperCallSites              int `json:"gh_api_read_helper_call_sites"`
	GHAPIReadHelperCallerFiles            int `json:"gh_api_read_helper_caller_files"`
}

type baselineLiteralOccurrence struct {
	Literal     string `json:"literal"`
	Occurrences int    `json:"occurrences"`
	FileCount   int    `json:"file_count"`
}

type baselinePinAnnotationDrift struct {
	Action                 string `json:"action"`
	SHA                    string `json:"sha"`
	V650CommentOccurrences int    `json:"v6_5_0_comment_occurrences"`
	V630CommentOccurrences int    `json:"v6_3_0_comment_occurrences"`
}

type baselineDuplicationInventory struct {
	OperationalSourceScope     []string                    `json:"operational_source_scope"`
	Transport                  baselineTransportInventory  `json:"transport"`
	LiteralOccurrences         []baselineLiteralOccurrence `json:"literal_occurrences"`
	ReviewedPinAnnotationDrift baselinePinAnnotationDrift  `json:"reviewed_pin_annotation_drift"`
	HomebrewTapFloatingAction  string                      `json:"homebrew_tap_floating_action"`
}

func TestReleaseRefactorBaselineIsStrictAndInternallyConsistent(t *testing.T) {
	data, err := os.ReadFile("../release/refactor-baseline.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var baseline refactorBaseline
	if err := decoder.Decode(&baseline); err != nil {
		t.Fatalf("strict baseline decode: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("unexpected trailing baseline JSON: %v", err)
	}

	if baseline.SchemaID != "env-vault.release-refactor-baseline.v1" || baseline.SchemaVersion != 1 || baseline.MeasurementDate != "2026-07-17" {
		t.Fatalf("baseline schema/date=%s/%d %s", baseline.SchemaID, baseline.SchemaVersion, baseline.MeasurementDate)
	}
	const releaseSHA = "c7dd1fd6176ac2abbea22f226795a0787e774c1b"
	const tapSHA = "71217af8d0c692e27d8c268c9cce5a2a533f4ea9"
	if baseline.Repositories.Release != (baselineRepository{"ildarbinanas-design/env-vault", releaseSHA}) ||
		baseline.Repositories.HomebrewTap != (baselineRepository{"ildarbinanas-design/homebrew-tap", tapSHA}) {
		t.Fatalf("baseline repository tuple drifted: %+v", baseline.Repositories)
	}
	history := baseline.ImmutableReleaseHistory
	if history.PublishedVersion != "v0.0.15" ||
		!reflect.DeepEqual(history.HistoricalVersionStates, []baselineHistoricalVersionState{
			{Version: "v0.0.12", State: "abandoned_no_tag_no_release"},
			{Version: "v0.0.13", State: "published_immutable"},
			{Version: "v0.0.14", State: "published_immutable"},
			{Version: "v0.0.15", State: "published_immutable"},
		}) ||
		history.ReleaseSourceSHA != releaseSHA || history.PublisherRunID != 29576465336 || history.PublisherRunAttempt != 1 ||
		history.EvidenceRunID != 29576963736 || history.EvidenceRunAttempt != 1 ||
		history.EvidenceCommitSHA != "af521d52b898088cb49f6256964e377e33e95a5d" ||
		history.EvidenceParentSHA != "68547bd880a4d49f44389476b77046aac2ab1675" ||
		history.HomebrewPullRequestNumber != 8 || history.HomebrewMergeSHA != tapSHA {
		t.Fatalf("immutable release history drifted: %+v", history)
	}

	wantDefinitions := []string{"aggregate_runner_seconds", "logical_path_bytes", "nonblank_lines", "physical_lines", "run_job_count", "static_job_definitions", "unique_git_blob_uncompressed_bytes", "wall_seconds"}
	gotDefinitions := sortedMapKeys(baseline.MeasurementDefinitions)
	if !reflect.DeepEqual(gotDefinitions, wantDefinitions) {
		t.Fatalf("measurement definitions=%v, want %v", gotDefinitions, wantDefinitions)
	}
	contract := baseline.ReleaseContractInventory
	if contract.PlatformCount != len(contract.PlatformIDs) || contract.PlatformCount != 5 || contract.ReleaseAssetCount != 10 || contract.WorkflowIdentityCount != 10 {
		t.Fatalf("release contract inventory is inconsistent: %+v", contract)
	}
	gate := baseline.ProductPathNoChangeGate
	if gate.BaseSHA != releaseSHA || gate.ExpectedChangedPathCount != 0 || len(gate.Paths) != 10 {
		t.Fatalf("product-path gate drifted: %+v", gate)
	}

	assertStaticBaselineArithmetic(t, baseline.StaticSourceSurface)
	assertRunBaselineArithmetic(t, baseline.RunMeasurements)
	assertEvidenceBaselineArithmetic(t, baseline.EvidenceSnapshot)

	brew := baseline.HomebrewSnapshot
	if brew.FormulaSHA256 != "6b1b7710b9b406ac5309aed21a6cd14269129a2142e33690ed15b3a49d882ced" ||
		brew.PullRequestNumber != 8 || brew.MergeSHA != tapSHA || brew.TapSHA != tapSHA {
		t.Fatalf("Homebrew snapshot drifted: %+v", brew)
	}
	transport := baseline.DuplicationInventory.Transport
	if transport.DirectGHAPICallSites != transport.DirectGHAPIReadSites+transport.DirectGHAPIMutationSites ||
		transport.DirectGHAPICallSites != 44 || transport.DirectGHAPIFiles != 17 ||
		transport.DirectReadSitesOutsideGHAPIReadHelper != transport.DirectGHAPIReadSites-1 ||
		transport.GHAPIReadHelperCallSites != 37 || transport.GHAPIReadHelperCallerFiles != 6 {
		t.Fatalf("transport inventory is inconsistent: %+v", transport)
	}
	seenLiterals := map[string]bool{}
	for _, item := range baseline.DuplicationInventory.LiteralOccurrences {
		if item.Literal == "" || item.Occurrences < 1 || item.FileCount < 1 || seenLiterals[item.Literal] {
			t.Fatalf("invalid or duplicate literal inventory entry: %+v", item)
		}
		seenLiterals[item.Literal] = true
	}
	if len(seenLiterals) != 14 || baseline.DuplicationInventory.HomebrewTapFloatingAction != "actions/checkout@v7" {
		t.Fatalf("duplication inventory is incomplete: literals=%d floating=%q", len(seenLiterals), baseline.DuplicationInventory.HomebrewTapFloatingAction)
	}
	assertNoUnsupportedSizeKeys(t, data)
}

func assertStaticBaselineArithmetic(t *testing.T, surface baselineStaticSourceSurface) {
	t.Helper()
	combined := surface.CombinedWorkflows
	if combined.FileCount != surface.EnvVaultWorkflows.FileCount+surface.HomebrewTapWorkflows.FileCount ||
		combined.StaticJobDefinitions != surface.EnvVaultWorkflows.StaticJobDefinitions+surface.HomebrewTapWorkflows.StaticJobDefinitions ||
		combined.PhysicalLines != surface.EnvVaultWorkflows.PhysicalLines+surface.HomebrewTapWorkflows.PhysicalLines ||
		combined.NonblankLines != surface.EnvVaultWorkflows.NonblankLines+surface.HomebrewTapWorkflows.NonblankLines {
		t.Fatalf("combined workflow baseline is inconsistent: %+v", combined)
	}
	if surface.ReleaseScripts.FileCount != surface.ReleaseScripts.ShellFileCount+surface.ReleaseScripts.JQFileCount ||
		surface.ReleaseGoCore.PhysicalLines != surface.ReleaseGoCore.NonTestPhysicalLines+surface.ReleaseGoCore.UnitTestPhysicalLines {
		t.Fatalf("release source baseline is inconsistent: scripts=%+v go=%+v", surface.ReleaseScripts, surface.ReleaseGoCore)
	}
	if surface.ReleaseOperatorIntegrationTests.GoFileCount != 12 || surface.ReleaseOperatorIntegrationTests.PhysicalLines != 6804 {
		t.Fatalf("release operator integration-test baseline drifted: %+v", surface.ReleaseOperatorIntegrationTests)
	}
	repositories := map[string]struct{ files, jobs int }{}
	seenFiles := map[string]bool{}
	for _, workflow := range surface.WorkflowStaticJobs {
		key := workflow.Repository + "/" + workflow.File
		if seenFiles[key] || workflow.StaticJobDefinitions < 1 {
			t.Fatalf("invalid workflow job entry: %+v", workflow)
		}
		seenFiles[key] = true
		entry := repositories[workflow.Repository]
		entry.files++
		entry.jobs += workflow.StaticJobDefinitions
		repositories[workflow.Repository] = entry
	}
	if repositories["env-vault"] != (struct{ files, jobs int }{10, 25}) || repositories["homebrew-tap"] != (struct{ files, jobs int }{1, 2}) {
		t.Fatalf("workflow static-job inventory is inconsistent: %+v", repositories)
	}
}

func assertRunBaselineArithmetic(t *testing.T, measurements baselineRunMeasurements) {
	t.Helper()
	historical := indexRuns(t, measurements.CheckedInHistoricalComparator)
	current := indexRuns(t, measurements.CurrentV0015)
	deltas := map[string]baselineRunDelta{}
	for _, delta := range measurements.CurrentMinusHistorical.Measurements {
		if _, present := deltas[delta.Scenario]; present {
			t.Fatalf("duplicate run delta %q", delta.Scenario)
		}
		deltas[delta.Scenario] = delta
	}
	for _, scenario := range []string{"main_ci", "release_pr_ci", "publisher"} {
		before, beforeOK := historical[scenario]
		after, afterOK := current[scenario]
		delta, deltaOK := deltas[scenario]
		if !beforeOK || !afterOK || !deltaOK {
			t.Fatalf("missing run scenario %q", scenario)
		}
		if delta.JobCountChange != after.JobCount-before.JobCount ||
			delta.WallSecondsChange != after.WallSeconds-before.WallSeconds ||
			delta.AggregateRunnerSecondsChange != after.AggregateRunnerSeconds-before.AggregateRunnerSeconds {
			t.Fatalf("run delta %q is inconsistent: before=%+v after=%+v delta=%+v", scenario, before, after, delta)
		}
	}
	assertRunTotals(t, measurements.CheckedInHistoricalComparator)
	assertRunTotals(t, measurements.CurrentV0015)
	wantDeltaTotals := baselineRunDeltaTotals{
		measurements.CurrentV0015.Totals.JobCount - measurements.CheckedInHistoricalComparator.Totals.JobCount,
		measurements.CurrentV0015.Totals.WallSeconds - measurements.CheckedInHistoricalComparator.Totals.WallSeconds,
		measurements.CurrentV0015.Totals.AggregateRunnerSeconds - measurements.CheckedInHistoricalComparator.Totals.AggregateRunnerSeconds,
	}
	if measurements.CurrentMinusHistorical.Totals != wantDeltaTotals {
		t.Fatalf("run delta totals=%+v, want %+v", measurements.CurrentMinusHistorical.Totals, wantDeltaTotals)
	}
	if deltas["publisher"].WallSecondsChange != 129 {
		t.Fatalf("publisher wall regression was obscured: %+v", deltas["publisher"])
	}
	if measurements.EvidenceWorkflowDerivedTiming.TimingNote == "" || measurements.EvidenceWorkflowDerivedTiming.JobCount != 2 {
		t.Fatalf("evidence derived timing is incomplete: %+v", measurements.EvidenceWorkflowDerivedTiming)
	}
	if measurements.HomebrewV0015.RuntimeJobCountPerRun != 3 || measurements.HomebrewV0015.PullRequestHeadCI.Conclusion != "success" || measurements.HomebrewV0015.PostMergeCI.Conclusion != "success" {
		t.Fatalf("Homebrew run baseline is incomplete: %+v", measurements.HomebrewV0015)
	}
}

func assertRunTotals(t *testing.T, set baselineRunSet) {
	t.Helper()
	var got baselineRunTotals
	for _, measurement := range set.Measurements {
		got.JobCount += measurement.JobCount
		got.WallSeconds += measurement.WallSeconds
		got.AggregateRunnerSeconds += measurement.AggregateRunnerSeconds
	}
	if got != set.Totals {
		t.Fatalf("run totals=%+v, want sum %+v", set.Totals, got)
	}
}

func indexRuns(t *testing.T, set baselineRunSet) map[string]baselineRunMeasurement {
	t.Helper()
	indexed := map[string]baselineRunMeasurement{}
	for _, measurement := range set.Measurements {
		if measurement.Scenario == "" || measurement.RunID < 1 || measurement.RunAttempt < 1 || indexed[measurement.Scenario].RunID != 0 {
			t.Fatalf("invalid or duplicate run measurement: %+v", measurement)
		}
		indexed[measurement.Scenario] = measurement
	}
	return indexed
}

func assertEvidenceBaselineArithmetic(t *testing.T, snapshot baselineEvidenceSnapshot) {
	t.Helper()
	projection := snapshot.V0015RootAndAttemptProjection
	if projection.PathCount != 2*projection.UniqueGitBlobCount || projection.LogicalPathBytes != 2*projection.UniqueGitBlobUncompressedBytes {
		t.Fatalf("v0.0.15 evidence mirror accounting is inconsistent: %+v", projection)
	}
	content := snapshot.AttestationDocumentContent
	if content.DuplicateDocumentBytes != content.DocumentBytesCountingEachEntry-content.DocumentBytesAfterContentDeduplication ||
		content.DuplicateFractionBasisPoints*content.DocumentBytesCountingEachEntry != 10000*content.DuplicateDocumentBytes ||
		content.VerificationEntryCount != 10 || content.UniqueDocumentCount != 2 || snapshot.OfflineReplayResult != "pass" {
		t.Fatalf("attestation content accounting is inconsistent: %+v result=%q", content, snapshot.OfflineReplayResult)
	}
	archives := snapshot.ActionsArtifactArchives
	if archives.ReportedSizeField != "size_in_bytes" || len(archives.Artifacts) != 2 ||
		archives.Artifacts[0] != (baselineActionsArtifact{
			Role: "candidate", ArtifactID: 8405407104,
			Name:                 "env-vault-release-evidence-candidate-v0.0.15-c7dd1fd6176ac2abbea22f226795a0787e774c1b-29576963736-1",
			ReportedArchiveBytes: 178515,
		}) || archives.Artifacts[1] != (baselineActionsArtifact{
		Role: "published", ArtifactID: 8405417386,
		Name:                 "env-vault-release-evidence-v0.0.15-af521d52b898088cb49f6256964e377e33e95a5d-publisher-29576465336-attempt-1-evidence-29576963736-1",
		ReportedArchiveBytes: 175700,
	}) {
		t.Fatalf("Actions artifact archive measurements drifted: %+v", archives)
	}
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func assertNoUnsupportedSizeKeys(t *testing.T, data []byte) {
	t.Helper()
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{
		"network_transfer_bytes": true,
		"hosted_storage_bytes":   true,
		"billing_bytes":          true,
		"compressed_bytes":       true,
	}
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if forbidden[key] {
					t.Fatalf("unsupported storage/transfer claim key %q", key)
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(document)
}
