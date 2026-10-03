package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestUnknownFlagOnRootCommand(t *testing.T) {
	assertUnknownFlag(t, []string{"--unknown"}, "env-vault")
}

func TestUnknownFlagOnNestedCommand(t *testing.T) {
	assertUnknownFlag(t, []string{"secret", "--unknown"}, "env-vault secret")
}

func TestUnknownFlagRemediationDoesNotDuplicateProgramName(t *testing.T) {
	for _, args := range [][]string{
		{"--unknown"},
		{"secret", "--unknown"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Fatalf("Run(%v) code=%d, want 2", args, code)
		}
		if strings.Contains(stderr.String(), "env-vault env-vault") {
			t.Fatalf("Run(%v) duplicated program name in remediation: %q", args, stderr.String())
		}
	}
}

func assertUnknownFlag(t *testing.T, args []string, commandPath string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("Run(%v) code=%d, want 2", args, code)
	}
	if stdout.String() != "" {
		t.Fatalf("Run(%v) stdout=%q, want empty", args, stdout.String())
	}
	want := "code=USAGE\n" +
		"message=unknown flag: --unknown\n" +
		"remediation=Run: " + commandPath + " --help\n"
	if stderr.String() != want {
		t.Fatalf("Run(%v) stderr=%q, want %q", args, stderr.String(), want)
	}
}

func TestUsageFormattingAndFlagBoundary(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "unknown"},
		{"--unknown", "--json"},
		{"secret", "--unknown", "--jsonl"},
		{"unknown", "--json"},
		{"secret", "unknown", "--json"},
		{"doctor", "extra", "--json"},
		{"secret", "list", "extra", "--jsonl"},
		{"version", "extra", "--json"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Fatalf("%v: exit=%d", args, code)
		}
		if !strings.Contains(stdout.String(), `"code":"USAGE"`) || stderr.Len() != 0 {
			t.Fatalf("%v: missing machine usage envelope", args)
		}
	}
	for _, args := range [][]string{
		{"unknown", "--", "--json"},
		{"--unknown", "--", "--json"},
		{"--unknown", "--config", "--json"},
		{"doctor", "--", "--json"},
		{"doctor", "--config", "--json", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Fatalf("%v: exit=%d", args, code)
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "code=USAGE") {
			t.Fatalf("%v: treated a value or post-delimiter argument as --json", args)
		}
	}
}
