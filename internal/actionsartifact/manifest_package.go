package actionsartifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/canonicalgzip"
	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

const (
	ManifestPackageSummarySchemaID      = "env-vault.actions-artifact-manifest-package-summary.v1"
	ManifestPackageSummarySchemaVersion = 1
	ManifestPackageRoot                 = "evidence/actions-artifact-cleanups"
	ManifestPackageObjectDirectory      = ManifestPackageRoot + "/objects/sha256"
	ManifestPackageSummaryDirectory     = ManifestPackageRoot + "/manifests"
	ManifestPackageCompression          = "gzip-stored-deflate-v1"
	MaxManifestPackageSummaryBytes      = 32 << 10
	MaxManifestPackageGZIPBytes         = MaxManifestBytes + ((MaxManifestBytes+65534)/65535)*5 + 18
)

// ManifestPackageSummary is deliberately timeless: the canonical manifest
// carries its observation bindings, while the package bytes remain identical
// across independent offline creation and review.
type ManifestPackageSummary struct {
	SchemaID                  string         `json:"schema_id"`
	SchemaVersion             int            `json:"schema_version"`
	ManifestSemanticSHA256    string         `json:"manifest_semantic_sha256"`
	ManifestRawSHA256         string         `json:"manifest_raw_sha256"`
	ManifestRawBytes          int64          `json:"manifest_raw_bytes"`
	ManifestObjectPath        string         `json:"manifest_object_path"`
	ManifestGZIPSHA256        string         `json:"manifest_gzip_sha256"`
	ManifestGZIPBytes         int64          `json:"manifest_gzip_bytes"`
	ManifestObjectCompression string         `json:"manifest_object_compression"`
	Totals                    DecisionTotals `json:"totals"`
}

type ManifestPackage struct {
	Summary             ManifestPackageSummary
	ObjectRelativePath  string
	SummaryRelativePath string
}

// CreateManifestPackage validates exact canonical manifest bytes and writes a
// no-clobber content-addressed object plus its canonical summary. It performs
// no network access and does not create deletion authority by itself.
func CreateManifestPackage(manifestFilename, repositoryRoot string) (ManifestPackage, error) {
	manifestData, err := readStableDeletionFile(manifestFilename, MaxManifestBytes)
	if err != nil {
		return ManifestPackage{}, err
	}
	manifest, err := decodeCanonicalAuthorizedManifest(manifestData)
	if err != nil {
		return ManifestPackage{}, err
	}
	rawSHA256 := manifestPackageSHA256(manifestData)
	compressed, err := canonicalgzip.Encode(manifestData)
	if err != nil || len(compressed) > MaxManifestPackageGZIPBytes {
		return ManifestPackage{}, errors.New("canonical manifest gzip exceeds its byte bound")
	}
	objectRelativePath := ManifestPackageObjectDirectory + "/" + rawSHA256 + ".json.gz"
	summaryRelativePath := ManifestPackageSummaryDirectory + "/" + manifest.SemanticSHA256 + ".summary.json"
	summary := ManifestPackageSummary{
		SchemaID: ManifestPackageSummarySchemaID, SchemaVersion: ManifestPackageSummarySchemaVersion,
		ManifestSemanticSHA256: manifest.SemanticSHA256,
		ManifestRawSHA256:      rawSHA256, ManifestRawBytes: int64(len(manifestData)),
		ManifestObjectPath: objectRelativePath,
		ManifestGZIPSHA256: manifestPackageSHA256(compressed), ManifestGZIPBytes: int64(len(compressed)),
		ManifestObjectCompression: ManifestPackageCompression, Totals: manifest.Totals,
	}
	summaryData, err := MarshalCanonical(summary)
	if err != nil || len(summaryData) > MaxManifestPackageSummaryBytes {
		return ManifestPackage{}, errors.New("canonical manifest package summary exceeds its byte bound")
	}
	root, err := checkedManifestPackageRoot(repositoryRoot)
	if err != nil {
		return ManifestPackage{}, err
	}
	if err := ensureManifestPackageDirectories(root); err != nil {
		return ManifestPackage{}, err
	}
	objectPath := filepath.Join(root, filepath.FromSlash(objectRelativePath))
	summaryPath := filepath.Join(root, filepath.FromSlash(summaryRelativePath))
	if err := requireManifestPackageTargetsAbsent(objectPath, summaryPath); err != nil {
		return ManifestPackage{}, err
	}
	if err := WriteNoClobber(objectPath, compressed); err != nil {
		return ManifestPackage{}, fmt.Errorf("write no-clobber manifest object: %w", err)
	}
	if err := syncManifestPackageDirectory(filepath.Dir(objectPath)); err != nil {
		return ManifestPackage{}, err
	}
	if err := WriteNoClobber(summaryPath, summaryData); err != nil {
		return ManifestPackage{}, fmt.Errorf("write no-clobber manifest summary: %w", err)
	}
	if err := syncManifestPackageDirectory(filepath.Dir(summaryPath)); err != nil {
		return ManifestPackage{}, err
	}
	return ManifestPackage{Summary: summary, ObjectRelativePath: objectRelativePath, SummaryRelativePath: summaryRelativePath}, nil
}

