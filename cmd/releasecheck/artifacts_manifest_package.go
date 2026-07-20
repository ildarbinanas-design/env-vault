package main

import (
	"fmt"
	"io"
	"reflect"

	"github.com/ildarbinanas-design/env-vault/internal/actionsartifact"
)

func runArtifactsPackageManifest(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("artifacts package-manifest")
	manifestPath := set.String("manifest", "", "exact canonical Stage-5 decision manifest")
	repositoryRoot := set.String("repository-root", "", "reviewed repository worktree root")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *manifestPath == "" || *repositoryRoot == "" {
		fmt.Fprint(stderr, artifactsUsage())
		return exitUsage
	}
	result, err := actionsartifact.CreateManifestPackage(*manifestPath, *repositoryRoot)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_OR_OUTPUT_INVALID", err, exitSnapshotInvalid)
	}
	fmt.Fprintf(stdout, "packaged Actions artifact manifest: semantic_sha256=%s raw_sha256=%s gzip_sha256=%s before=%d keep=%d delete=%d expected_after=%d object=%s summary=%s\n",
		result.Summary.ManifestSemanticSHA256, result.Summary.ManifestRawSHA256, result.Summary.ManifestGZIPSHA256,
		result.Summary.Totals.Before.Count, result.Summary.Totals.Keep.Count, result.Summary.Totals.Delete.Count,
		result.Summary.Totals.ExpectedAfter.Count, result.ObjectRelativePath, result.SummaryRelativePath)
	return exitOK
}

func runArtifactsVerifyManifestPackage(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("artifacts verify-manifest-package")
	repositoryRoot := set.String("repository-root", "", "reviewed repository worktree root")
	semanticSHA256 := set.String("manifest-sha256", "", "manifest semantic SHA-256")
	compareManifest := set.String("compare-manifest", "", "optional exact canonical manifest to compare")
	manifestOutput := set.String("manifest-output", "", "optional private no-clobber reconstructed manifest output")
	jsonOutput := set.Bool("json", false, "emit the safe package summary as JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *repositoryRoot == "" || *semanticSHA256 == "" || *manifestOutput == "-" {
		fmt.Fprint(stderr, artifactsUsage())
		return exitUsage
	}
	result, manifest, err := actionsartifact.VerifyManifestPackage(*repositoryRoot, *semanticSHA256)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	if *compareManifest != "" {
		comparison, err := actionsartifact.LoadAuthorizedDecisionManifestFile(*compareManifest)
		if err != nil || !reflect.DeepEqual(comparison, manifest) {
			if err == nil {
				err = fmt.Errorf("comparison manifest does not exactly equal the reconstructed canonical manifest")
			}
			return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
		}
	}
	if *manifestOutput != "" {
		data, err := actionsartifact.MarshalCanonical(manifest)
		if err != nil {
			return writeFailure(stdout, stderr, *jsonOutput, "OUTPUT_FAILED", err, exitInternal)
		}
		if err := actionsartifact.WriteNoClobber(*manifestOutput, data); err != nil {
			return writeFailure(stdout, stderr, *jsonOutput, "OUTPUT_FAILED", fmt.Errorf("reconstructed manifest output is not a new writable file: %w", err), exitInternal)
		}
	}
	if *jsonOutput {
		if err := writeJSON(stdout, result.Summary); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	fmt.Fprintf(stdout, "verified Actions artifact manifest package: semantic_sha256=%s raw_sha256=%s gzip_sha256=%s before=%d keep=%d delete=%d expected_after=%d\n",
		result.Summary.ManifestSemanticSHA256, result.Summary.ManifestRawSHA256, result.Summary.ManifestGZIPSHA256,
		result.Summary.Totals.Before.Count, result.Summary.Totals.Keep.Count, result.Summary.Totals.Delete.Count,
		result.Summary.Totals.ExpectedAfter.Count)
	return exitOK
}
