package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/actionsartifact"
	"github.com/ildarbinanas-design/env-vault/internal/githubtransport"
)

func TestExecutorCLIHasNoUserControlledValidationClock(t *testing.T) {
	if strings.Contains(usage(), "--now") {
		t.Fatal("mutation executor exposes a user-controlled freshness clock")
	}
	var stdout, stderr bytes.Buffer
	if status := run(context.Background(), []string{"--now", "2020-01-01T00:00:00Z"}, &stdout, &stderr); status != 2 {
		t.Fatalf("unsupported historical --now status=%d stderr=%q", status, stderr.String())
	}
	if _, err := parseMaxAge("1h"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"0", "-1s", "1h1ns", "not-a-duration"} {
		if _, err := parseMaxAge(invalid); err == nil {
			t.Fatalf("invalid max age %q accepted", invalid)
		}
	}
}

func TestCheckedAdapterUsesExactReadAndBodylessDeleteShapes(t *testing.T) {
	root := t.TempDir()
	readScript := filepath.Join(root, "read.sh")
	mutationScript := filepath.Join(root, "mutation.sh")
	readFixture := filepath.Join(root, "artifact.json")
	mutationFixture := filepath.Join(root, "mutation.json")
	logPath := filepath.Join(root, "mutation.log")
	record := actionsartifact.DecisionRecord{
		Repository: "example/repo", ArtifactID: 42, Name: "env-vault-linux-amd64-v1.2.3-attempt-1",
		ArtifactDigest: "sha256:" + strings.Repeat("a", 64), ProducerRunID: 7, ProducerRunAttempt: 1,
		WorkflowPath: ".github/workflows/build-binaries.yml", HeadSHA: strings.Repeat("b", 40), SizeInBytes: 99,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:01Z", ExpiresAt: "2026-01-08T00:00:01Z",
		Decision: actionsartifact.DecisionDelete, ReasonCode: actionsartifact.ReasonDeleteSuperseded,
	}
	writeTestJSON(t, readFixture, map[string]any{
		"id": record.ArtifactID, "name": record.Name, "digest": record.ArtifactDigest, "size_in_bytes": record.SizeInBytes,
		"created_at": record.CreatedAt, "updated_at": record.UpdatedAt, "expires_at": record.ExpiresAt, "expired": false,
		"workflow_run": map[string]any{"id": record.ProducerRunID, "repository_id": 1, "head_repository_id": 1, "head_sha": record.HeadSHA, "head_branch": "main"},
	})
	writeTestJSON(t, mutationFixture, githubtransport.MutationDocument{
		SchemaID: githubtransport.MutationSchemaID, SchemaVersion: 1, OK: true, Outcome: "success",
		Method: "DELETE", Endpoint: "repos/example/repo/actions/artifacts/42", HTTPStatus: 204,
	})
	writeTestScript(t, readScript, "#!/bin/sh\ncp \"$READ_FIXTURE\" \"$1\"\n")
	writeTestScript(t, mutationScript, "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$MUTATION_LOG\"\ncp \"$MUTATION_FIXTURE\" \"$4\"\n")
	t.Setenv("READ_FIXTURE", readFixture)
	t.Setenv("MUTATION_FIXTURE", mutationFixture)
	t.Setenv("MUTATION_LOG", logPath)
	adapter := &checkedArtifactAdapter{readAdapter: readScript, mutationAdapter: mutationScript, temporary: root}
	if observation := adapter.ReadArtifact(context.Background(), record); observation.Outcome != actionsartifact.ReadPresent || !observation.Exact {
		t.Fatalf("read observation=%+v", observation)
	}
	if mutation := adapter.DeleteArtifact(context.Background(), record); mutation.Outcome != actionsartifact.MutationSuccess || mutation.HTTPStatus != 204 {
		t.Fatalf("mutation=%+v", mutation)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	call := string(logData)
	for _, required := range []string{"rest mutate-once", "--method DELETE", "--endpoint repos/example/repo/actions/artifacts/42", "--expected-status 204"} {
		if !strings.Contains(call, required) {
			t.Fatalf("mutation call %q lacks %q", call, required)
		}
	}
	if strings.Contains(call, "--input") {
		t.Fatalf("bodyless artifact deletion passed an input: %q", call)
	}
}

func TestDeletionExecutorRemainsDormantOutsideItsOwnCommand(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, relative := range []string{".github/workflows", "scripts", "cmd/releasecheck"} {
		root := filepath.Join(repositoryRoot, relative)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), "actionsartifactdelete") {
				t.Fatalf("dormant deletion executor is invoked by %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestScript(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o700); err != nil {
		t.Fatal(err)
	}
}
