package e2esuite

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestHashTracksRunnerAndRendererButCanonicalizesReporterPin(t *testing.T) {
	repository := t.TempDir()
	write := func(relative, value string) {
		t.Helper()
		filename := filepath.Join(repository, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("e2e/scenarios.json", "{}\n")
	write("e2e/scenarios_test.go", "package e2e\nconst sentinel = 1\n")
	write("e2e/testhelper/main.go", "package main\n")
	write("e2e/cmd/e2e-runner/validator.go", "package main\nconst validator = 1\n")
	write("e2e/cmd/e2e-runner/render.go", "package main\nconst markdown = `# Report`\n")
	write("e2e/cmd/e2e-runner/tooling.go", "package main\nconst (\n\tgotestsumModuleVersion = \"gotest.tools/gotestsum@v1.12.2\"\n\tgotestsumVersion       = \"v1.12.2\"\n)\n")
	write("e2e/reports/generated.json", "generated\n")

	baseline, err := Hash(repository)
	if err != nil {
		t.Fatal(err)
	}
	write("e2e/cmd/e2e-runner/tooling.go", "package main\nconst (\n\tgotestsumModuleVersion = \"gotest.tools/gotestsum@v9.9.9\"\n\tgotestsumVersion       = \"v9.9.9\"\n)\n")
	write("e2e/reports/generated.json", "different generated output\n")
	reporterOnly, err := Hash(repository)
	if err != nil {
		t.Fatal(err)
	}
	if reporterOnly != baseline {
		t.Fatal("reporter pin or generated output changed the semantic suite hash")
	}
	write("e2e/cmd/e2e-runner/tooling.go", "package main\nconst (\n\tgotestsumModuleVersion = \"gotest.tools/gotestsum@v9.9.9\"\n\tgotestsumVersion = \"v9.9.9\"; hiddenSemanticValue = \"changed\"\n)\n")
	hiddenSuffix, err := Hash(repository)
	if err != nil {
		t.Fatal(err)
	}
	if hiddenSuffix == baseline {
		t.Fatal("reporter pin canonicalization hid additional semantic bytes")
	}
	write("e2e/cmd/e2e-runner/tooling.go", "package main\nconst (\n\tgotestsumModuleVersion = \"gotest.tools/gotestsum@v9.9.9\"\n\tgotestsumVersion       = \"v9.9.9\"\n)\n")
	write("e2e/cmd/e2e-runner/validator.go", "package main\nconst validator = 2\n")
	validatorChanged, err := Hash(repository)
	if err != nil {
		t.Fatal(err)
	}
	if validatorChanged == baseline {
		t.Fatal("semantic report validator change retained the semantic suite hash")
	}
	write("e2e/cmd/e2e-runner/validator.go", "package main\nconst validator = 1\n")
	write("e2e/cmd/e2e-runner/render.go", "package main\nconst markdown = `# Different report`\n")
	renderChanged, err := Hash(repository)
	if err != nil {
		t.Fatal(err)
	}
	if renderChanged == baseline {
		t.Fatal("Markdown renderer change retained the hash although only reporter pins are excluded")
	}
	write("e2e/cmd/e2e-runner/render.go", "package main\nconst markdown = `# Report`\n")
	write("e2e/scenarios_test.go", "package e2e\nconst sentinel = 2\n")
	semantic, err := Hash(repository)
	if err != nil {
		t.Fatal(err)
	}
	if semantic == baseline {
		t.Fatal("scenario implementation change retained the semantic suite hash")
	}
}

func TestHashIsPortableAndFailClosed(t *testing.T) {
	if got := CanonicalBytes([]byte("a\r\nb\n")); !bytes.Equal(got, []byte("a\nb\n")) {
		t.Fatalf("canonical bytes = %q", got)
	}

	repository := t.TempDir()
	if _, err := Hash(repository); err == nil {
		t.Fatal("missing suite was accepted")
	}
	if err := os.MkdirAll(filepath.Join(repository, "e2e"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repository, "target")
	if err := os.WriteFile(target, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repository, "e2e", "scenario_test.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Hash(repository); err == nil {
		t.Fatal("symlink suite input was accepted")
	}
}

func TestCanonicalRepositoryHashIsPinned(t *testing.T) {
	got, err := Hash(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const expected = "6b7f1d8a715e7f8b0f9e75e71f45a139e01deb1804a9d5556ca14071d10ae2f8"
	if got != expected {
		t.Fatalf("canonical semantic suite hash=%s, want %s", got, expected)
	}
}
