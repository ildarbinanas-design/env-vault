package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type commandResult struct {
	Name          string    `json:"name"`
	Arguments     []string  `json:"arguments"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	DurationMS    int64     `json:"duration_ms"`
	ExitCode      int       `json:"exit_code"`
	TimedOut      bool      `json:"timed_out"`
	Error         string    `json:"error,omitempty"`
	Seed          string    `json:"shuffle_seed,omitempty"`
	ScenarioSeeds []string  `json:"scenario_shuffle_seeds,omitempty"`
	Count         int       `json:"count,omitempty"`
}

type commandSpec struct {
	name       string
	args       []string
	dir        string
	env        []string
	timeout    time.Duration
	stdoutPath string
	stderrPath string
	stdout     io.Writer
	stderr     io.Writer
}

func runCommand(spec commandSpec) commandResult {
	result := commandResult{
		Name:      spec.name,
		Arguments: append([]string(nil), spec.args...),
		StartedAt: time.Now().UTC(),
		ExitCode:  -1,
	}
	if spec.timeout <= 0 {
		spec.timeout = 30 * time.Minute
	}

	ctx, cancel := context.WithTimeout(context.Background(), spec.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, spec.name, spec.args...)
	configureProcess(cmd)
	cmd.Dir = spec.dir
	cmd.Env = spec.env
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		return terminateProcess(cmd)
	}

	var opened []*os.File
	closeFiles := func() {
		for _, file := range opened {
			_ = file.Close()
		}
	}
	defer closeFiles()
	if spec.stdoutPath != "" {
		file, err := createPrivateFile(spec.stdoutPath)
		if err != nil {
			result.Error = "create stdout capture: " + err.Error()
			result.EndedAt = time.Now().UTC()
			result.DurationMS = result.EndedAt.Sub(result.StartedAt).Milliseconds()
			return result
		}
		opened = append(opened, file)
		if spec.stdout != nil {
			cmd.Stdout = io.MultiWriter(file, spec.stdout)
		} else {
			cmd.Stdout = file
		}
	} else {
		cmd.Stdout = spec.stdout
	}
	if spec.stderrPath != "" {
		file, err := createPrivateFile(spec.stderrPath)
		if err != nil {
			result.Error = "create stderr capture: " + err.Error()
			result.EndedAt = time.Now().UTC()
			result.DurationMS = result.EndedAt.Sub(result.StartedAt).Milliseconds()
			return result
		}
		opened = append(opened, file)
		if spec.stderr != nil {
			cmd.Stderr = io.MultiWriter(file, spec.stderr)
		} else {
			cmd.Stderr = file
		}
	} else {
		cmd.Stderr = spec.stderr
	}

	err := cmd.Run()
	result.EndedAt = time.Now().UTC()
	result.DurationMS = result.EndedAt.Sub(result.StartedAt).Milliseconds()
	result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	if err == nil {
		result.ExitCode = 0
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = 127
	}
	if result.TimedOut {
		result.Error = fmt.Sprintf("command exceeded deadline %s", spec.timeout)
	} else {
		result.Error = err.Error()
	}
	return result
}

func createPrivateFile(filename string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
}

func commandOutput(name string, args []string, dir string, env []string, timeout time.Duration) ([]byte, commandResult) {
	var output bytes.Buffer
	result := runCommand(commandSpec{
		name:    name,
		args:    args,
		dir:     dir,
		env:     env,
		timeout: timeout,
		stdout:  &output,
		stderr:  &output,
	})
	return output.Bytes(), result
}

func commandLabel(result commandResult) string {
	args := make([]string, 0, len(result.Arguments)+1)
	args = append(args, result.Name)
	for _, arg := range result.Arguments {
		if strings.ContainsAny(arg, " \t\r\n\"") {
			args = append(args, strconvQuote(arg))
		} else {
			args = append(args, arg)
		}
	}
	return strings.Join(args, " ")
}

func strconvQuote(value string) string {
	return fmt.Sprintf("%q", value)
}

func environment(overrides map[string]string) []string {
	values := make(map[string]string)
	keys := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		lookup := strings.ToUpper(name)
		keys[lookup] = name
		values[lookup] = value
	}
	for name, value := range overrides {
		lookup := strings.ToUpper(name)
		keys[lookup] = name
		values[lookup] = value
	}
	result := make([]string, 0, len(values))
	for lookup, value := range values {
		result = append(result, keys[lookup]+"="+value)
	}
	// Deterministic environments make process evidence easier to compare.
	sortStrings(result)
	return result
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
