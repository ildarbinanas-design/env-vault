package cli

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ildarbinanas-design/env-vault/internal/atomicfile"
	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
)

// Conflict policies for import.
const (
	conflictFail      = "fail"
	conflictSkip      = "skip"
	conflictOverwrite = "overwrite"
)

// Reported per-secret outcomes.
const (
	actionCreated     = "created"
	actionOverwritten = "overwritten"
	actionSkipped     = "skipped"
)

// A transfer container carries secret values only. Profile mappings live in
// `.env-vault.yaml`, which holds no values and travels with the repository, so
// neither command reads or writes a config file.
func (a *App) exportCommand() *cobra.Command {
	var outPath string
	var force bool
	var services []string
	cmd := &cobra.Command{
		Use:   "export --out <path>",
		Short: "Write stored secret values to an encrypted container",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if outPath == "" {
				return apperrors.Usage("export", "export requires --out", "Run: env-vault export --out <path>")
			}
			selected, err := exportServices("export", services)
			if err != nil {
				return err
			}

			dryRun := a.dryRun(cmd)
			// Refuse an unusable destination before the operator is asked to
			// type a passphrase twice.
			if !dryRun {
				if err := ensureExportTarget(outPath, force); err != nil {
					return err
				}
			}

			store, err := a.store("export")
			if err != nil {
				return err
			}

			var payload bundle.Payload
			reported := make([]map[string]string, 0, 16)
			for _, service := range selected {
				items, err := store.List(cmd.Context(), service)
				if err != nil {
					return backendUnavailable("export", err)
				}
				sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
				for _, item := range items {
					if !dryRun {
						value, err := store.Get(cmd.Context(), service, item.Name)
						if stderrors.Is(err, secretstore.ErrNotFound) {
							// Deleted between the listing and the read.
							continue
						}
						if err != nil {
							return backendUnavailable("export", err)
						}
						payload.Secrets = append(payload.Secrets, bundle.SecretEntry{
							Service: service,
							Name:    item.Name,
							Value:   value,
						})
					}
					recordID := secretstore.RecordID(service, item.Name)
					reported = append(reported, map[string]string{
						"service":     service,
						"name":        item.Name,
						"record_id":   recordID,
						"fingerprint": recordID,
					})
				}
			}

			if len(reported) == 0 {
				return apperrors.Usage(
					"export",
					"No stored secrets to export",
					"Store a secret first, or name another service with --with-services",
				)
			}

			data := map[string]any{
				"path":         outPath,
				"services":     selected,
				"secret_count": len(reported),
				"secrets":      reported,
				"kdf":          bundle.KDFArgon2id,
				"cipher":       bundle.CipherAES256GCM,
				"dry_run":      dryRun,
			}
			if dryRun {
				return a.renderer().Success("export", data, nil)
			}

			defer func() {
				for _, entry := range payload.Secrets {
					bundle.Wipe(entry.Value)
				}
			}()

			passphrase, err := a.readPassphrase("export", true)
			if err != nil {
				return err
			}
			defer bundle.Wipe(passphrase)

			raw, err := bundle.Seal(payload, passphrase, bundle.Options{ToolVersion: resolveVersion()})
			if err != nil {
				return bundleError("export", err)
			}
			if err := atomicfile.Write(outPath, raw); err != nil {
				return exportWriteError(err)
			}
			return a.renderer().Success("export", data, nil)
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "container path to write")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing container at --out")
	// Named --with-services rather than --service on purpose. On `secret set`,
	// --service replaces the default service; here the default is always
	// included and these are added to it. Reusing the name for the opposite
	// relationship would be a trap.
	cmd.Flags().StringSliceVar(&services, "with-services", nil, "keychain services to include beyond the default; comma-separated or repeated")
	return cmd
}

