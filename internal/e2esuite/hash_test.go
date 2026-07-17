package e2esuite

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalBytes(t *testing.T) {
	if got := CanonicalBytes([]byte("a\r\nb\r\n")); !bytes.Equal(got, []byte("a\nb\n")) {
		t.Fatalf("canonical bytes=%q", got)
	}
}

func TestHashChangesForSemanticInputButNotReporterPin(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "e2e", "cmd", "e2e-runner")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	tooling := filepath.Join(directory, "tooling.go")
	if err := os.WriteFile(tooling, []byte("const (\n\tgotestsumVersion       = \"v1.0.0\"\n)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Hash(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tooling, []byte("const (\n\tgotestsumVersion       = \"v2.0.0\"\n)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, _ := Hash(root)
	if first != second {
		t.Fatal("reporter pin changed semantic suite hash")
	}
	if err := os.WriteFile(filepath.Join(root, "e2e", "scenario.go"), []byte("package e2e\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, _ := Hash(root)
	if third == second {
		t.Fatal("semantic suite input did not change hash")
	}
}
