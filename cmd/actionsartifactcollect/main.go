// actionsartifactcollect is the only network-aware part of the Actions
// artifact inventory flow. Every REST read is delegated to the checked release
// read adapter; this command has no HTTP, gh, GraphQL, or mutation client.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/actionsartifact"
)

const collectorTimeout = 20 * time.Minute

type repositoryFlags []string

func (values *repositoryFlags) String() string { return strings.Join(*values, ",") }
func (values *repositoryFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type collector struct {
	adapter    string
	now        func() time.Time
	totalBytes int64
}

type collectionSummary struct {
	Repositories int
	Artifacts    int
	Runs         int
	Attempts     int
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("actionsartifactcollect", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	output := set.String("output", "", "private no-clobber raw collection directory")
	var repositories repositoryFlags
	set.Var(&repositories, "repository", "owner/repository to collect (repeatable)")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *output == "" || len(repositories) == 0 {
		fmt.Fprint(stderr, usage())
		return 2
	}
	repositories = canonicalRepositories(repositories)
	if len(repositories) == 0 || len(repositories) > actionsartifact.MaxRepositories {
		fmt.Fprintf(stderr, "INPUT_INVALID: repositories must contain 1..%d unique entries\n", actionsartifact.MaxRepositories)
		return 2
	}
	for _, repository := range repositories {
		if err := actionsartifact.ValidateRepositoryName(repository); err != nil {
			fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
			return 2
		}
	}
	adapter, err := findCheckedAdapter()
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, collectorTimeout)
	defer cancel()
	summary, err := (&collector{adapter: adapter, now: func() time.Time { return time.Now().UTC() }}).collect(bounded, *output, repositories)
	if err != nil {
		fmt.Fprintf(stderr, "COLLECTION_FAILED: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "collected Actions artifacts: repositories=%d artifacts=%d runs=%d attempts=%d\n", summary.Repositories, summary.Artifacts, summary.Runs, summary.Attempts)
	return 0
}

func (value *collector) collect(ctx context.Context, output string, repositories []string) (collectionSummary, error) {
	root, err := createOutputDirectory(output)
	if err != nil {
		return collectionSummary{}, err
	}
	started := value.now().UTC()
	collection := actionsartifact.RawCollection{
		SchemaID: actionsartifact.CollectionSchemaID, SchemaVersion: actionsartifact.CollectionSchemaVersion,
		ObservedStartedAt: started.Format(time.RFC3339Nano),
	}
	summary := collectionSummary{Repositories: len(repositories)}
	for index, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return collectionSummary{}, errors.New("collection deadline or cancellation reached")
		}
		directory := fmt.Sprintf("repository-%03d", index+1)
		repositoryRoot := filepath.Join(root, directory)
		for _, path := range []string{repositoryRoot, filepath.Join(repositoryRoot, "artifacts"), filepath.Join(repositoryRoot, "runs"), filepath.Join(repositoryRoot, "attempts")} {
			if err := os.Mkdir(path, 0o700); err != nil {
				return collectionSummary{}, fmt.Errorf("create private collection directory: %w", err)
			}
		}
		metadataPath := filepath.Join(repositoryRoot, "repository.json")
		if err := value.read(ctx, metadataPath, "repos/"+repository); err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q metadata read failed", repository)
		}
		metadata, err := value.readSaved(metadataPath)
		if err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q metadata output: %w", repository, err)
		}
		repositoryID, fullName, err := actionsartifact.InspectRepositoryResponse(metadata)
		if err != nil || fullName != repository {
			return collectionSummary{}, fmt.Errorf("repository %q metadata identity mismatch", repository)
		}

		artifactProof, artifactInspections, _, err := value.collectPages(ctx, repositoryRoot, repository, repositoryID, actionsartifact.RawResourceArtifacts)
		if err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q artifact pages: %w", repository, err)
		}
		runProof, _, runInspections, err := value.collectPages(ctx, repositoryRoot, repository, repositoryID, actionsartifact.RawResourceRuns)
		if err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q run pages: %w", repository, err)
		}
		runs := make(map[int64]actionsartifact.RawRunInspection, len(runInspections))
		for _, inspection := range runInspections {
			if inspection.CurrentAttempt < 1 || inspection.CurrentAttempt > actionsartifact.MaxRunAttempts {
				return collectionSummary{}, fmt.Errorf("run %d declares unsupported attempt %d", inspection.RunID, inspection.CurrentAttempt)
			}
			runs[inspection.RunID] = inspection
		}
		producerRuns := make(map[int64]bool)
		for _, inspection := range artifactInspections {
			if inspection.RepositoryID != repositoryID || inspection.HeadRepositoryID < 1 {
				return collectionSummary{}, fmt.Errorf("artifact %d repository identity mismatch", inspection.ArtifactID)
			}
			if _, ok := runs[inspection.ProducerRunID]; !ok {
				return collectionSummary{}, fmt.Errorf("artifact %d producer run %d is absent from complete run pages", inspection.ArtifactID, inspection.ProducerRunID)
			}
			producerRuns[inspection.ProducerRunID] = true
		}
		runIDs := make([]int64, 0, len(producerRuns))
		for runID := range producerRuns {
			runIDs = append(runIDs, runID)
		}
		sort.Slice(runIDs, func(i, j int) bool { return runIDs[i] < runIDs[j] })
		attemptDocuments := make([]actionsartifact.CollectedAttemptDocument, 0)
		for _, runID := range runIDs {
			for attempt := 1; attempt <= runs[runID].CurrentAttempt; attempt++ {
				document := actionsartifact.CollectedAttemptDocument{RunID: runID, RunAttempt: attempt}
				attemptPath := filepath.Join(repositoryRoot, "attempts", fmt.Sprintf("run-%019d-attempt-%02d.json", runID, attempt))
				endpoint := fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d", repository, runID, attempt)
				if err := value.read(ctx, attemptPath, endpoint); err != nil {
					return collectionSummary{}, fmt.Errorf("run %d attempt %d read failed", runID, attempt)
				}
				attemptData, err := value.readSaved(attemptPath)
				if err != nil || actionsartifact.InspectAttemptResponse(attemptData, repository, repositoryID, runID, attempt) != nil {
					return collectionSummary{}, fmt.Errorf("run %d attempt %d response identity mismatch", runID, attempt)
				}
				attemptDocuments = append(attemptDocuments, document)
			}
		}
		collection.Repositories = append(collection.Repositories, actionsartifact.RawCollectionRepository{
			Repository: repository, RepositoryID: repositoryID, Directory: directory,
			Artifacts: artifactProof, Runs: runProof, AttemptDocuments: attemptDocuments,
		})
		summary.Artifacts += artifactProof.TotalCount
		summary.Runs += runProof.TotalCount
		summary.Attempts += len(attemptDocuments)
	}
	// This is deliberately one global final phase. Every repository and every
	// producer-attempt document is already present before any page is trusted as
	// stable. A mutation during later collection therefore invalidates the
	// entire index, including older pages and earlier repositories.
	for index := range collection.Repositories {
		repository := &collection.Repositories[index]
		repositoryRoot := filepath.Join(root, repository.Directory)
		if err := value.finalizePages(ctx, repositoryRoot, repository.Repository, actionsartifact.RawResourceArtifacts, &repository.Artifacts); err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q final artifact reread: %w", repository.Repository, err)
		}
		if err := value.finalizePages(ctx, repositoryRoot, repository.Repository, actionsartifact.RawResourceRuns, &repository.Runs); err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q final run reread: %w", repository.Repository, err)
		}
	}
	finished := value.now().UTC()
	collection.ObservedFinishedAt = finished.Format(time.RFC3339Nano)
	if err := collection.Validate(); err != nil {
		return collectionSummary{}, err
	}
	encoded, err := actionsartifact.MarshalCanonical(collection)
	if err != nil || len(encoded) > actionsartifact.MaxRawPageBytes {
		return collectionSummary{}, errors.New("raw collection index exceeds its encoding bound")
	}
	if value.totalBytes > actionsartifact.MaxRawCollectionBytes-int64(len(encoded)) {
		return collectionSummary{}, errors.New("raw collection exceeds its byte budget")
	}
	if err := actionsartifact.WriteNoClobber(filepath.Join(root, "collection.json"), encoded); err != nil {
		return collectionSummary{}, fmt.Errorf("publish raw collection index: %w", err)
	}
	return summary, nil
}

