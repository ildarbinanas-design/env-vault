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
			contract, err := loadHistoricalCanonical(root, identity)
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
			if err := contract.ValidateHistoricalEvidenceIdentity(identity.Repository, identity.ReleaseVersion, identity.SourceSHA, identity.EvidenceRootSemanticSHA256); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHistoricalRegistryAuthorizesExactV0016BundleTupleOffline(t *testing.T) {
	root := filepath.Join("..", "..")
	observed := HistoricalBundleObservation{
		Repository: "ildarbinanas-design/env-vault", ReleaseVersion: "v0.0.16",
		SourceSHA: "ddfd38c3144ed3d0968d2c5e7e4b2acfef841478", EvidenceCommitSHA: "e697239298c4b5b1240fc53abe611131d45ac7c0",
		EvidenceRootFileSHA256:     "e6d1014c4c1a977367446827f8764d645fab3015f77e77a843fd4485a4ecd644",
		EvidenceRootSemanticSHA256: "1cc44109f18d9f6cba0da60e3368afaa186cd5a47d03dcf6b06b7f94f311d003",
		EvidenceRootSchemaID:       "env-vault.release-evidence-bundle.v2", EvidenceRootSchemaVersion: 2,
		ReconstructedLegacyEvidenceSHA256:      "f0e8ab2a0e706192f7ddcffb3d5124bda51d85737f43535763e094b00b96a29f",
		ReconstructedLegacyCanonicalJSONSHA256: "344568ad80a41c6ef97612d604225ce044fe5a48859e0cfd8ab3e89e019d9d70",
		ReconstructedLegacyCanonicalJSONSize:   1475935,
		EvidenceParentCommitSHA:                "af521d52b898088cb49f6256964e377e33e95a5d",
		PublisherRunID:                         29622574820, PublisherRunAttempt: 1, EvidenceRunID: 29622650408, EvidenceRunAttempt: 1,
		CompactArtifactID: 8422728320, CompactArtifactDigest: "sha256:8732f0365a4564c3d063b5a2ae1909c14996dca007a1321b0c66304190030eea",
	}
	contract, identity, err := LoadHistoricalBundleContract(
		filepath.Join(root, LegacyArchivePath), filepath.Join(root, HistoricalRegistryPath), observed,
	)
	if err != nil {
		t.Fatal(err)
	}
	if contract.FileSHA256() != LegacyCanonicalFileSHA256 || identity.EvidenceFormat != "v2" ||
		identity.EvidenceRunID != 29622650408 || identity.CompactArtifactID != 8422728320 ||
		identity.CompactArtifactDigest != "sha256:8732f0365a4564c3d063b5a2ae1909c14996dca007a1321b0c66304190030eea" {
		t.Fatalf("historical v0.0.16 identity=%+v contract_file=%s", identity, contract.FileSHA256())
	}
	if err := contract.ValidateHistoricalEvidenceIdentity(observed.Repository, observed.ReleaseVersion, observed.SourceSHA, observed.ReconstructedLegacyEvidenceSHA256); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHistoricalBundleReconstruction(identity, observed.ReconstructedLegacyEvidenceSHA256, observed.ReconstructedLegacyCanonicalJSONSHA256, observed.ReconstructedLegacyCanonicalJSONSize); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*HistoricalBundleObservation){
		"source":        func(value *HistoricalBundleObservation) { value.SourceSHA = strings.Repeat("a", 40) },
		"commit":        func(value *HistoricalBundleObservation) { value.EvidenceCommitSHA = strings.Repeat("b", 40) },
		"root raw":      func(value *HistoricalBundleObservation) { value.EvidenceRootFileSHA256 = strings.Repeat("c", 64) },
		"bundle digest": func(value *HistoricalBundleObservation) { value.EvidenceRootSemanticSHA256 = strings.Repeat("d", 64) },
		"legacy digest": func(value *HistoricalBundleObservation) {
			value.ReconstructedLegacyEvidenceSHA256 = strings.Repeat("e", 64)
		},
		"canonical": func(value *HistoricalBundleObservation) {
			value.ReconstructedLegacyCanonicalJSONSHA256 = strings.Repeat("f", 64)
		},
		"canonical size": func(value *HistoricalBundleObservation) { value.ReconstructedLegacyCanonicalJSONSize++ },
		"parent":         func(value *HistoricalBundleObservation) { value.EvidenceParentCommitSHA = strings.Repeat("1", 40) },
		"publisher":      func(value *HistoricalBundleObservation) { value.PublisherRunAttempt = 2 },
		"evidence run":   func(value *HistoricalBundleObservation) { value.EvidenceRunID++ },
		"artifact":       func(value *HistoricalBundleObservation) { value.CompactArtifactID++ },
		"artifact digest": func(value *HistoricalBundleObservation) {
			value.CompactArtifactDigest = "sha256:" + strings.Repeat("2", 64)
		},
	} {
		t.Run("reject "+name, func(t *testing.T) {
			candidate := observed
			mutate(&candidate)
			if _, _, err := LoadHistoricalBundleContract(filepath.Join(root, LegacyArchivePath), filepath.Join(root, HistoricalRegistryPath), candidate); err == nil {
				t.Fatal("substituted compact evidence tuple was accepted")
			}
		})
	}
}

