package actionsartifact

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

type LiveRepositoryInspection struct {
	RepositoryID  int64
	FullName      string
	DefaultBranch string
}

type GitObjectInspection struct {
	Ref        string
	ObjectType string
	ObjectSHA  string
}

type BranchInspection struct {
	Name      string
	CommitSHA string
	Protected bool
}

type ReleaseInspection struct {
	ReleaseID       int64
	Version         string
	TargetCommitish string
	Draft           bool
	Prerelease      bool
	PublishedAt     string
	Assets          []ReleaseAssetProjection
}

type ContractContentInspection struct {
	BlobSHA string
	Content []byte
}

func InspectLiveRepositoryResponse(data []byte) (LiveRepositoryInspection, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return LiveRepositoryInspection{}, err
	}
	var result LiveRepositoryInspection
	if err := decodeRequired(object, "id", &result.RepositoryID); err != nil || result.RepositoryID < 1 {
		return LiveRepositoryInspection{}, errors.New("repository metadata has invalid id")
	}
	if err := decodeRequired(object, "full_name", &result.FullName); err != nil || ValidateRepositoryName(result.FullName) != nil {
		return LiveRepositoryInspection{}, errors.New("repository metadata has invalid full_name")
	}
	if err := decodeRequired(object, "default_branch", &result.DefaultBranch); err != nil || !validRefName(result.DefaultBranch) {
		return LiveRepositoryInspection{}, errors.New("repository metadata has invalid default_branch")
	}
	return result, nil
}

func InspectGitRefResponse(data []byte) (GitObjectInspection, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return GitObjectInspection{}, err
	}
	var result GitObjectInspection
	if err := decodeRequired(object, "ref", &result.Ref); err != nil || !strings.HasPrefix(result.Ref, "refs/") {
		return GitObjectInspection{}, errors.New("git ref has invalid ref")
	}
	if err := decodeGitObject(object, &result.ObjectType, &result.ObjectSHA); err != nil {
		return GitObjectInspection{}, err
	}
	return result, nil
}

func InspectTagObjectResponse(data []byte, expectedTagSHA string) (GitObjectInspection, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return GitObjectInspection{}, err
	}
	var result GitObjectInspection
	var tagSHA string
	if err := decodeRequired(object, "sha", &tagSHA); err != nil || !objectSHAPattern(tagSHA) || tagSHA != expectedTagSHA {
		return GitObjectInspection{}, errors.New("annotated tag object top-level sha does not match the requested tag sha")
	}
	if err := decodeGitObject(object, &result.ObjectType, &result.ObjectSHA); err != nil {
		return GitObjectInspection{}, err
	}
	return result, nil
}

func decodeGitObject(parent map[string]json.RawMessage, objectType, objectSHA *string) error {
	var raw json.RawMessage
	if err := decodeRequired(parent, "object", &raw); err != nil {
		return err
	}
	object, err := parseVendorObject(raw)
	if err != nil {
		return err
	}
	if err := decodeRequired(object, "type", objectType); err != nil || (*objectType != "commit" && *objectType != "tag") {
		return errors.New("git object has unsupported type")
	}
	if err := decodeRequired(object, "sha", objectSHA); err != nil || !objectSHAPattern(*objectSHA) {
		return errors.New("git object has invalid sha")
	}
	return nil
}

func InspectBranchResponse(data []byte) (BranchInspection, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return BranchInspection{}, err
	}
	var result BranchInspection
	if err := decodeRequired(object, "name", &result.Name); err != nil || !validRefName(result.Name) {
		return BranchInspection{}, errors.New("branch has invalid name")
	}
	if err := decodeRequired(object, "protected", &result.Protected); err != nil {
		return BranchInspection{}, errors.New("branch is missing explicit protected state")
	}
	var rawCommit json.RawMessage
	if err := decodeRequired(object, "commit", &rawCommit); err != nil {
		return BranchInspection{}, err
	}
	commit, err := parseVendorObject(rawCommit)
	if err != nil {
		return BranchInspection{}, err
	}
	if err := decodeRequired(commit, "sha", &result.CommitSHA); err != nil || !shaPattern.MatchString(result.CommitSHA) {
		return BranchInspection{}, errors.New("branch has invalid commit sha")
	}
	return result, nil
}

