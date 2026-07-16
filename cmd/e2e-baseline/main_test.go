package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsMissingAndUnknownCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"generate"}, {"verify"}} {
		var stdout, stderr bytes.Buffer
		if err := run(args, &stdout, &stderr); err == nil {
			t.Fatalf("run(%v) unexpectedly succeeded", args)
		}
		if stdout.Len() != 0 || strings.Contains(stderr.String(), "secret") {
			t.Fatalf("unexpected output stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
}
