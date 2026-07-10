package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionFlagMatchesVersionCommand(t *testing.T) {
	oldVersion := Version
	Version = "v-test"
	t.Cleanup(func() { Version = oldVersion })

	var flagOut, flagErr bytes.Buffer
	if code := Run([]string{"--version"}, strings.NewReader(""), &flagOut, &flagErr); code != 0 {
		t.Fatalf("--version code=%d stderr=%s", code, flagErr.String())
	}
	var cmdOut, cmdErr bytes.Buffer
	if code := Run([]string{"version"}, strings.NewReader(""), &cmdOut, &cmdErr); code != 0 {
		t.Fatalf("version code=%d stderr=%s", code, cmdErr.String())
	}
	if flagOut.String() != cmdOut.String() {
		t.Fatalf("--version output %q differs from version output %q", flagOut.String(), cmdOut.String())
	}
	if strings.TrimSpace(flagOut.String()) != "v-test" {
		t.Fatalf("unexpected --version output: %q", flagOut.String())
	}
}

func TestVersionFlagJSON(t *testing.T) {
	oldVersion := Version
	Version = "v-test"
	t.Cleanup(func() { Version = oldVersion })

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--json", "--version"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{`"version":"v-test"`, `"command":"version"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %s: %s", want, stdout.String())
		}
	}
}

func TestResolveVersionPrefersLdflags(t *testing.T) {
	oldVersion := Version
	Version = "v9.9.9"
	t.Cleanup(func() { Version = oldVersion })

	if got := resolveVersion(); got != "v9.9.9" {
		t.Fatalf("resolveVersion()=%q, want ldflags value v9.9.9", got)
	}
}

func TestResolveVersionSourceBuildIsNeverEmpty(t *testing.T) {
	oldVersion := Version
	Version = "dev"
	t.Cleanup(func() { Version = oldVersion })

	got := resolveVersion()
	if got == "" || got == "(devel)" {
		t.Fatalf("resolveVersion()=%q, want a usable fallback", got)
	}
	if !strings.HasPrefix(got, "dev") && !strings.HasPrefix(got, "v") {
		t.Fatalf("resolveVersion()=%q, want dev* or v* form", got)
	}
}
