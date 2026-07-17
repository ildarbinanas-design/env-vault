package githubtransport

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifyBlobAcceptsWrappedCanonicalBase64AndExactBytes(t *testing.T) {
	content := []byte("durable evidence\n")
	sha := gitBlobSHA(content)
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(content)
	wrapped := encoded[:8] + "\r\n" + encoded[8:]
	runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "", fmt.Sprintf(`{"sha":%q,"encoding":"base64","size":%d,"content":%q,"node_id":"diagnostic"}`, sha, len(content), wrapped))}}
	var sleeps []time.Duration
	document, err := testClient(runner, &sleeps).VerifyBlob(context.Background(), "example/repo", sha, path)
	if err != nil || !document.OK || document.DeclaredSize != int64(len(content)) || document.DecodedSHA256 != document.ExpectedSHA256 {
		t.Fatalf("document=%+v error=%v", document, err)
	}
}

func TestVerifyBlobAcceptsCanonicalEmptyBlob(t *testing.T) {
	content := []byte{}
	sha := gitBlobSHA(content)
	path := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "", fmt.Sprintf(`{"sha":%q,"encoding":"base64","size":0,"content":""}`, sha))}}
	var sleeps []time.Duration
	document, err := testClient(runner, &sleeps).VerifyBlob(context.Background(), "example/repo", sha, path)
	if err != nil || !document.OK || document.DeclaredSize != 0 {
		t.Fatalf("document=%+v error=%v", document, err)
	}
}

func TestReadContentsPreservesOpaqueRawMediaBytes(t *testing.T) {
	body := []byte("# README\r\nnot JSON\n")
	response := liveResponse(200, "", string(body))
	response.Stdout = bytes.Replace(response.Stdout, []byte("Content-Type: application/json; charset=utf-8"), []byte("Content-Type: application/vnd.github.raw+json; charset=utf-8"), 1)
	runner := &scriptedRunner{responses: []CommandResult{response}}
	var sleeps []time.Duration
	data, err := testClient(runner, &sleeps).ReadContents(context.Background(), "example/repo", "README.md", strings.Repeat("a", 40))
	if err != nil || !bytes.Equal(data, body) {
		t.Fatalf("data=%q error=%v", data, err)
	}
}

func TestReadContentsRejectsWrongRawMediaType(t *testing.T) {
	for _, contentType := range []string{"text/html", "application/octet-stream", "application/json", "application/vnd.github.raw+json; charset=iso-8859-1", "application/vnd.github.raw+json; boundary=unreviewed"} {
		t.Run(contentType, func(t *testing.T) {
			response := liveResponse(200, "", "opaque bytes")
			response.Stdout = bytes.Replace(response.Stdout, []byte("Content-Type: application/json; charset=utf-8"), []byte("Content-Type: "+contentType), 1)
			runner := &scriptedRunner{responses: []CommandResult{response}}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).ReadContents(context.Background(), "example/repo", "README.md", strings.Repeat("a", 40))
			if transportErr == nil || transportErr.Code != "MALFORMED_RESPONSE" || transportErr.Retriable {
				t.Fatalf("error=%+v", transportErr)
			}
		})
	}
}

func TestVerifyBlobRejectsMalformedNoncanonicalSizeAndByteMismatch(t *testing.T) {
	content := []byte("abcde")
	sha := gitBlobSHA(content)
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	canonical := base64.StdEncoding.EncodeToString(content)
	tests := map[string]struct {
		size         int
		encoded      string
		expectedCode string
	}{
		"trailing":              {len(content), canonical + "!", "BLOB_INVALID"},
		"missing padding":       {len(content), strings.TrimRight(canonical, "="), "BLOB_INVALID"},
		"extra padding":         {len(content), canonical + "=", "BLOB_INVALID"},
		"noncanonical pad bits": {len(content), canonical[:len(canonical)-2] + "R=", "BLOB_INVALID"},
		"size mismatch":         {len(content) + 1, canonical, "BLOB_INVALID"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "", fmt.Sprintf(`{"sha":%q,"encoding":"base64","size":%d,"content":%q}`, sha, test.size, test.encoded))}}
			var sleeps []time.Duration
			_, err := testClient(runner, &sleeps).VerifyBlob(context.Background(), "example/repo", sha, path)
			if err == nil || err.Code != test.expectedCode {
				t.Fatalf("error=%+v", err)
			}
		})
	}

	remote := []byte("xxxxx")
	remoteSHA := gitBlobSHA(remote)
	runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "", fmt.Sprintf(`{"sha":%q,"encoding":"base64","size":%d,"content":%q}`, remoteSHA, len(remote), base64.StdEncoding.EncodeToString(remote)))}}
	var sleeps []time.Duration
	_, mismatch := testClient(runner, &sleeps).VerifyBlob(context.Background(), "example/repo", remoteSHA, path)
	if mismatch == nil || mismatch.Code != "BLOB_MISMATCH" {
		t.Fatalf("byte mismatch error=%+v", mismatch)
	}
}

func gitBlobSHA(content []byte) string {
	digest := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(content))), content...))
	return hex.EncodeToString(digest[:])
}
