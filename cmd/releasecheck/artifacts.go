package main

import (
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/actionsartifact"
)

const artifactSnapshotValidationSchemaID = "env-vault.actions-artifact-snapshot-validation.v1"

type artifactSnapshotValidation struct {
	SchemaID               string `json:"schema_id"`
	SchemaVersion          int    `json:"schema_version"`
	OK                     bool   `json:"ok"`
	SnapshotSemanticSHA256 string `json:"snapshot_semantic_sha256"`
	ArtifactCount          int    `json:"artifact_count"`
	ArtifactBytes          int64  `json:"artifact_bytes"`
	RunCount               int    `json:"run_count"`
	AttemptCount           int    `json:"attempt_count"`
}

func runArtifacts(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, artifactsUsage())
		return exitUsage
	}
	switch args[0] {
	case "validate-policy":
		return runArtifactsValidatePolicy(args[1:], stdout, stderr)
	case "assemble-snapshot":
		return runArtifactsAssembleSnapshot(args[1:], stdout, stderr)
	case "validate-snapshot":
		return runArtifactsValidateSnapshot(args[1:], stdout, stderr)
	case "derive-scope":
		return runArtifactsDeriveScope(args[1:], stdout, stderr)
	case "classify":
		return runArtifactsClassify(args[1:], stdout, stderr)
	case "package-manifest":
		return runArtifactsPackageManifest(args[1:], stdout, stderr)
	case "verify-manifest-package":
		return runArtifactsVerifyManifestPackage(args[1:], stdout, stderr)
	default:
		fmt.Fprint(stderr, artifactsUsage())
		return exitUsage
	}
}

