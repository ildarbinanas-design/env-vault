// actionsartifactscopecollect captures the authoritative live fence consumed
// by the offline Actions-artifact scope derivation. It has no HTTP, GraphQL,
// or mutation client: every remote read executes the checked gh-api-read.sh
// adapter, including when RELEASE_TRANSPORT_BIN points at one prebuilt binary.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/actionsartifact"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const collectorTimeout = 20 * time.Minute

type repositoryFlags []string

func (values *repositoryFlags) String() string { return strings.Join(*values, ",") }
func (values *repositoryFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type trackedRead struct {
	relative      string
	endpoint      string
	repository    string
	allowNotFound bool
	present       bool
}

type liveCollector struct {
	adapter    string
	now        func() time.Time
	reads      []trackedRead
	totalBytes int64
}

type collectionSummary struct {
	Repositories int
	PullRequests int
	Runs         int
	Attempts     int
	Stable       bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("actionsartifactscopecollect", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	output := set.String("output", "", "private no-clobber live collection directory")
	snapshotPath := set.String("snapshot", "", "fresh typed Actions artifact snapshot")
	policyPath := set.String("policy", actionsartifact.CanonicalPolicyPath, "checked Actions artifact policy")
	releaseRepository := set.String("release-repository", "", "designated source Release repository")
	var repositories repositoryFlags
	set.Var(&repositories, "repository", "owner/repository to fence (repeatable)")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *output == "" || *snapshotPath == "" || *policyPath == "" || *releaseRepository == "" || len(repositories) == 0 {
		fmt.Fprint(stderr, usage())
		return 2
	}
	repositories = canonicalRepositories(repositories)
	if len(repositories) == 0 || len(repositories) > actionsartifact.MaxRepositories {
		fmt.Fprintf(stderr, "INPUT_INVALID: repositories must contain 1..%d unique entries\n", actionsartifact.MaxRepositories)
		return 2
	}
	if err := actionsartifact.ValidateRepositoryName(*releaseRepository); err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
		return 2
	}
	releaseSeen := false
	for _, repository := range repositories {
		if err := actionsartifact.ValidateRepositoryName(repository); err != nil {
			fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
			return 2
		}
		if repository == *releaseRepository {
			releaseSeen = true
		}
	}
	if !releaseSeen {
		fmt.Fprintln(stderr, "INPUT_INVALID: release repository must be one of the exact repositories")
		return 2
	}
	policy, err := actionsartifact.LoadPolicyFile(*policyPath)
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
		return 2
	}
	now := time.Now().UTC()
	snapshot, err := actionsartifact.LoadSnapshotFile(*snapshotPath, policy, now, actionsartifact.MaxSnapshotAge)
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
		return 2
	}
	if !repositoriesEqualSnapshot(repositories, snapshot) {
		fmt.Fprintln(stderr, "INPUT_INVALID: repositories must exactly equal the snapshot repository set")
		return 2
	}
	adapter, err := findCheckedAdapter()
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, collectorTimeout)
	defer cancel()
	collector := &liveCollector{adapter: adapter, now: func() time.Time { return time.Now().UTC() }}
	summary, err := collector.collect(bounded, *output, repositories, *releaseRepository, snapshot, policy)
	if err != nil {
		fmt.Fprintf(stderr, "COLLECTION_FAILED: %v\n", err)
		return 1
	}
	state := "absent"
	if summary.Stable {
		state = "present"
	}
	fmt.Fprintf(stdout, "collected Actions artifact live scope: repositories=%d pull_requests=%d runs=%d attempts=%d stable_release=%s\n", summary.Repositories, summary.PullRequests, summary.Runs, summary.Attempts, state)
	return 0
}

