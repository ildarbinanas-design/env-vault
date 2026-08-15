// Package e2ebaseline defines the sealed E2E matrix proof that the runner
// hands to release promotion, together with the strict-JSON substrate both
// sides decode it with.
//
// The package keeps its name for historical reasons. The durable checked-in
// baseline it was built around was removed on 2026-08-14; renaming the package
// would edit files under `e2e/`, which would invalidate the pinned semantic
// suite hash for no benefit.
package e2ebaseline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
)

const maxJSONBytes = 16 << 20

var (
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	goPattern       = regexp.MustCompile(`^go[0-9]+\.[0-9]+\.[0-9]+$`)
	reporterPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
)

type Counts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Missing int `json:"missing"`
}

type ScenarioExpectation struct {
	ID     string `json:"id"`
	Result string `json:"result"`
}

type LeakExpectation struct {
	Status          string `json:"status"`
	Detected        bool   `json:"detected"`
	FilesScanned    int    `json:"files_scanned"`
	Occurrences     int    `json:"occurrences"`
	RegistryRecords int    `json:"registry_records"`
	Findings        int    `json:"findings"`
}

func Digest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func decodeStrict(data []byte, destination any) error {
	if err := validateExactJSON(data, reflect.TypeOf(destination)); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

var rawMessageType = reflect.TypeOf(json.RawMessage{})

// validateExactJSON closes two fail-open behaviors in encoding/json's struct
// decoder: duplicate object keys and case-insensitive field matching. It walks
// the complete document against the destination type before typed decoding.
func validateExactJSON(data []byte, destinationType reflect.Type) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateJSONValue(decoder, destinationType, "$"); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, expected reflect.Type, path string) error {
	for expected != nil && expected.Kind() == reflect.Pointer {
		expected = expected.Elem()
	}
	if expected == rawMessageType || expected != nil && expected.Kind() == reflect.Interface {
		expected = nil
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		if expected != nil && expected.Kind() != reflect.Struct && expected.Kind() != reflect.Map {
			return fmt.Errorf("%s: JSON object is incompatible with %s", path, expected)
		}
		fields := map[string]reflect.Type(nil)
		if expected != nil && expected.Kind() == reflect.Struct {
			fields = exactJSONFields(expected)
		}
		seen := make(map[string]bool)
		seenFolded := make(map[string]string)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%s: read object key: %w", path, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%s: object key is not a string", path)
			}
			folded := strings.ToLower(key)
			if seen[key] {
				return fmt.Errorf("%s: duplicate JSON key %q", path, key)
			}
			if prior, ok := seenFolded[folded]; ok {
				return fmt.Errorf("%s: case-colliding JSON keys %q and %q", path, prior, key)
			}
			seen[key], seenFolded[folded] = true, key
			var childType reflect.Type
			if fields != nil {
				var exists bool
				childType, exists = fields[key]
				if !exists {
					return fmt.Errorf("%s: unknown or incorrectly cased JSON field %q", path, key)
				}
			} else if expected != nil && expected.Kind() == reflect.Map {
				childType = expected.Elem()
			}
			if err := validateJSONValue(decoder, childType, path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("%s: unterminated JSON object", path)
		}
		return nil
	case '[':
		if expected != nil && expected.Kind() != reflect.Slice && expected.Kind() != reflect.Array {
			return fmt.Errorf("%s: JSON array is incompatible with %s", path, expected)
		}
		var elementType reflect.Type
		if expected != nil {
			elementType = expected.Elem()
		}
		index := 0
		for decoder.More() {
			if err := validateJSONValue(decoder, elementType, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("%s: unterminated JSON array", path)
		}
		return nil
	default:
		return fmt.Errorf("%s: unexpected JSON delimiter %q", path, delimiter)
	}
}

func exactJSONFields(structType reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for index := 0; index < structType.NumField(); index++ {
		field := structType.Field(index)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

func readBoundedRegular(filename string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, fmt.Errorf("must be a non-empty regular file no larger than %d bytes", maximum)
	}
	return os.ReadFile(filename)
}

func validSHA256(value string) bool { return sha256Pattern.MatchString(value) }

func validRepository(value string) bool {
	parts := strings.Split(value, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != "" && !strings.ContainsAny(value, " \\")
}

func positiveInteger(value string) bool {
	if value == "" || value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func sortedUnique(values []string) bool {
	for index, value := range values {
		if value == "" || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