func TestHistoricalRegistryRejectsCrossReleaseSubstitutionAndUnregisteredRelease(t *testing.T) {
	root := filepath.Join("..", "..")
	v14 := HistoricalIdentity{
		Repository: "ildarbinanas-design/env-vault", ReleaseVersion: "v0.0.14",
		SourceSHA: "c42a92144a82c19edea41c76328ec7fd1e408ceb", ContractGeneration: "v1", EvidenceFormat: "v1",
		EvidenceCommitSHA: "68547bd880a4d49f44389476b77046aac2ab1675", EvidenceParentCommitSHA: "c42a92144a82c19edea41c76328ec7fd1e408ceb",
		EvidenceRootPath: "evidence/releases/v0.0.14/release-evidence.json", EvidenceRootFileSHA256: "b6d56fc3675c2c4fc441a390249ac868a4453af77f7a0b8b06df8b75f1604d79",
		EvidenceRootSemanticSHA256: "6a4d45205a5a662cfb21beee5726a67473a42dd273763c0662299343c3e85076",
		EvidenceRootSchemaID:       "env-vault.release-evidence.v1", EvidenceRootSchemaVersion: 1,
		PublisherRunID: 29569706872, PublisherRunAttempt: 1, EvidenceRunID: 29569819553, EvidenceRunAttempt: 2,
	}
	for name, mutate := range map[string]func(*HistoricalIdentity){
		"source swap": func(identity *HistoricalIdentity) { identity.SourceSHA = "c7dd1fd6176ac2abbea22f226795a0787e774c1b" },
		"evidence swap": func(identity *HistoricalIdentity) {
			identity.EvidenceRootSemanticSHA256 = "2c339829ad1ea77c4f8e91dc8cfb896d43978e281ee76b2e82022fe0c65fc63e"
		},
		"raw evidence digest swap": func(identity *HistoricalIdentity) {
			identity.EvidenceRootFileSHA256 = "679a20101ca92f786d7417b984755305728a36fcafcc9d68bbe1540c92ab7026"
		},
		"evidence commit swap": func(identity *HistoricalIdentity) {
			identity.EvidenceCommitSHA = "af521d52b898088cb49f6256964e377e33e95a5d"
		},
		"new v1 release": func(identity *HistoricalIdentity) { identity.ReleaseVersion = "v0.0.17" },
		"schema alias":   func(identity *HistoricalIdentity) { identity.EvidenceRootSchemaID = "env-vault.release-evidence.v01" },
	} {
		t.Run(name, func(t *testing.T) {
			identity := v14
			mutate(&identity)
			if _, err := loadHistoricalCanonical(root, identity); err == nil {
				t.Fatal("unregistered historical tuple was accepted")
			}
		})
	}
}

func TestHistoricalEvidenceLoaderComputesRawDigestBeforeSemanticReplay(t *testing.T) {
	data := []byte(`{
  "schema_id": "env-vault.release-evidence.v1",
  "schema_version": 1,
  "repository": "ildarbinanas-design/env-vault",
  "release_version": "v0.0.15",
  "source_sha": "c7dd1fd6176ac2abbea22f226795a0787e774c1b",
  "evidence_sha256": "2c339829ad1ea77c4f8e91dc8cfb896d43978e281ee76b2e82022fe0c65fc63e"
}`)
	root := filepath.Join("..", "..")
	if _, _, err := LoadHistoricalEvidence(
		filepath.Join(root, LegacyArchivePath), filepath.Join(root, HistoricalRegistryPath),
		writeTempFile(t, data), "af521d52b898088cb49f6256964e377e33e95a5d",
	); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("wrong raw digest with registered internal digest was accepted: %v", err)
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