func (value *collector) collectPages(ctx context.Context, repositoryRoot, repository string, repositoryID int64, resource string) (actionsartifact.PaginationProof, []actionsartifact.RawArtifactInspection, []actionsartifact.RawRunInspection, error) {
	resourceRoot := filepath.Join(repositoryRoot, resource)
	var artifacts []actionsartifact.RawArtifactInspection
	var runs []actionsartifact.RawRunInspection
	seenIDs := make(map[int64]bool)
	proof := actionsartifact.PaginationProof{}
	page := 1
	for {
		path := filepath.Join(resourceRoot, fmt.Sprintf("page-%03d.json", page))
		endpoint := fmt.Sprintf("repos/%s/actions/%s?per_page=%d&page=%d", repository, resource, actionsartifact.MaxItemsPerPage, page)
		if err := value.read(ctx, path, endpoint); err != nil {
			return proof, nil, nil, fmt.Errorf("page %d read failed", page)
		}
		data, err := value.readSaved(path)
		if err != nil {
			return proof, nil, nil, err
		}
		inspection, err := actionsartifact.InspectRawResourcePage(data, resource, repository)
		if err != nil {
			return proof, nil, nil, fmt.Errorf("page %d: %w", page, err)
		}
		if page == 1 {
			proof.TotalCount = inspection.TotalCount
			maximumItems := actionsartifact.MaxPagesPerResource * actionsartifact.MaxItemsPerPage
			if proof.TotalCount > maximumItems {
				return proof, nil, nil, fmt.Errorf("pinned total_count %d exceeds item bound %d", proof.TotalCount, maximumItems)
			}
			wantPages := 1
			if proof.TotalCount > 0 {
				wantPages = (proof.TotalCount-1)/actionsartifact.MaxItemsPerPage + 1
			}
			proof.PageCount = wantPages
		} else if inspection.TotalCount != proof.TotalCount {
			return proof, nil, nil, fmt.Errorf("page %d total_count drifted", page)
		}
		remaining := proof.TotalCount - (page-1)*actionsartifact.MaxItemsPerPage
		wantItems := actionsartifact.MaxItemsPerPage
		if remaining < wantItems {
			wantItems = remaining
		}
		if proof.TotalCount == 0 {
			wantItems = 0
		}
		if inspection.ItemCount != wantItems {
			return proof, nil, nil, fmt.Errorf("page %d has %d items, want %d", page, inspection.ItemCount, wantItems)
		}
		proof.PageItemCounts = append(proof.PageItemCounts, inspection.ItemCount)
		digest := sha256.Sum256(data)
		proof.PageSHA256 = append(proof.PageSHA256, hex.EncodeToString(digest[:]))
		for _, id := range inspection.IDs {
			if id < 1 || seenIDs[id] {
				return proof, nil, nil, fmt.Errorf("page %d contains duplicate or non-positive ID %d", page, id)
			}
			seenIDs[id] = true
		}
		for _, artifact := range inspection.Artifacts {
			if artifact.RepositoryID != repositoryID {
				return proof, nil, nil, fmt.Errorf("artifact %d repository_id drift", artifact.ArtifactID)
			}
		}
		for _, run := range inspection.Runs {
			if run.RepositoryID != repositoryID {
				return proof, nil, nil, fmt.Errorf("run %d repository_id drift", run.RunID)
			}
		}
		artifacts = append(artifacts, inspection.Artifacts...)
		runs = append(runs, inspection.Runs...)
		if page == proof.PageCount {
			break
		}
		page++
	}
	return proof, artifacts, runs, nil
}

