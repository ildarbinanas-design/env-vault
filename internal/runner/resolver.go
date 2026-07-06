package runner

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/config"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/platform"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

type ResolveOptions struct {
	Command     string
	Service     string
	OverrideEnv bool
	CleanEnv    bool
	DryRun      bool
	CurrentEnv  []string
}

type ResolvedSecret struct {
	Name        string
	Env         string
	Fingerprint string
}

type ResolveResult struct {
	Env      []string
	Secrets  []ResolvedSecret
	DryRun   bool
	Warnings []string
}

func Resolve(ctx context.Context, store secretstore.Store, profileMappings, directMappings []config.SecretMapping, opts ResolveOptions) (ResolveResult, error) {
	if opts.Command == "" {
		opts.Command = "exec"
	}
	if opts.Service == "" {
		opts.Service = secretstore.DefaultService
	}
	base := opts.CurrentEnv
	if opts.CleanEnv {
		base = platform.MinimalEnv(base)
	}
	envMap := envSliceToMap(base)
	mappings := append([]config.SecretMapping{}, profileMappings...)
	mappings = append(mappings, directMappings...)
	if err := validateMappings(mappings, opts.Command); err != nil {
		return ResolveResult{}, err
	}
	result := ResolveResult{
		Env:    append([]string{}, base...),
		DryRun: opts.DryRun,
	}
	for _, mapping := range mappings {
		if _, exists := envMap[mapping.Env]; exists && !opts.OverrideEnv {
			return ResolveResult{}, apperrors.New(opts.Command, apperrors.CodeEnvCollision, "Environment variable already exists: "+mapping.Env, "Use --override-env or choose a different target env var", apperrors.ExitUsage)
		}
		exists, err := store.Exists(ctx, opts.Service, mapping.Name)
		if err != nil {
			return ResolveResult{}, apperrors.BackendUnavailable(opts.Command, "Secret backend unavailable", "Run env-vault doctor or configure the OS keychain", err)
		}
		if !exists {
			if mapping.Required {
				return ResolveResult{}, missingSecretError(opts.Command, opts.Service, mapping.Name)
			}
			continue
		}
		fingerprint := secretstore.Fingerprint(opts.Service, mapping.Name)
		result.Secrets = append(result.Secrets, ResolvedSecret{
			Name:        mapping.Name,
			Env:         mapping.Env,
			Fingerprint: fingerprint,
		})
		if opts.DryRun {
			continue
		}
		value, err := store.Get(ctx, opts.Service, mapping.Name)
		if stderrors.Is(err, secretstore.ErrNotFound) {
			return ResolveResult{}, missingSecretError(opts.Command, opts.Service, mapping.Name)
		}
		if err != nil {
			return ResolveResult{}, apperrors.BackendUnavailable(opts.Command, "Secret backend unavailable", "Run env-vault doctor or configure the OS keychain", err)
		}
		setEnv(&result.Env, envMap, mapping.Env, string(value))
	}
	return result, nil
}

func validateMappings(mappings []config.SecretMapping, command string) error {
	seenEnv := map[string]string{}
	for _, mapping := range mappings {
		if err := config.ValidateSecretName(mapping.Name); err != nil {
			return apperrors.ConfigInvalid(command, "Invalid secret mapping", "Use <secret-name>:<ENV_NAME>", err)
		}
		if err := config.ValidateEnvName(mapping.Env); err != nil {
			return apperrors.ConfigInvalid(command, "Invalid secret mapping", "Use <secret-name>:<ENV_NAME>", err)
		}
		if prior, ok := seenEnv[mapping.Env]; ok {
			return apperrors.ConfigInvalid(command, "Duplicate target env var: "+mapping.Env, fmt.Sprintf("Remove duplicate mappings for %s and %s", prior, mapping.Name), nil)
		}
		seenEnv[mapping.Env] = mapping.Name
	}
	return nil
}

func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			m[key] = value
		}
	}
	return m
}

func setEnv(env *[]string, envMap map[string]string, key, value string) {
	prefix := key + "="
	for i, item := range *env {
		if strings.HasPrefix(item, prefix) {
			(*env)[i] = prefix + value
			envMap[key] = value
			return
		}
	}
	*env = append(*env, prefix+value)
	envMap[key] = value
}

func missingSecretError(command, service, name string) *apperrors.AppError {
	remediation := "Run: env-vault secret set " + name
	if service != secretstore.DefaultService {
		remediation += " --service " + service
	}
	return apperrors.New(command, apperrors.CodeMissingSecret, "Missing secret: "+name, remediation, apperrors.ExitMissingSecret)
}
