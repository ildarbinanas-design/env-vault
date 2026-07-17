package releasectl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

type attestationRunner struct {
	assets       map[string][]byte
	calls        [][]string
	verifyErr    error
	verifyStderr []byte
}

func (r *attestationRunner) Run(_ context.Context, args []string) ([]byte, []byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(args) > 0 && args[0] == "api" {
		data, ok := r.assets[args[len(args)-1]]
		if !ok {
			return nil, nil, fmt.Errorf("unexpected asset endpoint")
		}
		return append([]byte(nil), data...), nil, nil
	}
	if len(args) > 1 && args[0] == "attestation" && args[1] == "verify" {
		if r.verifyErr != nil {
			return nil, r.verifyStderr, r.verifyErr
		}
		return []byte("[{}]\n"), nil, nil
	}
	return nil, nil, fmt.Errorf("unexpected command")
}

func TestGHClientCryptographicallyVerifiesEveryExactArchiveUsingGETOnly(t *testing.T) {
	runner := &attestationRunner{assets: map[string][]byte{}}
	assets := make([]releaseAssetEvidence, 0, 5)
	for index := 0; index < 5; index++ {
		id := int64(index + 1)
		content := []byte("archive-" + strconv.Itoa(index))
		digest := sha256.Sum256(content)
		asset := releaseAssetEvidence{ID: id, Name: fmt.Sprintf("env-vault-%d.tar.gz", index), SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content))}
		assets = append(assets, asset)
		runner.assets["repos/"+testRepository+"/releases/assets/"+strconv.FormatInt(id, 10)] = content
	}
	err := (ghClient{runner: runner}).VerifyArtifactAttestations(
		context.Background(), testRepository, testSHA,
		testRepository+"/.github/workflows/build-binaries.yml", assets,
	)
	if err != nil {
		t.Fatal(err)
	}
	downloads, verifications := 0, 0
	for _, call := range runner.calls {
		joined := strings.Join(call, " ")
		if call[0] == "api" {
			downloads++
			if !strings.Contains(joined, " --method GET ") || strings.Contains(joined, " POST ") || strings.Contains(joined, " PATCH ") || strings.Contains(joined, " PUT ") || strings.Contains(joined, " DELETE ") {
				t.Fatalf("asset download was not GET-only: %v", call)
			}
		} else {
			verifications++
			for _, required := range []string{"--repo " + testRepository, "--signer-workflow " + testRepository + "/.github/workflows/build-binaries.yml", "--source-digest " + testSHA, "--predicate-type"} {
				if !strings.Contains(joined, required) {
					t.Fatalf("verification command lacks %q: %v", required, call)
				}
			}
		}
	}
	if downloads != 5 || verifications != 10 {
		t.Fatalf("downloads=%d verifications=%d calls=%v", downloads, verifications, runner.calls)
	}
}

func TestGHClientAttestationVerifierDistinguishesInvalidEvidenceFromUnavailableTransport(t *testing.T) {
	content := []byte("archive")
	digest := sha256.Sum256(content)
	asset := releaseAssetEvidence{ID: 1, Name: "env-vault.tar.gz", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content))}
	endpoint := "repos/" + testRepository + "/releases/assets/1"

	invalidRunner := &attestationRunner{assets: map[string][]byte{endpoint: content}, verifyErr: errors.New("exit status 1")}
	err := (ghClient{runner: invalidRunner}).VerifyArtifactAttestations(context.Background(), testRepository, testSHA, testRepository+"/.github/workflows/build-binaries.yml", []releaseAssetEvidence{asset, asset, asset, asset, asset})
	var invalid *attestationVerificationFailure
	if !errors.As(err, &invalid) {
		t.Fatalf("invalid signature error=%T %v", err, err)
	}

	unavailableRunner := &attestationRunner{assets: map[string][]byte{endpoint: content}, verifyErr: errors.New("exit status 1"), verifyStderr: []byte("could not resolve host")}
	err = (ghClient{runner: unavailableRunner}).VerifyArtifactAttestations(context.Background(), testRepository, testSHA, testRepository+"/.github/workflows/build-binaries.yml", []releaseAssetEvidence{asset, asset, asset, asset, asset})
	var apiErr *apiError
	if !errors.As(err, &apiErr) || apiErr.Code != "API_UNAVAILABLE" {
		t.Fatalf("transport error=%T %v", err, err)
	}
}
