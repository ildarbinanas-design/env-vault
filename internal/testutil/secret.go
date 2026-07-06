package testutil

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func EphemeralValue(t testing.TB) string {
	t.Helper()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("generate ephemeral value: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func AssertNotContains(t testing.TB, label, haystack, value string) {
	t.Helper()
	if value == "" {
		t.Fatalf("generated value is empty")
	}
	if strings.Contains(haystack, value) {
		t.Fatalf("%s contains generated sensitive value", label)
	}
}

func AssertFileNotContains(t testing.TB, label, path, value string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", label, err)
	}
	AssertNotContains(t, label, string(data), value)
}