func runArtifactsDeriveScope(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("artifacts derive-scope")
	policyPath := set.String("policy", actionsartifact.CanonicalPolicyPath, "checked Actions artifact policy JSON")
	snapshotPath := set.String("snapshot", "", "typed Actions artifact snapshot")
	collection := set.String("live-collection", "", "checked live-scope raw collection directory")
	nowValue := set.String("now", "", "explicit canonical UTC validation time")
	maxAgeValue := set.String("max-age", "", "positive shared freshness bound")
	observationOutput := set.String("observation-output", "", "canonical live observation output path")
	repairOutput := set.String("repair-proof-output", "", "canonical repair proof output path")
	output := set.String("output", "", "derived decision scope output path or -")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *policyPath == "" || *snapshotPath == "" || *collection == "" || *nowValue == "" || *maxAgeValue == "" || *observationOutput == "" || *repairOutput == "" || *output == "" || *observationOutput == "-" || *repairOutput == "-" {
		fmt.Fprint(stderr, artifactsUsage())
		return exitUsage
	}
	now, maxAge, err := parseArtifactFreshness(*nowValue, *maxAgeValue)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	policy, err := actionsartifact.LoadPolicyFile(*policyPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	snapshot, err := actionsartifact.LoadSnapshotFile(*snapshotPath, policy, now, maxAge)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	observation, repair, scope, err := actionsartifact.DeriveLiveDecisionScope(*collection, snapshot, policy, now, maxAge)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	observationData, err := actionsartifact.MarshalCanonical(observation)
	if err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	repairData, err := actionsartifact.MarshalCanonical(repair)
	if err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	scopeData, err := actionsartifact.MarshalCanonical(scope)
	if err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeArtifactOutput(*observationOutput, observationData, stdout); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeArtifactOutput(*repairOutput, repairData, stdout); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeArtifactOutput(*output, scopeData, stdout); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if *output != "-" {
		fmt.Fprintf(stdout, "derived Actions artifact scope: repositories=%d live_observation_sha256=%s repair_proof_sha256=%s\n", len(scope.Repositories), observation.SemanticSHA256, repair.SemanticSHA256)
	}
	return exitOK
}

func runArtifactsValidatePolicy(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("artifacts validate-policy")
	policyPath := set.String("policy", actionsartifact.CanonicalPolicyPath, "checked Actions artifact policy JSON")
	workflowDirectory := set.String("workflow-dir", ".github/workflows", "local workflow source directory")
	jsonOutput := set.Bool("json", false, "emit versioned JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *policyPath == "" || *workflowDirectory == "" {
		fmt.Fprint(stderr, artifactsUsage())
		return exitUsage
	}
	policy, err := actionsartifact.LoadPolicyFile(*policyPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	document, err := actionsartifact.ValidateWorkflowDirectory(policy, *workflowDirectory)
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
	fmt.Fprintf(stdout, "valid Actions artifact policy: sites=%d workflows=%d classes=%d semantic_sha256=%s\n", document.UploadSiteCount, document.WorkflowCount, document.ClassCount, document.PolicySHA256)
	return exitOK
}

func runArtifactsAssembleSnapshot(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("artifacts assemble-snapshot")
	policyPath := set.String("policy", actionsartifact.CanonicalPolicyPath, "checked Actions artifact policy JSON")
	collection := set.String("collection", "", "raw collector directory")
	output := set.String("output", "", "snapshot output path or -")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *policyPath == "" || *collection == "" || *output == "" {
		fmt.Fprint(stderr, artifactsUsage())
		return exitUsage
	}
	policy, err := actionsartifact.LoadPolicyFile(*policyPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	snapshot, err := actionsartifact.AssembleSnapshot(*collection, policy)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	data, err := actionsartifact.MarshalCanonical(snapshot)
	if err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeArtifactOutput(*output, data, stdout); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if *output != "-" {
		fmt.Fprintf(stdout, "assembled Actions artifact snapshot: artifacts=%d bytes=%d runs=%d attempts=%d\n", snapshot.ArtifactCount, snapshot.ArtifactBytes, snapshot.RunCount, snapshot.AttemptCount)
	}
	return exitOK
}

func runArtifactsValidateSnapshot(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("artifacts validate-snapshot")
	policyPath := set.String("policy", actionsartifact.CanonicalPolicyPath, "checked Actions artifact policy JSON")
	snapshotPath := set.String("snapshot", "", "typed Actions artifact snapshot")
	nowValue := set.String("now", "", "explicit canonical UTC validation time")
	maxAgeValue := set.String("max-age", "", "positive snapshot age bound")
	jsonOutput := set.Bool("json", false, "emit versioned JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *policyPath == "" || *snapshotPath == "" || *nowValue == "" || *maxAgeValue == "" {
		fmt.Fprint(stderr, artifactsUsage())
		return exitUsage
	}
	now, maxAge, err := parseArtifactFreshness(*nowValue, *maxAgeValue)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	policy, err := actionsartifact.LoadPolicyFile(*policyPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	snapshot, err := actionsartifact.LoadSnapshotFile(*snapshotPath, policy, now, maxAge)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	digest, err := actionsartifact.SnapshotSemanticSHA256(snapshot, policy, now, maxAge)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	document := artifactSnapshotValidation{
		SchemaID: artifactSnapshotValidationSchemaID, SchemaVersion: 1, OK: true, SnapshotSemanticSHA256: digest,
		ArtifactCount: snapshot.ArtifactCount, ArtifactBytes: snapshot.ArtifactBytes, RunCount: snapshot.RunCount, AttemptCount: snapshot.AttemptCount,
	}
	if *jsonOutput {
		if err := writeJSON(stdout, document); err != nil {
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "valid Actions artifact snapshot: artifacts=%d bytes=%d runs=%d attempts=%d semantic_sha256=%s\n", snapshot.ArtifactCount, snapshot.ArtifactBytes, snapshot.RunCount, snapshot.AttemptCount, digest)
	}
	return exitOK
}

func runArtifactsClassify(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("artifacts classify")
	policyPath := set.String("policy", actionsartifact.CanonicalPolicyPath, "checked Actions artifact policy JSON")
	snapshotPath := set.String("snapshot", "", "typed Actions artifact snapshot")
	scopePath := set.String("scope", "", "strict live decision scope")
	liveCollection := set.String("live-collection", "", "checked raw collection used to replay the supplied scope")
	nowValue := set.String("now", "", "explicit canonical UTC validation time")
	maxAgeValue := set.String("max-age", "", "positive snapshot age bound")
	output := set.String("output", "", "decision manifest output path or -")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *policyPath == "" || *snapshotPath == "" || *scopePath == "" || *liveCollection == "" || *nowValue == "" || *maxAgeValue == "" || *output == "" {
		fmt.Fprint(stderr, artifactsUsage())
		return exitUsage
	}
	now, maxAge, err := parseArtifactFreshness(*nowValue, *maxAgeValue)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	policy, err := actionsartifact.LoadPolicyFile(*policyPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	snapshot, err := actionsartifact.LoadSnapshotFile(*snapshotPath, policy, now, maxAge)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	scope, err := actionsartifact.LoadDecisionScopeFile(*scopePath, snapshot, now, maxAge)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	_, _, expectedScope, err := actionsartifact.DeriveLiveDecisionScope(*liveCollection, snapshot, policy, now, maxAge)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	if !reflect.DeepEqual(scope, expectedScope) {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", fmt.Errorf("decision scope does not equal checked live-collection replay"), exitSnapshotInvalid)
	}
	manifest, err := actionsartifact.Classify(snapshot, scope, policy, now, maxAge)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	data, err := actionsartifact.MarshalCanonical(manifest)
	if err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeArtifactOutput(*output, data, stdout); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if *output != "-" {
		fmt.Fprintf(stdout, "classified Actions artifacts: before=%d keep=%d delete=%d semantic_sha256=%s\n", manifest.Totals.Before.Count, manifest.Totals.Keep.Count, manifest.Totals.Delete.Count, manifest.SemanticSHA256)
	}
	return exitOK
}

func parseArtifactFreshness(nowValue, maxAgeValue string) (time.Time, time.Duration, error) {
	now, err := time.Parse(time.RFC3339Nano, nowValue)
	if err != nil || now.Location() != time.UTC || now.Format(time.RFC3339Nano) != nowValue {
		return time.Time{}, 0, fmt.Errorf("--now must be canonical UTC RFC3339")
	}
	maxAge, err := time.ParseDuration(maxAgeValue)
	if err != nil || maxAge <= 0 || maxAge > actionsartifact.MaxSnapshotAge {
		return time.Time{}, 0, fmt.Errorf("--max-age must be a positive duration no greater than 1h")
	}
	return now, maxAge, nil
}

func writeArtifactOutput(output string, data []byte, stdout io.Writer) error {
	if output == "-" {
		_, err := stdout.Write(data)
		return err
	}
	return writeExclusiveFile(output, data)
}

func artifactsUsage() string {
	return `usage:
  releasecheck artifacts validate-policy [--policy FILE] [--workflow-dir DIR] [--json]
  releasecheck artifacts assemble-snapshot --collection DIR --output FILE|- [--policy FILE]
  releasecheck artifacts validate-snapshot --snapshot FILE --now UTC --max-age DURATION [--policy FILE] [--json]
  releasecheck artifacts derive-scope --snapshot FILE --live-collection DIR --now UTC --max-age DURATION --observation-output FILE --repair-proof-output FILE --output FILE|- [--policy FILE]
  releasecheck artifacts classify --snapshot FILE --scope FILE --live-collection DIR --now UTC --max-age DURATION --output FILE|- [--policy FILE]
  releasecheck artifacts package-manifest --manifest FILE --repository-root DIR
  releasecheck artifacts verify-manifest-package --repository-root DIR --manifest-sha256 SHA256 [--compare-manifest FILE] [--manifest-output FILE] [--json]
`
}
