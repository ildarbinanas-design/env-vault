package platform

import (
	"slices"
	"testing"
)

func TestCanonicalEnvKeyIsPortableAcrossWindowsCaseRules(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"PATH", "Path", "path"} {
		if got := CanonicalEnvKey(name); got != "PATH" {
			t.Fatalf("CanonicalEnvKey(%q)=%q, want PATH", name, got)
		}
	}
}

func TestMinimalEnvKeepsWindowsPathAndDeduplicatesCaseVariants(t *testing.T) {
	t.Parallel()
	got := MinimalEnv([]string{
		"Path=C:\\Windows\\System32",
		"PATH=/replacement",
		"SystemRoot=C:\\Windows",
		"ComSpec=C:\\Windows\\System32\\cmd.exe",
		"DROP_ME=value",
	})
	want := []string{
		"PATH=/replacement",
		"SystemRoot=C:\\Windows",
		"ComSpec=C:\\Windows\\System32\\cmd.exe",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("MinimalEnv=%v, want %v", got, want)
	}
}
