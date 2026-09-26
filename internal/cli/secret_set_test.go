package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

type secretSetData struct {
	RecordID    string `json:"record_id"`
	Fingerprint string `json:"fingerprint"`
	Action      string `json:"action"`
	Verified    bool   `json:"verified"`
}

func runSecretSet(t *testing.T, value string, extra ...string) (secretSetData, string) {
	t.Helper()
	args := append([]string{"--json", "secret", "set", "nexus-token", "--stdin"}, extra...)
	var stdout, stderr bytes.Buffer
	if code := Run(args, strings.NewReader(value+"\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	testutil.AssertNotContains(t, "secret set stdout", stdout.String(), value)
	testutil.AssertNotContains(t, "secret set stderr", stderr.String(), value)
	var env output.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	var data secretSetData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("data: %v", err)
	}
	return data, stdout.String()
}

func TestSecretSetReportsCreatedThenOverwrittenWithStableRecordID(t *testing.T) {
	setupTestBackend(t)
	want := secretstore.RecordID(secretstore.DefaultService, "nexus-token")

	first, _ := runSecretSet(t, testutil.EphemeralValue(t))
	second, _ := runSecretSet(t, testutil.EphemeralValue(t))

	if first.Action != "created" || second.Action != "overwritten" {
		t.Fatalf("actions=%q,%q, want created,overwritten", first.Action, second.Action)
	}
	for _, data := range []secretSetData{first, second} {
		if data.RecordID != want || data.Fingerprint != want {
			t.Fatalf("record_id=%q fingerprint=%q, want both %q", data.RecordID, data.Fingerprint, want)
		}
		if data.Verified {
			t.Fatal("verified must be false without --verify")
		}
	}
}

func TestSecretSetVerifyReadsBackWithoutPrinting(t *testing.T) {
	setupTestBackend(t)
	data, _ := runSecretSet(t, testutil.EphemeralValue(t), "--verify")
	if !data.Verified || data.Action != "created" {
		t.Fatalf("data=%+v, want verified created", data)
	}

	value := testutil.EphemeralValue(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"secret", "set", "nexus-token", "--stdin", "--verify"}, strings.NewReader(value+"\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	want := "secret overwritten: nexus-token (record: " + secretstore.RecordID(secretstore.DefaultService, "nexus-token") + ", verified)\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q, want %q", stdout.String(), want)
	}
	testutil.AssertNotContains(t, "secret set --verify stdout", stdout.String(), value)
}

func TestSecretDryRunHumanOutputDoesNotClaimMutation(t *testing.T) {
	setupTestBackend(t)
	recordID := secretstore.RecordID(secretstore.DefaultService, "nexus-token")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--dry-run", "secret", "set", "nexus-token"}, "dry run: secret nexus-token would be stored (record: " + recordID + ")\n"},
		{[]string{"--dry-run", "secret", "delete", "nexus-token", "--confirm", "nexus-token"}, "dry run: secret nexus-token would be deleted\n"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(tc.args, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("%v: code=%d stderr=%s", tc.args, code, stderr.String())
		}
		if stdout.String() != tc.want {
			t.Fatalf("%v: stdout=%q, want %q", tc.args, stdout.String(), tc.want)
		}
	}
}

// droppingStore models the failure the read-back must catch: the backend
// reports a successful write but keeps the previous value.
type droppingStore struct {
	secretstore.Store
	value []byte
	err   error
}

func (s droppingStore) Get(context.Context, string, string) ([]byte, error) {
	return append([]byte(nil), s.value...), s.err
}

func TestVerifyStoredSecretRejectsWriteThatDidNotTakeEffect(t *testing.T) {
	written := []byte(testutil.EphemeralValue(t))
	previous := []byte(testutil.EphemeralValue(t))
	backendErr := errors.New("backend offline")

	for _, tc := range []struct {
		name  string
		store droppingStore
		code  string
	}{
		{"previous value kept", droppingStore{value: previous}, apperrors.CodeSecretUnverified},
		{"record missing", droppingStore{err: secretstore.ErrNotFound}, apperrors.CodeSecretUnverified},
		{"backend failure", droppingStore{err: backendErr}, apperrors.CodeBackendUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyStoredSecret(context.Background(), tc.store, "env-vault", "nexus-token", written)
			appErr, ok := apperrors.From(err)
			if !ok || appErr.Code != tc.code {
				t.Fatalf("err=%v, want code %s", err, tc.code)
			}
			testutil.AssertNotContains(t, "verify error", appErr.Error(), string(written))
			testutil.AssertNotContains(t, "verify error", appErr.Error(), string(previous))
		})
	}

	if err := verifyStoredSecret(context.Background(), droppingStore{value: written}, "env-vault", "nexus-token", written); err != nil {
		t.Fatalf("matching read-back rejected: %v", err)
	}
}
