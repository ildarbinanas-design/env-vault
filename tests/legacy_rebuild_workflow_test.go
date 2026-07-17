package tests

import (
	"slices"
	"strings"
	"testing"
)

func TestLegacyRebuildWorkflowIsSeparateDiagnosticOnlyBreakGlass(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/legacy-rebuild.yml")
	if wf.RunName != "legacy-rebuild version=${{ inputs.version }} source=${{ inputs.source_sha }} plan=${{ inputs.plan_digest }}" {
		t.Fatalf("run-name=%q", wf.RunName)
	}
	wantInputs := []string{"version", "source_sha", "control_sha", "plan_digest", "release_state_digest"}
	if len(wf.On.WorkflowDispatch.Inputs) != len(wantInputs) {
		t.Fatalf("workflow_dispatch inputs=%v", wf.On.WorkflowDispatch.Inputs)
	}
	for _, name := range wantInputs {
		input, ok := wf.On.WorkflowDispatch.Inputs[name]
		if !ok || !input.Required || input.Type != "string" {
			t.Fatalf("input %s=%+v present=%v", name, input, ok)
		}
	}
	if len(wf.Permissions) != 2 || wf.Permissions["contents"] != "read" || wf.Permissions["actions"] != "read" {
		t.Fatalf("permissions=%v", wf.Permissions)
	}
	if wf.Concurrency.Group != "env-vault-legacy-rebuild-${{ inputs.version }}-${{ inputs.plan_digest }}" || wf.Concurrency.CancelInProgress || wf.Concurrency.Group == "env-vault-release" {
		t.Fatalf("concurrency=%+v", wf.Concurrency)
	}
	if len(wf.Jobs) != 4 {
		t.Fatalf("jobs=%v", wf.Jobs)
	}
	for jobName, job := range wf.Jobs {
		if job.Uses != "" || len(job.Permissions) != 0 {
			t.Fatalf("job %s unexpectedly expands capabilities: uses=%q permissions=%v", jobName, job.Uses, job.Permissions)
		}
		for _, step := range job.Steps {
			if step.Uses != "" {
				parts := strings.SplitN(step.Uses, "@", 2)
				pins := map[string]string{
					"actions/checkout":          "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
					"actions/setup-go":          "924ae3a1cded613372ab5595356fb5720e22ba16",
					"actions/upload-artifact":   "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
					"actions/download-artifact": "3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
				}
				want, known := pins[parts[0]]
				if !known || len(parts) != 2 || parts[1] != want {
					t.Fatalf("job %s has unreviewed action %q", jobName, step.Uses)
				}
			}
			for _, forbidden := range []string{"gh release", "/releases", "actions/attest", "sbom-action", "homebrew", "secrets."} {
				if strings.Contains(step.Run, forbidden) || strings.Contains(step.Uses, forbidden) {
					t.Fatalf("job %s step %q contains publication capability %q", jobName, step.Name, forbidden)
				}
			}
		}
	}
}

func TestLegacyRebuildWorkflowSerializesAndNoOpsConcurrentExactPlans(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/legacy-rebuild.yml")
	if wf.Concurrency.Group != "env-vault-legacy-rebuild-${{ inputs.version }}-${{ inputs.plan_digest }}" || wf.Concurrency.CancelInProgress {
		t.Fatalf("concurrency=%+v", wf.Concurrency)
	}
	idempotency := readWorkflowJob(t, "../.github/workflows/legacy-rebuild.yml", "idempotency")
	if idempotency.RunsOn != "ubuntu-latest" || idempotency.TimeoutMinutes != 5 || len(idempotency.Steps) != 1 {
		t.Fatalf("idempotency=%+v", idempotency)
	}
	if idempotency.Outputs["decision"] != "${{ steps.decision.outputs.decision }}" || idempotency.Outputs["prior_run_id"] != "${{ steps.decision.outputs.prior_run_id }}" || idempotency.Outputs["reason_code"] != "${{ steps.decision.outputs.reason_code }}" {
		t.Fatalf("outputs=%v", idempotency.Outputs)
	}
	decision := namedStep(t, idempotency, "Classify the exact plan digest before allocating native runners")
	for _, snippet := range []string{
		"actions/workflows/legacy-rebuild.yml/runs",
		`.total_count >= 0 and .total_count <= 100`,
		`.display_title == $title`,
		`.head_sha == $control_sha`,
		`actions/runs/${candidate}/jobs`,
		`select(.name == "preflight")`,
		`preflight_conclusion=`,
		`queued|requested|waiting|pending)`,
		`failure|cancelled|skipped|timed_out|action_required|startup_failure|stale)`,
		"decision=no_op",
		"legacy_exact_plan_already_started",
		"decision=build",
		"legacy_exact_plan_not_started",
	} {
		if !strings.Contains(decision.Run, snippet) {
			t.Fatalf("idempotency classifier missing %q", snippet)
		}
	}
	if strings.Contains(decision.Run, "gh run rerun") || strings.Contains(decision.Run, "--method POST") {
		t.Fatalf("idempotency classifier acquired mutation capability: %q", decision.Run)
	}

	preflight := readWorkflowJob(t, "../.github/workflows/legacy-rebuild.yml", "preflight")
	if preflight.If != "needs.idempotency.outputs.decision == 'build'" || !slices.Equal(preflight.Needs, []string{"idempotency"}) {
		t.Fatalf("preflight routing if=%q needs=%v", preflight.If, preflight.Needs)
	}
	gate := readWorkflowJob(t, "../.github/workflows/legacy-rebuild.yml", "legacy-gate")
	if !slices.Equal(gate.Needs, []string{"idempotency", "preflight", "build"}) {
		t.Fatalf("gate needs=%v", gate.Needs)
	}
	route := namedStep(t, gate, "Require one build or one deterministic exact-plan no-op")
	for _, snippet := range []string{
		`[[ "$IDEMPOTENCY_RESULT" == "success" ]]`,
		`[[ "$PRIOR_RUN_ID" =~ ^[1-9][0-9]*$ ]]`,
		`[[ "$PREFLIGHT_RESULT" == "skipped" ]]`,
		`[[ "$BUILD_RESULT" == "skipped" ]]`,
	} {
		if !strings.Contains(route.Run, snippet) {
			t.Fatalf("gate routing missing %q", snippet)
		}
	}
	for _, step := range gate.Steps[1:] {
		if step.If != "needs.idempotency.outputs.decision == 'build'" {
			t.Fatalf("expensive/evidence step %q is not build-only: if=%q", step.Name, step.If)
		}
	}
}

