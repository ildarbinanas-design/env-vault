package main

import (
	"fmt"
	"io"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releaseevidence"
)

type genesisVerificationDocument struct {
	SchemaID            string `json:"schema_id"`
	SchemaVersion       int    `json:"schema_version"`
	OK                  bool   `json:"ok"`
	Repository          string `json:"repository"`
	FirstReleaseVersion string `json:"first_release_version"`
	SourceSHA           string `json:"source_sha"`
	FirstBundleSHA256   string `json:"first_bundle_sha256"`
	AnchorSHA256        string `json:"anchor_sha256"`
	PublisherRunID      int64  `json:"publisher_run_id"`
	PublisherRunAttempt int    `json:"publisher_run_attempt"`
	EvidenceRunID       int64  `json:"evidence_run_id"`
	EvidenceRunAttempt  int    `json:"evidence_run_attempt"`
	BundleTupleVerified bool   `json:"bundle_tuple_verified"`
	Decision            string `json:"decision"`
	ErrorCode           string `json:"error_code"`
}

func runEvidenceGenesisCreate(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("evidence genesis-create")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	bundleDir := set.String("bundle-dir", "", "complete first v2 bundle directory")
	output := set.String("output", "", "new canonical genesis anchor JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *bundleDir == "" || *output == "" || *output == "-" {
		fmt.Fprint(stderr, "usage: releasecheck evidence genesis-create --bundle-dir DIR --output FILE [--contract FILE]\n")
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	files, err := readBundleDirectory(*bundleDir)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	evidence, err := releaseevidence.VerifyBundle(files, contract)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	bundle, err := releaseevidence.ParseBundle(files.Root)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	anchor, err := releaseevidence.BuildGenesisAnchor(bundle, evidence)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	encoded, err := releaseevidence.MarshalJSON(anchor)
	if err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeExclusiveFile(*output, encoded); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	fmt.Fprintf(stdout, "created evidence ledger genesis: version=%s source_sha=%s bundle_sha256=%s anchor_sha256=%s\n", anchor.FirstReleaseVersion, anchor.SourceSHA, anchor.FirstBundleSHA256, anchor.AnchorSHA256)
	return exitOK
}

func runEvidenceGenesisVerify(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("evidence genesis-verify")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	input := set.String("input", "", "canonical genesis anchor JSON")
	bundleDir := set.String("bundle-dir", "", "optional complete first v2 bundle directory")
	jsonOutput := set.Bool("json", false, "emit typed verification JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *input == "" {
		fmt.Fprint(stderr, "usage: releasecheck evidence genesis-verify --input FILE [--bundle-dir DIR] [--contract FILE] [--json]\n")
		return exitUsage
	}
	data, err := readBoundedRegularFile(*input, releaseevidence.MaxGenesisAnchorBytes)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	anchor, err := releaseevidence.ParseGenesisAnchor(data)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, *jsonOutput, err)
	}
	var bundle *releaseevidence.Bundle
	var evidence *releaseevidence.Evidence
	if *bundleDir != "" {
		contract, contractErr := releasecontract.LoadFile(*contractPath)
		if contractErr != nil {
			return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", contractErr, exitContractInvalid)
		}
		files, readErr := readBundleDirectory(*bundleDir)
		if readErr != nil {
			return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", readErr, exitSnapshotInvalid)
		}
		reconstructed, verifyErr := releaseevidence.VerifyBundle(files, contract)
		if verifyErr != nil {
			return writeEvidenceFailure(stdout, stderr, *jsonOutput, verifyErr)
		}
		parsed, parseErr := releaseevidence.ParseBundle(files.Root)
		if parseErr != nil {
			return writeEvidenceFailure(stdout, stderr, *jsonOutput, parseErr)
		}
		bundle = &parsed
		evidence = &reconstructed
	}
	if err := releaseevidence.VerifyGenesisAnchor(anchor, bundle, evidence); err != nil {
		return writeEvidenceFailure(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		document := genesisVerificationDocument{
			SchemaID: "env-vault.release-evidence-genesis-verification.v1", SchemaVersion: 1, OK: true,
			Repository: anchor.Repository, FirstReleaseVersion: anchor.FirstReleaseVersion, SourceSHA: anchor.SourceSHA,
			FirstBundleSHA256: anchor.FirstBundleSHA256, AnchorSHA256: anchor.AnchorSHA256,
			PublisherRunID: anchor.PublisherRunID, PublisherRunAttempt: anchor.PublisherRunAttempt,
			EvidenceRunID: anchor.EvidenceRunID, EvidenceRunAttempt: anchor.EvidenceRunAttempt,
			BundleTupleVerified: bundle != nil, Decision: "pass", ErrorCode: "",
		}
		if err := writeJSON(stdout, document); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "verified evidence ledger genesis: version=%s source_sha=%s anchor_sha256=%s bundle_tuple_verified=%t\n", anchor.FirstReleaseVersion, anchor.SourceSHA, anchor.AnchorSHA256, bundle != nil)
	}
	return exitOK
}
