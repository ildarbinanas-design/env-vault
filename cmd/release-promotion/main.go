// Command release-promotion records, assembles, and verifies exact-source
// promotion evidence for the release planner and publisher.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "release-promotion:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("command required: record-platform, assemble, inventory, or verify")
	}
	switch args[0] {
	case "record-platform":
		return recordPlatform(args[1:], stdout, stderr)
	case "assemble":
		return assemble(args[1:], stdout, stderr)
	case "inventory":
		return inventory(args[1:], stdout, stderr)
	case "verify":
		return verify(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func inventory(args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("release-promotion inventory", flag.ContinueOnError)
	set.SetOutput(stderr)
	contract := set.String("contract", releasecontract.CanonicalPath, "release contract path")
	runPath := set.String("run-json", "", "workflow run API response path")
	artifactsPath := set.String("artifacts-json", "", "workflow artifact inventory API response path")
	source := set.String("source-sha", "", "exact source SHA")
	repository := set.String("repository", "", "owner/repository")
	runID := set.Int64("run-id", 0, "workflow run ID")
	attempt := set.Int("run-attempt", 0, "workflow run attempt")
	branch := set.String("branch", "", "expected default branch")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 || *runPath == "" || *artifactsPath == "" {
		return errors.New("unexpected arguments or missing --run-json/--artifacts-json")
	}
	if err := releasepromotion.ValidateInventory(releasepromotion.InventoryOptions{
		ContractPath: *contract, RunPath: *runPath, ArtifactsPath: *artifactsPath,
		SourceSHA: *source, Repository: *repository, RunID: *runID, RunAttempt: *attempt, Branch: *branch,
	}); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "verified exact promotion artifact inventory: source_sha=%s run_id=%d run_attempt=%d\n", *source, *runID, *attempt)
	return err
}

func recordPlatform(args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("release-promotion record-platform", flag.ContinueOnError)
	set.SetOutput(stderr)
	contract := set.String("contract", releasecontract.CanonicalPath, "release contract path")
	platform := set.String("platform", "", "release contract platform ID")
	source := set.String("source-sha", "", "exact source SHA")
	version := set.String("version", "", "exact vX.Y.Z or ci-SHA build version")
	repository := set.String("repository", "", "owner/repository")
	runID := set.Int64("run-id", 0, "workflow run ID")
	attempt := set.Int("run-attempt", 0, "workflow run attempt")
	archive := set.String("archive", "", "archive path")
	checksum := set.String("checksum", "", "checksum sidecar path")
	binary := set.String("binary", "", "native binary path")
	reports := set.String("reports", "", "candidate E2E reports root")
	artifactName := set.String("artifact-name", "", "attempt-qualified release artifact name")
	e2eArtifactName := set.String("e2e-artifact-name", "", "attempt-qualified E2E artifact name")
	coverageFloor := set.Float64("coverage-floor", 60, "minimum statement coverage")
	output := set.String("output", "", "platform evidence JSON path")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 || *output == "" {
		return errors.New("unexpected arguments or missing --output")
	}
	evidence, err := releasepromotion.RecordPlatform(releasepromotion.RecordOptions{
		ContractPath: *contract, PlatformID: *platform, SourceSHA: *source, ReleaseVersion: *version,
		Repository: *repository, RunID: *runID, RunAttempt: *attempt,
		ArchivePath: *archive, ChecksumPath: *checksum, BinaryPath: *binary, ReportsRoot: *reports,
		ArtifactName: *artifactName, E2EArtifactName: *e2eArtifactName, CoverageFloor: *coverageFloor,
	})
	if err != nil {
		return err
	}
	if err := releasepromotion.WriteJSON(*output, evidence); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "recorded exact platform promotion evidence: %s\n", *output)
	return err
}

func assemble(args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("release-promotion assemble", flag.ContinueOnError)
	set.SetOutput(stderr)
	contract := set.String("contract", releasecontract.CanonicalPath, "release contract path")
	source := set.String("source-sha", "", "exact source SHA")
	version := set.String("version", "", "exact vX.Y.Z")
	repository := set.String("repository", "", "owner/repository")
	runID := set.Int64("run-id", 0, "workflow run ID")
	attempt := set.Int("run-attempt", 0, "workflow run attempt")
	event := set.String("event", "", "workflow event")
	createdAt := set.String("created-at", "", "RFC3339 creation time")
	evidencePaths := set.String("evidence", "", "comma-separated platform evidence JSON paths")
	output := set.String("output", "", "promotion manifest JSON path")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 || *output == "" || *evidencePaths == "" {
		return errors.New("unexpected arguments or missing --evidence/--output")
	}
	when, err := time.Parse(time.RFC3339, *createdAt)
	if err != nil {
		return fmt.Errorf("created-at: %w", err)
	}
	var evidence []releasepromotion.PlatformEvidence
	for _, filename := range strings.Split(*evidencePaths, ",") {
		item, err := releasepromotion.ReadPlatform(filename)
		if err != nil {
			return fmt.Errorf("read platform evidence %s: %w", filename, err)
		}
		evidence = append(evidence, item)
	}
	manifest, err := releasepromotion.Assemble(releasepromotion.AssembleOptions{
		ContractPath: *contract, SourceSHA: *source, ReleaseVersion: *version, Repository: *repository,
		RunID: *runID, RunAttempt: *attempt, Event: *event, CreatedAt: when, Evidence: evidence,
	})
	if err != nil {
		return err
	}
	if err := releasepromotion.WriteJSON(*output, manifest); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "assembled promotion manifest for run %d attempt %d: %s\n", *runID, *attempt, *output)
	return err
}

func verify(args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("release-promotion verify", flag.ContinueOnError)
	set.SetOutput(stderr)
	contract := set.String("contract", releasecontract.CanonicalPath, "release contract path")
	manifestPath := set.String("manifest", "", "promotion manifest path")
	source := set.String("source-sha", "", "expected source SHA")
	version := set.String("version", "", "expected vX.Y.Z")
	repository := set.String("repository", "", "expected owner/repository")
	runIDText := set.String("run-id", "", "expected workflow run ID")
	attemptText := set.String("run-attempt", "", "expected workflow run attempt")
	artifacts := set.String("artifacts", "", "downloaded artifacts root")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 || *manifestPath == "" {
		return errors.New("unexpected arguments or missing --manifest")
	}
	var runID int64
	var attempt int
	var err error
	if *runIDText != "" {
		runID, err = strconv.ParseInt(*runIDText, 10, 64)
		if err != nil {
			return fmt.Errorf("run-id: %w", err)
		}
	}
	if *attemptText != "" {
		attempt, err = strconv.Atoi(*attemptText)
		if err != nil {
			return fmt.Errorf("run-attempt: %w", err)
		}
	}
	manifest, err := releasepromotion.ReadManifest(*manifestPath)
	if err != nil {
		return err
	}
	if err := releasepromotion.Verify(manifest, releasepromotion.VerifyOptions{
		ContractPath: *contract, SourceSHA: *source, ReleaseVersion: *version, Repository: *repository,
		RunID: runID, RunAttempt: attempt, ArtifactsRoot: *artifacts,
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "verified promotion tuple: version=%s source_sha=%s run_id=%d run_attempt=%d manifest_sha256=%s\n", manifest.ReleaseVersion, manifest.SourceSHA, manifest.Workflow.RunID, manifest.Workflow.RunAttempt, manifest.ManifestSHA256)
	return err
}
