package releasepromotion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"time"
)

func ParseLiteralVersionEvidence(data []byte) (LiteralVersionEvidence, error) {
	var evidence LiteralVersionEvidence
	if err := decodeStrict(data, maxVersionEvidenceBytes, &evidence); err != nil {
		return LiteralVersionEvidence{}, coded(CodeVersionMismatch, "decode literal version evidence", err)
	}
	return evidence, nil
}

func ReadLiteralVersionEvidence(filename string) (LiteralVersionBinding, error) {
	data, record, err := readBoundedRegular(filename, maxVersionEvidenceBytes)
	if err != nil {
		return LiteralVersionBinding{}, coded(CodeVersionMismatch, "read literal version evidence", err)
	}
	evidence, err := ParseLiteralVersionEvidence(data)
	if err != nil {
		return LiteralVersionBinding{}, err
	}
	binding := LiteralVersionBinding{File: record, Evidence: evidence}
	if err := ValidateLiteralVersionBinding(binding); err != nil {
		return LiteralVersionBinding{}, err
	}
	canonical, err := MarshalJSON(evidence)
	if err != nil {
		return LiteralVersionBinding{}, coded(CodeVersionMismatch, "encode literal version evidence", err)
	}
	if !bytes.Equal(data, canonical) {
		return LiteralVersionBinding{}, coded(CodeVersionMismatch, "literal version evidence is not canonical JSON", nil)
	}
	return binding, nil
}

func LiteralVersionEvidenceSHA256(evidence LiteralVersionEvidence) (string, error) {
	evidence.EvidenceSHA256 = ""
	data, err := jsonCompact(evidence)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func ValidateLiteralVersionBinding(binding LiteralVersionBinding) error {
	if !validFileDigest(binding.File) || binding.File.Name != binding.Evidence.PlatformID+"-version-results.json" {
		return coded(CodeDigestMismatch, "literal version evidence file identity is invalid", nil)
	}
	if err := ValidateLiteralVersionEvidence(binding.Evidence); err != nil {
		return err
	}
	canonical, err := MarshalJSON(binding.Evidence)
	if err != nil {
		return coded(CodeVersionMismatch, "encode literal version evidence", err)
	}
	digest := sha256.Sum256(canonical)
	if binding.File.Size != int64(len(canonical)) || binding.File.SHA256 != hex.EncodeToString(digest[:]) {
		return coded(CodeDigestMismatch, "literal version evidence file digest does not match embedded evidence", nil)
	}
	return nil
}

func ValidateLiteralVersionEvidence(evidence LiteralVersionEvidence) error {
	if evidence.SchemaID != LiteralVersionEvidenceSchema || evidence.SchemaVersion != SchemaVersion || evidence.Result != "pass" {
		return coded(CodeVersionMismatch, "literal version evidence schema or result is invalid", nil)
	}
	if evidence.PlatformID == "" || filepath.Base(evidence.PlatformID) != evidence.PlatformID {
		return coded(CodeVersionMismatch, "literal version evidence platform is invalid", nil)
	}
	if err := validateTuple(evidence.SourceSHA, evidence.ReleaseVersion, evidence.Repository, evidence.RunID, evidence.RunAttempt); err != nil {
		return err
	}
	if !validFileDigest(evidence.Binary) {
		return coded(CodeDigestMismatch, "literal version evidence binary digest is invalid", nil)
	}
	wantCommands := []LiteralVersionCommand{
		{Surface: "flag", Args: []string{"--version"}, Stdout: evidence.ReleaseVersion + "\n", Stderr: "", ExitCode: 0},
		{Surface: "command", Args: []string{"version"}, Stdout: evidence.ReleaseVersion + "\n", Stderr: "", ExitCode: 0},
	}
	if len(evidence.Commands) != 3 || !reflect.DeepEqual(evidence.Commands[:2], wantCommands) {
		return coded(CodeVersionMismatch, "literal text version commands differ from the exact release version", nil)
	}
	jsonCommand := evidence.Commands[2]
	if jsonCommand.Surface != "json" || !reflect.DeepEqual(jsonCommand.Args, []string{"version", "--json"}) || jsonCommand.Stderr != "" || jsonCommand.ExitCode != 0 {
		return coded(CodeVersionMismatch, "literal JSON version command identity is invalid", nil)
	}
	var response struct {
		OK        bool   `json:"ok"`
		Command   string `json:"command"`
		Timestamp string `json:"timestamp"`
		Data      struct {
			Version string `json:"version"`
		} `json:"data"`
		Warnings []string `json:"warnings"`
		Error    *struct {
			Code        string `json:"code"`
			Message     string `json:"message"`
			Remediation string `json:"remediation"`
		} `json:"error"`
	}
	if err := decodeStrict([]byte(jsonCommand.Stdout), maxReportBytes, &response); err != nil {
		return coded(CodeVersionMismatch, "decode JSON version output", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, response.Timestamp); err != nil || !response.OK || response.Command != "version" || response.Data.Version != evidence.ReleaseVersion || len(response.Warnings) != 0 || response.Error != nil {
		return coded(CodeVersionMismatch, "JSON version output differs from the exact release contract", err)
	}
	wantResults := LiteralVersionResults{Flag: evidence.ReleaseVersion, Command: evidence.ReleaseVersion, JSON: evidence.ReleaseVersion}
	if evidence.Results != wantResults {
		return coded(CodeVersionMismatch, "literal version results differ from the exact release version", nil)
	}
	digest, err := LiteralVersionEvidenceSHA256(evidence)
	if err != nil {
		return coded(CodeVersionMismatch, "hash literal version evidence", err)
	}
	if evidence.EvidenceSHA256 != digest {
		return coded(CodeDigestMismatch, "literal version evidence self-digest mismatch", nil)
	}
	return nil
}

func jsonCompact(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("empty JSON encoding")
	}
	return data, nil
}
