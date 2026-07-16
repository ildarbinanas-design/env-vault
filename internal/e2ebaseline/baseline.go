// Package e2ebaseline stores and verifies durable E2E compatibility floors.
package e2ebaseline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/e2esuite"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	SchemaID                = "env-vault.e2e-baseline.v2"
	SchemaVersion           = 2
	VerificationSchemaID    = "env-vault.e2e-baseline-verification.v1"
	DiffSchemaID            = "env-vault.e2e-baseline-diff.v1"
	CanonicalPath           = "docs/e2e-baseline.json"
	ReviewedSuiteTransition = "independent_second_secret_sentinel"
	maxJSONBytes            = 16 << 20
)

var (
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	goPattern       = regexp.MustCompile(`^go[0-9]+\.[0-9]+\.[0-9]+$`)
	reporterPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
)

type Baseline struct {
	SchemaID      string              `json:"schema_id"`
	SchemaVersion int                 `json:"schema_version"`
	SemanticSuite SemanticSuite       `json:"semantic_suite"`
	Toolchain     Toolchain           `json:"toolchain"`
	Provenance    Provenance          `json:"provenance"`
	Migration     *MigrationReference `json:"migration,omitempty"`
	Platforms     []PlatformBaseline  `json:"platforms"`
}

type SemanticSuite struct {
	Algorithm        string `json:"algorithm"`
	Hash             string `json:"hash"`
	SourceReportHash string `json:"source_report_hash"`
	TransitionCode   string `json:"transition_code,omitempty"`
}

type MigrationReference struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Toolchain struct {
	GoVersion        string `json:"go_version"`
	GotestsumVersion string `json:"gotestsum_version"`
}

type Provenance struct {
	Repository string `json:"repository"`
	CommitSHA  string `json:"commit_sha"`
	RunID      string `json:"run_id"`
	RunURL     string `json:"run_url"`
	RunAttempt string `json:"run_attempt"`
	Phase      string `json:"phase"`
}

type PlatformBaseline struct {
	ID                   string                `json:"id"`
	GOOS                 string                `json:"goos"`
	GOARCH               string                `json:"goarch"`
	ContractSHA256       string                `json:"contract_sha256"`
	CoverageFloorPercent float64               `json:"coverage_floor_percent"`
	Counts               Counts                `json:"counts"`
	ExpectedSkips        []string              `json:"expected_skips"`
	CriticalScenarios    []ScenarioExpectation `json:"critical_scenarios"`
	Leak                 LeakExpectation       `json:"leak"`
}

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

// BaselineFacts is the normalized run evidence bound by the one-time
// migration proof. Keeping semantic transition metadata out of this digest
// avoids a circular baseline/proof reference.
type BaselineFacts struct {
	Toolchain  Toolchain          `json:"toolchain"`
	Provenance Provenance         `json:"provenance"`
	Platforms  []PlatformBaseline `json:"platforms"`
}

func (baseline Baseline) Facts() BaselineFacts {
	return BaselineFacts{Toolchain: baseline.Toolchain, Provenance: baseline.Provenance, Platforms: baseline.Platforms}
}

func LoadFile(filename string, contract releasecontract.Contract) (Baseline, error) {
	data, err := readBoundedRegular(filename, maxJSONBytes)
	if err != nil {
		return Baseline{}, fmt.Errorf("read E2E baseline: %w", err)
	}
	var baseline Baseline
	if err := decodeStrict(data, &baseline); err != nil {
		return Baseline{}, fmt.Errorf("decode E2E baseline: %w", err)
	}
	if err := baseline.Validate(contract); err != nil {
		return Baseline{}, err
	}
	return baseline, nil
}

