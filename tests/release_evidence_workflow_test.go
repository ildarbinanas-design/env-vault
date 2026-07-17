package tests

import (
	"strings"
	"testing"
)

func TestReleaseEvidenceWorkflowIsAutomaticReadOnlyAndAttemptQualified(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/release-evidence.yml")
	if len(wf.On.WorkflowRun.Workflows) != 1 || wf.On.WorkflowRun.Workflows[0] != "build-binaries" || len(wf.On.WorkflowRun.Types) != 1 || wf.On.WorkflowRun.Types[0] != "completed" {
		t.Fatalf("workflow_run trigger=%+v", wf.On.WorkflowRun)
	}
	wantPermissions := map[string]string{"actions": "read", "attestations": "read", "contents": "read", "pull-requests": "read"}
	if len(wf.Permissions) != len(wantPermissions) {
		t.Fatalf("permissions=%v", wf.Permissions)
	}
	for name, want := range wantPermissions {
		if wf.Permissions[name] != want {
			t.Fatalf("permission %s=%q want %q", name, wf.Permissions[name], want)
		}
	}
	if wf.Concurrency.Group != "release-evidence-${{ github.event.workflow_run.id }}-${{ github.event.workflow_run.run_attempt }}" || wf.Concurrency.CancelInProgress || wf.Concurrency.Group == "env-vault-release" {
		t.Fatalf("concurrency=%+v", wf.Concurrency)
	}
	if len(wf.Jobs) != 1 {
		t.Fatalf("jobs=%v", wf.Jobs)
	}
	job := readWorkflowJob(t, "../.github/workflows/release-evidence.yml", "collect")
	for _, required := range []string{"github.event.workflow_run.event == 'push'", "startsWith(github.event.workflow_run.head_branch, 'v')", "github.event.workflow_run.event == 'workflow_dispatch'"} {
		if !strings.Contains(job.If, required) {
			t.Fatalf("job condition %q lacks %q", job.If, required)
		}
	}
	checkout := usesStep(t, job, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	if checkout.With["ref"] != "${{ github.event.workflow_run.head_sha }}" || checkout.With["persist-credentials"] != "false" {
		t.Fatalf("checkout=%v", checkout.With)
	}
	setup := usesStep(t, job, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16")
	if setup.With["go-version-file"] != "go.mod" || setup.With["cache"] != "false" {
		t.Fatalf("setup-go=%v", setup.With)
	}
	identity := namedStep(t, job, "Resolve exact publisher, tag, and main CI identity")
	if !strings.Contains(identity.Run, `go run ./cmd/release-evidence trigger --event "$GITHUB_EVENT_PATH"`) || !strings.Contains(identity.Run, `>> "$GITHUB_OUTPUT"`) {
		t.Fatalf("identity step=%+v", identity)
	}
	download := namedStep(t, job, "Download exact published release assets")
	if download.If != "steps.identity.outputs.publisher_conclusion == 'success'" || download.Env["VERSION"] != "${{ steps.identity.outputs.version }}" || !strings.Contains(download.Run, `gh release download "$VERSION"`) || !strings.Contains(download.Run, `--repo "$REPOSITORY"`) {
		t.Fatalf("release download step=%+v", download)
	}
	promotion := namedStep(t, job, "Download exact main CI promotion manifest")
	if promotion.If != "steps.identity.outputs.publisher_conclusion == 'success'" || !strings.Contains(promotion.Run, `gh run download "${{ steps.identity.outputs.ci_run_id }}"`) || !strings.Contains(promotion.Run, `env-vault-promotion-${{ steps.identity.outputs.source_sha }}-attempt-${{ steps.identity.outputs.ci_run_attempt }}`) {
		t.Fatalf("promotion download step=%+v", promotion)
	}
	collect := namedStep(t, job, "Collect exact release evidence and metrics")
	for _, required := range []string{
		"go run ./cmd/release-evidence collect", `--source-sha "$SOURCE_SHA"`, `--publisher-run-id "$PUBLISHER_RUN_ID"`, `--publisher-run-attempt "$PUBLISHER_RUN_ATTEMPT"`, "--promotion-manifest promotion-manifest/promotion-manifest.json",
		"go run ./cmd/release-evidence validate", "go run ./cmd/release-evidence index",
	} {
		if !strings.Contains(collect.Run, required) {
			t.Fatalf("collector step lacks %q", required)
		}
	}
	for _, forbidden := range []string{"repair apply", "workflow run", "workflow dispatch", "gh api --method POST", "secrets."} {
		if strings.Contains(collect.Run, forbidden) {
			t.Fatalf("collector step contains mutation capability %q", forbidden)
		}
	}
	upload := usesStep(t, job, "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a")
	if upload.With["name"] != "release-evidence-${{ github.event.workflow_run.id }}-attempt-${{ github.event.workflow_run.run_attempt }}" || upload.With["if-no-files-found"] != "error" {
		t.Fatalf("upload=%v", upload.With)
	}
}

func TestSourceQualityValidatesCheckedInEvidenceWithoutExecutingRecordCommands(t *testing.T) {
	race := readWorkflowJob(t, "../.github/workflows/reusable-quality.yml", "race")
	step := namedStep(t, race, "Validate checked-in machine evidence")
	if strings.TrimSpace(step.Run) != "go run ./cmd/release-evidence validate-records" {
		t.Fatalf("machine evidence validation step=%+v", step)
	}
	for _, forbidden := range []string{"jq -r .checks", "eval", "bash -c", "sh -c", "xargs"} {
		if strings.Contains(step.Run, forbidden) {
			t.Fatalf("machine evidence validator can execute record commands via %q", forbidden)
		}
	}
}