func (a *App) importCommand() *cobra.Command {
	var onConflict string
	cmd := &cobra.Command{
		Use:   "import <path>",
		Short: "Restore secret values from an encrypted container",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return apperrors.Usage("import", "import requires exactly one container path", "Run: env-vault import <path>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			sourcePath := args[0]
			policy, err := parseConflictPolicy(onConflict)
			if err != nil {
				return err
			}
			raw, err := readContainerFile("import", sourcePath)
			if err != nil {
				return err
			}

			passphrase, err := a.readPassphrase("import", false)
			if err != nil {
				return err
			}
			defer bundle.Wipe(passphrase)

			payload, err := bundle.Open(raw, passphrase)
			if err != nil {
				return bundleError("import", err)
			}
			defer func() {
				for _, entry := range payload.Secrets {
					bundle.Wipe(entry.Value)
				}
			}()
			if err := validateImportedPayload(payload); err != nil {
				return err
			}

			store, err := a.store("import")
			if err != nil {
				return err
			}

			dryRun := a.dryRun(cmd)
			reported := make([]map[string]string, 0, len(payload.Secrets))
			writes := make([]bundle.SecretEntry, 0, len(payload.Secrets))
			services := map[string]struct{}{}

			for _, entry := range payload.Secrets {
				exists, err := store.Exists(cmd.Context(), entry.Service, entry.Name)
				if err != nil {
					return backendUnavailable("import", err)
				}
				action := actionCreated
				switch {
				case !exists:
					writes = append(writes, entry)
				case policy == conflictFail:
					return apperrors.SecretExists("import", entry.Name)
				case policy == conflictSkip:
					action = actionSkipped
				default:
					action = actionOverwritten
					writes = append(writes, entry)
				}
				services[entry.Service] = struct{}{}
				recordID := secretstore.RecordID(entry.Service, entry.Name)
				reported = append(reported, map[string]string{
					"service":     entry.Service,
					"name":        entry.Name,
					"record_id":   recordID,
					"fingerprint": recordID,
					"action":      action,
				})
			}

			data := map[string]any{
				"path":         sourcePath,
				"services":     sortedKeys(services),
				"secret_count": len(reported),
				"secrets":      reported,
				"on_conflict":  policy,
				"dry_run":      dryRun,
			}
			if dryRun {
				return a.renderer().Success("import", data, nil)
			}

			for _, entry := range writes {
				if err := store.Set(cmd.Context(), entry.Service, entry.Name, entry.Value); err != nil {
					return backendUnavailable("import", err)
				}
			}
			return a.renderer().Success("import", data, nil)
		},
	}
	cmd.Flags().StringVar(&onConflict, "on-conflict", conflictFail, "conflict policy: fail, skip, or overwrite")
	return cmd
}

// exportServices resolves the services to read. The default service is always
// included. Additional ones must be named explicitly, because a keychain
// offers no way to enumerate the services an application has used.
//
// Values arrive already split on commas by pflag, so surrounding whitespace in
// a list such as `--with-services a, b` is trimmed here rather than becoming
// part of a service name.
func exportServices(command string, extra []string) ([]string, error) {
	selected := []string{secretstore.DefaultService}
	seen := map[string]struct{}{secretstore.DefaultService: {}}
	for _, service := range extra {
		service = strings.TrimSpace(service)
		if service == "" {
			return nil, apperrors.Usage(command, "Empty service name in --with-services", "Remove the empty entry from the service list")
		}
		if err := secretstore.ValidateServiceName(service); err != nil {
			return nil, apperrors.Usage(command, err.Error(), "Use a safe relative slash-separated service name")
		}
		if _, duplicate := seen[service]; duplicate {
			continue
		}
		seen[service] = struct{}{}
		selected = append(selected, service)
	}
	return selected, nil
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// readPassphrase collects the container passphrase.
//
// There is deliberately no flag or environment variable carrying a passphrase,
// mirroring the rule that a secret value never reaches the command line.
func (a *App) readPassphrase(command string, confirm bool) ([]byte, error) {
	read := a.passphraseInput(command)

	first, err := read("Passphrase: ")
	if err != nil {
		return nil, err
	}
	if len(first) == 0 {
		return nil, apperrors.PassphraseInvalid(command, "Passphrase is empty", "Provide a non-empty passphrase")
	}
	if !confirm {
		return first, nil
	}

	if err := bundle.ValidatePassphrase(first); err != nil {
		bundle.Wipe(first)
		return nil, apperrors.PassphraseInvalid(
			command,
			capitalize(err.Error()),
			fmt.Sprintf("Use a passphrase of at least %d characters", bundle.MinPassphraseLength),
		)
	}
	second, err := read("Confirm passphrase: ")
	if err != nil {
		bundle.Wipe(first)
		return nil, err
	}
	defer bundle.Wipe(second)
	if !bytes.Equal(first, second) {
		bundle.Wipe(first)
		return nil, apperrors.PassphraseInvalid(command, "Passphrases do not match", "Retype the same passphrase at both prompts")
	}
	return first, nil
}

// passphraseInput selects where the passphrase comes from. Every source must
// return a freshly allocated slice on each call, because the caller wipes the
// confirmation independently of the passphrase it keeps.
func (a *App) passphraseInput(command string) func(prompt string) ([]byte, error) {
	if a.passphraseReader != nil {
		return a.passphraseReader
	}
	if teststore.EnabledFromEnv() {
		return a.gatedStdinPassphrase(command)
	}
	return a.terminalPassphrase(command)
}

func (a *App) terminalPassphrase(command string) func(string) ([]byte, error) {
	return func(prompt string) ([]byte, error) {
		file, ok := a.stdin.(interface{ Fd() uintptr })
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return nil, apperrors.Usage(command, "Interactive hidden prompt requires a terminal", "Run "+command+" from an interactive terminal")
		}
		fmt.Fprint(a.stderr, prompt)
		value, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(a.stderr)
		if err != nil {
			return nil, apperrors.Wrap(command, apperrors.CodeRuntimeError, "Unable to read hidden passphrase prompt", "Retry from an interactive terminal", apperrors.ExitRuntimeError, err)
		}
		return value, nil
	}
}

