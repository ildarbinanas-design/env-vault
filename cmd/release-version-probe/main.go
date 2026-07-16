// release-version-probe executes the three native version surfaces and saves
// bounded evidence for the file-only releasecheck verifier.
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
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
)

const (
	maxBinaryBytes = 128 << 20
	maxOutputBytes = 64 << 10
	commandTimeout = 30 * time.Second
)

// processWaitDelay bounds os/exec's post-exit pipe drain. A target can exit
// while a descendant keeps inherited stdout or stderr open; without a
// WaitDelay, Cmd.Wait may then remain blocked long after commandTimeout.
var processWaitDelay = 2 * time.Second

var (
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	platformPattern   = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
)

func main() {
	var platform, source, version, repository, binary string
	var runID int64
	var attempt int
	flag.StringVar(&platform, "platform", "", "release contract platform ID")
	flag.StringVar(&source, "source-sha", "", "exact source SHA")
	flag.StringVar(&version, "release-version", "", "exact vX.Y.Z version")
	flag.StringVar(&repository, "repository", "", "owner/repository")
	flag.Int64Var(&runID, "run-id", 0, "workflow run ID")
	flag.IntVar(&attempt, "run-attempt", 0, "workflow run attempt")
	flag.StringVar(&binary, "binary", "", "native binary path")
	flag.Parse()
	if flag.NArg() != 0 || !platformPattern.MatchString(platform) || !shaPattern.MatchString(source) ||
		!releasecontract.IsVersion(version) || !repositoryPattern.MatchString(repository) ||
		strings.Contains(repository, "..") || runID <= 0 || attempt <= 0 || binary == "" {
		fmt.Fprintln(os.Stderr, "usage: release-version-probe --platform ID --source-sha SHA --release-version vX.Y.Z --repository OWNER/REPO --run-id ID --run-attempt N --binary FILE")
		os.Exit(2)
	}

	absBinary, err := filepath.Abs(binary)
	if err != nil {
		fail("resolve native binary", err)
	}
	before, err := digestBinary(absBinary)
	if err != nil {
		fail("hash native binary before execution", err)
	}

	commands := make([]releasepromotion.LiteralVersionCommand, 0, 3)
	for _, invocation := range []struct {
		surface string
		args    []string
	}{
		{"flag", []string{"--version"}},
		{"command", []string{"version"}},
		{"json", []string{"version", "--json"}},
	} {
		result, err := runBounded(absBinary, invocation.surface, invocation.args)
		if err != nil {
			fail("execute "+invocation.surface+" version surface", err)
		}
		commands = append(commands, result)
	}
	after, err := digestBinary(absBinary)
	if err != nil {
		fail("hash native binary after execution", err)
	}
	if before != after {
		fail("bind native binary", errors.New("binary changed while version surfaces were executed"))
	}

	evidence := releasepromotion.LiteralVersionEvidence{
		SchemaID: releasepromotion.LiteralVersionEvidenceSchema, SchemaVersion: releasepromotion.SchemaVersion,
		PlatformID: platform, SourceSHA: source, ReleaseVersion: version, Repository: repository,
		RunID: runID, RunAttempt: attempt, Binary: before, Commands: commands,
		Results: releasepromotion.LiteralVersionResults{Flag: version, Command: version, JSON: version}, Result: "pass",
	}
	evidence.EvidenceSHA256, err = releasepromotion.LiteralVersionEvidenceSHA256(evidence)
	if err != nil {
		fail("seal literal version evidence", err)
	}
	if err := releasepromotion.ValidateLiteralVersionEvidence(evidence); err != nil {
		fail("validate literal version evidence", err)
	}
	encoded, err := releasepromotion.MarshalJSON(evidence)
	if err != nil {
		fail("encode literal version evidence", err)
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		fail("write literal version evidence", err)
	}
}

func runBounded(binary, surface string, args []string) (releasepromotion.LiteralVersionCommand, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = sanitizedEnvironment()
	command.WaitDelay = processWaitDelay
	configureProcessTree(command)
	defer terminateProcessTree(command)
	var stdout, stderr boundedBuffer
	stdout.limit, stderr.limit = maxOutputBytes, maxOutputBytes
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return releasepromotion.LiteralVersionCommand{}, errors.New("command exceeded 30 seconds")
	}
	if stdout.exceeded || stderr.exceeded {
		return releasepromotion.LiteralVersionCommand{}, errors.New("command output exceeded 64 KiB")
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return releasepromotion.LiteralVersionCommand{}, fmt.Errorf("command descendants kept output handles open after exit: %w", exec.ErrWaitDelay)
	}
	if err != nil {
		return releasepromotion.LiteralVersionCommand{}, err
	}
	return releasepromotion.LiteralVersionCommand{
		Surface: surface, Args: append([]string(nil), args...),
		Stdout: normalizeNewlines(stdout.Bytes()), Stderr: normalizeNewlines(stderr.Bytes()), ExitCode: 0,
	}, nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	if buffer.buffer.Len()+len(data) > buffer.limit {
		remaining := buffer.limit - buffer.buffer.Len()
		if remaining > 0 {
			_, _ = buffer.buffer.Write(data[:remaining])
		}
		buffer.exceeded = true
		return len(data), nil
	}
	return buffer.buffer.Write(data)
}

func (buffer *boundedBuffer) Bytes() []byte { return buffer.buffer.Bytes() }

func digestBinary(filename string) (releasepromotion.FileDigest, error) {
	before, err := os.Lstat(filename)
	if err != nil {
		return releasepromotion.FileDigest{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxBinaryBytes {
		return releasepromotion.FileDigest{}, errors.New("binary must be a bounded regular non-symlink file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return releasepromotion.FileDigest{}, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() {
		return releasepromotion.FileDigest{}, errors.New("binary changed identity while opening")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, maxBinaryBytes+1))
	if err != nil || n != before.Size() {
		return releasepromotion.FileDigest{}, errors.New("binary changed while hashing")
	}
	return releasepromotion.FileDigest{Name: filepath.Base(filename), Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func sanitizedEnvironment() []string {
	environment := []string{"LC_ALL=C", "LANG=C", "TZ=UTC"}
	if runtime.GOOS != "windows" {
		return environment
	}
	for _, wanted := range []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "TEMP", "TMP"} {
		for _, entry := range os.Environ() {
			name, _, found := strings.Cut(entry, "=")
			if found && strings.EqualFold(name, wanted) {
				environment = append(environment, entry)
				break
			}
		}
	}
	return environment
}

func normalizeNewlines(data []byte) string {
	return string(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
}

func fail(operation string, err error) {
	fmt.Fprintf(os.Stderr, "release-version-probe: %s: %v\n", operation, err)
	os.Exit(1)
}