func (baseline Baseline) Validate(contract releasecontract.Contract) error {
	var problems []string
	add := func(format string, values ...any) { problems = append(problems, fmt.Sprintf(format, values...)) }
	if baseline.SchemaID != SchemaID || baseline.SchemaVersion != SchemaVersion {
		add("schema must be %s version %d", SchemaID, SchemaVersion)
	}
	if baseline.SemanticSuite.Algorithm != e2esuite.SchemaID || !validSHA256(baseline.SemanticSuite.Hash) || !validSHA256(baseline.SemanticSuite.SourceReportHash) {
		add("semantic suite algorithm or hashes are invalid")
	}
	if baseline.SemanticSuite.Hash == baseline.SemanticSuite.SourceReportHash {
		if baseline.SemanticSuite.TransitionCode != "" || baseline.Migration != nil {
			add("suite transition and migration must be empty when source and semantic hashes match")
		}
	} else {
		if baseline.SemanticSuite.TransitionCode != ReviewedSuiteTransition {
			add("suite hash transition requires code %s", ReviewedSuiteTransition)
		}
		if baseline.Migration == nil || !safeRepositoryPath(baseline.Migration.Path) || !validSHA256(baseline.Migration.SHA256) {
			add("suite hash transition requires a valid checked-in migration proof reference")
		}
	}
	if !goPattern.MatchString(baseline.Toolchain.GoVersion) || !reporterPattern.MatchString(baseline.Toolchain.GotestsumVersion) {
		add("toolchain versions must be exact stable Go and gotestsum versions")
	}
	if !validRepository(baseline.Provenance.Repository) || !commitPattern.MatchString(baseline.Provenance.CommitSHA) || !positiveInteger(baseline.Provenance.RunID) || !positiveInteger(baseline.Provenance.RunAttempt) || baseline.Provenance.Phase != "candidate" {
		add("provenance repository, commit, run, attempt, or phase is invalid")
	}
	wantURL := "https://github.com/" + baseline.Provenance.Repository + "/actions/runs/" + baseline.Provenance.RunID
	if baseline.Provenance.RunURL != wantURL {
		add("provenance run URL does not match repository and run ID")
	}
	if len(baseline.Platforms) != len(contract.Platforms) {
		add("baseline platform count=%d, want %d", len(baseline.Platforms), len(contract.Platforms))
	}
	seen := make(map[string]bool)
	for index, platform := range baseline.Platforms {
		if index >= len(contract.Platforms) {
			break
		}
		declared := contract.Platforms[index]
		if platform.ID != declared.ID || platform.GOOS != declared.GOOS || platform.GOARCH != declared.GOARCH {
			add("platform %d does not match release contract order/target", index)
		}
		if seen[platform.ID] || !validSHA256(platform.ContractSHA256) || math.IsNaN(platform.CoverageFloorPercent) || math.IsInf(platform.CoverageFloorPercent, 0) || platform.CoverageFloorPercent <= 0 || platform.CoverageFloorPercent > 100 {
			add("platform %q has duplicate ID, invalid contract hash, or invalid coverage floor", platform.ID)
		}
		seen[platform.ID] = true
		if platform.Counts.Passed <= 0 || platform.Counts.Failed != 0 || platform.Counts.Missing != 0 || platform.Counts.Skipped != len(platform.ExpectedSkips) {
			add("platform %q counts do not represent a complete passing run", platform.ID)
		}
		if !sortedUnique(platform.ExpectedSkips) || len(platform.CriticalScenarios) == 0 {
			add("platform %q skip/scenario expectations are empty, unsorted, or duplicated", platform.ID)
		}
		scenarioIDs := make(map[string]bool)
		var skips []string
		for scenarioIndex, scenario := range platform.CriticalScenarios {
			if scenario.ID == "" || scenarioIDs[scenario.ID] || (scenario.Result != "pass" && scenario.Result != "expected_skip") {
				add("platform %q has malformed critical scenario %q", platform.ID, scenario.ID)
			}
			if scenarioIndex > 0 && platform.CriticalScenarios[scenarioIndex-1].ID >= scenario.ID {
				add("platform %q critical scenarios are not sorted and unique", platform.ID)
			}
			scenarioIDs[scenario.ID] = true
			if scenario.Result == "expected_skip" {
				skips = append(skips, scenario.ID)
			}
		}
		sort.Strings(skips)
		if !equalStrings(skips, platform.ExpectedSkips) {
			add("platform %q expected skips differ from scenario results", platform.ID)
		}
		if platform.Leak.Status != "pass" || platform.Leak.Detected || platform.Leak.FilesScanned <= 0 || platform.Leak.Occurrences != 0 || platform.Leak.RegistryRecords <= 0 || platform.Leak.Findings != 0 {
			add("platform %q leak expectation does not fail closed", platform.ID)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New("invalid E2E baseline: " + strings.Join(problems, "; "))
	}
	return nil
}

func Marshal(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func WriteFile(filename string, value any) error {
	data, err := Marshal(value)
	if err != nil {
		return err
	}
	return writeAtomic(filename, data)
}

func Digest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func fileSHA256(filename string) (string, error) {
	data, err := readBoundedRegular(filename, maxJSONBytes)
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

func writeAtomic(filename string, data []byte) error {
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".e2e-baseline-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, filename)
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

func safeRepositoryPath(value string) bool {
	if value == "" || strings.Contains(value, `\`) || filepath.IsAbs(value) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
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
