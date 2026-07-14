package cli

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ildarbinanas-design/env-vault/internal/config"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/redact"
	"github.com/ildarbinanas-design/env-vault/internal/runner"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	keyringstore "github.com/ildarbinanas-design/env-vault/internal/secretstore/keyring"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
)

var Version = "dev"

// resolveVersion prefers the release version injected through -ldflags and
// falls back to module/VCS metadata the Go toolchain embeds into source builds.
func resolveVersion() string {
	if Version != "dev" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	revision := ""
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return Version
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		return Version + "-" + revision + "-dirty"
	}
	return Version + "-" + revision
}

type App struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	currentEnv []string
	output     output.Options
	dryRunFlag bool
	configPath string
	redactor   redact.Redactor
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	app := &App{
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		currentEnv: os.Environ(),
		redactor:   redact.New(),
	}
	root := app.rootCommand()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		if exitStatus, ok := apperrors.ExitStatusFrom(err); ok {
			return exitStatus.Code
		}
		appErr, ok := apperrors.From(err)
		if !ok {
			appErr = apperrors.Wrap("root", apperrors.CodeRuntimeError, "Unexpected runtime error", "Retry with --verbose or run env-vault doctor", apperrors.ExitRuntimeError, err)
		}
		_ = app.renderer().Error(appErr.Command, appErr)
		return appErr.ExitCode
	}
	return apperrors.ExitSuccess
}

func (a *App) rootCommand() *cobra.Command {
	var showVersion bool
	root := &cobra.Command{
		Use:           "env-vault",
		Short:         "OS-keychain-backed environment profile executor",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				return a.renderVersion()
			}
			return apperrors.Usage("root", "Command is required", "Run: env-vault --help")
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if a.output.JSON && a.output.JSONL {
				return apperrors.Usage(commandID(cmd), "Use only one of --json or --jsonl", "Choose a single machine-readable output mode")
			}
			return nil
		},
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return apperrors.Usage(commandID(cmd), err.Error(), "Run: "+cmd.CommandPath()+" --help")
	})

	flags := root.PersistentFlags()
	flags.BoolVar(&a.output.JSON, "json", false, "write env-vault messages as one JSON object")
	flags.BoolVar(&a.output.JSONL, "jsonl", false, "write env-vault messages as JSONL events")
	flags.StringVar(&a.output.OutputPath, "output", "", "write machine-readable env-vault metadata to a file")
	flags.BoolVar(&a.output.Quiet, "quiet", false, "suppress non-error human output")
	flags.BoolVar(&a.output.Verbose, "verbose", false, "include additional non-secret diagnostics")
	flags.BoolVar(&a.dryRunFlag, "dry-run", false, "validate without performing mutations or executing child processes")
	flags.StringVar(&a.configPath, "config", "", "config file path")
	root.Flags().BoolVar(&showVersion, "version", false, "print version and exit")

	root.AddCommand(a.versionCommand())
	root.AddCommand(a.secretCommand())
	root.AddCommand(a.profileCommand())
	root.AddCommand(a.execCommand())
	root.AddCommand(a.doctorCommand())
	return root
}

func (a *App) renderer() output.Renderer {
	return output.New(a.stdout, a.stderr, a.output, a.redactor)
}

func (a *App) dryRun(cmd *cobra.Command) bool {
	value, _ := cmd.Flags().GetBool("dry-run")
	if value {
		return true
	}
	value, _ = cmd.Root().PersistentFlags().GetBool("dry-run")
	return value
}

func (a *App) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.renderVersion()
		},
	}
}

func (a *App) renderVersion() error {
	return a.renderer().Success("version", map[string]any{"version": resolveVersion()}, nil)
}

func (a *App) secretCommand() *cobra.Command {
	secretCmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage secret metadata in the OS keychain",
		RunE: func(cmd *cobra.Command, args []string) error {
			return apperrors.Usage("secret", "Subcommand is required", "Run: env-vault secret --help")
		},
	}
	secretCmd.AddCommand(a.secretSetCommand())
	secretCmd.AddCommand(a.secretCheckCommand())
	secretCmd.AddCommand(a.secretDeleteCommand())
	secretCmd.AddCommand(a.secretListCommand())
	return secretCmd
}

