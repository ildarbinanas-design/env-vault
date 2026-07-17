// Command e2e-baseline generates, updates, and verifies the durable E2E
// compatibility baseline without downloading historical workflow artifacts.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/e2ebaseline"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "e2e-baseline:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("command required: generate, update, or verify")
	}
	command, args := args[0], args[1:]
	switch command {
	case "generate", "update":
		return runGenerate(command, args, stdout, stderr)
	case "verify":
		return runVerify(args, stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q (want generate, update, or verify)", command)
	}
}

func runGenerate(command string, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("e2e-baseline "+command, flag.ContinueOnError)
	set.SetOutput(stderr)
	proof := set.String("proof", "", "sealed matrix-validation.json proof")
	repositoryRoot := set.String("repository-root", ".", "repository root for semantic suite hashing")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract path")
	baselinePath := set.String("baseline", e2ebaseline.CanonicalPath, "existing baseline path (update only)")
	output := set.String("output", "", "generated baseline output path")
	diffOutput := set.String("diff-output", "", "reviewable JSON diff output path (update only)")
	transition := set.String("suite-transition-code", "", "explicit reviewed suite transition code")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 || *proof == "" {
		return errors.New("--proof is required and positional arguments are not accepted")
	}
	if *output == "" {
		if command == "generate" {
			return errors.New("generate requires --output")
		}
		*output = *baselinePath
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return err
	}
	baseline, err := e2ebaseline.Generate(e2ebaseline.GenerateOptions{
		ProofPath:           *proof,
		RepositoryRoot:      *repositoryRoot,
		SuiteTransitionCode: *transition,
	}, contract)
	if err != nil {
		return err
	}
	if command == "update" {
		previous, err := os.ReadFile(*baselinePath)
		if err != nil {
			return fmt.Errorf("read existing baseline: %w", err)
		}
		diff, err := e2ebaseline.DiffJSON(previous, baseline)
		if err != nil {
			return err
		}
		if *diffOutput == "" {
			*diffOutput = *baselinePath + ".diff.json"
		}
		if err := e2ebaseline.WriteFile(*diffOutput, diff); err != nil {
			return fmt.Errorf("write baseline diff: %w", err)
		}
	}
	if err := e2ebaseline.WriteFile(*output, baseline); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}
	digest, err := e2ebaseline.Digest(baseline)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "wrote %s digest=%s\n", *output, digest)
	return err
}

func runVerify(args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("e2e-baseline verify", flag.ContinueOnError)
	set.SetOutput(stderr)
	baselinePath := set.String("baseline", e2ebaseline.CanonicalPath, "durable baseline path")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract path")
	proof := set.String("proof", "", "sealed matrix-validation.json proof")
	output := set.String("output", "", "verification report directory")
	repositoryRoot := set.String("repository-root", ".", "repository root for semantic suite hashing")
	phase := set.String("phase", "candidate", "required report phase")
	expectedCommit := set.String("expected-commit", "", "exact source commit")
	expectedRunID := set.String("expected-run-id", "", "exact workflow run ID")
	expectedRunURL := set.String("expected-run-url", "", "exact workflow run URL")
	expectedRunAttempt := set.String("expected-run-attempt", "", "exact workflow run attempt")
	expectedRepository := set.String("expected-repository", "", "exact owner/repository")
	sourceProof := set.Bool("source-proof", false, "verify only the exact provenance reports used to derive the baseline")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 || *proof == "" {
		return errors.New("--proof is required and positional arguments are not accepted")
	}
	if *output == "" {
		*output = filepath.Dir(*proof)
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return err
	}
	baseline, err := e2ebaseline.LoadFile(*baselinePath, contract)
	if err != nil {
		return err
	}
	report, verifyErr := e2ebaseline.Verify(e2ebaseline.VerifyOptions{
		ProofPath:          *proof,
		RepositoryRoot:     *repositoryRoot,
		Phase:              *phase,
		ExpectedCommit:     *expectedCommit,
		ExpectedRunID:      *expectedRunID,
		ExpectedRunURL:     *expectedRunURL,
		ExpectedRunAttempt: *expectedRunAttempt,
		ExpectedRepository: *expectedRepository,
		SourceProof:        *sourceProof,
	}, baseline, contract)
	jsonPath := filepath.Join(*output, "baseline-verification.json")
	markdownPath := filepath.Join(*output, "baseline-verification.md")
	if err := e2ebaseline.WriteFile(jsonPath, report); err != nil {
		return err
	}
	if err := os.WriteFile(markdownPath, e2ebaseline.VerificationMarkdown(report), 0o600); err != nil {
		return err
	}
	if verifyErr != nil {
		return fmt.Errorf("%w; see %s", verifyErr, jsonPath)
	}
	_, err = fmt.Fprintf(stdout, "verified durable E2E baseline for %s\n", strings.Join(report.Platforms, ", "))
	return err
}