// VerifyManifestPackage reconstructs and validates the packaged manifest
// entirely offline, including canonical summary/object/path/size/digest
// bindings and the complete manifest semantic digest and totals.
func VerifyManifestPackage(repositoryRoot, manifestSemanticSHA256 string) (ManifestPackage, DecisionManifest, error) {
	if !sha256Pattern.MatchString(manifestSemanticSHA256) {
		return ManifestPackage{}, DecisionManifest{}, errors.New("manifest semantic SHA-256 is malformed")
	}
	root, err := checkedManifestPackageRoot(repositoryRoot)
	if err != nil {
		return ManifestPackage{}, DecisionManifest{}, err
	}
	summaryRelativePath := ManifestPackageSummaryDirectory + "/" + manifestSemanticSHA256 + ".summary.json"
	summaryPath := filepath.Join(root, filepath.FromSlash(summaryRelativePath))
	summaryData, err := readStableDeletionFile(summaryPath, MaxManifestPackageSummaryBytes)
	if err != nil {
		return ManifestPackage{}, DecisionManifest{}, err
	}
	var summary ManifestPackageSummary
	if err := strictjson.Decode(summaryData, MaxManifestPackageSummaryBytes, &summary); err != nil {
		return ManifestPackage{}, DecisionManifest{}, err
	}
	canonicalSummary, err := MarshalCanonical(summary)
	if err != nil || !bytes.Equal(summaryData, canonicalSummary) {
		return ManifestPackage{}, DecisionManifest{}, errors.New("manifest package summary is not canonical JSON")
	}
	if err := validateManifestPackageSummary(summary, manifestSemanticSHA256); err != nil {
		return ManifestPackage{}, DecisionManifest{}, err
	}
	objectPath := filepath.Join(root, filepath.FromSlash(summary.ManifestObjectPath))
	compressed, err := readStableDeletionFile(objectPath, MaxManifestPackageGZIPBytes)
	if err != nil {
		return ManifestPackage{}, DecisionManifest{}, err
	}
	if int64(len(compressed)) != summary.ManifestGZIPBytes || manifestPackageSHA256(compressed) != summary.ManifestGZIPSHA256 {
		return ManifestPackage{}, DecisionManifest{}, errors.New("manifest package gzip size or SHA-256 mismatch")
	}
	manifestData, err := canonicalgzip.Decode(compressed, summary.ManifestRawBytes, MaxManifestBytes, MaxManifestPackageGZIPBytes)
	if err != nil {
		return ManifestPackage{}, DecisionManifest{}, fmt.Errorf("manifest package gzip: %w", err)
	}
	if manifestPackageSHA256(manifestData) != summary.ManifestRawSHA256 {
		return ManifestPackage{}, DecisionManifest{}, errors.New("manifest package raw SHA-256 mismatch")
	}
	manifest, err := decodeCanonicalAuthorizedManifest(manifestData)
	if err != nil {
		return ManifestPackage{}, DecisionManifest{}, err
	}
	if manifest.SemanticSHA256 != summary.ManifestSemanticSHA256 || manifest.Totals != summary.Totals {
		return ManifestPackage{}, DecisionManifest{}, errors.New("manifest package semantic digest or totals mismatch")
	}
	result := ManifestPackage{Summary: summary, ObjectRelativePath: summary.ManifestObjectPath, SummaryRelativePath: summaryRelativePath}
	return result, manifest, nil
}

