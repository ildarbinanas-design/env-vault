package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/platform"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"gopkg.in/yaml.v3"
)

const (
	Version   = 1
	LocalFile = ".env-vault.yaml"
)

var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type File struct {
	Version  int                `yaml:"version"`
	Profiles map[string]Profile `yaml:"profiles"`
}

type Profile struct {
	Description string          `yaml:"description,omitempty"`
	Secrets     []SecretMapping `yaml:"secrets,omitempty"`
}

type SecretMapping struct {
	Name     string `yaml:"name"`
	Env      string `yaml:"env"`
	Required bool   `yaml:"required"`
}

func Empty() *File {
	return &File{
		Version:  Version,
		Profiles: map[string]Profile{},
	}
}

func ParseMapping(spec string) (SecretMapping, error) {
	name, envName, ok := strings.Cut(spec, ":")
	if !ok {
		return SecretMapping{}, fmt.Errorf("mapping must use <secret-name>:<ENV_NAME>")
	}
	if err := ValidateSecretName(name); err != nil {
		return SecretMapping{}, err
	}
	if err := ValidateEnvName(envName); err != nil {
		return SecretMapping{}, err
	}
	return SecretMapping{Name: name, Env: envName, Required: true}, nil
}

func ValidateSecretName(name string) error {
	return secretstore.ValidateSecretName(name)
}

func ValidateEnvName(name string) error {
	if name == "" {
		return fmt.Errorf("env var name is empty")
	}
	if !envNameRE.MatchString(name) {
		return fmt.Errorf("env var name must match [A-Za-z_][A-Za-z0-9_]*")
	}
	return nil
}

func LoadForRead(explicitPath string) (*File, string, bool, error) {
	path, exists, err := ResolveReadPath(explicitPath)
	if err != nil {
		return nil, "", false, err
	}
	if !exists {
		return Empty(), path, false, nil
	}
	cfg, err := Load(path)
	return cfg, path, true, err
}