func (collector *liveCollector) collect(ctx context.Context, output string, repositories []string, releaseRepository string, snapshot actionsartifact.Snapshot, policy actionsartifact.Policy) (collectionSummary, error) {
	root, err := createOutputDirectory(output)
	if err != nil {
		return collectionSummary{}, err
	}
	started := collector.now().UTC()
	snapshotDigest, err := actionsartifact.SnapshotSemanticSHA256(snapshot, policy, started, actionsartifact.MaxSnapshotAge)
	if err != nil {
		return collectionSummary{}, err
	}
	collection := actionsartifact.LiveRawCollection{
		SchemaID: actionsartifact.LiveCollectionSchemaID, SchemaVersion: actionsartifact.LiveCollectionSchemaVersion,
		ObservedStartedAt: started.Format(time.RFC3339Nano), SnapshotSemanticSHA256: snapshotDigest,
		ReleaseRepository: releaseRepository,
	}
	summary := collectionSummary{Repositories: len(repositories)}
	snapshotRepositoryByName := make(map[string]actionsartifact.SnapshotRepository, len(snapshot.Repositories))
	for _, repository := range snapshot.Repositories {
		snapshotRepositoryByName[repository.Repository] = repository
	}
	snapshotRuns := make(map[string]actionsartifact.SnapshotRun, len(snapshot.Runs))
	for _, run := range snapshot.Runs {
		snapshotRuns[fmt.Sprintf("%s\x00%d", run.Repository, run.RunID)] = run
	}

	for index, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return collectionSummary{}, errors.New("collection deadline or cancellation reached")
		}
		directory := fmt.Sprintf("repository-%03d", index+1)
		for _, relative := range []string{
			directory,
			directory + "/pull-requests", directory + "/pull-requests/pages", directory + "/pull-requests/exact",
			directory + "/runs", directory + "/attempts",
		} {
			if err := os.Mkdir(filepath.Join(root, filepath.FromSlash(relative)), 0o700); err != nil {
				return collectionSummary{}, fmt.Errorf("create private live collection directory: %w", err)
			}
		}
		metadataData, _, err := collector.readInitial(ctx, root, directory+"/repository.json", "repos/"+repository, repository, false)
		if err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q metadata read failed", repository)
		}
		metadata, err := actionsartifact.InspectLiveRepositoryResponse(metadataData)
		snapshotRepository := snapshotRepositoryByName[repository]
		if err != nil || metadata.FullName != repository || metadata.RepositoryID != snapshotRepository.RepositoryID {
			return collectionSummary{}, fmt.Errorf("repository %q metadata identity mismatch", repository)
		}
		branchPath := url.PathEscape(metadata.DefaultBranch)
		refData, _, err := collector.readInitial(ctx, root, directory+"/default-ref.json", "repos/"+repository+"/git/ref/heads/"+branchPath, repository, false)
		if err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q default ref read failed", repository)
		}
		ref, err := actionsartifact.InspectGitRefResponse(refData)
		if err != nil || ref.Ref != "refs/heads/"+metadata.DefaultBranch || ref.ObjectType != "commit" {
			return collectionSummary{}, fmt.Errorf("repository %q default ref identity mismatch", repository)
		}
		branchData, _, err := collector.readInitial(ctx, root, directory+"/default-branch.json", "repos/"+repository+"/branches/"+branchPath, repository, false)
		if err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q default branch read failed", repository)
		}
		branch, err := actionsartifact.InspectBranchResponse(branchData)
		if err != nil || branch.Name != metadata.DefaultBranch || branch.CommitSHA != ref.ObjectSHA || !branch.Protected {
			return collectionSummary{}, fmt.Errorf("repository %q default ref/branch mismatch or branch is unprotected", repository)
		}

		pullProof, pullRequests, err := collector.collectPullRequests(ctx, root, directory, repository, metadata.RepositoryID)
		if err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q open pull requests: %w", repository, err)
		}
		numbers := make([]int64, 0, len(pullRequests))
		for number := range pullRequests {
			numbers = append(numbers, number)
		}
		sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
		for _, number := range numbers {
			if err := ctx.Err(); err != nil {
				return collectionSummary{}, errors.New("collection deadline or cancellation reached")
			}
			relative := fmt.Sprintf("%s/pull-requests/exact/pr-%019d.json", directory, number)
			endpoint := fmt.Sprintf("repos/%s/pulls/%d", repository, number)
			data, _, err := collector.readInitial(ctx, root, relative, endpoint, repository, false)
			if err != nil {
				return collectionSummary{}, fmt.Errorf("exact pull request %d read failed", number)
			}
			exact, err := actionsartifact.InspectExactPullRequest(data, repository, metadata.RepositoryID, number)
			if err != nil || exact != pullRequests[number] {
				return collectionSummary{}, fmt.Errorf("exact pull request %d differs from list projection", number)
			}
		}

		runProof, runInspections, err := collector.collectRuns(ctx, root, directory, repository, metadata.RepositoryID)
		if err != nil {
			return collectionSummary{}, fmt.Errorf("repository %q complete workflow runs: %w", repository, err)
		}
		if runProof.TotalCount != snapshotRepository.Runs.TotalCount {
			return collectionSummary{}, fmt.Errorf("repository %q complete live run count differs from the snapshot", repository)
		}
		liveRuns := make(map[int64]actionsartifact.RawRunInspection, len(runInspections))
		for _, run := range runInspections {
			liveRuns[run.RunID] = run
		}
		attemptDocuments := make([]actionsartifact.CollectedAttemptDocument, 0)
		for _, attempt := range snapshot.Attempts {
			if attempt.Repository != repository {
				continue
			}
			if err := ctx.Err(); err != nil {
				return collectionSummary{}, errors.New("collection deadline or cancellation reached")
			}
			liveRun, exists := liveRuns[attempt.RunID]
			snapshotRun := snapshotRuns[fmt.Sprintf("%s\x00%d", repository, attempt.RunID)]
			if !exists || liveRun.CurrentAttempt != snapshotRun.CurrentAttempt {
				return collectionSummary{}, fmt.Errorf("snapshot producer run %d changed or disappeared", attempt.RunID)
			}
			document := actionsartifact.CollectedAttemptDocument{RunID: attempt.RunID, RunAttempt: attempt.RunAttempt}
			relative := fmt.Sprintf("%s/attempts/run-%019d-attempt-%02d.json", directory, attempt.RunID, attempt.RunAttempt)
			endpoint := fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d", repository, attempt.RunID, attempt.RunAttempt)
			data, _, err := collector.readInitial(ctx, root, relative, endpoint, repository, false)
			if err != nil || actionsartifact.InspectAttemptResponse(data, repository, metadata.RepositoryID, attempt.RunID, attempt.RunAttempt) != nil {
				return collectionSummary{}, fmt.Errorf("run %d attempt %d response identity mismatch", attempt.RunID, attempt.RunAttempt)
			}
			attemptDocuments = append(attemptDocuments, document)
		}

		stableCollection := actionsartifact.RawStableReleaseCollection{}
		if repository == releaseRepository {
			if err := os.Mkdir(filepath.Join(root, directory, "release"), 0o700); err != nil {
				return collectionSummary{}, err
			}
			contractEndpoint := fmt.Sprintf("repos/%s/contents/%s?ref=%s", repository, releasecontract.CanonicalPath, url.QueryEscape(ref.ObjectSHA))
			contractData, _, err := collector.readInitial(ctx, root, directory+"/contract-content.json", contractEndpoint, repository, false)
			if err != nil {
				return collectionSummary{}, errors.New("protected-main operational contract read failed")
			}
			if _, err := actionsartifact.InspectContractContentsResponse(contractData, releasecontract.CanonicalPath); err != nil {
				return collectionSummary{}, fmt.Errorf("protected-main operational contract response: %w", err)
			}
			stableCollection, summary.Stable, err = collector.collectStableRelease(ctx, root, directory, repository)
			if err != nil {
				return collectionSummary{}, err
			}
		}
		collection.Repositories = append(collection.Repositories, actionsartifact.LiveRawCollectionRepository{
			Repository: repository, RepositoryID: metadata.RepositoryID, Directory: directory, DefaultBranch: metadata.DefaultBranch,
			PullRequests: pullProof, Runs: runProof, PullRequestNumbers: numbers, AttemptDocuments: attemptDocuments,
			StableRelease: stableCollection,
		})
		summary.PullRequests += len(numbers)
		summary.Runs += runProof.TotalCount
		summary.Attempts += len(attemptDocuments)
	}

	// Global final reread: no repository, page, exact PR, attempt, contract,
	// Release, ref, or tag projection is considered stable until every initial
	// read in every repository is already present.
	if err := collector.finalizeAll(ctx, root); err != nil {
		return collectionSummary{}, err
	}
	for index := range collection.Repositories {
		collection.Repositories[index].PullRequests.FinalPageSHA256 = append([]string(nil), collection.Repositories[index].PullRequests.PageSHA256...)
		collection.Repositories[index].Runs.FinalPageSHA256 = append([]string(nil), collection.Repositories[index].Runs.PageSHA256...)
	}
	collection.ObservedFinishedAt = collector.now().UTC().Format(time.RFC3339Nano)
	files, err := collector.fileProofs(root)
	if err != nil {
		return collectionSummary{}, err
	}
	collection.Files = files
	if err := collection.Validate(); err != nil {
		return collectionSummary{}, err
	}
	encoded, err := actionsartifact.MarshalCanonical(collection)
	if err != nil || len(encoded) > actionsartifact.MaxRawPageBytes {
		return collectionSummary{}, errors.New("live collection index exceeds its encoding bound")
	}
	if err := actionsartifact.ValidateLiveCollectionByteBudget(collector.totalBytes, int64(len(encoded))); err != nil {
		return collectionSummary{}, err
	}
	if err := ctx.Err(); err != nil {
		return collectionSummary{}, errors.New("collection deadline or cancellation reached")
	}
	if err := actionsartifact.WriteNoClobber(filepath.Join(root, "collection.json"), encoded); err != nil {
		return collectionSummary{}, fmt.Errorf("publish live collection index: %w", err)
	}
	return summary, nil
}