func (a *App) secretSetCommand() *cobra.Command {
	var useStdin bool
	var service string
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Store a secret without echoing it",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return apperrors.Usage("secret_set", "secret set requires exactly one name", "Run: env-vault secret set <name>")
			}
			return wrapUsage("secret_set", config.ValidateSecretName(args[0]), "Use letters, digits, dot, underscore, dash, slash, or at-sign")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			service = defaultService(service)
			if err := validateService(service); err != nil {
				return apperrors.Usage("secret_set", err.Error(), "Use a safe relative slash-separated service name")
			}
			name := args[0]
			data := map[string]any{
				"name":        name,
				"service":     service,
				"fingerprint": secretstore.Fingerprint(service, name),
				"dry_run":     a.dryRun(cmd),
			}
			if a.dryRun(cmd) {
				return a.renderer().Success("secret_set", data, nil)
			}
			value, err := a.readSecret(useStdin)
			if err != nil {
				return err
			}
			a.redactor = a.redactor.With(string(value))
			store, err := a.store("secret_set")
			if err != nil {
				return err
			}
			if err := store.Set(context.Background(), service, name, value); err != nil {
				return backendUnavailable("secret_set", err)
			}
			return a.renderer().Success("secret_set", data, nil)
		},
	}
	cmd.Flags().BoolVar(&useStdin, "stdin", false, "read secret from stdin and trim exactly one trailing newline")
	cmd.Flags().StringVar(&service, "service", secretstore.DefaultService, "keychain service name")
	return cmd
}

func (a *App) secretCheckCommand() *cobra.Command {
	var service string
	cmd := &cobra.Command{
		Use:   "check <name>",
		Short: "Check whether a secret exists",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return apperrors.Usage("secret_check", "secret check requires exactly one name", "Run: env-vault secret check <name>")
			}
			return wrapUsage("secret_check", config.ValidateSecretName(args[0]), "Use a valid secret name")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			service = defaultService(service)
			if err := validateService(service); err != nil {
				return apperrors.Usage("secret_check", err.Error(), "Use a safe relative slash-separated service name")
			}
			store, err := a.store("secret_check")
			if err != nil {
				return err
			}
			exists, err := store.Exists(context.Background(), service, args[0])
			if err != nil {
				return backendUnavailable("secret_check", err)
			}
			if !exists {
				return missingSecretError("secret_check", service, args[0])
			}
			return a.renderer().Success("secret_check", map[string]any{
				"name":        args[0],
				"service":     service,
				"fingerprint": secretstore.Fingerprint(service, args[0]),
			}, nil)
		},
	}
	cmd.Flags().StringVar(&service, "service", secretstore.DefaultService, "keychain service name")
	return cmd
}

func (a *App) secretDeleteCommand() *cobra.Command {
	var service string
	var confirm string
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a secret after explicit confirmation",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return apperrors.Usage("secret_delete", "secret delete requires exactly one name", "Run: env-vault secret delete <name> --confirm <name>")
			}
			return wrapUsage("secret_delete", config.ValidateSecretName(args[0]), "Use a valid secret name")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			service = defaultService(service)
			if err := validateService(service); err != nil {
				return apperrors.Usage("secret_delete", err.Error(), "Use a safe relative slash-separated service name")
			}
			name := args[0]
			if confirm != name {
				return apperrors.New("secret_delete", apperrors.CodeConfirmationRequired, "Delete confirmation does not match secret name", "Re-run with --confirm "+name, apperrors.ExitUsage)
			}
			data := map[string]any{
				"name":        name,
				"service":     service,
				"fingerprint": secretstore.Fingerprint(service, name),
				"dry_run":     a.dryRun(cmd),
			}
			if a.dryRun(cmd) {
				return a.renderer().Success("secret_delete", data, nil)
			}
			store, err := a.store("secret_delete")
			if err != nil {
				return err
			}
			if err := store.Delete(context.Background(), service, name); stderrors.Is(err, secretstore.ErrNotFound) {
				return missingSecretError("secret_delete", service, name)
			} else if err != nil {
				return backendUnavailable("secret_delete", err)
			}
			return a.renderer().Success("secret_delete", data, nil)
		},
	}
	cmd.Flags().StringVar(&service, "service", secretstore.DefaultService, "keychain service name")
	cmd.Flags().StringVar(&confirm, "confirm", "", "required confirmation matching the secret name")
	return cmd
}

