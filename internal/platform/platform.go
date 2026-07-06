package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func UserConfigPath() (string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "env-vault", "config.yaml"), nil
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("APPDATA")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(base, "env-vault", "config.yaml"), nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "env-vault", "config.yaml"), nil
}

func MinimalEnv(current []string) []string {
	keep := map[string]bool{
		"PATH":       true,
		"HOME":       true,
		"USER":       true,
		"LOGNAME":    true,
		"TMPDIR":     true,
		"TMP":        true,
		"TEMP":       true,
		"SystemRoot": true,
		"WINDIR":     true,
		"ComSpec":    true,
	}
	out := make([]string, 0, len(keep))
	for _, item := range current {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if keep[name] {
			out = append(out, item)
		}
	}
	return out
}

func IsExecutable(mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return mode&0o111 != 0
}
