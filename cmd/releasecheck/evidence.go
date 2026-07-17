package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releaseevidence"
	"github.com/ildarbinanas-design/env-vault/internal/releasemetrics"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
)

const maxEvidenceInputBytes = 16 << 20

var evidenceSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func runEvidence(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, evidenceUsage())
		return exitUsage
	}
	switch args[0] {
	case "seal-health":
		return runEvidenceSealHealth(args[1:], stdout, stderr)
	case "assemble":
		return runEvidenceAssemble(args[1:], stdout, stderr)
	case "verify":
		return runEvidenceVerify(args[1:], stdout, stderr)
	case "bundle-create":
		return runEvidenceBundleCreate(args[1:], stdout, stderr)
	case "bundle-verify":
		return runEvidenceBundleVerify(args[1:], stdout, stderr)
	case "bundle-parity":
		return runEvidenceBundleParity(args[1:], stdout, stderr)
	case "bundle-measure":
		return runEvidenceBundleMeasure(args[1:], stdout, stderr)
	case "genesis-create":
		return runEvidenceGenesisCreate(args[1:], stdout, stderr)
	case "genesis-verify":
		return runEvidenceGenesisVerify(args[1:], stdout, stderr)
	default:
		fmt.Fprint(stderr, evidenceUsage())
		return exitUsage
	}
}

