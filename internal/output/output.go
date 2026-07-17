package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/redact"
)

type Options struct {
	JSON       bool
	JSONL      bool
	OutputPath string
	Quiet      bool
	Verbose    bool
}

type ErrorObject struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Remediation string `json:"remediation"`
}

type Envelope struct {
	OK        bool         `json:"ok"`
	Command   string       `json:"command"`
	Timestamp string       `json:"timestamp"`
	Data      any          `json:"data"`
	Warnings  []string     `json:"warnings"`
	Error     *ErrorObject `json:"error"`
}

type Renderer struct {
	stdout   io.Writer
	stderr   io.Writer
	options  Options
	redactor redact.Redactor
	now      func() time.Time
}

func New(stdout, stderr io.Writer, options Options, redactor redact.Redactor) Renderer {
	return Renderer{
		stdout:   stdout,
		stderr:   stderr,
		options:  options,
		redactor: redactor,
		now:      time.Now,
	}
}

func (r Renderer) WithRedactor(redactor redact.Redactor) Renderer {
	r.redactor = redactor
	return r
}

func (r Renderer) Success(command string, data any, warnings []string) error {
	env := r.envelope(true, command, data, warnings, nil)
	if err := r.writeOutputFile(env); err != nil {
		return err
	}
	if r.options.JSON || r.options.JSONL {
		return writeMachine(r.stdout, env)
	}
	if r.options.Quiet {
		return nil
	}
	return r.writeHumanSuccess(env)
}

func (r Renderer) Error(command string, err *apperrors.AppError) error {
	if err == nil {
		err = apperrors.New(command, apperrors.CodeRuntimeError, "Unknown error", "Retry with --verbose or run env-vault doctor", apperrors.ExitRuntimeError)
	}
	if err.Command != "" {
		command = err.Command
	}
	obj := &ErrorObject{
		Code:        err.Code,
		Message:     err.Message,
		Remediation: err.Remediation,
	}
	env := r.envelope(false, command, nil, nil, obj)
	if fileErr := r.writeOutputFile(env); fileErr != nil && r.options.Verbose {
		fmt.Fprintf(r.stderr, "OUTPUT_WRITE_FAILED: %s\n", r.redactor.String(fileErr.Error()))
	}
	if r.options.JSON || r.options.JSONL {
		return writeMachine(r.stdout, env)
	}
	_, writeErr := fmt.Fprintf(r.stderr, "code=%s\nmessage=%s\nremediation=%s\n",
		env.Error.Code, env.Error.Message, env.Error.Remediation)
	return writeErr
}

func (r Renderer) envelope(ok bool, command string, data any, warnings []string, err *ErrorObject) Envelope {
	redactedWarnings := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		redactedWarnings = append(redactedWarnings, r.redactor.String(warning))
	}
	var redactedErr *ErrorObject
	if err != nil {
		redactedErr = &ErrorObject{
			Code:        r.redactor.String(err.Code),
			Message:     r.redactor.String(err.Message),
			Remediation: r.redactor.String(err.Remediation),
		}
	}
	return Envelope{
		OK:        ok,
		Command:   command,
		Timestamp: r.now().UTC().Format(time.RFC3339),
		Data:      r.redactor.Any(data),
		Warnings:  redactedWarnings,
		Error:     redactedErr,
	}
}

func writeMachine(w io.Writer, env Envelope) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(env)
}

func (r Renderer) writeHumanSuccess(env Envelope) error {
	switch env.Command {
	case "version":
		if value, ok := stringFromData(env.Data, "version"); ok {
			_, err := fmt.Fprintln(r.stdout, value)
			return err
		}
	case "secret_set":
		name, _ := stringFromData(env.Data, "name")
		fp, _ := stringFromData(env.Data, "fingerprint")
		_, err := fmt.Fprintf(r.stdout, "secret stored: %s (fingerprint: %s)\n", name, fp)
		return err
	case "secret_check":
		name, _ := stringFromData(env.Data, "name")
		fp, _ := stringFromData(env.Data, "fingerprint")
		_, err := fmt.Fprintf(r.stdout, "secret exists: %s (fingerprint: %s)\n", name, fp)
		return err
	case "secret_delete":
		name, _ := stringFromData(env.Data, "name")
		_, err := fmt.Fprintf(r.stdout, "secret deleted: %s\n", name)
		return err
	case "secret_list":
		return writeSecretList(r.stdout, env.Data)
	case "profile_create":
		profile, _ := stringFromData(env.Data, "profile")
		path, _ := stringFromData(env.Data, "path")
		_, err := fmt.Fprintf(r.stdout, "profile created: %s (%s)\n", profile, path)
		return err
	case "profile_add":
		profile, _ := stringFromData(env.Data, "profile")
		envName, _ := stringFromData(env.Data, "env")
		_, err := fmt.Fprintf(r.stdout, "profile updated: %s added %s\n", profile, envName)
		return err
	case "profile_remove":
		profile, _ := stringFromData(env.Data, "profile")
		envName, _ := stringFromData(env.Data, "env")
		_, err := fmt.Fprintf(r.stdout, "profile updated: %s removed %s\n", profile, envName)
		return err
	case "profile_show":
		return writeProfile(r.stdout, env.Data)
	case "exec":
		if dryRun, ok := boolFromData(env.Data, "dry_run"); ok && dryRun {
			_, err := fmt.Fprintln(r.stdout, "exec dry-run passed")
			return err
		}
		return nil
	case "doctor":
		return writeDoctor(r.stdout, env)
	}
	_, err := fmt.Fprintln(r.stdout, "ok")
	return err
}

func (r Renderer) writeOutputFile(env Envelope) error {
	if r.options.OutputPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.options.OutputPath), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(r.options.OutputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(env); err != nil {
		return err
	}
	return os.Chmod(r.options.OutputPath, 0o600)
}

func stringFromData(data any, key string) (string, bool) {
	m, ok := data.(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := m[key].(string)
	return value, ok
}

func boolFromData(data any, key string) (bool, bool) {
	m, ok := data.(map[string]any)
	if !ok {
		return false, false
	}
	value, ok := m[key].(bool)
	return value, ok
}

func writeSecretList(w io.Writer, data any) error {
	m, ok := data.(map[string]any)
	if !ok {
		_, err := fmt.Fprintln(w, "no secrets")
		return err
	}
	items, _ := m["secrets"].([]map[string]string)
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "no secrets")
		return err
	}
	for _, item := range items {
		if _, err := fmt.Fprintf(w, "%s %s\n", item["name"], item["fingerprint"]); err != nil {
			return err
		}
	}
	return nil
}

func writeProfile(w io.Writer, data any) error {
	m, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	profile, _ := m["profile"].(string)
	if _, err := fmt.Fprintf(w, "profile: %s\n", profile); err != nil {
		return err
	}
	items, _ := m["secrets"].([]map[string]string)
	for _, item := range items {
		if _, err := fmt.Fprintf(w, "%s:%s required=%s\n", item["name"], item["env"], item["required"]); err != nil {
			return err
		}
	}
	return nil
}

func writeDoctor(w io.Writer, env Envelope) error {
	if _, err := fmt.Fprintln(w, "doctor: ok"); err != nil {
		return err
	}
	for _, warning := range env.Warnings {
		if _, err := fmt.Fprintf(w, "warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}