func Load(path string) (*File, error) {
	data, err := readConfigFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Empty(), nil
		}
		return nil, apperrors.ConfigInvalid("config", "Unable to read config", "Check the config path and permissions", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cfg File
	if err := decoder.Decode(&cfg); err != nil {
		return nil, apperrors.ConfigInvalid("config", "Invalid YAML config", "Fix the YAML syntax and schema", err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	if err := Validate(&cfg); err != nil {
		return nil, apperrors.ConfigInvalid("config", "Invalid config", "Fix profile mappings and schema version", err)
	}
	return &cfg, nil
}

func Save(path string, cfg *File) error {
	if cfg == nil {
		cfg = Empty()
	}
	if cfg.Version == 0 {
		cfg.Version = Version
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	if err := Validate(cfg); err != nil {
		return apperrors.ConfigInvalid("config", "Invalid config", "Fix profile mappings before saving", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return apperrors.ConfigInvalid("config", "Unable to create config directory", "Check directory permissions", err)
	}
	if err := validateConfigTarget(path); err != nil {
		return configTargetValidationError(err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return apperrors.ConfigInvalid("config", "Unable to encode config", "Report this bug with no secret values", err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return apperrors.ConfigInvalid("config", "Unable to create temporary config", "Check directory permissions", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return apperrors.ConfigInvalid("config", "Unable to secure temporary config", "Check filesystem permissions", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return apperrors.ConfigInvalid("config", "Unable to write config", "Check the config path and permissions", err)
	}
	if err := temporary.Sync(); err != nil {
		return apperrors.ConfigInvalid("config", "Unable to sync config", "Check filesystem health and permissions", err)
	}
	if err := temporary.Close(); err != nil {
		return apperrors.ConfigInvalid("config", "Unable to close config", "Check filesystem health and permissions", err)
	}
	if err := runE2ESaveCrashHook(); err != nil {
		return apperrors.ConfigInvalid("config", "Unable to complete gated E2E save hook", "Use the E2E runner or unset E2E hook variables", err)
	}
	// Recheck immediately before every replacement attempt. Replacement
	// replaces a raced-in symlink itself instead of following it, while this
	// check gives callers a clear failure for a symlink that was already
	// present. Windows may briefly deny replacement while a scanner holds the
	// target, so only known transient Windows filesystem errors receive bounded
	// retries.
	unsafeTarget, err := replaceConfigFile(temporaryPath, path)
	if unsafeTarget {
		return apperrors.ConfigInvalid("config", "Unsafe config target", "Use a regular config file, not a symlink", err)
	}
	if err != nil {
		return apperrors.ConfigInvalid("config", "Unable to replace config safely", "Check the config path and permissions", err)
	}
	committed = true
	return nil
}

func validateSaveTarget(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &unsafeConfigTargetError{reason: "config path is a symlink"}
	}
	if !info.Mode().IsRegular() {
		return &unsafeConfigTargetError{reason: "config path is not a regular file"}
	}
	return nil
}

func configTargetValidationError(err error) error {
	if isUnsafeConfigTargetError(err) {
		return apperrors.ConfigInvalid("config", "Unsafe config target", "Use a regular config file, not a symlink", err)
	}
	return apperrors.ConfigInvalid("config", "Unable to inspect config target", "Check the config path and permissions", err)
}

func Validate(cfg *File) error {
	if cfg.Version != Version {
		return fmt.Errorf("unsupported config version %d", cfg.Version)
	}
	for profileName, profile := range cfg.Profiles {
		if profileName == "" {
			return fmt.Errorf("profile name is empty")
		}
		seenEnv := map[string]string{}
		for _, mapping := range profile.Secrets {
			if err := ValidateSecretName(mapping.Name); err != nil {
				return fmt.Errorf("profile %s: %w", profileName, err)
			}
			if err := ValidateEnvName(mapping.Env); err != nil {
				return fmt.Errorf("profile %s: %w", profileName, err)
			}
			canonicalEnv := platform.CanonicalEnvKey(mapping.Env)
			if prior, ok := seenEnv[canonicalEnv]; ok {
				return fmt.Errorf("profile %s: duplicate env vars %s and %s differ only by case", profileName, prior, mapping.Env)
			}
			seenEnv[canonicalEnv] = mapping.Env
		}
	}
	return nil
}

func ResolveReadPath(explicitPath string) (string, bool, error) {
	if explicitPath != "" {
		return explicitPath, fileExists(explicitPath), nil
	}
	local := filepath.Join(".", LocalFile)
	if fileExists(local) {
		return local, true, nil
	}
	userPath, err := platform.UserConfigPath()
	if err != nil {
		return "", false, apperrors.ConfigInvalid("config", "Unable to resolve user config path", "Set --config explicitly", err)
	}
	return userPath, fileExists(userPath), nil
}

func ResolveCreatePath(explicitPath string, local, global bool) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}
	if local && global {
		return "", apperrors.Usage("profile_create", "Use only one of --local or --global", "Choose one config target")
	}
	if global {
		return platform.UserConfigPath()
	}
	return filepath.Join(".", LocalFile), nil
}

func ProfileNames(cfg *File) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func RemoveMapping(profile Profile, selector string) (Profile, string, bool, error) {
	var wanted SecretMapping
	var byPair bool
	if strings.Contains(selector, ":") {
		mapping, err := ParseMapping(selector)
		if err != nil {
			return profile, "", false, err
		}
		wanted = mapping
		byPair = true
	} else {
		if err := ValidateEnvName(selector); err != nil {
			if nameErr := ValidateSecretName(selector); nameErr != nil {
				return profile, "", false, err
			}
		}
	}
	out := profile.Secrets[:0]
	var removedEnv string
	removed := false
	for _, mapping := range profile.Secrets {
		match := false
		if byPair {
			match = mapping.Name == wanted.Name && mapping.Env == wanted.Env
		} else {
			match = mapping.Env == selector || mapping.Name == selector
		}
		if match {
			removed = true
			removedEnv = mapping.Env
			continue
		}
		out = append(out, mapping)
	}
	profile.Secrets = out
	return profile, removedEnv, removed, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