func (collector *liveCollector) collectPullRequests(ctx context.Context, root, directory, repository string, repositoryID int64) (actionsartifact.ArrayPaginationProof, map[int64]actionsartifact.LivePullRequest, error) {
	proof := actionsartifact.ArrayPaginationProof{}
	values := make(map[int64]actionsartifact.LivePullRequest)
	for page := 1; page <= actionsartifact.MaxPagesPerResource; page++ {
		relative := fmt.Sprintf("%s/pull-requests/pages/page-%03d.json", directory, page)
		endpoint := fmt.Sprintf("repos/%s/pulls?state=open&per_page=%d&page=%d", repository, actionsartifact.MaxItemsPerPage, page)
		data, _, err := collector.readInitial(ctx, root, relative, endpoint, repository, false)
		if err != nil {
			return proof, nil, fmt.Errorf("page %d read failed", page)
		}
		pageValues, err := actionsartifact.InspectPullRequestPage(data, repository, repositoryID)
		if err != nil {
			return proof, nil, fmt.Errorf("page %d: %w", page, err)
		}
		proof.PageCount++
		proof.PageItemCounts = append(proof.PageItemCounts, len(pageValues))
		proof.PageSHA256 = append(proof.PageSHA256, digestBytes(data))
		proof.ItemCount += len(pageValues)
		for _, pullRequest := range pageValues {
			if _, exists := values[pullRequest.Number]; exists {
				return proof, nil, fmt.Errorf("page %d duplicates pull request %d", page, pullRequest.Number)
			}
			values[pullRequest.Number] = pullRequest
		}
		if len(pageValues) < actionsartifact.MaxItemsPerPage {
			return proof, values, nil
		}
	}
	return proof, nil, errors.New("open pull-request pagination has no bounded short terminal page")
}

