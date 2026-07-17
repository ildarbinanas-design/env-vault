// releasecheck validates saved release inputs without network or credentials.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasemetrics"
)

const (
	checkerVersion = "1.3.0"

	exitOK              = 0
	exitUsage           = 2
	exitContractInvalid = 3
	exitActionRequired  = 4
	exitSnapshotInvalid = 5
	exitInternal        = 6
)

// sourceRevision can be set for archive builds with -ldflags -X. Development
// builds fall back to Go's VCS build settings.
var sourceRevision string

type errorDocument struct {
	SchemaID      string    `json:"schema_id"`
	SchemaVersion int       `json:"schema_version"`
	OK            bool      `json:"ok"`
	Error         errorInfo `json:"error"`
}

type errorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type versionDocument struct {
	SchemaID                string           `json:"schema_id"`
	SchemaVersion           int              `json:"schema_version"`
	OK                      bool             `json:"ok"`
	CheckerVersion          string           `json:"checker_version"`
	SourceRevision          string           `json:"source_revision,omitempty"`
	SourceModified          *bool            `json:"source_modified,omitempty"`
	SupportedSchemaVersions map[string][]int `json:"supported_schema_versions"`
	ReleaseContractSchema   string           `json:"release_contract_schema"`
	SemanticContractSHA256  string           `json:"semantic_contract_sha256"`
}

type validationDocument struct {
	SchemaID               string `json:"schema_id"`
	SchemaVersion          int    `json:"schema_version"`
	OK                     bool   `json:"ok"`
	ReleaseContractSchema  string `json:"release_contract_schema"`
	SemanticContractSHA256 string `json:"semantic_contract_sha256"`
	PlatformCount          int    `json:"platform_count"`
	AssetCount             int    `json:"asset_count"`
}