func InspectPullRequestPage(data []byte, repository string, repositoryID int64) ([]LivePullRequest, error) {
	if err := strictjson.Validate(data, MaxRawPageBytes); err != nil {
		return nil, err
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(data, &messages); err != nil || messages == nil || len(messages) > MaxItemsPerPage {
		return nil, errors.New("pull-request page must be a non-null bounded array")
	}
	result := make([]LivePullRequest, 0, len(messages))
	for index, message := range messages {
		pullRequest, err := parsePullRequest(message, repository, repositoryID)
		if err != nil {
			return nil, fmt.Errorf("pull_requests[%d]: %w", index, err)
		}
		result = append(result, pullRequest)
	}
	return result, nil
}

func InspectExactPullRequest(data []byte, repository string, repositoryID, number int64) (LivePullRequest, error) {
	pullRequest, err := parsePullRequest(data, repository, repositoryID)
	if err != nil {
		return LivePullRequest{}, err
	}
	if pullRequest.Number != number {
		return LivePullRequest{}, errors.New("exact pull-request number mismatch")
	}
	return pullRequest, nil
}

func parsePullRequest(data []byte, repository string, repositoryID int64) (LivePullRequest, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return LivePullRequest{}, err
	}
	var result LivePullRequest
	var state string
	if err := decodeRequired(object, "number", &result.Number); err != nil || result.Number < 1 {
		return LivePullRequest{}, errors.New("pull request has invalid number")
	}
	if err := decodeRequired(object, "state", &state); err != nil || state != "open" {
		return LivePullRequest{}, errors.New("pull request is not open")
	}
	if err := decodeRequired(object, "draft", &result.Draft); err != nil {
		return LivePullRequest{}, errors.New("pull request is missing explicit draft state")
	}
	baseRef, baseSHA, baseRepositoryID, baseRepository, err := parsePullRequestSide(object, "base")
	if err != nil || baseRepositoryID != repositoryID || baseRepository != repository {
		return LivePullRequest{}, errors.New("pull request base repository mismatch")
	}
	headRef, headSHA, headRepositoryID, headRepository, err := parsePullRequestSide(object, "head")
	if err != nil || headRepositoryID != repositoryID || headRepository != repository {
		return LivePullRequest{}, errors.New("pull request head is null, malformed, or forked")
	}
	if !validRefName(baseRef) || !validRefName(headRef) || !shaPattern.MatchString(baseSHA) || !shaPattern.MatchString(headSHA) {
		return LivePullRequest{}, errors.New("pull request base/head identity is malformed")
	}
	result.BaseRef, result.HeadRef, result.HeadSHA = baseRef, headRef, headSHA
	return result, nil
}

func parsePullRequestSide(parent map[string]json.RawMessage, field string) (string, string, int64, string, error) {
	var raw json.RawMessage
	if err := decodeRequired(parent, field, &raw); err != nil {
		return "", "", 0, "", err
	}
	object, err := parseVendorObject(raw)
	if err != nil {
		return "", "", 0, "", err
	}
	var ref, sha string
	if err := decodeRequired(object, "ref", &ref); err != nil {
		return "", "", 0, "", err
	}
	if err := decodeRequired(object, "sha", &sha); err != nil {
		return "", "", 0, "", err
	}
	repositoryID, repository, err := parseNestedRepository(object, "repo")
	return ref, sha, repositoryID, repository, err
}

func InspectReleaseResponse(data []byte) (ReleaseInspection, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return ReleaseInspection{}, err
	}
	var result ReleaseInspection
	if err := decodeRequired(object, "id", &result.ReleaseID); err != nil || result.ReleaseID < 1 {
		return ReleaseInspection{}, errors.New("release has invalid id")
	}
	if err := decodeRequired(object, "tag_name", &result.Version); err != nil || !releaseVersionPattern.MatchString(result.Version) {
		return ReleaseInspection{}, errors.New("release has invalid tag_name")
	}
	if err := decodeRequired(object, "target_commitish", &result.TargetCommitish); err != nil || strings.TrimSpace(result.TargetCommitish) == "" {
		return ReleaseInspection{}, errors.New("release has invalid target_commitish")
	}
	if err := decodeRequired(object, "draft", &result.Draft); err != nil {
		return ReleaseInspection{}, errors.New("release is missing draft state")
	}
	if err := decodeRequired(object, "prerelease", &result.Prerelease); err != nil {
		return ReleaseInspection{}, errors.New("release is missing prerelease state")
	}
	if err := decodeRequired(object, "published_at", &result.PublishedAt); err != nil {
		return ReleaseInspection{}, errors.New("release is missing published_at")
	}
	if _, err := parseCanonicalTime(result.PublishedAt); err != nil {
		return ReleaseInspection{}, errors.New("release has invalid published_at")
	}
	var assets []json.RawMessage
	if err := decodeRequired(object, "assets", &assets); err != nil || assets == nil || len(assets) > 1000 {
		return ReleaseInspection{}, errors.New("release has invalid assets array")
	}
	seenIDs := make(map[int64]bool, len(assets))
	seenNames := make(map[string]bool, len(assets))
	for index, raw := range assets {
		asset, err := parseReleaseAsset(raw)
		if err != nil {
			return ReleaseInspection{}, fmt.Errorf("assets[%d]: %w", index, err)
		}
		if seenIDs[asset.ID] || seenNames[asset.Name] {
			return ReleaseInspection{}, errors.New("release assets contain a duplicate id or name")
		}
		seenIDs[asset.ID], seenNames[asset.Name] = true, true
		result.Assets = append(result.Assets, asset)
	}
	sort.Slice(result.Assets, func(i, j int) bool { return result.Assets[i].ID < result.Assets[j].ID })
	return result, nil
}

