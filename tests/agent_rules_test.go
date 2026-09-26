package tests

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// The owner opened a temporary audit autonomy window on 2026-09-26. It must
// close by this deadline even if nobody remembers to close it.
var auditAutonomyWindowDeadline = time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)

const auditAutonomyWindowHeading = "### Audit Autonomy Window"

func TestAuditAutonomyWindowClosesByDeadline(t *testing.T) {
	data, err := os.ReadFile("../AGENTS.md")
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, auditAutonomyWindowHeading) {
		return
	}
	deadline := auditAutonomyWindowDeadline.Format(time.RFC3339)
	if !strings.Contains(text, deadline) {
		t.Fatalf("AGENTS.md audit autonomy window must state its deadline %s", deadline)
	}
	if !time.Now().Before(auditAutonomyWindowDeadline) {
		t.Fatalf("AGENTS.md still opens the audit autonomy window after %s; delete the %q subsection to restore the Standing Delegation", deadline, auditAutonomyWindowHeading)
	}
}

func TestAgentSettingsKeepReservedActionsDenied(t *testing.T) {
	data, err := os.ReadFile("../.claude/settings.json")
	if err != nil {
		t.Fatalf("read .claude/settings.json: %v", err)
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse .claude/settings.json: %v", err)
	}
	if len(settings.Permissions.Allow) != 0 {
		t.Fatalf(".claude/settings.json must not pre-approve tools; allow=%v", settings.Permissions.Allow)
	}
	for _, rule := range []string{
		"Bash(git push --force *)",
		"Bash(git push * refs/tags/*)",
		"Bash(gh release create *)",
		"Bash(gh release delete *)",
		"Bash(gh workflow run build-binaries.yml *)",
		"Bash(go run ./cmd/actionsartifactdelete *)",
	} {
		if !slices.Contains(settings.Permissions.Deny, rule) {
			t.Fatalf(".claude/settings.json must deny %s", rule)
		}
	}
}