func (collector *liveCollector) collectRuns(ctx context.Context, root, directory, repository string, repositoryID int64) (actionsartifact.PaginationProof, []actionsartifact.RawRunInspection, error) {
	proof := actionsartifact.PaginationProof{}
	var runs []actionsartifact.RawRunInspection
	seen := make(map[int64]bool)
	for page := 1; ; page++ {
		relative := fmt.Sprintf("%s/runs/page-%03d.json", directory, page)
		endpoint := fmt.Sprintf("repos/%s/actions/runs?per_page=%d&page=%d", repository, actionsartifact.MaxItemsPerPage, page)
		data, _, err := collector.readInitial(ctx, root, relative, endpoint, repository, false)
		if err != nil {
			return proof, nil, fmt.Errorf("page %d read failed", page)
		}
		inspection, err := actionsartifact.InspectRawResourcePage(data, actionsartifact.RawResourceRuns, repository)
		if err != nil {
			return proof, nil, fmt.Errorf("page %d: %w", page, err)
		}
		if page == 1 {
			proof.TotalCount = inspection.TotalCount
			maximum := actionsartifact.MaxPagesPerResource * actionsartifact.MaxItemsPerPage
			if proof.TotalCount > maximum {
				return proof, nil, fmt.Errorf("pinned total_count %d exceeds item bound %d", proof.TotalCount, maximum)
			}
			proof.PageCount = 1
			if proof.TotalCount > 0 {
				proof.PageCount = (proof.TotalCount-1)/actionsartifact.MaxItemsPerPage + 1
			}
		} else if inspection.TotalCount != proof.TotalCount {
			return proof, nil, fmt.Errorf("page %d total_count drifted", page)
		}
		remaining := proof.TotalCount - (page-1)*actionsartifact.MaxItemsPerPage
		want := actionsartifact.MaxItemsPerPage
		if remaining < want {
			want = remaining
		}
		if proof.TotalCount == 0 {
			want = 0
		}
		if inspection.ItemCount != want {
			return proof, nil, fmt.Errorf("page %d has %d runs, want %d", page, inspection.ItemCount, want)
		}
		proof.PageItemCounts = append(proof.PageItemCounts, inspection.ItemCount)
		proof.PageSHA256 = append(proof.PageSHA256, digestBytes(data))
		for _, run := range inspection.Runs {
			if run.RepositoryID != repositoryID || seen[run.RunID] {
				return proof, nil, fmt.Errorf("page %d has duplicate or repository-mismatched run %d", page, run.RunID)
			}
			seen[run.RunID] = true
			runs = append(runs, run)
		}
		if page == proof.PageCount {
			break
		}
	}
	return proof, runs, nil
}