func parseReleaseAsset(data []byte) (ReleaseAssetProjection, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return ReleaseAssetProjection{}, err
	}
	var result ReleaseAssetProjection
	fields := []struct {
		name string
		dest any
	}{
		{"id", &result.ID}, {"node_id", &result.NodeID}, {"name", &result.Name},
		{"content_type", &result.ContentType}, {"state", &result.State}, {"size", &result.Size},
		{"created_at", &result.CreatedAt}, {"updated_at", &result.UpdatedAt}, {"browser_download_url", &result.BrowserDownloadURL},
	}
	for _, field := range fields {
		if err := decodeRequired(object, field.name, field.dest); err != nil {
			return ReleaseAssetProjection{}, err
		}
	}
	if err := decodeNullableString(object, "label", &result.Label); err != nil {
		return ReleaseAssetProjection{}, err
	}
	if err := decodeNullableString(object, "digest", &result.Digest); err != nil {
		return ReleaseAssetProjection{}, err
	}
	if result.ID < 1 || result.NodeID == "" || strings.TrimSpace(result.Name) != result.Name || result.Name == "" || result.ContentType == "" || result.State != "uploaded" || result.Size < 0 || result.BrowserDownloadURL == "" {
		return ReleaseAssetProjection{}, errors.New("release asset has invalid immutable identity")
	}
	if result.Digest != nil && !digestPattern.MatchString(*result.Digest) {
		return ReleaseAssetProjection{}, errors.New("release asset has invalid digest")
	}
	created, createdErr := parseCanonicalTime(result.CreatedAt)
	updated, updatedErr := parseCanonicalTime(result.UpdatedAt)
	if createdErr != nil || updatedErr != nil || updated.Before(created) {
		return ReleaseAssetProjection{}, errors.New("release asset has invalid timestamps")
	}
	var uploader json.RawMessage
	if err := decodeRequired(object, "uploader", &uploader); err != nil {
		return ReleaseAssetProjection{}, err
	}
	uploaderObject, err := parseVendorObject(uploader)
	if err != nil {
		return ReleaseAssetProjection{}, err
	}
	if err := decodeRequired(uploaderObject, "id", &result.UploaderID); err != nil || result.UploaderID < 1 {
		return ReleaseAssetProjection{}, errors.New("release asset has invalid uploader id")
	}
	if err := decodeRequired(uploaderObject, "login", &result.UploaderLogin); err != nil || strings.TrimSpace(result.UploaderLogin) == "" {
		return ReleaseAssetProjection{}, errors.New("release asset has invalid uploader login")
	}
	return result, nil
}

func decodeNullableString(object map[string]json.RawMessage, name string, destination **string) error {
	raw, ok := object[name]
	if !ok || len(raw) == 0 {
		return fmt.Errorf("vendor JSON is missing field %q", name)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*destination = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("vendor JSON field %q: %w", name, err)
	}
	*destination = &value
	return nil
}

func InspectContractContentsResponse(data []byte, expectedPath string) (ContractContentInspection, error) {
	object, err := parseVendorObject(data)
	if err != nil {
		return ContractContentInspection{}, err
	}
	var objectType, path, encoding, content string
	var size int
	var result ContractContentInspection
	fields := []struct {
		name string
		dest any
	}{
		{"type", &objectType}, {"path", &path}, {"sha", &result.BlobSHA}, {"size", &size},
		{"encoding", &encoding}, {"content", &content},
	}
	for _, field := range fields {
		if err := decodeRequired(object, field.name, field.dest); err != nil {
			return ContractContentInspection{}, err
		}
	}
	if objectType != "file" || path != expectedPath || !objectSHAPattern(result.BlobSHA) || size < 1 || size > 1<<20 || encoding != "base64" {
		return ContractContentInspection{}, errors.New("contract contents response identity is invalid")
	}
	compact := strings.NewReplacer("\r", "", "\n", "").Replace(content)
	decoded, err := base64.StdEncoding.DecodeString(compact)
	if err != nil || len(decoded) != size || base64.StdEncoding.EncodeToString(decoded) != compact {
		return ContractContentInspection{}, errors.New("contract contents response has non-canonical or size-mismatched base64")
	}
	result.Content = decoded
	return result, nil
}

func ParseStableReleaseAbsenceProof(data []byte, repository, endpoint string) (StableReleaseAbsenceProof, error) {
	var proof StableReleaseAbsenceProof
	if err := strictjson.Decode(data, MaxRawPageBytes, &proof); err != nil {
		return StableReleaseAbsenceProof{}, err
	}
	if proof.SchemaID != "env-vault.github-exact-absence.v1" || proof.SchemaVersion != 1 || proof.Repository != repository || proof.Endpoint != endpoint || proof.ReasonCode != "REMOTE_NOT_FOUND" || proof.TransportExit != 4 {
		return StableReleaseAbsenceProof{}, errors.New("latest stable absence proof is invalid")
	}
	return proof, nil
}

func releasesEqual(left, right ReleaseInspection) bool { return reflect.DeepEqual(left, right) }

func objectSHAPattern(value string) bool {
	return shaPattern.MatchString(value) || (len(value) == 64 && strings.IndexFunc(value, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	}) == -1)
}