func decodeCanonicalAuthorizedManifest(data []byte) (DecisionManifest, error) {
	var manifest DecisionManifest
	if err := strictjson.Decode(data, MaxManifestBytes, &manifest); err != nil {
		return DecisionManifest{}, err
	}
	if err := ValidateAuthorizedDecisionManifest(manifest); err != nil {
		return DecisionManifest{}, err
	}
	canonical, err := MarshalCanonical(manifest)
	if err != nil || !bytes.Equal(data, canonical) {
		return DecisionManifest{}, errors.New("authorized decision manifest JSON is not canonical")
	}
	return manifest, nil
}

func validateManifestPackageSummary(summary ManifestPackageSummary, semanticSHA256 string) error {
	if summary.SchemaID != ManifestPackageSummarySchemaID || summary.SchemaVersion != ManifestPackageSummarySchemaVersion ||
		summary.ManifestSemanticSHA256 != semanticSHA256 || !sha256Pattern.MatchString(summary.ManifestRawSHA256) ||
		!sha256Pattern.MatchString(summary.ManifestGZIPSHA256) || summary.ManifestRawBytes <= 0 || summary.ManifestRawBytes > MaxManifestBytes ||
		summary.ManifestGZIPBytes <= 0 || summary.ManifestGZIPBytes > MaxManifestPackageGZIPBytes ||
		summary.ManifestObjectCompression != ManifestPackageCompression {
		return errors.New("manifest package summary fields are malformed")
	}
	wantObjectPath := ManifestPackageObjectDirectory + "/" + summary.ManifestRawSHA256 + ".json.gz"
	if summary.ManifestObjectPath != wantObjectPath || path.Clean(summary.ManifestObjectPath) != summary.ManifestObjectPath || strings.Contains(summary.ManifestObjectPath, "\\") {
		return errors.New("manifest package object path is not bound to the raw SHA-256")
	}
	return nil
}

func manifestPackageSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func checkedManifestPackageRoot(repositoryRoot string) (string, error) {
	if repositoryRoot == "" {
		return "", errors.New("repository root is required")
	}
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("repository root must be a regular non-symlink directory")
	}
	return root, nil
}

func ensureManifestPackageDirectories(root string) error {
	for _, relative := range []string{
		"evidence",
		ManifestPackageRoot,
		ManifestPackageRoot + "/objects",
		ManifestPackageObjectDirectory,
		ManifestPackageSummaryDirectory,
	} {
		filename := filepath.Join(root, filepath.FromSlash(relative))
		created := false
		if err := os.Mkdir(filename, 0o755); err != nil {
			if !os.IsExist(err) {
				return fmt.Errorf("create manifest package directory: %w", err)
			}
		} else {
			created = true
		}
		info, err := os.Lstat(filename)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("manifest package path contains a non-directory or symlink")
		}
		if created {
			if err := syncManifestPackageDirectory(filepath.Dir(filename)); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireManifestPackageTargetsAbsent(filenames ...string) error {
	for _, filename := range filenames {
		if _, err := os.Lstat(filename); err == nil {
			return fmt.Errorf("manifest package target already exists: %s", filepath.Base(filename))
		} else if !os.IsNotExist(err) {
			return errors.New("manifest package target cannot be checked safely")
		}
	}
	return nil
}

func syncManifestPackageDirectory(filename string) error {
	directory, err := os.Open(filename)
	if err != nil {
		return errors.New("manifest package directory cannot be opened for sync")
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.New("manifest package directory could not be synced")
	}
	return nil
}