func (collector *liveCollector) collectStableRelease(ctx context.Context, root, directory, repository string) (actionsartifact.RawStableReleaseCollection, bool, error) {
	latestEndpoint := "repos/" + repository + "/releases/latest"
	data, present, err := collector.readInitial(ctx, root, directory+"/release/latest.json", latestEndpoint, repository, true)
	if err != nil {
		return actionsartifact.RawStableReleaseCollection{}, false, errors.New("latest stable Release read failed")
	}
	if !present {
		return actionsartifact.RawStableReleaseCollection{Designated: true, State: actionsartifact.StableReleaseAbsent}, false, nil
	}
	release, err := actionsartifact.InspectReleaseResponse(data)
	if err != nil || release.Draft || release.Prerelease {
		return actionsartifact.RawStableReleaseCollection{}, false, errors.New("latest stable Release is malformed, draft, or prerelease")
	}
	versionPath := url.PathEscape(release.Version)
	exactData, _, err := collector.readInitial(ctx, root, directory+"/release/exact.json", "repos/"+repository+"/releases/tags/"+versionPath, repository, false)
	if err != nil {
		return actionsartifact.RawStableReleaseCollection{}, false, errors.New("exact stable Release read failed")
	}
	exact, err := actionsartifact.InspectReleaseResponse(exactData)
	if err != nil || exact.ReleaseID != release.ReleaseID || exact.Version != release.Version {
		return actionsartifact.RawStableReleaseCollection{}, false, errors.New("exact stable Release identity mismatch")
	}
	tagRefData, _, err := collector.readInitial(ctx, root, directory+"/release/tag-ref.json", "repos/"+repository+"/git/ref/tags/"+versionPath, repository, false)
	if err != nil {
		return actionsartifact.RawStableReleaseCollection{}, false, errors.New("stable tag ref read failed")
	}
	object, err := actionsartifact.InspectGitRefResponse(tagRefData)
	if err != nil || object.Ref != "refs/tags/"+release.Version {
		return actionsartifact.RawStableReleaseCollection{}, false, errors.New("stable tag ref identity mismatch")
	}
	seen := make(map[string]bool)
	depth := 0
	for object.ObjectType == "tag" {
		if err := ctx.Err(); err != nil {
			return actionsartifact.RawStableReleaseCollection{}, false, errors.New("collection deadline or cancellation reached")
		}
		depth++
		if depth > actionsartifact.MaxTagPeelDepth || seen[object.ObjectSHA] {
			return actionsartifact.RawStableReleaseCollection{}, false, errors.New("stable annotated-tag chain is cyclic or too deep")
		}
		seen[object.ObjectSHA] = true
		requestedTagSHA := object.ObjectSHA
		relative := fmt.Sprintf("%s/release/tag-object-%02d.json", directory, depth)
		endpoint := fmt.Sprintf("repos/%s/git/tags/%s", repository, requestedTagSHA)
		data, _, err := collector.readInitial(ctx, root, relative, endpoint, repository, false)
		if err != nil {
			return actionsartifact.RawStableReleaseCollection{}, false, fmt.Errorf("stable tag object %d read failed", depth)
		}
		object, err = actionsartifact.InspectTagObjectResponse(data, requestedTagSHA)
		if err != nil {
			return actionsartifact.RawStableReleaseCollection{}, false, fmt.Errorf("stable tag object %d is malformed", depth)
		}
	}
	if object.ObjectType != "commit" {
		return actionsartifact.RawStableReleaseCollection{}, false, errors.New("stable tag does not peel to a commit")
	}
	return actionsartifact.RawStableReleaseCollection{Designated: true, State: actionsartifact.StableReleasePresent, Version: release.Version, TagObjectDocuments: depth}, true, nil
}