func (a *App) secretListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List secret names for the default service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := a.store("secret_list")
			if err != nil {
				return err
			}
			items, err := store.List(context.Background(), secretstore.DefaultService)
			if err != nil {
				return backendUnavailable("secret_list", err)
			}
			secrets := make([]map[string]string, 0, len(items))
			for _, item := range items {
				secrets = append(secrets, map[string]string{
					"name":        item.Name,
					"fingerprint": item.Fingerprint,
				})
			}
			return a.renderer().Success("secret_list", map[string]any{"service": secretstore.DefaultService, "secrets": secrets}, nil)
		},
	}
}

func (a *App) profileCommand() *cobra.Command {
	profileCmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage secret-to-env profile mappings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return apperrors.Usage("profile", "Subcommand is required", "Run: env-vault profile --help")
		},
	}
	profileCmd.AddCommand(a.profileCreateCommand())
	profileCmd.AddCommand(a.profileAddCommand())
	profileCmd.AddCommand(a.profileRemoveCommand())
	profileCmd.AddCommand(a.profileShowCommand())
	return profileCmd
}

func (a *App) profileCreateCommand() *cobra.Command {
	var local bool
	var global bool
	cmd := &cobra.Command{
		Use:   "create <profile>",
		Short: "Create a profile mapping file entry",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return apperrors.Usage("profile_create", "profile create requires exactly one profile name", "Run: env-vault profile create <profile>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.ResolveCreatePath(a.configPath, local, global)
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			profile := args[0]
			if _, exists := cfg.Profiles[profile]; exists {
				return apperrors.New("profile_create", apperrors.CodeProfileExists, "Profile already exists: "+profile, "Choose another profile or update the existing one", apperrors.ExitUsage)
			}
			cfg.Profiles[profile] = config.Profile{}
			data := map[string]any{"profile": profile, "path": path, "dry_run": a.dryRun(cmd)}
			if a.dryRun(cmd) {
				return a.renderer().Success("profile_create", data, nil)
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			return a.renderer().Success("profile_create", data, nil)
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "write .env-vault.yaml in the current directory")
	cmd.Flags().BoolVar(&global, "global", false, "write the user config")
	return cmd
}

func (a *App) profileAddCommand() *cobra.Command {
	var checkSecret bool
	cmd := &cobra.Command{
		Use:   "add <profile> <secret-name:ENV_NAME>",
		Short: "Add a secret mapping to a profile",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return apperrors.Usage("profile_add", "profile add requires a profile and mapping", "Run: env-vault profile add <profile> <secret-name:ENV_NAME>")
			}
			_, err := config.ParseMapping(args[1])
			return wrapUsage("profile_add", err, "Use <secret-name>:<ENV_NAME>")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mapping, _ := config.ParseMapping(args[1])
			if checkSecret {
				store, err := a.store("profile_add")
				if err != nil {
					return err
				}
				exists, err := store.Exists(context.Background(), secretstore.DefaultService, mapping.Name)
				if err != nil {
					return backendUnavailable("profile_add", err)
				}
				if !exists {
					return missingSecretError("profile_add", secretstore.DefaultService, mapping.Name)
				}
			}
			cfg, path, _, err := config.LoadForRead(a.configPath)
			if err != nil {
				return err
			}
			profileName := args[0]
			profile, ok := cfg.Profiles[profileName]
			if !ok {
				return apperrors.New("profile_add", apperrors.CodeProfileNotFound, "Profile not found: "+profileName, "Run: env-vault profile create "+profileName, apperrors.ExitUsage)
			}
			for _, existing := range profile.Secrets {
				if existing.Env == mapping.Env {
					if existing.Name == mapping.Name {
						return a.renderer().Success("profile_add", map[string]any{"profile": profileName, "name": mapping.Name, "env": mapping.Env, "path": path, "dry_run": a.dryRun(cmd)}, nil)
					}
					return apperrors.ConfigInvalid("profile_add", "Target env var is already mapped: "+mapping.Env, "Remove the old mapping before adding a new one", nil)
				}
			}
			profile.Secrets = append(profile.Secrets, mapping)
			cfg.Profiles[profileName] = profile
			data := map[string]any{"profile": profileName, "name": mapping.Name, "env": mapping.Env, "path": path, "dry_run": a.dryRun(cmd)}
			if a.dryRun(cmd) {
				return a.renderer().Success("profile_add", data, nil)
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			return a.renderer().Success("profile_add", data, nil)
		},
	}
	cmd.Flags().BoolVar(&checkSecret, "check-secret", false, "verify that the mapped secret already exists")
	return cmd
}

func (a *App) profileRemoveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <profile> <ENV_NAME|secret-name:ENV_NAME>",
		Short: "Remove a secret mapping from a profile",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return apperrors.Usage("profile_remove", "profile remove requires a profile and selector", "Run: env-vault profile remove <profile> <ENV_NAME|secret-name:ENV_NAME>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, _, err := config.LoadForRead(a.configPath)
			if err != nil {
				return err
			}
			profileName := args[0]
			profile, ok := cfg.Profiles[profileName]
			if !ok {
				return apperrors.New("profile_remove", apperrors.CodeProfileNotFound, "Profile not found: "+profileName, "Run: env-vault profile create "+profileName, apperrors.ExitUsage)
			}
			updated, removedEnv, removed, err := config.RemoveMapping(profile, args[1])
			if err != nil {
				return apperrors.Usage("profile_remove", err.Error(), "Use ENV_NAME or <secret-name>:<ENV_NAME>")
			}
			if !removed {
				return apperrors.New("profile_remove", apperrors.CodeConfigInvalid, "Mapping not found: "+args[1], "Run: env-vault profile show "+profileName, apperrors.ExitConfigInvalid)
			}
			cfg.Profiles[profileName] = updated
			data := map[string]any{"profile": profileName, "env": removedEnv, "path": path, "dry_run": a.dryRun(cmd)}
			if a.dryRun(cmd) {
				return a.renderer().Success("profile_remove", data, nil)
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			return a.renderer().Success("profile_remove", data, nil)
		},
	}
	return cmd
}

