// actionsartifactdelete is a dormant, explicitly authorized bounded executor.
// It is not invoked by workflows or normal validation. All GitHub I/O stays
// behind the checked read adapter and reviewed one-shot mutation transport.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/actionsartifact"
	"github.com/ildarbinanas-design/env-vault/internal/githubtransport"
	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

const executorTimeout = 45 * time.Minute

type resultFlags []string

func (values *resultFlags) String() string { return strings.Join(*values, ",") }
func (values *resultFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runWithClock(ctx, args, stdout, stderr, func() time.Time { return time.Now().UTC() })
}

func runWithClock(ctx context.Context, args []string, stdout, stderr io.Writer, wallNow func() time.Time) int {
	set := flag.NewFlagSet("actionsartifactdelete", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	policyPath := set.String("policy", actionsartifact.CanonicalPolicyPath, "checked Actions artifact policy")
	authorizedManifestPath := set.String("authorized-manifest", "", "canonical Stage-5 authorized manifest")
	authorizedSHA := set.String("authorized-manifest-sha256", "", "exact authorized manifest semantic SHA-256")
	deleteCount := set.Int("delete-count", -1, "exact authorized delete count")
	deleteBytes := set.Int64("delete-bytes", -1, "exact authorized delete bytes")
	confirmation := set.String("confirmation", "", "byte-exact destructive confirmation line")
	batchPath := set.String("batch", "", "canonical explicit artifact-ID batch")
	currentSnapshotPath := set.String("current-snapshot", "", "newly collected current snapshot")
	currentLiveCollection := set.String("current-live-collection", "", "newly collected raw live fence")
	currentScopePath := set.String("current-scope", "", "current scope derived from the raw live fence")
	maxAgeValue := set.String("max-age", "", "positive current-proof age no greater than one hour")
	resultPath := set.String("result", "", "no-clobber synced JSONL result")
	var priorPaths resultFlags
	set.Var(&priorPaths, "prior-result", "prior canonical JSONL result in chronological order (repeatable)")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *policyPath == "" || *authorizedManifestPath == "" ||
		*authorizedSHA == "" || *deleteCount < 0 || *deleteBytes < 0 || *confirmation == "" || *batchPath == "" ||
		*currentSnapshotPath == "" || *currentLiveCollection == "" || *currentScopePath == "" ||
		*maxAgeValue == "" || *resultPath == "" {
		fmt.Fprintln(stderr, usage())
		return 2
	}
	maxAge, err := parseMaxAge(*maxAgeValue)
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
		return 2
	}
	validatedAt := wallNow().UTC()
	policy, err := actionsartifact.LoadPolicyFile(*policyPath)
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
		return 2
	}
	snapshot, err := actionsartifact.LoadSnapshotFile(*currentSnapshotPath, policy, validatedAt, maxAge)
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
		return 2
	}
	scope, err := actionsartifact.LoadDecisionScopeFile(*currentScopePath, snapshot, validatedAt, maxAge)
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
		return 2
	}
	currentManifest, err := actionsartifact.ReplayCurrentDeletionManifest(*currentLiveCollection, snapshot, scope, policy, validatedAt, maxAge)
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: current replay: %v\n", err)
		return 2
	}
	proofExpiresAt, err := actionsartifact.DeletionProofExpiresAt(snapshot, scope, maxAge)
	if err != nil || !validatedAt.Before(proofExpiresAt) {
		fmt.Fprintln(stderr, "INPUT_INVALID: current proof has no remaining wall-clock freshness")
		return 2
	}
	authorizedManifest, err := actionsartifact.LoadAuthorizedDecisionManifestFile(*authorizedManifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: authorized manifest: %v\n", err)
		return 2
	}
	batch, err := actionsartifact.LoadDeletionBatchFile(*batchPath)
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: deletion batch: %v\n", err)
		return 2
	}
	priorResults := make([]actionsartifact.PriorDeletionResult, 0, len(priorPaths))
	for index, path := range priorPaths {
		result, err := actionsartifact.LoadPriorDeletionResultFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "INPUT_INVALID: prior result %d: %v\n", index+1, err)
			return 2
		}
		priorResults = append(priorResults, result)
	}
	plan, err := actionsartifact.PrepareDeletionPlan(actionsartifact.DeletionPreparation{
		AuthorizedManifest: authorizedManifest, AuthorizedManifestSHA256: *authorizedSHA,
		AuthorizedDeleteCount: *deleteCount, AuthorizedDeleteBytes: *deleteBytes, Confirmation: *confirmation,
		Batch: batch, CurrentManifest: currentManifest, PriorResults: priorResults,
		ValidatedAt: validatedAt, ProofExpiresAt: proofExpiresAt,
	})
	if err != nil {
		fmt.Fprintf(stderr, "AUTHORIZATION_REJECTED: %v\n", err)
		return 2
	}
	readAdapter, mutationAdapter, err := findCheckedAdapters()
	if err != nil {
		fmt.Fprintf(stderr, "INPUT_INVALID: %v\n", err)
		return 2
	}
	temporary, err := os.MkdirTemp("", "env-vault-actions-artifact-delete-")
	if err != nil {
		fmt.Fprintln(stderr, "OUTPUT_FAILED: cannot create private adapter workspace")
		return 1
	}
	defer os.RemoveAll(temporary)
	deadline := validatedAt.Add(executorTimeout)
	if proofExpiresAt.Before(deadline) {
		deadline = proofExpiresAt
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	adapter := &checkedArtifactAdapter{readAdapter: readAdapter, mutationAdapter: mutationAdapter, temporary: temporary}
	result, err := actionsartifact.ExecuteDeletionBatch(bounded, plan, adapter, adapter, *resultPath, wallNow)
	if err != nil {
		fmt.Fprintf(stderr, "DELETE_STOPPED: status=%s attempts=%d reason=%v\n", result.Status, result.AttemptCount, err)
		return 1
	}
	fmt.Fprintf(stdout, "completed bounded Actions artifact deletion: attempts=%d result=%s\n", result.AttemptCount, *resultPath)
	return 0
}