func (collector *liveCollector) readInitial(ctx context.Context, root, relative, endpoint, repository string, allowNotFound bool) ([]byte, bool, error) {
	filename := filepath.Join(root, filepath.FromSlash(relative))
	present, err := collector.checkedRead(ctx, filename, endpoint, repository, allowNotFound)
	if err != nil {
		return nil, false, err
	}
	data, err := readBoundedSavedWithAccount(filename, collector.accountBytes)
	if err != nil {
		return nil, false, err
	}
	collector.reads = append(collector.reads, trackedRead{relative: relative, endpoint: endpoint, repository: repository, allowNotFound: allowNotFound, present: present})
	return data, present, nil
}

func (collector *liveCollector) checkedRead(ctx context.Context, filename, endpoint, repository string, allowNotFound bool) (bool, error) {
	command := exec.CommandContext(ctx, collector.adapter, filename, endpoint)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	err := command.Run()
	if err == nil {
		return true, nil
	}
	var exitError *exec.ExitError
	if allowNotFound && errors.As(err, &exitError) && exitError.ExitCode() == 4 {
		proof := actionsartifact.StableReleaseAbsenceProof{
			SchemaID: "env-vault.github-exact-absence.v1", SchemaVersion: 1,
			Repository: repository, Endpoint: endpoint, ReasonCode: "REMOTE_NOT_FOUND", TransportExit: 4,
		}
		data, encodeErr := actionsartifact.MarshalCanonical(proof)
		if encodeErr != nil {
			return false, encodeErr
		}
		if writeErr := actionsartifact.WriteNoClobber(filename, data); writeErr != nil {
			return false, fmt.Errorf("write typed exact-absence proof: %w", writeErr)
		}
		return false, nil
	}
	return false, errors.New("checked read adapter returned a failure")
}

