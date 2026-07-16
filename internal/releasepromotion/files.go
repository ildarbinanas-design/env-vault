package releasepromotion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

const (
	maxContractBytes        = 1 << 20
	maxProofBytes           = 8 << 20
	maxReportBytes          = 16 << 20
	maxVersionEvidenceBytes = 256 << 10
	maxChecksumBytes        = 8 << 10
	maxArchiveBytes         = 256 << 20
	maxBinaryBytes          = 128 << 20
	maxArtifactEntryCount   = 10
)

func ParsePlatformProof(data []byte) (PlatformProof, error) {
	var proof PlatformProof
	if err := decodeStrict(data, maxProofBytes, &proof); err != nil {
		return PlatformProof{}, coded(CodePromotionManifestInvalid, "decode platform proof", err)
	}
	return proof, nil
}

func ParseSourceQualityProof(data []byte) (SourceQualityProof, error) {
	var proof SourceQualityProof
	if err := decodeStrict(data, maxProofBytes, &proof); err != nil {
		return SourceQualityProof{}, coded(CodePromotionManifestInvalid, "decode source-quality proof", err)
	}
	return proof, nil
}

func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := decodeStrict(data, maxProofBytes, &manifest); err != nil {
		return Manifest{}, coded(CodePromotionManifestInvalid, "decode promotion manifest", err)
	}
	return manifest, nil
}

func ReadPlatformProof(filename string) (PlatformProof, FileDigest, error) {
	data, record, err := readBoundedRegular(filename, maxProofBytes)
	if err != nil {
		return PlatformProof{}, FileDigest{}, coded(CodePromotionManifestInvalid, "read platform proof", err)
	}
	proof, err := ParsePlatformProof(data)
	return proof, record, err
}

func ReadSourceQualityProof(filename string) (SourceQualityProof, FileDigest, error) {
	data, record, err := readBoundedRegular(filename, maxProofBytes)
	if err != nil {
		return SourceQualityProof{}, FileDigest{}, coded(CodePromotionManifestInvalid, "read source-quality proof", err)
	}
	proof, err := ParseSourceQualityProof(data)
	return proof, record, err
}

func ReadManifest(filename string) (Manifest, error) {
	data, _, err := readBoundedRegular(filename, maxProofBytes)
	if err != nil {
		return Manifest{}, coded(CodePromotionManifestInvalid, "read promotion manifest", err)
	}
	return ParseManifest(data)
}

// MarshalJSON produces a stable, reviewable document. Self-digests use the
// compact encoding/json representation of the same typed value.
func MarshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func readBoundedRegular(filename string, limit int64) ([]byte, FileDigest, error) {
	file, info, err := openBoundedRegular(filename, limit)
	if err != nil {
		return nil, FileDigest{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, FileDigest{}, err
	}
	if int64(len(data)) != info.Size() || int64(len(data)) > limit {
		return nil, FileDigest{}, errors.New("file size changed while reading or exceeded its bound")
	}
	sum := sha256.Sum256(data)
	return data, FileDigest{Name: filepath.Base(filename), Size: info.Size(), SHA256: hex.EncodeToString(sum[:])}, nil
}

func digestBoundedRegular(filename string, limit int64) (FileDigest, error) {
	file, info, err := openBoundedRegular(filename, limit)
	if err != nil {
		return FileDigest{}, err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil {
		return FileDigest{}, err
	}
	if n != info.Size() || n > limit {
		return FileDigest{}, errors.New("file size changed while hashing or exceeded its bound")
	}
	return FileDigest{Name: filepath.Base(filename), Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func openBoundedRegular(filename string, limit int64) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(filename)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s is not a regular non-symlink file", filename)
	}
	if before.Size() <= 0 || before.Size() > limit {
		return nil, nil, fmt.Errorf("%s size %d is outside 1..%d", filename, before.Size(), limit)
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, nil, err
	}
	after, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != before.Size() {
		file.Close()
		return nil, nil, fmt.Errorf("%s changed identity while opening", filename)
	}
	return file, after, nil
}

func decodeStrict(data []byte, limit int, destination any) error {
	return strictjson.Decode(data, limit, destination)
}