func usage() string {
	return "usage: actionsartifactdelete --authorized-manifest PATH --authorized-manifest-sha256 SHA256 --delete-count N --delete-bytes N --confirmation LINE --batch PATH --current-snapshot PATH --current-live-collection DIR --current-scope PATH --max-age DURATION [--prior-result PATH ...] --result PATH"
}

func parseMaxAge(maxAgeValue string) (time.Duration, error) {
	maxAge, err := time.ParseDuration(maxAgeValue)
	if err != nil || maxAge <= 0 || maxAge > actionsartifact.MaxSnapshotAge {
		return 0, fmt.Errorf("max-age must be positive and no greater than %s", actionsartifact.MaxSnapshotAge)
	}
	return maxAge, nil
}

type checkedArtifactAdapter struct {
	readAdapter     string
	mutationAdapter string
	temporary       string
	sequence        int
}

func (adapter *checkedArtifactAdapter) nextPath(prefix string, artifactID int64) string {
	adapter.sequence++
	return filepath.Join(adapter.temporary, fmt.Sprintf("%s-%04d-%019d.json", prefix, adapter.sequence, artifactID))
}

func (adapter *checkedArtifactAdapter) ReadArtifact(ctx context.Context, record actionsartifact.DecisionRecord) actionsartifact.CheckedArtifactRead {
	output := adapter.nextPath("read", record.ArtifactID)
	endpoint := fmt.Sprintf("repos/%s/actions/artifacts/%d", record.Repository, record.ArtifactID)
	command := exec.CommandContext(ctx, adapter.readAdapter, output, endpoint)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	err := command.Run()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == githubtransport.ExitNotFound {
			return actionsartifact.CheckedArtifactRead{Outcome: actionsartifact.ReadAbsent}
		}
		return actionsartifact.CheckedArtifactRead{Outcome: actionsartifact.ReadUnknown, ErrorCode: "CHECKED_READ_FAILED"}
	}
	data, err := readBoundedAdapterOutput(output, 1<<20)
	if err != nil {
		return actionsartifact.CheckedArtifactRead{Outcome: actionsartifact.ReadUnknown, ErrorCode: "CHECKED_READ_OUTPUT_INVALID"}
	}
	if err := actionsartifact.InspectDeletionArtifactResponse(data, record); err != nil {
		return actionsartifact.CheckedArtifactRead{Outcome: actionsartifact.ReadPresent, Exact: false}
	}
	return actionsartifact.CheckedArtifactRead{Outcome: actionsartifact.ReadPresent, Exact: true}
}