func (collector *liveCollector) finalizeAll(ctx context.Context, root string) error {
	initialReads := append([]trackedRead(nil), collector.reads...)
	for _, initial := range initialReads {
		if err := ctx.Err(); err != nil {
			return errors.New("collection deadline or cancellation reached")
		}
		finalRelative := finalName(initial.relative)
		filename := filepath.Join(root, filepath.FromSlash(finalRelative))
		present, err := collector.checkedRead(ctx, filename, initial.endpoint, initial.repository, initial.allowNotFound)
		if err != nil {
			return fmt.Errorf("global final reread %q failed", initial.endpoint)
		}
		if present != initial.present {
			return fmt.Errorf("global final reread %q changed presence state", initial.endpoint)
		}
		original, err := readBoundedSaved(filepath.Join(root, filepath.FromSlash(initial.relative)))
		if err != nil {
			return err
		}
		final, err := readBoundedSavedWithAccount(filename, collector.accountBytes)
		if err != nil {
			return err
		}
		if !bytes.Equal(original, final) {
			return fmt.Errorf("global final reread %q changed bytes", initial.endpoint)
		}
		collector.reads = append(collector.reads, trackedRead{relative: finalRelative, endpoint: initial.endpoint, repository: initial.repository, allowNotFound: initial.allowNotFound, present: present})
	}
	return nil
}

func (collector *liveCollector) fileProofs(root string) ([]actionsartifact.RawFileProof, error) {
	proofs := make([]actionsartifact.RawFileProof, 0, len(collector.reads))
	seen := make(map[string]bool, len(collector.reads))
	var total int64
	for _, read := range collector.reads {
		if seen[read.relative] {
			return nil, fmt.Errorf("duplicate tracked raw path %q", read.relative)
		}
		seen[read.relative] = true
		data, err := readBoundedSaved(filepath.Join(root, filepath.FromSlash(read.relative)))
		if err != nil {
			return nil, err
		}
		if total > actionsartifact.MaxLiveCollectionBytes-int64(len(data)) {
			return nil, errors.New("live collection exceeds its byte budget")
		}
		total += int64(len(data))
		proofs = append(proofs, actionsartifact.RawFileProof{Path: read.relative, Bytes: int64(len(data)), SHA256: digestBytes(data)})
	}
	if total != collector.totalBytes {
		return nil, errors.New("tracked live collection byte accounting mismatch")
	}
	sort.Slice(proofs, func(i, j int) bool { return proofs[i].Path < proofs[j].Path })
	return proofs, nil
}

func (collector *liveCollector) accountBytes(size int64) error {
	if size < 1 || size > actionsartifact.MaxRawPageBytes || collector.totalBytes > actionsartifact.MaxLiveCollectionBytes-size {
		return errors.New("live collection exceeds its byte budget")
	}
	collector.totalBytes += size
	return nil
}

func readBoundedSaved(filename string) ([]byte, error) {
	return readBoundedSavedWithAccount(filename, nil)
}

func readBoundedSavedWithAccount(filename string, account func(int64) error) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > actionsartifact.MaxRawPageBytes {
		return nil, errors.New("checked adapter output is not a bounded regular non-symlink file")
	}
	if account != nil {
		if err := account(info.Size()); err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(filename)
	if err != nil || int64(len(data)) != info.Size() {
		return nil, errors.New("cannot read complete checked adapter output")
	}
	return data, nil
}

func finalName(relative string) string {
	return strings.TrimSuffix(relative, ".json") + "-final.json"
}

func digestBytes(data []byte) string {
	digest := fmt.Sprintf("%x", sha256Sum(data))
	return digest
}

func sha256Sum(data []byte) [32]byte {
	// Kept as a tiny seam so fake-transport tests can validate exact file
	// accounting without introducing another encoder or shell dependency.
	return sha256.Sum256(data)
}

func createOutputDirectory(output string) (string, error) {
	abs, err := filepath.Abs(output)
	if err != nil || output == "" {
		return "", errors.New("output directory is required")
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

func repositoriesEqualSnapshot(repositories []string, snapshot actionsartifact.Snapshot) bool {
	if len(repositories) != len(snapshot.Repositories) {
		return false
	}
	for index := range repositories {
		if repositories[index] != snapshot.Repositories[index].Repository {
			return false
		}
	}
	return true
}

func usage() string {
	return "usage: actionsartifactscopecollect --output DIR --snapshot FILE --release-repository OWNER/REPO --repository OWNER/REPO [--repository OWNER/REPO ...] [--policy FILE]\n"
}
