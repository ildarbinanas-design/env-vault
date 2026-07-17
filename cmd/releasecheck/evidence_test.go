package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/releaseevidence"
)

func TestEvidenceSealHealthCLIIsStrictAndNoClobber(t *testing.T) {
	proof := releaseevidence.HealthProof{
		SchemaID: releaseevidence.HealthProofSchemaID, SchemaVersion: 1,
		Repository: "ildarbinanas-design/env-vault", ReleaseVersion: "v0.0.9",
		SourceSHA: strings.Repeat("a", 40), PublisherRunID: 42, PublisherRunAttempt: 2,
		CheckedAt: "2026-07-16T09:00:00Z", TagExactSource: true, ReleasePublished: true,
		AssetsExact: true, AttestationsExact: true, HomebrewExact: true,
		HomebrewPRHeadCISuccess: true, HomebrewPostMergeCISuccess: true,
		BlockedVersionPolicyExact: true, AbandonedReleasePolicyExact: true, Result: "pass",
	}
	input, err := releaseevidence.MarshalJSON(proof)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	inputPath := filepath.Join(root, "health-input.json")
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "health.json")
	var stdout, stderr bytes.Buffer
	args := []string{"evidence", "seal-health", "--contract", canonicalContractPath(t), "--input", inputPath, "--output", output}
	if code := run(args, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	sealedData, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := releaseevidence.ParseHealthProof(sealedData)
	if err != nil {
		t.Fatal(err)
	}
	want, err := releaseevidence.HealthProofSHA256(sealed)
	if err != nil || sealed.ProofSHA256 != want || len(want) != 64 {
		t.Fatalf("sealed proof digest=%q want=%q err=%v", sealed.ProofSHA256, want, err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(args, &stdout, &stderr); code != exitInternal || !strings.Contains(stderr.String(), "file exists") {
		t.Fatalf("no-clobber code=%d stderr=%s", code, stderr.String())
	}
}

func TestEvidenceSealHealthRejectsFailedGuaranteeAndSymlink(t *testing.T) {
	proof := releaseevidence.HealthProof{
		SchemaID: releaseevidence.HealthProofSchemaID, SchemaVersion: 1,
		Repository: "ildarbinanas-design/env-vault", ReleaseVersion: "v0.0.9",
		SourceSHA: strings.Repeat("b", 40), PublisherRunID: 9, PublisherRunAttempt: 1,
		CheckedAt: "2026-07-16T09:00:00Z", Result: "pass",
	}
	data, err := releaseevidence.MarshalJSON(proof)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	input := filepath.Join(root, "input.json")
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"evidence", "seal-health", "--contract", canonicalContractPath(t), "--input", input, "--output", filepath.Join(root, "output.json"), "--json"}
	if code := run(args, &stdout, &stderr); code != exitSnapshotInvalid {
		t.Fatalf("failed guarantee code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	link := filepath.Join(root, "input-link.json")
	if err := os.Symlink(input, link); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	args[5] = link
	if code := run(args, &stdout, &stderr); code != exitSnapshotInvalid {
		t.Fatalf("symlink code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestEvidenceAssembleRequiresAuthorizationAndAttestationRecords(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{
		"evidence", "assemble",
		"--manifest", "manifest.json",
		"--ci-metrics", "ci.json",
		"--publisher-metrics", "publisher.json",
		"--observation", "observation.json",
		"--output", "evidence.json",
		"--markdown-output", "evidence.md",
	}
	if code := run(args, &stdout, &stderr); code != exitUsage {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--authorization FILE") || !strings.Contains(stderr.String(), "--attestations FILE") {
		t.Fatalf("usage does not document required authorization and attestation inputs: %s", stderr.String())
	}
}
