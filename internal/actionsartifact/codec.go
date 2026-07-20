package actionsartifact

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

const (
	MaxSnapshotBytes = 64 << 20
	MaxScopeBytes    = 1 << 20
	MaxManifestBytes = 96 << 20
)

func LoadSnapshotFile(filename string, policy Policy, now time.Time, maxAge time.Duration) (Snapshot, error) {
	data, err := readLimitedFile(filename, MaxSnapshotBytes)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read Actions artifact snapshot: %w", err)
	}
	var snapshot Snapshot
	if err := strictjson.Decode(data, MaxSnapshotBytes, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode Actions artifact snapshot: %w", err)
	}
	if err := ValidateSnapshot(snapshot, policy, now, maxAge); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func LoadDecisionScopeFile(filename string, snapshot Snapshot, now time.Time, maxAge time.Duration) (DecisionScope, error) {
	data, err := readLimitedFile(filename, MaxScopeBytes)
	if err != nil {
		return DecisionScope{}, fmt.Errorf("read Actions artifact decision scope: %w", err)
	}
	var scope DecisionScope
	if err := strictjson.Decode(data, MaxScopeBytes, &scope); err != nil {
		return DecisionScope{}, fmt.Errorf("decode Actions artifact decision scope: %w", err)
	}
	if err := ValidateDecisionScope(scope, snapshot, now, maxAge); err != nil {
		return DecisionScope{}, err
	}
	return scope, nil
}

func LoadDecisionManifestFile(filename string, snapshot Snapshot, scope DecisionScope, policy Policy, now time.Time, maxAge time.Duration) (DecisionManifest, error) {
	data, err := readLimitedFile(filename, MaxManifestBytes)
	if err != nil {
		return DecisionManifest{}, fmt.Errorf("read Actions artifact decision manifest: %w", err)
	}
	var manifest DecisionManifest
	if err := strictjson.Decode(data, MaxManifestBytes, &manifest); err != nil {
		return DecisionManifest{}, fmt.Errorf("decode Actions artifact decision manifest: %w", err)
	}
	if err := ValidateDecisionManifest(manifest, snapshot, scope, policy, now, maxAge); err != nil {
		return DecisionManifest{}, err
	}
	return manifest, nil
}

func SnapshotSemanticSHA256(snapshot Snapshot, policy Policy, now time.Time, maxAge time.Duration) (string, error) {
	if err := ValidateSnapshot(snapshot, policy, now, maxAge); err != nil {
		return "", err
	}
	return snapshotSemanticSHA256(snapshot)
}

// MarshalCanonical emits the deterministic typed JSON representation used by
// offline handoffs. A final newline is included for normal file semantics but
// is not part of any semantic digest.
func MarshalCanonical(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical Actions artifact JSON: %w", err)
	}
	return append(data, '\n'), nil
}

// WriteNoClobber writes a private regular output only when the exact path does
// not already exist. Callers choose a fully validated parent directory.
func WriteNoClobber(filename string, data []byte) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = file.Close()
			_ = os.Remove(filename)
		}
	}()
	if written, err := file.Write(data); err != nil {
		return err
	} else if written != len(data) {
		return fmt.Errorf("short write: wrote %d of %d bytes", written, len(data))
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}