func (a *App) profileShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show <profile>",
		Short: "Show profile mappings without secret values",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return apperrors.Usage("profile_show", "profile show requires exactly one profile name", "Run: env-vault profile show <profile>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, _, err := config.LoadForRead(a.configPath)
			if err != nil {
				return err
			}
			profileName := args[0]
			profile, ok := cfg.Profiles[profileName]
			if !ok {
				return apperrors.New("profile_show", apperrors.CodeProfileNotFound, "Profile not found: "+profileName, "Run: env-vault profile create "+profileName, apperrors.ExitUsage)
			}
			secrets := make([]map[string]string, 0, len(profile.Secrets))
			for _, mapping := range profile.Secrets {
				required := "false"
				if mapping.Required {
					required = "true"
				}
				secrets = append(secrets, map[string]string{
					"name":     mapping.Name,
					"env":      mapping.Env,
					"required": required,
				})
			}
			return a.renderer().Success("profile_show", map[string]any{
				"profile": profileName,
				"path":    path,
				"secrets": secrets,
			}, nil)
		},
	}
}

func (a *App) execCommand() *cobra.Command {
	var directSpecs []string
	var overrideEnv bool
	var cleanEnv bool
	cmd := &cobra.Command{
		Use:   "exec [<profile>] [--secret <secret-name:ENV_NAME> ...] [--override-env] [--clean-env] -- <cmd> [args...]",
		Short: "Execute a command with secrets injected through environment variables",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 0 {
				return apperrors.Usage("exec", "Missing -- delimiter before child command", "Run: env-vault exec [profile] -- <cmd> [args...]")
			}
			if dash > 1 {
				return apperrors.Usage("exec", "exec accepts at most one profile before --", "Run: env-vault exec [profile] -- <cmd> [args...]")
			}
			argv := args[dash:]
			commandRunner := runner.CommandRunner{Stdin: a.stdin, Stdout: a.stdout, Stderr: a.stderr}
			var profileMappings []config.SecretMapping
			if dash == 1 {
				cfg, _, _, err := config.LoadForRead(a.configPath)
				if err != nil {
					return err
				}
				profile, ok := cfg.Profiles[args[0]]
				if !ok {
					return apperrors.New("exec", apperrors.CodeProfileNotFound, "Profile not found: "+args[0], "Run: env-vault profile create "+args[0], apperrors.ExitUsage)
				}
				profileMappings = profile.Secrets
			}
			directMappings := make([]config.SecretMapping, 0, len(directSpecs))
			for _, spec := range directSpecs {
				mapping, err := config.ParseMapping(spec)
				if err != nil {
					return apperrors.Usage("exec", err.Error(), "Use --secret <secret-name>:<ENV_NAME>")
				}
				directMappings = append(directMappings, mapping)
			}
			store, err := a.store("exec")
			if err != nil {
				return err
			}
			resolved, err := runner.Resolve(context.Background(), store, profileMappings, directMappings, runner.ResolveOptions{
				Command:     "exec",
				Service:     secretstore.DefaultService,
				OverrideEnv: overrideEnv,
				CleanEnv:    cleanEnv,
				DryRun:      a.dryRun(cmd),
				CurrentEnv:  a.currentEnv,
			})
			if err != nil {
				return err
			}
			if err := commandRunner.Validate(argv); err != nil {
				return err
			}
			data := execData(argv, resolved, overrideEnv, cleanEnv, a.dryRun(cmd))
			if a.dryRun(cmd) {
				return a.renderer().Success("exec", data, nil)
			}
			exitCode, err := commandRunner.Run(context.Background(), argv, resolved.Env)
			if err != nil {
				return err
			}
			if exitCode != 0 {
				return apperrors.NewExitStatus(exitCode)
			}
			return a.renderer().Success("exec", data, nil)
		},
	}
	cmd.Flags().StringArrayVar(&directSpecs, "secret", nil, "direct secret mapping <secret-name:ENV_NAME>")
	cmd.Flags().BoolVar(&overrideEnv, "override-env", false, "allow secrets to override existing env vars")
	cmd.Flags().BoolVar(&cleanEnv, "clean-env", false, "start the child with a minimal environment")
	return cmd
}