func (adapter *checkedArtifactAdapter) DeleteArtifact(ctx context.Context, record actionsartifact.DecisionRecord) actionsartifact.ArtifactMutation {
	output := adapter.nextPath("mutation", record.ArtifactID)
	endpoint := fmt.Sprintf("repos/%s/actions/artifacts/%d", record.Repository, record.ArtifactID)
	command := exec.CommandContext(ctx, adapter.mutationAdapter, "rest", "mutate-once", "--output", output,
		"--method", "DELETE", "--endpoint", endpoint, "--expected-status", "204")
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil {
		return actionsartifact.ArtifactMutation{Outcome: actionsartifact.MutationAmbiguous, ErrorCode: "MUTATION_TRANSPORT_FAILED"}
	}
	data, err := readBoundedAdapterOutput(output, 64<<10)
	if err != nil {
		return actionsartifact.ArtifactMutation{Outcome: actionsartifact.MutationAmbiguous, ErrorCode: "MUTATION_OUTCOME_INVALID"}
	}
	var document githubtransport.MutationDocument
	if err := strictjson.Decode(data, 64<<10, &document); err != nil || document.SchemaID != githubtransport.MutationSchemaID ||
		document.SchemaVersion != 1 || document.Method != "DELETE" || document.Endpoint != endpoint || len(document.Body) != 0 || document.BodySHA256 != "" {
		return actionsartifact.ArtifactMutation{Outcome: actionsartifact.MutationAmbiguous, ErrorCode: "MUTATION_OUTCOME_INVALID"}
	}
	switch document.Outcome {
	case actionsartifact.MutationSuccess:
		if !document.OK || document.HTTPStatus != 204 || document.ErrorCode != "" {
			return actionsartifact.ArtifactMutation{Outcome: actionsartifact.MutationAmbiguous, HTTPStatus: document.HTTPStatus, ErrorCode: "MUTATION_OUTCOME_INVALID"}
		}
	case actionsartifact.MutationHTTPError, actionsartifact.MutationAmbiguous:
		if document.OK || document.ErrorCode == "" {
			return actionsartifact.ArtifactMutation{Outcome: actionsartifact.MutationAmbiguous, HTTPStatus: document.HTTPStatus, ErrorCode: "MUTATION_OUTCOME_INVALID"}
		}
	default:
		return actionsartifact.ArtifactMutation{Outcome: actionsartifact.MutationAmbiguous, HTTPStatus: document.HTTPStatus, ErrorCode: "MUTATION_OUTCOME_INVALID"}
	}
	return actionsartifact.ArtifactMutation{Outcome: document.Outcome, HTTPStatus: document.HTTPStatus, ErrorCode: document.ErrorCode}
}

func readBoundedAdapterOutput(filename string, limit int64) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("adapter output is not a bounded regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() {
		return nil, errors.New("adapter output changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) != info.Size() {
		return nil, errors.New("adapter output could not be read stably")
	}
	return data, nil
}

func findCheckedAdapters() (string, string, error) {
	read, err := checkedScript("gh-api-read.sh")
	if err != nil {
		return "", "", err
	}
	mutation, err := checkedScript("releasetransport.sh")
	if err != nil {
		return "", "", err
	}
	return read, mutation, nil
}

func checkedScript(name string) (string, error) {
	path, err := filepath.Abs(filepath.Join("scripts", "release", name))
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("checked adapter %s is unavailable", name)
	}
	return path, nil
}
