package releaseevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestGenesisAnchorBuildVerifyAndCanonicalSelfDigest(t *testing.T) {
	_, evidence, files := newBundleFixture(t)
	bundle, err := ParseBundle(files.Root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := BuildGenesisAnchor(bundle, evidence)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildGenesisAnchor(bundle, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("repeated genesis anchors differ:\n%+v\n%+v", first, second)
	}
	if first.FirstBundleSHA256 != bundle.BundleSHA256 || first.Repository != bundle.Repository || first.SourceSHA != bundle.SourceSHA || first.FirstReleaseVersion != bundle.ReleaseVersion || first.AnchorSHA256 == "" {
		t.Fatalf("genesis tuple=%+v bundle=%+v", first, bundle)
	}
	if err := VerifyGenesisAnchor(first, &bundle, &evidence); err != nil {
		t.Fatal(err)
	}
	standalone := first
	standalone.AnchorSHA256 = ""
	blankCanonical, err := json.Marshal(standalone)
	if err != nil {
		t.Fatal(err)
	}
	blankDigest := sha256.Sum256(blankCanonical)
	if first.AnchorSHA256 != hex.EncodeToString(blankDigest[:]) {
		t.Fatalf("anchor digest does not use canonical blank-self-digest encoding: %s", first.AnchorSHA256)
	}
	encoded, err := MarshalJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseGenesisAnchor(encoded)
	if err != nil || parsed != first {
		t.Fatalf("parse genesis: parsed=%+v err=%v", parsed, err)
	}
}

func TestGenesisAnchorRejectsTamperingAliasesAndTupleMismatch(t *testing.T) {
	_, evidence, files := newBundleFixture(t)
	bundle, err := ParseBundle(files.Root)
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := BuildGenesisAnchor(bundle, evidence)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalJSON(anchor)
	if err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string][]byte{
		"leading whitespace":   append([]byte{' '}, encoded...),
		"duplicate field":      append([]byte(`{"schema_id":"duplicate",`), bytes.TrimSpace(encoded)[1:]...),
		"case alias":           append([]byte(`{"SCHEMA_ID":"duplicate",`), bytes.TrimSpace(encoded)[1:]...),
		"unknown secret field": append(append([]byte(nil), bytes.TrimSpace(encoded)[:len(bytes.TrimSpace(encoded))-1]...), []byte(`,"token":"sentinel"}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseGenesisAnchor(candidate); err == nil {
				t.Fatal("invalid genesis JSON was accepted")
			}
		})
	}

	tampered := anchor
	tampered.AnchorSHA256 = strings.Repeat("0", 64)
	if err := VerifyGenesisAnchor(tampered, nil, nil); ErrorCode(err) != CodeDigestMismatch {
		t.Fatalf("self-digest error=%v code=%q", err, ErrorCode(err))
	}

	tupleMismatch := anchor
	tupleMismatch.Repository = "other/repository"
	resealGenesisForTest(t, &tupleMismatch)
	if err := VerifyGenesisAnchor(tupleMismatch, &bundle, &evidence); ErrorCode(err) != CodeSourceMismatch {
		t.Fatalf("tuple mismatch error=%v code=%q", err, ErrorCode(err))
	}

	invalidVersion := anchor
	invalidVersion.FirstReleaseVersion = "v01.0.0"
	resealGenesisForTest(t, &invalidVersion)
	if err := VerifyGenesisAnchor(invalidVersion, nil, nil); ErrorCode(err) != CodeInputInvalid {
		t.Fatalf("invalid version error=%v code=%q", err, ErrorCode(err))
	}

	invalidEvidenceRun := anchor
	invalidEvidenceRun.EvidenceRunAttempt = 0
	resealGenesisForTest(t, &invalidEvidenceRun)
	if err := VerifyGenesisAnchor(invalidEvidenceRun, nil, nil); ErrorCode(err) != CodeInputInvalid {
		t.Fatalf("invalid evidence attempt error=%v code=%q", err, ErrorCode(err))
	}

	crossLinked := evidence
	crossLinked.Authorization.EvidenceWorkflow.RunID++
	if err := VerifyGenesisAnchor(anchor, &bundle, &crossLinked); ErrorCode(err) != CodeDigestMismatch {
		t.Fatalf("tampered evidence workflow error=%v code=%q", err, ErrorCode(err))
	}

	if _, err := ParseGenesisAnchor(bytes.Repeat([]byte{'x'}, MaxGenesisAnchorBytes+1)); ErrorCode(err) != CodeInputInvalid {
		t.Fatalf("oversized anchor error=%v code=%q", err, ErrorCode(err))
	}
}

func resealGenesisForTest(t *testing.T, anchor *GenesisAnchor) {
	t.Helper()
	anchor.AnchorSHA256 = ""
	digest, err := GenesisAnchorSHA256(*anchor)
	if err != nil {
		t.Fatal(err)
	}
	anchor.AnchorSHA256 = digest
}