func (a *App) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check non-secret env-vault configuration and backend status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfgPath, cfgExists, cfgErr := config.LoadForRead(a.configPath)
			warnings := []string{}
			if cfgErr != nil {
				if appErr, ok := apperrors.From(cfgErr); ok {
					return appErr
				}
				return cfgErr
			}
			backend := "keyring"
			if teststore.EnabledFromEnv() {
				backend = "test"
			} else if os.Getenv(teststore.BackendEnv) == "pass" {
				backend = "pass"
			}
			store, err := a.store("doctor")
			if err != nil {
				warnings = append(warnings, err.Error())
			} else if _, err := store.List(context.Background(), secretstore.DefaultService); err != nil {
				warnings = append(warnings, "secret backend unavailable")
			}
			return a.renderer().Success("doctor", map[string]any{
				"config_path":   cfgPath,
				"config_exists": cfgExists,
				"backend":       backend,
				"test_backend":  teststore.EnabledFromEnv(),
			}, warnings)
		},
	}
}

func (a *App) store(command string) (secretstore.Store, error) {
	if teststore.EnabledFromEnv() {
		return teststore.NewFromEnv(command)
	}
	if teststore.RequestedFromEnv() {
		return teststore.NewFromEnv(command)
	}
	switch os.Getenv(teststore.BackendEnv) {
	case "", "keyring":
		return keyringstore.New(), nil
	case "pass":
		return keyringstore.NewPass(), nil
	default:
		return nil, apperrors.BackendUnavailable(command, "Unsupported secret backend requested", "Use an allowed production keyring backend or the explicitly gated test backend", secretstore.ErrUnavailable)
	}
}

