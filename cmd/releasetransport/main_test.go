package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/githubtransport"
)

func TestEveryUsageFailureIsOneVersionedSafeErrorDocument(t *testing.T) {
	const sentinel = "DO_NOT_ECHO_SECRET_SENTINEL"
	for name, args := range map[string][]string{
		"no arguments":        nil,
		"unknown subcommand":  {sentinel},
		"bad top-level flags": {"preflight", "--" + sentinel},
		"bad read flags":      {"read", filepath.Join(t.TempDir(), "output.json"), "--" + sentinel},
		"bad nested flags":    {"actions", "identity", "--" + sentinel},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			status := run(args, &stdout, &stderr)
			if status != githubtransport.ExitUsage || stdout.Len() != 0 || strings.Contains(stderr.String(), sentinel) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
			var document githubtransport.ErrorDocument
			decoder := json.NewDecoder(bytes.NewReader(stderr.Bytes()))
			if err := decoder.Decode(&document); err != nil {
				t.Fatalf("decode error document: %v\n%s", err, stderr.String())
			}
			if decoder.Decode(&struct{}{}) == nil {
				t.Fatalf("usage failure emitted more than one JSON value: %q", stderr.String())
			}
			if document.SchemaID != githubtransport.ErrorSchemaID || document.SchemaVersion != 1 || document.OK ||
				document.Error.Code != "INPUT_INVALID" || document.Error.Message == "" || document.Error.Attempts != 0 {
				t.Fatalf("unexpected error document: %+v", document)
			}
		})
	}
}

func TestHelpRemainsHumanReadableAndSuccessful(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := run([]string{"help"}, &stdout, &stderr)
	if status != githubtransport.ExitOK || stderr.Len() != 0 || !strings.HasPrefix(stdout.String(), "usage:\n") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
}

