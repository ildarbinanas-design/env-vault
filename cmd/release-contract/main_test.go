package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func TestMatrixJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	path := filepath.Join("..", "..", filepath.FromSlash(releasecontract.CanonicalPath))
	if err := run([]string{"matrix", "--contract", path, "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v stderr=%s", err, stderr.String())
	}
	var matrix releasecontract.Matrix
	if err := json.Unmarshal(stdout.Bytes(), &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Include) != 5 || matrix.Include[0].Archive != "env-vault-linux-amd64.tar.gz" {
		t.Fatalf("matrix=%+v", matrix)
	}
}

func TestValidate(t *testing.T) {
	var stdout bytes.Buffer
	path := filepath.Join("..", "..", filepath.FromSlash(releasecontract.CanonicalPath))
	if err := run([]string{"validate", "--contract", path}, &stdout, &stdout); err != nil {
		t.Fatal(err)
	}
}

func TestAppJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	path := filepath.Join("..", "..", filepath.FromSlash(releasecontract.CanonicalPath))
	if err := run([]string{"app", "--contract", path, "--id", "homebrew_tap", "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v stderr=%s", err, stderr.String())
	}
	var app releasecontract.App
	if err := json.Unmarshal(stdout.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	if app.Slug != "env-vault-tap-release" || app.Repository != "ildarbinanas-design/homebrew-tap" {
		t.Fatalf("app=%+v", app)
	}
}

func TestWorkflowJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	path := filepath.Join("..", "..", filepath.FromSlash(releasecontract.CanonicalPath))
	if err := run([]string{"workflow", "--contract", path, "--id", "publisher", "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v stderr=%s", err, stderr.String())
	}
	var workflow releasecontract.Workflow
	if err := json.Unmarshal(stdout.Bytes(), &workflow); err != nil {
		t.Fatal(err)
	}
	if workflow.Name != "build-binaries" || workflow.File != "build-binaries.yml" {
		t.Fatalf("workflow=%+v", workflow)
	}
}
