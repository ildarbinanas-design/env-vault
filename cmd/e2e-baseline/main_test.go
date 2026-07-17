package main

import (
	"bytes"
	"testing"
)

func TestRequiresCommandAndProof(t *testing.T) {
	var output bytes.Buffer
	if err := run(nil, &output, &output); err == nil {
		t.Fatal("missing command was accepted")
	}
	if err := run([]string{"verify"}, &output, &output); err == nil {
		t.Fatal("missing proof was accepted")
	}
}
