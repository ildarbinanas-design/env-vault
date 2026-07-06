package runner

import (
	"context"
	stderrors "errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/platform"
)

type CommandRunner struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (r CommandRunner) Validate(argv []string) error {
	if len(argv) == 0 {
		return apperrors.Usage("exec", "Command is missing after --", "Run: env-vault exec [profile] -- <cmd> [args...]")
	}
	return validateExecutable(argv[0])
}

func (r CommandRunner) Run(ctx context.Context, argv []string, env []string) (int, error) {
	if err := r.Validate(argv); err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin = r.Stdin
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	if err := cmd.Start(); err != nil {
		if stderrors.Is(err, exec.ErrNotFound) {
			return apperrors.ExitCommandNotFound, apperrors.New("exec", apperrors.CodeCommandNotFound, "Command not found: "+argv[0], "Check the command name or PATH", apperrors.ExitCommandNotFound)
		}
		return apperrors.ExitRuntimeError, apperrors.Wrap("exec", apperrors.CodeRuntimeError, "Unable to start command", "Check command permissions and arguments", apperrors.ExitRuntimeError, err)
	}
	stop := forwardSignals(cmd.Process)
	defer stop()
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if stderrors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return apperrors.ExitRuntimeError, apperrors.Wrap("exec", apperrors.CodeRuntimeError, "Command failed", "Inspect the child process error", apperrors.ExitRuntimeError, err)
	}
	return apperrors.ExitSuccess, nil
}

func validateExecutable(name string) error {
	if strings.ContainsRune(name, os.PathSeparator) || strings.Contains(name, "/") {
		info, err := os.Stat(name)
		if os.IsNotExist(err) {
			return apperrors.New("exec", apperrors.CodeCommandNotFound, "Command not found: "+name, "Check the command path", apperrors.ExitCommandNotFound)
		}
		if err != nil {
			return apperrors.Wrap("exec", apperrors.CodeRuntimeError, "Unable to inspect command", "Check command permissions", apperrors.ExitRuntimeError, err)
		}
		if info.IsDir() || !platform.IsExecutable(info.Mode()) {
			return apperrors.New("exec", apperrors.CodeCommandNotExecutable, "Command is not executable: "+name, "Set execute permissions or choose another command", apperrors.ExitCommandNotExecutable)
		}
		return nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return apperrors.New("exec", apperrors.CodeCommandNotFound, "Command not found: "+name, "Check the command name or PATH", apperrors.ExitCommandNotFound)
	}
	info, err := os.Stat(filepath.Clean(path))
	if err == nil && (info.IsDir() || !platform.IsExecutable(info.Mode())) {
		return apperrors.New("exec", apperrors.CodeCommandNotExecutable, "Command is not executable: "+name, "Set execute permissions or choose another command", apperrors.ExitCommandNotExecutable)
	}
	return nil
}