type legacyDocument struct {
	SchemaID                string `json:"schema_id"`
	SchemaVersion           int    `json:"schema_version"`
	OK                      bool   `json:"ok"`
	Version                 string `json:"version"`
	TagSHA                  string `json:"tag_sha"`
	GoVersion               string `json:"go_version"`
	LiteralVersionSupported bool   `json:"literal_version_supported"`
	PublicationEligible     bool   `json:"publication_eligible"`
	ActionCode              string `json:"action_code"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText())
		return exitUsage
	}
	switch args[0] {
	case "help", "--help", "-h":
		if len(args) != 1 {
			fmt.Fprint(stderr, usageText())
			return exitUsage
		}
		fmt.Fprint(stdout, usageText())
		return exitOK
	case "validate-contract":
		return runValidateContract(args[1:], stdout, stderr)
	case "classify-attempt":
		return runClassifyAttempt(args[1:], stdout, stderr)
	case "metrics":
		if len(args) > 1 && args[1] == "compare" {
			return runMetricsCompare(args[2:], stdout, stderr)
		}
		return runMetrics(args[1:], stdout, stderr)
	case "legacy":
		return runLegacy(args[1:], stdout, stderr)
	case "contract":
		return runContract(args[1:], stdout, stderr)
	case "promotion":
		return runPromotion(args[1:], stdout, stderr)
	case "evidence":
		return runEvidence(args[1:], stdout, stderr)
	case "settings":
		return runSettings(args[1:], stdout, stderr)
	case "recovery":
		return runRecovery(args[1:], stdout, stderr)
	default:
		return runRootFlags(args, stdout, stderr)
	}
}

func runRootFlags(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("releasecheck")
	showVersion := set.Bool("version", false, "print releasecheck version and contract identity")
	jsonOutput := set.Bool("json", false, "emit versioned JSON")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	if err := set.Parse(args); err != nil || !*showVersion || set.NArg() != 0 {
		fmt.Fprint(stderr, usageText())
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	digest, err := releasecontract.SemanticSHA256(contract)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	revision, modified, revisionAvailable := buildRevision()
	document := versionDocument{
		SchemaID: releasecontract.VersionSchemaID, SchemaVersion: 1, OK: true,
		CheckerVersion: checkerVersion,
		SupportedSchemaVersions: map[string][]int{
			"attempt_classification":                {1},
			"contract_validation":                   {1},
			"e2e_matrix_proof":                      {1},
			"legacy_rebuild_query":                  {1},
			"legacy_rebuild_diagnostic":             {1},
			"release_contract":                      {releasecontract.SchemaVersion},
			"release_contract_matrix":               {1},
			"releasecheck_error":                    {1},
			"releasecheck_version":                  {1},
			"release_metrics":                       {1},
			"release_metrics_baseline":              {1},
			"release_metrics_comparison":            {1},
			"source_quality_proof":                  {1},
			"literal_version_results":               {1},
			"promotion_platform":                    {1},
			"promotion_manifest":                    {1},
			"promotion_verification":                {1},
			"release_observation":                   {1},
			"release_health_proof":                  {1},
			"release_authorization":                 {1},
			"release_please_recovery":               {1},
			"release_please_recovery_check":         {1},
			"attestation_verification_bundle":       {1},
			"release_evidence":                      {1},
			"release_evidence_bundle":               {2},
			"release_evidence_bundle_verification":  {1},
			"release_evidence_parity":               {1},
			"release_evidence_storage_metrics":      {1},
			"release_evidence_genesis":              {1},
			"release_evidence_genesis_verification": {1},
			"repository_release_settings_check":     {1},
			"repository_release_settings_proof":     {1},
		},
		ReleaseContractSchema: contract.SchemaID, SemanticContractSHA256: digest,
	}
	if revisionAvailable {
		document.SourceRevision = revision
		document.SourceModified = &modified
	}
	if *jsonOutput {
		if err := writeJSON(stdout, document); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	fmt.Fprintf(stdout, "releasecheck %s\nrelease contract: %s\nsemantic contract sha256: %s\n", checkerVersion, contract.SchemaID, digest)
	if revisionAvailable {
		fmt.Fprintf(stdout, "source revision: %s (modified=%t)\n", revision, modified)
	}
	return exitOK
}

func runLegacy(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("legacy")
	version := set.String("version", "", "legacy version v0.0.1 through v0.0.7")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	jsonOutput := set.Bool("json", false, "emit versioned JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *version == "" {
		fmt.Fprint(stderr, legacyUsage())
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	legacy, found := contract.LegacyVersion(*version)
	if !releasecontract.IsVersion(*version) || !found || contract.VersionPolicy.LegacyRebuild.PublicationEligible {
		return writeFailure(stdout, stderr, *jsonOutput, "LEGACY_REBUILD_UNSUPPORTED", fmt.Errorf("version %q is not eligible for diagnostic legacy rebuild", *version), exitSnapshotInvalid)
	}
	document := legacyDocument{
		SchemaID: releasecontract.LegacyQuerySchemaID, SchemaVersion: 1, OK: true,
		Version: legacy.Version, TagSHA: legacy.TagSHA,
		GoVersion:               contract.VersionPolicy.LegacyRebuild.GoVersion,
		LiteralVersionSupported: legacy.LiteralVersionSupported,
		PublicationEligible:     contract.VersionPolicy.LegacyRebuild.PublicationEligible,
		ActionCode:              "dispatch_legacy_rebuild",
	}
	if *jsonOutput {
		if err := writeJSON(stdout, document); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "legacy diagnostic: version=%s tag_sha=%s go=%s publication_eligible=false\n", document.Version, document.TagSHA, document.GoVersion)
	}
	return exitOK
}

func runContract(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "matrix" {
		fmt.Fprint(stderr, contractUsage())
		return exitUsage
	}
	set := newFlagSet("contract matrix")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	jsonOutput := set.Bool("json", false, "emit GitHub Actions matrix JSON")
	if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 {
		fmt.Fprint(stderr, contractUsage())
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	matrix := contract.Matrix()
	if *jsonOutput {
		if err := writeJSON(stdout, matrix); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else {
		for _, platform := range matrix.Include {
			fmt.Fprintf(stdout, "%s %s\n", platform.ID, platform.Runner)
		}
	}
	return exitOK
}

func runMetrics(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("metrics")
	runPath := set.String("run-json", "", "saved gh run view JSON")
	outputPath := set.String("output", "", "output JSON file, or - for stdout")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *runPath == "" || *outputPath == "" {
		fmt.Fprint(stderr, metricsUsage())
		return exitUsage
	}
	data, err := readLimitedInput(*runPath, 32<<20)
	if err != nil {
		return writeFailure(stdout, stderr, *outputPath == "-", "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	runDocument, err := releasemetrics.DecodeGHRun(data)
	if err != nil {
		return writeFailure(stdout, stderr, *outputPath == "-", "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	metrics, err := releasemetrics.Compute(runDocument)
	if err != nil {
		return writeFailure(stdout, stderr, *outputPath == "-", "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	if err := releasemetrics.Validate(metrics); err != nil {
		return writeFailure(stdout, stderr, *outputPath == "-", "INPUT_INVALID", fmt.Errorf("validate computed metrics: %w", err), exitSnapshotInvalid)
	}
	if *outputPath == "-" {
		if err := writeJSON(stdout, metrics); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	encoded, err := json.Marshal(metrics)
	if err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	encoded = append(encoded, '\n')
	if err := writeExclusiveFile(*outputPath, encoded); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	return exitOK
}

func runValidateContract(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("validate-contract")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	jsonOutput := set.Bool("json", false, "emit versioned JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		fmt.Fprint(stderr, validateUsage())
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	digest, err := releasecontract.SemanticSHA256(contract)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	document := validationDocument{
		SchemaID: releasecontract.ValidationSchemaID, SchemaVersion: 1, OK: true,
		ReleaseContractSchema: contract.SchemaID, SemanticContractSHA256: digest,
		PlatformCount: len(contract.Platforms), AssetCount: len(contract.Assets),
	}
	if *jsonOutput {
		if err := writeJSON(stdout, document); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "valid release contract: schema=%s platforms=%d assets=%d semantic_sha256=%s\n", contract.SchemaID, len(contract.Platforms), len(contract.Assets), digest)
	}
	return exitOK
}

func runClassifyAttempt(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("classify-attempt")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	runPath := set.String("run", "", "saved GitHub REST workflow-run JSON")
	artifactsPath := set.String("artifacts", "", "saved GitHub REST run-artifacts JSON")
	jsonOutput := set.Bool("json", false, "emit versioned JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *runPath == "" || *artifactsPath == "" {
		fmt.Fprint(stderr, classifyUsage())
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	result, err := releasecontract.ClassifyAttemptFiles(*runPath, *artifactsPath, contract)
	if err != nil {
		code := releasecontract.ErrorCode(err)
		exitCode := exitSnapshotInvalid
		if code == "CONTRACT_INVALID" || code == "SCHEMA_UNSUPPORTED" {
			exitCode = exitContractInvalid
		}
		return writeFailure(stdout, stderr, *jsonOutput, code, err, exitCode)
	}
	if *jsonOutput {
		if err := writeJSON(stdout, result); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "attempt classification: run_id=%d attempt=%d action=%s reason=%s missing_targets=%s\n", result.RunID, result.Attempt, result.ActionCode, result.ReasonCode, strings.Join(result.MissingTargets, ","))
	}
	if result.OK {
		return exitOK
	}
	return exitActionRequired
}

func runRecovery(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "validate-config" {
		fmt.Fprint(stderr, recoveryUsage())
		return exitUsage
	}
	set := newFlagSet("recovery validate-config")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	configPath := set.String("config", "", "release-please config JSON")
	manifestPath := set.String("manifest", "", "release-please manifest JSON")
	jsonOutput := set.Bool("json", false, "emit versioned JSON")
	if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 || *configPath == "" || *manifestPath == "" {
		fmt.Fprint(stderr, recoveryUsage())
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	document, err := releasecontract.CheckReleasePleaseRecoveryFiles(contract, *configPath, *manifestPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	if *jsonOutput {
		if err := writeJSON(stdout, document); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	fmt.Fprintf(stdout, "valid release-please recovery: state=%s abandoned=%s resume=%s semantic_sha256=%s\n", document.State, document.AbandonedVersion, document.ResumeVersion, document.SemanticContractSHA256)
	return exitOK
}

func newFlagSet(name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	return set
}

func writeFailure(stdout, stderr io.Writer, jsonOutput bool, code string, err error, exitCode int) int {
	if jsonOutput {
		document := errorDocument{
			SchemaID: releasecontract.ErrorSchemaID, SchemaVersion: 1, OK: false,
			Error: errorInfo{Code: code, Message: err.Error()},
		}
		if writeErr := writeJSON(stdout, document); writeErr != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", writeErr)
			return exitInternal
		}
	} else {
		fmt.Fprintf(stderr, "%s: %v\n", code, err)
	}
	return exitCode
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func readLimitedInput(filename string, limit int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	return data, nil
}

func writeExclusiveFile(filename string, data []byte) (err error) {
	base := filepath.Base(filename)
	if base == "" || base == "." || base == ".." || filepath.Clean(base) != base {
		return errors.New("output must have a safe basename")
	}
	parent := filepath.Dir(filename)
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("resolve output parent: %w", err)
	}
	info, err := os.Stat(realParent)
	if err != nil {
		return fmt.Errorf("stat output parent: %w", err)
	}
	if !info.IsDir() {
		return errors.New("output parent is not a directory")
	}
	target := filepath.Join(realParent, base)
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create no-clobber output: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = file.Close()
			_ = os.Remove(target)
		}
	}()
	written, err := file.Write(data)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	complete = true
	return nil
}

func buildRevision() (revision string, modified bool, available bool) {
	if sourceRevision != "" {
		return sourceRevision, false, true
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false, false
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			parsed, err := strconv.ParseBool(setting.Value)
			if err == nil {
				modified = parsed
			}
		}
	}
	return revision, modified, revision != ""
}

func usageText() string {
	return `releasecheck is an offline validator for saved release JSON and artifacts.
It never accesses the network or credentials. Use gh outside this process to
save GitHub responses, then pass only filenames to releasecheck.

Usage:
  releasecheck --version [--contract FILE] [--json]
  releasecheck validate-contract [--contract FILE] [--json]
  releasecheck classify-attempt --run FILE --artifacts FILE [--contract FILE] [--json]
  releasecheck metrics --run-json FILE --output FILE|-
  releasecheck metrics compare --main-ci FILE --pr-ci FILE --publisher FILE --output FILE|- [--markdown-output FILE]
  releasecheck legacy --version v0.0.N [--contract FILE] [--json]
  releasecheck contract matrix [--contract FILE] [--json]
  releasecheck promotion <record-platform|seal-source-quality|assemble|verify> ...
  releasecheck evidence <seal-health|assemble|verify> ...
  releasecheck settings <check|seal|verify> ...
  releasecheck recovery validate-config --contract FILE --config FILE --manifest FILE [--json]
  releasecheck help

classify-attempt expects one complete REST workflow-run object and either one
complete run-artifacts response ({"total_count":N,"artifacts":[...]}) or the
array produced by gh api --paginate --slurp. Artifacts from prior attempts may
be present but can never satisfy the current attempt.

Transport examples (run outside releasecheck):
  gh api "repos/$REPOSITORY/actions/runs/$RUN_ID" > run.json
  gh api --paginate --slurp "repos/$REPOSITORY/actions/runs/$RUN_ID/artifacts?per_page=100" > artifacts.json
  gh run view "$RUN_ID" --attempt "$RUN_ATTEMPT" --json attempt,conclusion,createdAt,databaseId,event,headSha,jobs,startedAt,status,updatedAt,url,workflowName > metrics-input.json

Exit statuses:
  0  requested offline validation or evidence generation succeeded
  2  command-line usage error
  3  release contract invalid or schema unsupported
  4  valid classification requires wait, inspection, or rerun_all_jobs
  5  saved input or promotion evidence invalid, incomplete, or inconsistent
  6  internal output failure

The classifier always emits rerun_failed_jobs_allowed=false and names
rerun_failed_jobs as prohibited. GitHub mutations are intentionally out of
scope; the caller may map rerun_all_jobs to an exact gh run rerun operation.
`
}

func validateUsage() string {
	return "usage: releasecheck validate-contract [--contract FILE] [--json]\n"
}

func classifyUsage() string {
	return "usage: releasecheck classify-attempt --run FILE --artifacts FILE [--contract FILE] [--json]\n"
}

func metricsUsage() string {
	return "usage: releasecheck metrics --run-json FILE --output FILE|-\n"
}

func legacyUsage() string {
	return "usage: releasecheck legacy --version v0.0.N [--contract FILE] [--json]\n"
}

func contractUsage() string {
	return "usage: releasecheck contract matrix [--contract FILE] [--json]\n"
}

func recoveryUsage() string {
	return "usage: releasecheck recovery validate-config --contract FILE --config FILE --manifest FILE [--json]\n"
}
