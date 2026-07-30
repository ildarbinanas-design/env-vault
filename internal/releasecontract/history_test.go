package releasecontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoricalRegistryReplaysOnlyExactV1ReleaseTuplesOffline(t *testing.T) {
	root := filepath.Join("..", "..")
	identities := []HistoricalIdentity{
		{
			Repository: "ildarbinanas-design/env-vault", ReleaseVersion: "v0.0.14",
			SourceSHA: "c42a92144a82c19edea41c76328ec7fd1e408ceb", ContractGeneration: "v1", EvidenceFormat: "v1",
			EvidenceCommitSHA: "68547bd880a4d49f44389476b77046aac2ab1675", EvidenceParentCommitSHA: "c42a92144a82c19edea41c76328ec7fd1e408ceb",
			EvidenceRootPath: "evidence/releases/v0.0.14/release-evidence.json", EvidenceRootFileSHA256: "b6d56fc3675c2c4fc441a390249ac868a4453af77f7a0b8b06df8b75f1604d79",
			EvidenceRootSemanticSHA256: "6a4d45205a5a662cfb21beee5726a67473a42dd273763c0662299343c3e85076",
			EvidenceRootSchemaID:       "env-vault.release-evidence.v1", EvidenceRootSchemaVersion: 1,
			PublisherRunID: 29569706872, PublisherRunAttempt: 1, EvidenceRunID: 29569819553, EvidenceRunAttempt: 2,
		},
		{
			Repository: "ildarbinanas-design/env-vault", ReleaseVersion: "v0.0.15",
			SourceSHA: "c7dd1fd6176ac2abbea22f226795a0787e774c1b", ContractGeneration: "v1", EvidenceFormat: "v1",
			EvidenceCommitSHA: "af521d52b898088cb49f6256964e377e33e95a5d", EvidenceParentCommitSHA: "68547bd880a4d49f44389476b77046aac2ab1675",
			EvidenceRootPath: "evidence/releases/v0.0.15/release-evidence.json", EvidenceRootFileSHA256: "679a20101ca92f786d7417b984755305728a36fcafcc9d68bbe1540c92ab7026",
			EvidenceRootSemanticSHA256: "2c339829ad1ea77c4f8e91dc8cfb896d43978e281ee76b2e82022fe0c65fc63e",
			EvidenceRootSchemaID:       "env-vault.release-evidence.v1", EvidenceRootSchemaVersion: 1,
			PublisherRunID: 29576465336, PublisherRunAttempt: 1, EvidenceRunID: 29576963736, EvidenceRunAttempt: 1,
		},
	}
	for _, identity := range identities {
		t.Run(identity.ReleaseVersion, func(t *testing.T) {
			contract, err := loadHistoricalContract(
				filepath.Join(root, LegacyArchivePath), filepath.Join(root, HistoricalRegistryPath), identity)
			if err != nil {
				t.Fatal(err)
			}
			if contract.SchemaID != LegacySchemaID || contract.SchemaVersion != LegacySchemaVersion {
				t.Fatalf("historical schema=%s/%d", contract.SchemaID, contract.SchemaVersion)
			}
			digest, err := SemanticSHA256(contract)
			if err != nil || digest != LegacySemanticSHA256 {
				t.Fatalf("semantic digest=%q error=%v", digest, err)
			}
		})
	}
}

func TestHistoricalSourceAuthorizationRequiresARegisteredTuple(t *testing.T) {
	root := filepath.Join("..", "..")
	contractPath := filepath.Join(root, LegacyArchivePath)
	registryPath := filepath.Join(root, HistoricalRegistryPath)
	authorization, err := AuthorizeHistoricalSource(
		contractPath, registryPath, "ildarbinanas-design/env-vault", "v0.0.15",
		"c7dd1fd6176ac2abbea22f226795a0787e774c1b")
	if err != nil {
		t.Fatal(err)
	}
	if !authorization.OK || authorization.Identity.ContractGeneration != "v1" {
		t.Fatalf("authorization=%+v", authorization)
	}
	for name, tuple := range map[string][3]string{
		"repository": {"ildarbinanas-design/other", "v0.0.15", "c7dd1fd6176ac2abbea22f226795a0787e774c1b"},
		"version":    {"ildarbinanas-design/env-vault", "v0.0.99", "c7dd1fd6176ac2abbea22f226795a0787e774c1b"},
		"source":     {"ildarbinanas-design/env-vault", "v0.0.15", strings.Repeat("a", 40)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := AuthorizeHistoricalSource(contractPath, registryPath, tuple[0], tuple[1], tuple[2]); err == nil {
				t.Fatal("unregistered historical tuple was authorized")
			}
		})
	}
}

func TestHistoricalRegistryAndContractTamperingFailClosed(t *testing.T) {
	root := filepath.Join("..", "..")
	identity := HistoricalIdentity{
		Repository: "ildarbinanas-design/env-vault", ReleaseVersion: "v0.0.15",
		SourceSHA: "c7dd1fd6176ac2abbea22f226795a0787e774c1b", ContractGeneration: "v1", EvidenceFormat: "v1",
		EvidenceCommitSHA: "af521d52b898088cb49f6256964e377e33e95a5d", EvidenceParentCommitSHA: "68547bd880a4d49f44389476b77046aac2ab1675",
		EvidenceRootPath: "evidence/releases/v0.0.15/release-evidence.json", EvidenceRootFileSHA256: "679a20101ca92f786d7417b984755305728a36fcafcc9d68bbe1540c92ab7026",
		EvidenceRootSemanticSHA256: "2c339829ad1ea77c4f8e91dc8cfb896d43978e281ee76b2e82022fe0c65fc63e",
		EvidenceRootSchemaID:       "env-vault.release-evidence.v1", EvidenceRootSchemaVersion: 1,
		PublisherRunID: 29576465336, PublisherRunAttempt: 1, EvidenceRunID: 29576963736, EvidenceRunAttempt: 1,
	}
	registry, err := os.ReadFile(filepath.Join(root, HistoricalRegistryPath))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := os.ReadFile(filepath.Join(root, LegacyArchivePath))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"registry digest":       []byte(strings.Replace(string(registry), LegacySemanticSHA256, strings.Repeat("f", 64), 1)),
		"registry null entries": []byte(strings.Replace(string(registry), `"entries": [`, `"entries": null, "ignored": [`, 1)),
		"legacy bytes":          []byte(strings.Replace(string(legacy), `"product": "env-vault"`, `"product": "env-vault-tampered"`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			registryPath := filepath.Join(root, HistoricalRegistryPath)
			contractPath := filepath.Join(root, LegacyArchivePath)
			if strings.HasPrefix(name, "registry") {
				registryPath = writeTempFile(t, data)
			} else {
				contractPath = writeTempFile(t, data)
			}
			if _, err := loadHistoricalContract(contractPath, registryPath, identity); err == nil {
				t.Fatal("historical tampering was accepted")
			}
		})
	}
}

func TestArbitraryInMemoryV1ContractHasNoCompatibilityAuthority(t *testing.T) {
	contract := Contract{SchemaID: LegacySchemaID, SchemaVersion: LegacySchemaVersion}
	if err := contract.Validate(); err == nil || !strings.Contains(err.Error(), "compatibility binding") {
		t.Fatalf("error=%v", err)
	}
}
