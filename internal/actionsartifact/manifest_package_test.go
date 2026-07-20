package actionsartifact

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestManifestPackageIsDeterministicContentAddressedAndOfflineVerifiable(t *testing.T) {
	manifest := deletionTestManifest(t, 3)
	manifestData, err := MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(input, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}

	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	first, err := CreateManifestPackage(input, firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateManifestPackage(input, secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	rawDigest := sha256.Sum256(manifestData)
	rawSHA256 := hex.EncodeToString(rawDigest[:])
	wantObject := ManifestPackageObjectDirectory + "/" + rawSHA256 + ".json.gz"
	wantSummary := ManifestPackageSummaryDirectory + "/" + manifest.SemanticSHA256 + ".summary.json"
	if first.ObjectRelativePath != wantObject || first.SummaryRelativePath != wantSummary || !reflect.DeepEqual(first, second) {
		t.Fatalf("package paths or summary are not deterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}
	firstObject := readPackageTestFile(t, firstRoot, first.ObjectRelativePath)
	secondObject := readPackageTestFile(t, secondRoot, second.ObjectRelativePath)
	firstSummary := readPackageTestFile(t, firstRoot, first.SummaryRelativePath)
	secondSummary := readPackageTestFile(t, secondRoot, second.SummaryRelativePath)
	if !bytes.Equal(firstObject, secondObject) || !bytes.Equal(firstSummary, secondSummary) {
		t.Fatal("independent package creation did not produce byte-identical outputs")
	}
	if got := manifestPackageSHA256(firstObject); got != first.Summary.ManifestGZIPSHA256 || int64(len(firstObject)) != first.Summary.ManifestGZIPBytes ||
		first.Summary.ManifestRawSHA256 != rawSHA256 || first.Summary.ManifestRawBytes != int64(len(manifestData)) || first.Summary.Totals != manifest.Totals {
		t.Fatalf("summary bindings=%+v", first.Summary)
	}

	wantHeader := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff}
	if len(firstObject) < 23 || !bytes.Equal(firstObject[:10], wantHeader) || firstObject[10] != 0x01 {
		t.Fatalf("canonical gzip header/final block=%x", firstObject[:min(len(firstObject), 11)])
	}
	storedLength := binary.LittleEndian.Uint16(firstObject[11:13])
	storedInverse := binary.LittleEndian.Uint16(firstObject[13:15])
	if int(storedLength) != len(manifestData) || storedInverse != ^storedLength {
		t.Fatalf("canonical stored block length=%d inverse=%04x raw=%d", storedLength, storedInverse, len(manifestData))
	}
	trailer := firstObject[len(firstObject)-8:]
	if binary.LittleEndian.Uint32(trailer[:4]) != crc32.ChecksumIEEE(manifestData) || binary.LittleEndian.Uint32(trailer[4:]) != uint32(len(manifestData)) {
		t.Fatal("canonical gzip CRC32/ISIZE trailer mismatch")
	}
	reader, err := gzip.NewReader(bytes.NewReader(firstObject))
	if err != nil {
		t.Fatal(err)
	}
	if !reader.ModTime.IsZero() || reader.Name != "" || reader.Comment != "" || len(reader.Extra) != 0 || reader.OS != 255 {
		t.Fatalf("noncanonical gzip metadata: %+v", reader.Header)
	}
	decoded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(decoded, manifestData) {
		t.Fatalf("standard gzip roundtrip read=%v close=%v exact=%t", readErr, closeErr, bytes.Equal(decoded, manifestData))
	}

	verifiedPackage, verifiedManifest, err := VerifyManifestPackage(firstRoot, manifest.SemanticSHA256)
	if err != nil || !reflect.DeepEqual(verifiedPackage, first) || !reflect.DeepEqual(verifiedManifest, manifest) {
		t.Fatalf("offline verification package=%+v manifest=%+v error=%v", verifiedPackage, verifiedManifest, err)
	}
}

func TestManifestPackageRejectsNoncanonicalInputAndRewrite(t *testing.T) {
	manifest := deletionTestManifest(t, 1)
	canonical, err := MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(input, append(canonical, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := CreateManifestPackage(input, root); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("noncanonical manifest error=%v", err)
	}
	if err := os.WriteFile(input, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := CreateManifestPackage(input, root)
	if err != nil {
		t.Fatal(err)
	}
	objectBefore := readPackageTestFile(t, root, result.ObjectRelativePath)
	summaryBefore := readPackageTestFile(t, root, result.SummaryRelativePath)
	if _, err := CreateManifestPackage(input, root); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("rewrite error=%v", err)
	}
	if !bytes.Equal(objectBefore, readPackageTestFile(t, root, result.ObjectRelativePath)) || !bytes.Equal(summaryBefore, readPackageTestFile(t, root, result.SummaryRelativePath)) {
		t.Fatal("rewrite attempt changed existing package bytes")
	}
}

func TestManifestPackageVerifierRejectsPathDigestTotalsAndCanonicalDrift(t *testing.T) {
	manifest := deletionTestManifest(t, 1)
	canonical, err := MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*ManifestPackageSummary, *[]byte)
		want   string
	}{
		{"object path", func(summary *ManifestPackageSummary, _ *[]byte) {
			summary.ManifestObjectPath = ManifestPackageObjectDirectory + "/" + strings.Repeat("f", 64) + ".json.gz"
		}, "not bound"},
		{"gzip digest", func(summary *ManifestPackageSummary, _ *[]byte) { summary.ManifestGZIPSHA256 = strings.Repeat("f", 64) }, "gzip size or SHA-256"},
		{"totals", func(summary *ManifestPackageSummary, _ *[]byte) { summary.Totals.Delete.Bytes++ }, "semantic digest or totals"},
		{"gzip bytes", func(_ *ManifestPackageSummary, object *[]byte) { (*object)[len(*object)-1] ^= 0xff }, "gzip size or SHA-256"},
		{"noncanonical summary", func(_ *ManifestPackageSummary, object *[]byte) { *object = nil }, "not canonical"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(input, canonical, 0o600); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			result, err := CreateManifestPackage(input, root)
			if err != nil {
				t.Fatal(err)
			}
			objectPath := filepath.Join(root, filepath.FromSlash(result.ObjectRelativePath))
			summaryPath := filepath.Join(root, filepath.FromSlash(result.SummaryRelativePath))
			object := readPackageTestFile(t, root, result.ObjectRelativePath)
			summary := result.Summary
			test.mutate(&summary, &object)
			if test.name == "noncanonical summary" {
				data, err := json.Marshal(summary)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(summaryPath, append(data, '\n', '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				summaryData, err := MarshalCanonical(summary)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(summaryPath, summaryData, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(objectPath, object, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := VerifyManifestPackage(root, manifest.SemanticSHA256); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("verification error=%v want=%q", err, test.want)
			}
		})
	}
}

func readPackageTestFile(t *testing.T, root, relative string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