func TestLegacyRebuildWorkflowBindsOneAttemptToFiveExactTargets(t *testing.T) {
	preflight := readWorkflowJob(t, "../.github/workflows/legacy-rebuild.yml", "preflight")
	identity := namedStep(t, preflight, "Verify exact plan and control identity")
	for _, snippet := range []string{
		`[[ "$VERSION" =~ ^v0\.0\.[1-7]$ ]]`,
		`[[ "$EVENT_SHA" == "$CONTROL_SHA" ]]`,
	} {
		if !strings.Contains(identity.Run, snippet) {
			t.Fatalf("preflight missing %q", snippet)
		}
	}
	legacy := namedStep(t, preflight, "Validate version, source, toolchain, and proof boundary from the contract")
	for _, snippet := range []string{".version_policy.legacy_rebuild.versions[]", ".tag_sha == $source_sha", ".publication_eligible == false", "literal_version_supported"} {
		if !strings.Contains(legacy.Run, snippet) {
			t.Fatalf("legacy contract validation missing %q", snippet)
		}
	}
	contract := namedStep(t, preflight, "Resolve the five native targets from the release contract")
	if !strings.Contains(contract.Run, "go run ./cmd/release-contract matrix --json") || !strings.Contains(contract.Run, "length == 5") {
		t.Fatalf("contract step=%q", contract.Run)
	}

	build := readWorkflowJob(t, "../.github/workflows/legacy-rebuild.yml", "build")
	if build.Strategy.Matrix.Expression != "${{ fromJSON(needs.preflight.outputs.matrix) }}" || build.Strategy.FailFast == nil || *build.Strategy.FailFast {
		t.Fatalf("build strategy=%+v", build.Strategy)
	}
	setup := usesStep(t, build, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16")
	if setup.With["go-version"] != "${{ needs.preflight.outputs.go_version }}" || setup.With["check-latest"] != "false" {
		t.Fatalf("legacy toolchain=%v", setup.With)
	}
	tag := namedStep(t, build, "Verify immutable tag resolves to the exact source")
	if !strings.Contains(tag.Run, `git rev-parse "${VERSION}^{commit}"`) || !strings.Contains(tag.Run, `== "$SOURCE_SHA"`) {
		t.Fatalf("tag proof=%q", tag.Run)
	}
	version := namedStep(t, build, "Verify tag-era version surfaces and record the proof boundary")
	for _, snippet := range []string{"--json version", "LITERAL_VERSION_SUPPORTED", "historically unavailable", "publication_eligible:false", "LEGACY_PROMOTION_PROOF_UNAVAILABLE"} {
		if !strings.Contains(version.Run, snippet) {
			t.Fatalf("version proof missing %q", snippet)
		}
	}
	upload := namedStep(t, build, "Upload attempt-qualified diagnostic artifact")
	if upload.With["name"] != "env-vault-legacy-${{ matrix.id }}-attempt-${{ github.run_attempt }}" {
		t.Fatalf("artifact name=%q", upload.With["name"])
	}

	gate := readWorkflowJob(t, "../.github/workflows/legacy-rebuild.yml", "legacy-gate")
	if gate.If != "always() && !cancelled()" {
		t.Fatalf("gate if=%q", gate.If)
	}
	download := usesStep(t, gate, "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c")
	if download.With["pattern"] != "env-vault-legacy-*-attempt-${{ github.run_attempt }}" || download.With["merge-multiple"] != "true" {
		t.Fatalf("download=%v", download.With)
	}
	validate := namedStep(t, gate, "Validate the complete current-attempt matrix")
	if !strings.Contains(validate.Run, "${#platforms[@]} -eq 5") || !strings.Contains(validate.Run, `== "15"`) || !strings.Contains(validate.Run, ".publication_eligible == false") {
		t.Fatalf("matrix validation=%q", validate.Run)
	}
}
