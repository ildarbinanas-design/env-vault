package releasecontract

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRouteSourceContractUsesSeparateSourceAndControlRoots(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	controlRoot := t.TempDir()
	copyTestFile(t, filepath.Join(repositoryRoot, HistoricalRegistryPath), filepath.Join(controlRoot, HistoricalRegistryPath))

	t.Run("current v2", func(t *testing.T) {
		sourceRoot := t.TempDir()
		copyTestFile(t, filepath.Join(repositoryRoot, CanonicalPath), filepath.Join(sourceRoot, CanonicalPath))
		route, err := RouteSourceContract(
			filepath.Join(sourceRoot, CanonicalPath), filepath.Join(controlRoot, HistoricalRegistryPath),
			"ildarbinanas-design/env-vault", "", "fa5e3fdfe75c956dbd9e4f70484de1f0ec81de3a",
		)
		if err != nil {
			t.Fatal(err)
		}
		if route.ContractGeneration != "v2" || route.EvidenceFormat != "v2" || route.Operational == nil || route.Historical != nil ||
			route.SchemaID != SourceRouteSchemaID || route.ReleaseAppSlug != "env-vault-release-planning" ||
			route.ContractSemanticSHA256 != route.Operational.ContractSemanticSHA256 {
			t.Fatalf("v2 route=%+v", route)
		}
	})

	for _, test := range []struct {
		name, version, sourceSHA, evidenceCommit, evidenceFormat string
	}{
		{"v0.0.14", "v0.0.14", "c42a92144a82c19edea41c76328ec7fd1e408ceb", "68547bd880a4d49f44389476b77046aac2ab1675", "v1"},
		{"v0.0.15", "v0.0.15", "c7dd1fd6176ac2abbea22f226795a0787e774c1b", "af521d52b898088cb49f6256964e377e33e95a5d", "v1"},
		{"v0.0.16", "v0.0.16", "ddfd38c3144ed3d0968d2c5e7e4b2acfef841478", "e697239298c4b5b1240fc53abe611131d45ac7c0", "v2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sourceRoot := t.TempDir()
			copyTestFile(t, filepath.Join(repositoryRoot, LegacyArchivePath), filepath.Join(sourceRoot, LegacyCanonicalPath))
			route, err := RouteSourceContract(
				filepath.Join(sourceRoot, LegacyCanonicalPath), filepath.Join(controlRoot, HistoricalRegistryPath),
				"ildarbinanas-design/env-vault", test.version, test.sourceSHA,
			)
			if err != nil {
				t.Fatal(err)
			}
			if route.ContractGeneration != "v1" || route.EvidenceFormat != test.evidenceFormat || route.Operational != nil || route.Historical == nil ||
				route.Historical.EvidenceCommitSHA != test.evidenceCommit || route.ContractSemanticSHA256 != LegacySemanticSHA256 ||
				route.ContractFileSHA256 != LegacyCanonicalFileSHA256 {
				t.Fatalf("historical route=%+v", route)
			}
		})
	}
}

func TestRouteSourceContractFailsClosedWithoutAnExactGenerationTuple(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, test := range []struct {
		name, sourceContract, registry, version, sourceSHA string
	}{
		{"unregistered v1", LegacyArchivePath, HistoricalRegistryPath, "v0.0.17", "fa5e3fdfe75c956dbd9e4f70484de1f0ec81de3a"},
		{"v1 without release version", LegacyArchivePath, HistoricalRegistryPath, "", "c7dd1fd6176ac2abbea22f226795a0787e774c1b"},
		{"missing v2", "release/missing-contract.v2.json", HistoricalRegistryPath, "", "fa5e3fdfe75c956dbd9e4f70484de1f0ec81de3a"},
		{"unsupported generation", HistoricalRegistryPath, HistoricalRegistryPath, "v0.0.15", "c7dd1fd6176ac2abbea22f226795a0787e774c1b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := RouteSourceContract(filepath.Join(root, test.sourceContract), filepath.Join(root, test.registry), "ildarbinanas-design/env-vault", test.version, test.sourceSHA); err == nil {
				t.Fatal("unsafe source route was accepted")
			}
		})
	}
}

func copyTestFile(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
