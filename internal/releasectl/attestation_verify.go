package releasectl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/githubapi"
)

type cryptographicAttestationVerifier interface {
	VerifyArtifactAttestations(context.Context, string, string, string, []releaseAssetEvidence) error
}

type attestationVerificationFailure struct{ cause error }

func (e *attestationVerificationFailure) Error() string {
	return "artifact attestation cryptographic verification failed"
}

func (e *attestationVerificationFailure) Unwrap() error { return e.cause }

// VerifyArtifactAttestations downloads only the five exact archive subjects via
// GET, checks their GitHub asset digests, then delegates trust-root, certificate,
// DSSE signature, and transparency-log verification to gh attestation verify.
func (c ghClient) VerifyArtifactAttestations(
	ctx context.Context,
	repository string,
	sourceSHA string,
	signerWorkflow string,
	assets []releaseAssetEvidence,
) error {
	workflowPrefix := repository + "/.github/workflows/"
	workflowFile := strings.TrimPrefix(signerWorkflow, workflowPrefix)
	if c.runner == nil || !repositoryPattern.MatchString(repository) || !shaPattern.MatchString(sourceSHA) ||
		!strings.HasPrefix(signerWorkflow, workflowPrefix) || filepath.Base(workflowFile) != workflowFile || workflowFile == "" || len(assets) != 5 {
		return &apiError{Code: "DEPENDENCY_MISSING", Endpoint: "artifact_attestation_crypto", cause: errors.New("cryptographic verifier inputs are unavailable")}
	}
	directory, err := os.MkdirTemp("", "env-vault-attestation-verify-")
	if err != nil {
		return &apiError{Code: "DEPENDENCY_MISSING", Endpoint: "artifact_attestation_crypto", cause: err}
	}
	defer os.RemoveAll(directory)

	for _, asset := range assets {
		if asset.ID <= 0 || filepath.Base(asset.Name) != asset.Name || asset.Name == "" || asset.Size <= 0 || !digestPattern.MatchString(asset.SHA256) {
			return &attestationVerificationFailure{cause: errors.New("archive asset identity is malformed")}
		}
		endpoint := "repos/" + repository + "/releases/assets/" + strconv.FormatInt(asset.ID, 10)
		data, stderr, runErr := c.runner.Run(ctx, []string{
			"api", "--method", "GET", "--hostname", "github.com",
			"--header", "Accept: application/octet-stream",
			"--header", "X-GitHub-Api-Version: " + githubapi.Version,
			endpoint,
		})
		if runErr != nil {
			if ctx.Err() != nil {
				return &apiError{Code: "API_UNAVAILABLE", Endpoint: endpoint, Retryable: true, cause: ctx.Err()}
			}
			return classifyAPIError(endpoint, stderr, runErr)
		}
		digest := sha256.Sum256(data)
		if int64(len(data)) != asset.Size || hex.EncodeToString(digest[:]) != asset.SHA256 {
			return &attestationVerificationFailure{cause: errors.New("downloaded archive differs from the exact release asset evidence")}
		}
		filename := filepath.Join(directory, asset.Name)
		if err := os.WriteFile(filename, data, 0o600); err != nil {
			return &apiError{Code: "DEPENDENCY_MISSING", Endpoint: "artifact_attestation_crypto", cause: err}
		}
		for _, predicate := range []string{provenancePredicateType, sbomPredicateType} {
			stdout, stderr, verifyErr := c.runner.Run(ctx, []string{
				"attestation", "verify", filename,
				"--repo", repository,
				"--signer-workflow", signerWorkflow,
				"--source-digest", sourceSHA,
				"--predicate-type", predicate,
				"--format", "json",
			})
			if verifyErr != nil {
				if ctx.Err() != nil {
					return &apiError{Code: "API_UNAVAILABLE", Endpoint: "artifact_attestation_crypto", Retryable: true, cause: ctx.Err()}
				}
				classified := classifyAPIError("artifact_attestation_crypto", stderr, verifyErr)
				var apiErr *apiError
				if errors.As(classified, &apiErr) {
					switch apiErr.Code {
					case "DEPENDENCY_MISSING", "AUTH_REQUIRED", "AUTH_FORBIDDEN", "RATE_LIMITED", "API_UNAVAILABLE":
						return classified
					}
				}
				return &attestationVerificationFailure{cause: verifyErr}
			}
			decoder := json.NewDecoder(bytes.NewReader(stdout))
			var verified []json.RawMessage
			if err := decoder.Decode(&verified); err != nil || len(verified) == 0 {
				return &attestationVerificationFailure{cause: errors.New("gh attestation verify returned malformed evidence")}
			}
			var trailing json.RawMessage
			if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
				return &attestationVerificationFailure{cause: errors.New("gh attestation verify returned trailing JSON data")}
			}
		}
	}
	return nil
}