func (value *collector) finalizePages(ctx context.Context, repositoryRoot, repository, resource string, proof *actionsartifact.PaginationProof) error {
	resourceRoot := filepath.Join(repositoryRoot, resource)
	proof.FinalPageSHA256 = make([]string, 0, proof.PageCount)
	for page := 1; page <= proof.PageCount; page++ {
		originalPath := filepath.Join(resourceRoot, fmt.Sprintf("page-%03d.json", page))
		original, err := value.readSavedExisting(originalPath)
		if err != nil {
			return fmt.Errorf("page %d original: %w", page, err)
		}
		originalDigest := sha256.Sum256(original)
		if hex.EncodeToString(originalDigest[:]) != proof.PageSHA256[page-1] {
			return fmt.Errorf("page %d original changed on disk", page)
		}
		finalPath := filepath.Join(resourceRoot, fmt.Sprintf("page-%03d-final.json", page))
		endpoint := fmt.Sprintf("repos/%s/actions/%s?per_page=%d&page=%d", repository, resource, actionsartifact.MaxItemsPerPage, page)
		if err := value.read(ctx, finalPath, endpoint); err != nil {
			return fmt.Errorf("page %d checked reread failed", page)
		}
		final, err := value.readSaved(finalPath)
		if err != nil {
			return fmt.Errorf("page %d final output: %w", page, err)
		}
		if !bytes.Equal(original, final) {
			return fmt.Errorf("page %d changed during global final reread", page)
		}
		finalDigest := sha256.Sum256(final)
		proof.FinalPageSHA256 = append(proof.FinalPageSHA256, hex.EncodeToString(finalDigest[:]))
	}
	return validateProof(*proof)
}

