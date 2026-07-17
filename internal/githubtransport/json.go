package githubtransport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

const maxJSONBytes = 64 << 20

func decodeStrict(data []byte, destination any) error {
	return strictjson.Decode(data, maxJSONBytes, destination)
}

func validateVendorJSON(data []byte) error {
	return strictjson.Validate(data, maxJSONBytes)
}

func MarshalDocument(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func WriteNoClobber(path string, data []byte) error {
	return writeNoClobber(path, data, nil)
}

func writeNoClobber(path string, data []byte, beforePublish func(string) error) error {
	if err := ValidateOutputPath(path); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".releasetransport.*")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary output: %w", err)
	}
	if _, err := io.Copy(temporary, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("write temporary output: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary output: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary output: %w", err)
	}
	if beforePublish != nil {
		if err := beforePublish(temporaryPath); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("output appeared during publication")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reinspect output: %w", err)
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("publish no-clobber output: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("remove temporary output: %w", err)
	}
	keep = true
	return nil
}

// ValidateOutputPath performs the no-clobber policy check before any network
// call. WriteNoClobber repeats it and uses a hard-link publication to close the
// race without replacing an existing path.
func ValidateOutputPath(path string) error {
	if path == "" || path == "-" {
		return errors.New("output must be a filesystem path")
	}
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("output directory must be an existing non-symlink directory")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect output: %w", err)
	}
	return nil
}