// gatedStdinPassphrase reads the passphrase from stdin, and is reachable only
// when the complete insecure test-backend gate is already active. Under that
// gate every secret lives in a throwaway store, so this discloses nothing that
// the store itself does not. It exists so the black-box E2E suite, which runs
// the binary without a terminal, can still exercise a full round trip and scan
// the resulting container for leaked values.
func (a *App) gatedStdinPassphrase(command string) func(string) ([]byte, error) {
	var cached []byte
	var loaded bool
	return func(string) ([]byte, error) {
		if !loaded {
			value, err := io.ReadAll(a.stdin)
			if err != nil {
				return nil, apperrors.Wrap(command, apperrors.CodeRuntimeError, "Unable to read passphrase from stdin", "Provide the passphrase on stdin", apperrors.ExitRuntimeError, err)
			}
			if len(value) > 0 && value[len(value)-1] == '\n' {
				value = value[:len(value)-1]
			}
			if len(value) > 0 && value[len(value)-1] == '\r' {
				value = value[:len(value)-1]
			}
			cached, loaded = value, true
		}
		return append([]byte(nil), cached...), nil
	}
}

func parseConflictPolicy(value string) (string, error) {
	switch value {
	case conflictFail, conflictSkip, conflictOverwrite:
		return value, nil
	default:
		return "", apperrors.Usage("import", "Unsupported --on-conflict value: "+value, "Use --on-conflict fail, skip, or overwrite")
	}
}

func ensureExportTarget(path string, force bool) error {
	if err := atomicfile.ValidateTarget(path); err != nil {
		if atomicfile.IsUnsafeTarget(err) {
			return apperrors.Usage("export", "Unsafe container target: "+err.Error(), "Write the container to a regular file path")
		}
		return apperrors.Wrap("export", apperrors.CodeRuntimeError, "Unable to inspect the container target", "Check the --out path and permissions", apperrors.ExitRuntimeError, err)
	}
	if force {
		return nil
	}
	if _, err := os.Lstat(path); err == nil {
		return apperrors.Usage("export", "Container already exists: "+path, "Choose another --out path or pass --force")
	}
	return nil
}

func exportWriteError(err error) error {
	if atomicfile.IsUnsafeTarget(err) {
		return apperrors.Usage("export", "Unsafe container target: "+err.Error(), "Write the container to a regular file path")
	}
	return apperrors.Wrap("export", apperrors.CodeRuntimeError, "Unable to write the container", "Check the --out path and permissions", apperrors.ExitRuntimeError, err)
}

func readContainerFile(command, path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, apperrors.BundleInvalid(command, "Unable to read the container", "Check the container path and permissions", err)
	}
	if !info.Mode().IsRegular() {
		return nil, apperrors.BundleInvalid(command, "Container path is not a regular file", "Point import at a container file", nil)
	}
	if info.Size() > bundle.MaxContainerBytes {
		return nil, apperrors.BundleInvalid(command, "Container is too large", "Check that the file is an env-vault container", nil)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, apperrors.BundleInvalid(command, "Unable to read the container", "Check the container path and permissions", err)
	}
	return data, nil
}

// validateImportedPayload re-runs the ordinary name rules on decrypted
// contents. The container came from another machine, so its names are
// untrusted input even though the authentication tag verified.
func validateImportedPayload(payload bundle.Payload) error {
	for _, entry := range payload.Secrets {
		if err := secretstore.ValidateServiceName(entry.Service); err != nil {
			return apperrors.BundleInvalid("import", "Container has an invalid service name", "Re-export with a supported env-vault version", err)
		}
		if err := secretstore.ValidateSecretName(entry.Name); err != nil {
			return apperrors.BundleInvalid("import", "Container has an invalid secret name", "Re-export with a supported env-vault version", err)
		}
	}
	return nil
}

func bundleError(command string, err error) error {
	switch {
	case stderrors.Is(err, bundle.ErrAuthFailed):
		return apperrors.BundleAuthFailed(command, err)
	case stderrors.Is(err, bundle.ErrInvalid):
		return apperrors.BundleInvalid(command, containerDetail(err), "Check that the file is an env-vault container written by a compatible version", err)
	default:
		return apperrors.Wrap(command, apperrors.CodeRuntimeError, "Unable to process the container", "Retry with --verbose or run env-vault doctor", apperrors.ExitRuntimeError, err)
	}
}

func containerDetail(err error) string {
	detail := strings.TrimPrefix(err.Error(), bundle.ErrInvalid.Error()+": ")
	if detail == err.Error() {
		return "Invalid container"
	}
	return "Invalid container: " + detail
}

func capitalize(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
