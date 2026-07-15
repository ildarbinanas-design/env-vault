package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CanonicalEnvKey defines the portable environment-variable identity used by
// configs and child-process construction. Windows treats environment names as
// case-insensitive, so using the same rule on every host prevents a config that
// is valid on Unix from becoming ambiguous when moved to Windows.
func CanonicalEnvKey(name string) string {
	return strings.ToUpper(name)
}

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
		"SYSTEMROOT": true,
		"WINDIR":     true,
		"COMSPEC":    true,
	}
	out := make([]string, 0, len(keep))
	positions := make(map[string]int, len(keep))
	for _, item := range current {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		canonical := CanonicalEnvKey(name)
		if !keep[canonical] {
			continue
		}
		if position, exists := positions[canonical]; exists {
			out[position] = item
			continue
		}
		positions[canonical] = len(out)
		out = append(out, item)
	}
	return out
}

func IsExecutable(mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return mode&0o111 != 0
}