func runEvidenceSealHealth(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("evidence seal-health")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	input := set.String("input", "", "unsealed health proof JSON")
	output := set.String("output", "", "new sealed health proof, or - for stdout")
	echoJSON := set.Bool("json", false, "also emit the sealed proof to stdout")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *input == "" || *output == "" {
		fmt.Fprint(stderr, evidenceSealHealthUsage())
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *echoJSON || *output == "-", "CONTRACT_INVALID", err, exitContractInvalid)
	}
	data, err := readRegularEvidenceInput(*input)
	if err != nil {
		return writeFailure(stdout, stderr, *echoJSON || *output == "-", "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	proof, err := releaseevidence.ParseHealthProof(data)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, *echoJSON || *output == "-", err)
	}
	if err := validateUnsealedHealthProof(proof, contract); err != nil {
		return writeEvidenceFailure(stdout, stderr, *echoJSON || *output == "-", err)
	}
	if err := releaseevidence.SealHealthProof(&proof); err != nil {
		return writeEvidenceFailure(stdout, stderr, *echoJSON || *output == "-", err)
	}
	encoded, err := releaseevidence.MarshalJSON(proof)
	if err != nil {
		return writeFailure(stdout, stderr, *echoJSON || *output == "-", "OUTPUT_FAILED", err, exitInternal)
	}
	if code := writeEvidenceOutput(*output, encoded, stdout, stderr); code != exitOK {
		return code
	}
	if *echoJSON && *output != "-" {
		if _, err := stdout.Write(encoded); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else if !*echoJSON && *output != "-" {
		fmt.Fprintf(stdout, "sealed release health proof: run_id=%d attempt=%d output=%s\n", proof.PublisherRunID, proof.PublisherRunAttempt, *output)
	}
	return exitOK
}

func runEvidenceAssemble(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("evidence assemble")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	authorizationPath := set.String("authorization", "", "release authorization and planning record JSON")
	attestationsPath := set.String("attestations", "", "raw attestation verification bundle JSON")
	manifestPath := set.String("manifest", "", "promotion manifest JSON")
	ciMetricsPath := set.String("ci-metrics", "", "CI metrics JSON")
	publisherMetricsPath := set.String("publisher-metrics", "", "publisher metrics JSON")
	observationPath := set.String("observation", "", "post-release observation JSON")
	output := set.String("output", "", "new durable evidence JSON")
	markdownOutput := set.String("markdown-output", "", "new deterministic Markdown index")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *authorizationPath == "" || *attestationsPath == "" || *manifestPath == "" || *ciMetricsPath == "" || *publisherMetricsPath == "" || *observationPath == "" || *output == "" || *markdownOutput == "" || *output == "-" || *markdownOutput == "-" {
		fmt.Fprint(stderr, evidenceAssembleUsage())
		return exitUsage
	}
	if filepath.Clean(*output) == filepath.Clean(*markdownOutput) {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", errors.New("evidence and Markdown outputs must differ"), exitInternal)
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	authorizationData, err := readRegularEvidenceInput(*authorizationPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	authorization, err := releaseevidence.ParseAuthorization(authorizationData)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	attestationsData, err := readRegularEvidenceInput(*attestationsPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	attestationBundle, err := releaseevidence.ParseAttestationVerificationBundle(attestationsData)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	manifestData, err := readRegularEvidenceInput(*manifestPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	manifest, err := releasepromotion.ParseManifest(manifestData)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	ciMetrics, err := parseEvidenceMetricsFile(*ciMetricsPath)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	publisherMetrics, err := parseEvidenceMetricsFile(*publisherMetricsPath)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	observationData, err := readRegularEvidenceInput(*observationPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	observation, err := releaseevidence.ParseObservation(observationData)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	evidence, err := releaseevidence.Assemble(contract, authorization, manifest, ciMetrics, publisherMetrics, observation, attestationBundle)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	encoded, err := releaseevidence.MarshalJSON(evidence)
	if err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	markdown, err := releaseevidence.Markdown(evidence, contract)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	if err := preflightEvidenceOutput(*output); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := preflightEvidenceOutput(*markdownOutput); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeExclusiveFile(*output, encoded); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeExclusiveFile(*markdownOutput, markdown); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	fmt.Fprintf(stdout, "assembled release evidence: version=%s source_sha=%s evidence_sha256=%s\n", evidence.ReleaseVersion, evidence.SourceSHA, evidence.EvidenceSHA256)
	return exitOK
}

func runEvidenceVerify(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("evidence verify")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	input := set.String("input", "", "durable evidence JSON")
	markdownOutput := set.String("markdown-output", "", "optional new deterministic Markdown index")
	jsonOutput := set.Bool("json", false, "emit the verified evidence JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *input == "" {
		fmt.Fprint(stderr, evidenceVerifyUsage())
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	data, err := readRegularEvidenceInput(*input)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	evidence, err := releaseevidence.ParseEvidence(data)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, *jsonOutput, err)
	}
	if err := releaseevidence.Verify(evidence, contract); err != nil {
		return writeEvidenceFailure(stdout, stderr, *jsonOutput, err)
	}
	if *markdownOutput != "" {
		markdown, err := releaseevidence.Markdown(evidence, contract)
		if err != nil {
			return writeEvidenceFailure(stdout, stderr, *jsonOutput, err)
		}
		if err := writeExclusiveFile(*markdownOutput, markdown); err != nil {
			return writeFailure(stdout, stderr, *jsonOutput, "OUTPUT_FAILED", err, exitInternal)
		}
	}
	if *jsonOutput {
		encoded, err := releaseevidence.MarshalJSON(evidence)
		if err != nil {
			return writeFailure(stdout, stderr, true, "OUTPUT_FAILED", err, exitInternal)
		}
		if _, err := stdout.Write(encoded); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "verified release evidence: version=%s source_sha=%s evidence_sha256=%s\n", evidence.ReleaseVersion, evidence.SourceSHA, evidence.EvidenceSHA256)
	}
	return exitOK
}

func parseEvidenceMetricsFile(filename string) (interfaceMetrics, error) {
	data, err := readRegularEvidenceInput(filename)
	if err != nil {
		return interfaceMetrics{}, err
	}
	return releaseevidence.ParseMetrics(data)
}

// interfaceMetrics aliases the concrete type without making callers duplicate
// the strict metrics parser at the evidence trust boundary.
type interfaceMetrics = releasemetrics.Metrics

func validateUnsealedHealthProof(proof releaseevidence.HealthProof, contract releasecontract.Contract) error {
	if proof.SchemaID != contract.Schemas["release_health_proof"] || proof.SchemaVersion != 1 || proof.ProofSHA256 != "" || proof.Result != "pass" {
		return errors.New("health proof schema, result, or unsealed state is invalid")
	}
	if proof.Repository == "" || !releasecontract.IsVersion(proof.ReleaseVersion) || !evidenceSHA.MatchString(proof.SourceSHA) || proof.PublisherRunID <= 0 || proof.PublisherRunAttempt <= 0 {
		return errors.New("health proof release or publisher identity is invalid")
	}
	checkedAt, err := time.Parse(time.RFC3339, proof.CheckedAt)
	if err != nil || checkedAt.Location() != time.UTC || checkedAt.Format(time.RFC3339) != proof.CheckedAt {
		return errors.New("health proof checked_at must be canonical UTC RFC3339")
	}
	if !proof.TagExactSource || !proof.ReleasePublished || !proof.AssetsExact || !proof.AttestationsExact || !proof.HomebrewExact || !proof.HomebrewPRHeadCISuccess || !proof.HomebrewPostMergeCISuccess || !proof.BlockedVersionPolicyExact || !proof.AbandonedReleasePolicyExact {
		return errors.New("health proof cannot be sealed before every guarantee passes")
	}
	return nil
}

func readRegularEvidenceInput(filename string) ([]byte, error) {
	before, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxEvidenceInputBytes {
		return nil, fmt.Errorf("%s is not a bounded regular non-symlink file", filename)
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() {
		return nil, fmt.Errorf("%s changed identity while opening", filename)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxEvidenceInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read stable evidence input %s: %w", filename, err)
	}
	if int64(len(data)) != before.Size() {
		return nil, fmt.Errorf("read stable evidence input %s: size changed from %d to %d", filename, before.Size(), len(data))
	}
	return data, nil
}

func preflightEvidenceOutput(filename string) error {
	if _, err := os.Lstat(filename); err == nil {
		return fmt.Errorf("output already exists: %s", filename)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeEvidenceOutput(filename string, data []byte, stdout, stderr io.Writer) int {
	if filename == "-" {
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	if err := writeExclusiveFile(filename, data); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	return exitOK
}

func writeEvidenceFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	code := releaseevidence.ErrorCode(err)
	if code == "" {
		code = releasepromotion.ErrorCode(err)
	}
	if code == "" {
		code = "INPUT_INVALID"
	}
	return writeFailure(stdout, stderr, jsonOutput, code, err, exitSnapshotInvalid)
}

func evidenceUsage() string {
	return `usage: releasecheck evidence <command> [flags]

Commands:
  seal-health  strictly parse and self-digest a passing health proof
  assemble     bind authorization, promotion, metrics, raw attestations, observation, and health into evidence
  verify       revalidate durable evidence entirely offline
  bundle-create compact canonical v1 evidence into a deterministic v2 bundle
  bundle-verify reconstruct and revalidate a complete v2 bundle offline
  bundle-parity require byte-exact v1/v2 reconstruction and decision parity
  bundle-measure report explicit logical, object, compressed, and export bytes
  genesis-create create a self-digested anchor for the first v2 ledger entry
  genesis-verify verify a canonical genesis anchor, optionally against its bundle
`
}

func evidenceSealHealthUsage() string {
	return "usage: releasecheck evidence seal-health --input FILE --output FILE|- [--contract FILE] [--json]\n"
}

func evidenceAssembleUsage() string {
	return "usage: releasecheck evidence assemble --authorization FILE --attestations FILE --manifest FILE --ci-metrics FILE --publisher-metrics FILE --observation FILE --output FILE --markdown-output FILE [--contract FILE]\n"
}

func evidenceVerifyUsage() string {
	return "usage: releasecheck evidence verify --input FILE [--markdown-output FILE] [--contract FILE] [--json]\n"
}