func (value *collector) read(ctx context.Context, output, endpoint string) error {
	command := exec.CommandContext(ctx, value.adapter, output, endpoint)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return errors.New("checked read adapter returned a failure")
	}
	return nil
}

func (value *collector) readSaved(filename string) ([]byte, error) {
	return value.readSavedFile(filename, true)
}

func (value *collector) readSavedExisting(filename string) ([]byte, error) {
	return value.readSavedFile(filename, false)
}

func (value *collector) readSavedFile(filename string, account bool) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > actionsartifact.MaxRawPageBytes {
		return nil, errors.New("adapter output is not a bounded regular non-symlink file")
	}
	if account && value.totalBytes > actionsartifact.MaxRawCollectionBytes-info.Size() {
		return nil, errors.New("raw collection exceeds its byte budget")
	}
	data, err := os.ReadFile(filename)
	if err != nil || int64(len(data)) != info.Size() {
		return nil, errors.New("cannot read complete adapter output")
	}
	if account {
		value.totalBytes += int64(len(data))
	}
	return data, nil
}

func validateProof(proof actionsartifact.PaginationProof) error {
	if proof.PageCount < 1 || proof.PageCount > actionsartifact.MaxPagesPerResource || len(proof.PageItemCounts) != proof.PageCount || len(proof.PageSHA256) != proof.PageCount || len(proof.FinalPageSHA256) != proof.PageCount {
		return errors.New("pagination proof is incomplete")
	}
	for index := range proof.PageSHA256 {
		if proof.PageSHA256[index] == "" || proof.PageSHA256[index] != proof.FinalPageSHA256[index] {
			return fmt.Errorf("pagination page %d final digest drift", index+1)
		}
	}
	return nil
}

func createOutputDirectory(output string) (string, error) {
	if output == "" {
		return "", errors.New("output directory is required")
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}
	base := filepath.Base(abs)
	if base == "." || base == ".." || filepath.Clean(base) != base {
		return "", errors.New("output must have a safe basename")
	}
	parent := filepath.Dir(abs)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("output parent must be an existing non-symlink directory")
	}
	if err := os.Mkdir(abs, 0o700); err != nil {
		return "", fmt.Errorf("create no-clobber output directory: %w", err)
	}
	return abs, nil
}

func findCheckedAdapter() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for depth := 0; depth < 10; depth++ {
		candidate := filepath.Join(directory, "scripts", "release", "gh-api-read.sh")
		transport := filepath.Join(directory, "scripts", "release", "releasetransport.sh")
		if adapterInfo, adapterErr := os.Lstat(candidate); adapterErr == nil && adapterInfo.Mode().IsRegular() && adapterInfo.Mode()&os.ModeSymlink == 0 && adapterInfo.Mode()&0o111 != 0 {
			if transportInfo, transportErr := os.Lstat(transport); transportErr == nil && transportInfo.Mode().IsRegular() && transportInfo.Mode()&os.ModeSymlink == 0 {
				return candidate, nil
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return "", errors.New("checked scripts/release/gh-api-read.sh adapter was not found")
}

func canonicalRepositories(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if write == 0 || result[write-1] != value {
			result[write] = value
			write++
		}
	}
	return result[:write]
}

func usage() string {
	return "usage: actionsartifactcollect --output DIR --repository OWNER/REPO [--repository OWNER/REPO ...]\n"
}