func TestPreflightCLIEmitsVersionedNumericalBounds(t *testing.T) {
	bin := t.TempDir()
	gh := filepath.Join(bin, "gh")
	script := `#!/bin/sh
if [ "$1" = --version ]; then printf 'gh version 2.96.0 (2026-07-02)\n'; exit 0; fi
if [ "$1" = api ] && [ "$2" = --help ]; then printf '%s\n' '--include --hostname --method --header --raw-field --input'; exit 0; fi
exit 1
`
	if err := os.WriteFile(gh, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	if status := run([]string{"preflight", "--output", "-"}, &stdout, &stderr); status != githubtransport.ExitOK || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	var document githubtransport.CapabilitiesDocument
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaID != "env-vault.github-transport-capabilities.v2" || document.SchemaVersion != 2 ||
		document.TransportVersion != "1.2.0" || document.MaxRequestSeconds != 60 || document.MaxOperationSeconds != 300 ||
		document.MaxAggregateResponseBytes != 268435456 {
		t.Fatalf("capabilities=%+v", document)
	}
}

func TestReadRejectsExistingOutputBeforeLookingForGH(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, []byte("trusted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	var stdout, stderr bytes.Buffer
	status := run([]string{"read", path, "repos/example/repo"}, &stdout, &stderr)
	if status != 6 || !strings.Contains(stderr.String(), `"code":"OUTPUT_FAILED"`) {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "trusted\n" {
		t.Fatalf("output changed: %q", data)
	}
}

func TestGitBlobReadWritesOpaqueLargeBytesAtomicallyAndNoClobber(t *testing.T) {
	content := bytes.Repeat([]byte{0x00, 0x80, 0xff, 0x5a}, (2<<20)/4)
	gitObject := append([]byte(fmt.Sprintf("blob %d\x00", len(content))), content...)
	digest := sha1.Sum(gitObject)
	sha := hex.EncodeToString(digest[:])
	response, err := json.Marshal(map[string]any{
		"sha": sha, "encoding": "base64", "size": len(content), "content": base64.StdEncoding.EncodeToString(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	responsePath := filepath.Join(root, "response.json")
	if err := os.WriteFile(responsePath, response, 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	gh := filepath.Join(bin, "gh")
	script := `#!/bin/sh
if [ "$1" = --version ]; then printf 'gh version 2.96.0 (2026-07-02)\n'; exit 0; fi
if [ "$1" = api ] && [ "$2" = --help ]; then printf '%s\n' '--include --hostname --method --header --raw-field --input'; exit 0; fi
printf 'HTTP/2 200 OK\r\nContent-Type: application/vnd.github+json; charset=utf-8\r\nDate: Fri, 17 Jul 2026 12:00:00 GMT\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n\r\n'
cat "$FAKE_BLOB_RESPONSE"
`
	if err := os.WriteFile(gh, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_BLOB_RESPONSE", responsePath)
	output := filepath.Join(root, "object.gz")
	var stdout, stderr bytes.Buffer
	args := []string{"git-blob", "read", "--output", output, "--repository", "example/repo", "--sha", sha}
	if status := run(args, &stdout, &stderr); status != githubtransport.ExitOK {
		t.Fatalf("status=%d stderr=%s", status, stderr.String())
	}
	got, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("opaque output mismatch: size=%d err=%v", len(got), err)
	}
	stdout.Reset()
	stderr.Reset()
	if status := run(args, &stdout, &stderr); status != githubtransport.ExitOutput {
		t.Fatalf("no-clobber status=%d stderr=%s", status, stderr.String())
	}
	got, _ = os.ReadFile(output)
	if !bytes.Equal(got, content) {
		t.Fatal("no-clobber failure changed existing opaque output")
	}
}

func TestReadRejectsMutationAndCustomHostShapes(t *testing.T) {
	for name, args := range map[string][]string{
		"POST":          {"read", filepath.Join(t.TempDir(), "post.json"), "--method", "POST", "repos/example/repo"},
		"body":          {"read", filepath.Join(t.TempDir(), "body.json"), "--input", "payload.json", "repos/example/repo"},
		"host":          {"read", filepath.Join(t.TempDir(), "host.json"), "https://attacker.invalid/repos/example/repo"},
		"authorization": {"read", filepath.Join(t.TempDir(), "auth.json"), "--header", "Authorization: secret", "repos/example/repo"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			status := run(args, &stdout, &stderr)
			if status != 2 {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
		})
	}
}

func TestTransportErrorsHaveStableExitCodes(t *testing.T) {
	for name, testCase := range map[string]struct {
		code string
		want int
	}{
		"input":      {code: "INPUT_INVALID", want: githubtransport.ExitUsage},
		"capability": {code: "CLI_CAPABILITY_DRIFT", want: githubtransport.ExitCapability},
		"not found":  {code: "REMOTE_NOT_FOUND", want: githubtransport.ExitNotFound},
		"auth":       {code: "AUTH_REQUIRED", want: githubtransport.ExitRemote},
		"transport":  {code: "TRANSPORT_FAILED", want: githubtransport.ExitRemote},
	} {
		t.Run(name, func(t *testing.T) {
			got := exitForTransportError(&githubtransport.TransportError{Code: testCase.code})
			if got != testCase.want {
				t.Fatalf("exit=%d, want %d", got, testCase.want)
			}
		})
	}
}

func TestReadRequiresExplicitGETForFields(t *testing.T) {
	_, err := parseReadArguments([]string{"repos/example/repo/actions/runs", "--raw-field", "per_page=100"})
	if err == nil || !strings.Contains(err.Error(), "explicit GET") {
		t.Fatalf("err=%v, want explicit GET requirement", err)
	}
}

func TestPreflightAndReadUseTheSameCapabilityContract(t *testing.T) {
	bin := t.TempDir()
	gh := filepath.Join(bin, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then printf 'gh version 2.80.0 (future)\\n'; exit 0; fi\nexit 90\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	var preflightOut, preflightErr bytes.Buffer
	if status := run([]string{"preflight", "--output", "-"}, &preflightOut, &preflightErr); status != githubtransport.ExitCapability {
		t.Fatalf("preflight status=%d stderr=%q", status, preflightErr.String())
	}
	var readOut, readErr bytes.Buffer
	output := filepath.Join(t.TempDir(), "read.json")
	if status := run([]string{"read", output, "repos/example/repo"}, &readOut, &readErr); status != githubtransport.ExitCapability {
		t.Fatalf("read status=%d stderr=%q", status, readErr.String())
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("capability failure created output: %v", err)
	}
}