func (a *App) readSecret(useStdin bool) ([]byte, error) {
	if useStdin {
		value, err := io.ReadAll(a.stdin)
		if err != nil {
			return nil, apperrors.Wrap("secret_set", apperrors.CodeRuntimeError, "Unable to read secret from stdin", "Retry with --stdin and a readable pipe", apperrors.ExitRuntimeError, err)
		}
		if len(value) > 0 && value[len(value)-1] == '\n' {
			value = value[:len(value)-1]
		}
		if len(value) == 0 {
			return nil, apperrors.Usage("secret_set", "Secret input is empty", "Provide secret input through a hidden prompt or --stdin")
		}
		return value, nil
	}
	file, ok := a.stdin.(interface{ Fd() uintptr })
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return nil, apperrors.Usage("secret_set", "Interactive hidden prompt requires a terminal", "Use --stdin when piping secret input")
	}
	fmt.Fprint(a.stderr, "Secret: ")
	value, err := term.ReadPassword(int(file.Fd()))
	fmt.Fprintln(a.stderr)
	if err != nil {
		return nil, apperrors.Wrap("secret_set", apperrors.CodeRuntimeError, "Unable to read hidden secret prompt", "Retry from an interactive terminal or use --stdin", apperrors.ExitRuntimeError, err)
	}
	if len(value) == 0 {
		return nil, apperrors.Usage("secret_set", "Secret input is empty", "Provide a non-empty secret")
	}
	return value, nil
}

func execData(argv []string, resolved runner.ResolveResult, overrideEnv, cleanEnv, dryRun bool) map[string]any {
	secrets := make([]map[string]string, 0, len(resolved.Secrets))
	for _, item := range resolved.Secrets {
		secrets = append(secrets, map[string]string{
			"name":        item.Name,
			"env":         item.Env,
			"fingerprint": item.Fingerprint,
		})
	}
	return map[string]any{
		"argv":         append([]string{}, argv...),
		"secret_count": len(secrets),
		"secrets":      secrets,
		"override_env": overrideEnv,
		"clean_env":    cleanEnv,
		"dry_run":      dryRun,
	}
}

func commandID(cmd *cobra.Command) string {
	if cmd == nil {
		return "root"
	}
	switch cmd.Name() {
	case "version":
		return "version"
	case "set":
		return "secret_set"
	case "check":
		return "secret_check"
	case "delete":
		return "secret_delete"
	case "list":
		return "secret_list"
	case "create":
		return "profile_create"
	case "add":
		return "profile_add"
	case "remove":
		return "profile_remove"
	case "show":
		return "profile_show"
	case "exec":
		return "exec"
	case "doctor":
		return "doctor"
	default:
		return cmd.Name()
	}
}

func wrapUsage(command string, err error, remediation string) error {
	if err == nil {
		return nil
	}
	return apperrors.Usage(command, err.Error(), remediation)
}

func defaultService(service string) string {
	if service == "" {
		return secretstore.DefaultService
	}
	return service
}

func validateService(service string) error {
	return secretstore.ValidateServiceName(service)
}

func missingSecretError(command, service, name string) *apperrors.AppError {
	remediation := "Run: env-vault secret set " + name
	if service != secretstore.DefaultService {
		remediation += " --service " + service
	}
	return apperrors.New(command, apperrors.CodeMissingSecret, "Missing secret: "+name, remediation, apperrors.ExitMissingSecret)
}

func backendUnavailable(command string, err error) *apperrors.AppError {
	return apperrors.BackendUnavailable(command, "Secret backend unavailable", secretstore.BackendRemediation(err), err)
}
