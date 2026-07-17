package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasesettings"
)

const (
	manifestV007  = "{\n  \".\": \"0.0.7\"\n}\n"
	readmeV007    = "# fixture\n\nCurrent version: `v0.0.7`. <!-- x-release-please-version -->\n"
	changelogV007 = "# Changelog\n\n" +
		"## [0.0.7](https://example.invalid/compare/v0.0.6...v0.0.7) (2026-07-15)\n\n" +
		"- Previous release.\n"
)

func TestClassifyReleaseCommit(t *testing.T) {
	t.Run("ordinary green main commit is planning only", func(t *testing.T) {
		repo := newReleaseAutomationRepo(t)
		writeReleaseFixture(t, repo, "code.txt", "changed\n")
		gitFixture(t, repo, "add", "code.txt")
		gitFixture(t, repo, "commit", "-m", "fix: ordinary change")

		output, err := runReleaseAutomationScript(t, repo, "classify-release-commit.sh", "HEAD")
		if err != nil {
			t.Fatalf("classify ordinary commit: %v\n%s", err, output)
		}
		if !strings.Contains(output, "publish=false\n") || !strings.Contains(output, "version=\n") {
			t.Fatalf("ordinary classification=%q", output)
		}
	})

	t.Run("reviewed release commit returns exact tag inputs", func(t *testing.T) {
		repo := newReleaseAutomationRepo(t)
		commitReleaseFixture(t, repo, releaseMutation{})

		output, err := runReleaseAutomationScript(t, repo, "classify-release-commit.sh", "HEAD")
		if err != nil {
			t.Fatalf("classify release commit: %v\n%s", err, output)
		}
		if !strings.Contains(output, "publish=true\n") || !strings.Contains(output, "version=v0.0.8\n") {
			t.Fatalf("release classification=%q", output)
		}
		sha := strings.TrimSpace(gitFixture(t, repo, "rev-parse", "HEAD"))
		if !strings.Contains(output, "source_sha="+sha+"\n") {
			t.Fatalf("release classification does not bind exact SHA: %q", output)
		}
	})

	cases := []struct {
		name     string
		mutation releaseMutation
	}{
		{name: "non-release subject", mutation: releaseMutation{subject: "chore: update files"}},
		{name: "version downgrade", mutation: releaseMutation{version: "0.0.6"}},
		{name: "stale README", mutation: releaseMutation{staleREADME: true}},
		{name: "missing changelog section", mutation: releaseMutation{missingChangelog: true}},
		{name: "empty changelog section", mutation: releaseMutation{emptyChangelog: true}},
		{name: "unexpected source path", mutation: releaseMutation{unexpectedPath: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name+" fails closed", func(t *testing.T) {
			repo := newReleaseAutomationRepo(t)
			commitReleaseFixture(t, repo, tc.mutation)
			output, err := runReleaseAutomationScript(t, repo, "classify-release-commit.sh", "HEAD")
			if err == nil {
				t.Fatalf("classification unexpectedly succeeded: %s", output)
			}
			if !strings.Contains(output, "release:") {
				t.Fatalf("classification failure is not structured: %q", output)
			}
		})
	}
}

func TestExtractChangelogSection(t *testing.T) {
	directory := t.TempDir()
	changelog := filepath.Join(directory, "CHANGELOG.md")
	writeReleaseFixture(t, directory, "CHANGELOG.md", "# Changelog\n\n"+
		"## [0.0.8](https://example.invalid/compare/v0.0.7...v0.0.8) (2026-07-16)\n\n"+
		"### Bug Fixes\n\n- Automate releases.\n\n"+
		"## [0.0.7](https://example.invalid/compare/v0.0.6...v0.0.7) (2026-07-15)\n\n- Previous release.\n")

	output, err := runReleaseAutomationScript(t, directory, "extract-changelog-section.sh", "v0.0.8", changelog)
	if err != nil {
		t.Fatalf("extract changelog: %v\n%s", err, output)
	}
	want := "### Bug Fixes\n\n- Automate releases.\n"
	if output != want {
		t.Fatalf("release notes=%q, want %q", output, want)
	}

	if output, err := runReleaseAutomationScript(t, directory, "extract-changelog-section.sh", "v0.0.9", changelog); err == nil {
		t.Fatalf("missing changelog section unexpectedly succeeded: %s", output)
	}
	writeReleaseFixture(t, directory, "CHANGELOG.md", "# Changelog\n\n## [0.0.8]\n\n- One.\n\n## [0.0.8]\n\n- Two.\n")
	if output, err := runReleaseAutomationScript(t, directory, "extract-changelog-section.sh", "v0.0.8", changelog); err == nil {
		t.Fatalf("duplicate changelog section unexpectedly succeeded: %s", output)
	}
}

func TestCurrentReleaseHasExtractableReviewedNotes(t *testing.T) {
	changelog, err := filepath.Abs("../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	output, err := runReleaseAutomationScript(t, t.TempDir(), "extract-changelog-section.sh", "v0.0.7", changelog)
	if err != nil {
		t.Fatalf("extract current release notes: %v\n%s", err, output)
	}
	if !strings.Contains(output, "fix(security): harden runtime and Homebrew release contract") {
		t.Fatalf("current release notes are incomplete: %q", output)
	}
}

func TestCurrentREADMEUsesSharedReleaseVersionLine(t *testing.T) {
	manifestData, err := os.ReadFile("../.release-please-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	version, ok := manifest["."]
	if !ok || len(manifest) != 1 {
		t.Fatalf("release manifest=%v, want one root version", manifest)
	}

	library, err := filepath.Abs("../scripts/release/lib.sh")
	if err != nil {
		t.Fatal(err)
	}
	format := exec.Command("bash", "-c", `source "$1"; release_readme_version_line "$2"`, "bash", library, "v"+version)
	formatted, err := format.CombinedOutput()
	if err != nil {
		t.Fatalf("format README version line: %v\n%s", err, formatted)
	}
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(readme), string(formatted)); count != 1 {
		t.Fatalf("README shared version line count=%d, want 1; line=%q", count, formatted)
	}
}

func TestVerifyReleaseAuthorization(t *testing.T) {
	const sourceSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const releasePRHeadSHA = "ffffffffffffffffffffffffffffffffffffffff"
	commandDir := installFakeReleaseGH(t)
	baseEnv := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"RELEASE_APP_SLUG=env-vault-release-planning",
		"FAKE_SOURCE_SHA=" + sourceSHA,
		"FAKE_MAIN_SHA=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"FAKE_MANIFEST_VERSION=0.0.8",
		"FAKE_COMPARE_STATUS=ahead",
		"FAKE_CI_CONCLUSION=success",
		"FAKE_PR_AUTHOR=env-vault-release-planning[bot]",
		"FAKE_PR_LABEL=autorelease: pending",
		"FAKE_PR_HEAD_SHA=" + releasePRHeadSHA,
		"FAKE_PR_MERGED_AT=2026-07-16T00:00:00Z",
		"FAKE_CONFIRMATION_ACTOR=ildarbinanas-design",
		"FAKE_CONFIRMATION_ASSOCIATION=OWNER",
		"FAKE_CONFIRMATION_USER_TYPE=User",
		"FAKE_CONFIRMATION_CREATED_AT=2026-07-15T23:59:00Z",
		"FAKE_CONFIRMATION_UPDATED_AT=2026-07-15T23:59:00Z",
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}

	authorizationOutput := filepath.Join(t.TempDir(), "release-authorization-checkpoint.json")
	authorizationEnv := append([]string{}, baseEnv...)
	authorizationEnv = append(authorizationEnv, "RELEASE_AUTHORIZATION_OUTPUT="+authorizationOutput)
	output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), authorizationEnv, "verify-release-authorization.sh", "v0.0.8", sourceSHA, "prepublish")
	if err != nil {
		t.Fatalf("verify valid release authorization: %v\n%s", err, output)
	}
	if output != "42\n" {
		t.Fatalf("authorized PR=%q, want 42", output)
	}
	checkpointData, err := os.ReadFile(authorizationOutput)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint struct {
		Repository         string `json:"repository"`
		ReleaseVersion     string `json:"release_version"`
		ReleaseSourceSHA   string `json:"release_source_sha"`
		GeneratedReleasePR struct {
			Number   int64  `json:"number"`
			HeadSHA  string `json:"head_sha"`
			MergeSHA string `json:"merge_sha"`
			MergedAt string `json:"merged_at"`
		} `json:"generated_release_pr"`
		Confirmation struct {
			CommentID        int64  `json:"comment_id"`
			URL              string `json:"url"`
			Actor            string `json:"actor"`
			ActorAssociation string `json:"actor_association"`
			CreatedAt        string `json:"created_at"`
			UpdatedAt        string `json:"updated_at"`
			BodySHA256       string `json:"body_sha256"`
		} `json:"confirmation"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(checkpointData, &checkpoint); err != nil {
		t.Fatalf("decode authorization checkpoint: %v", err)
	}
	canonicalBody := "ПОДТВЕРЖДАЮ RELEASE v0.0.8 PR #42 SHA " + releasePRHeadSHA
	bodyDigest := sha256.Sum256([]byte(canonicalBody))
	if checkpoint.Repository != "example/env-vault" || checkpoint.ReleaseVersion != "v0.0.8" ||
		checkpoint.ReleaseSourceSHA != sourceSHA || checkpoint.GeneratedReleasePR.Number != 42 ||
		checkpoint.GeneratedReleasePR.HeadSHA != releasePRHeadSHA || checkpoint.GeneratedReleasePR.MergeSHA != sourceSHA ||
		checkpoint.GeneratedReleasePR.MergedAt != "2026-07-16T00:00:00Z" || checkpoint.Confirmation.CommentID != 9001 ||
		checkpoint.Confirmation.URL != "https://github.com/example/env-vault/pull/42#issuecomment-9001" ||
		checkpoint.Confirmation.Actor != "ildarbinanas-design" || checkpoint.Confirmation.ActorAssociation != "OWNER" ||
		checkpoint.Confirmation.CreatedAt != "2026-07-15T23:59:00Z" || checkpoint.Confirmation.UpdatedAt != "2026-07-15T23:59:00Z" ||
		checkpoint.Confirmation.BodySHA256 != hex.EncodeToString(bodyDigest[:]) || checkpoint.Result != "pass" {
		t.Fatalf("authorization checkpoint is not exact: %+v", checkpoint)
	}
	taggedEnv := append([]string{}, baseEnv...)
	taggedEnv = append(taggedEnv, "FAKE_PR_LABEL=autorelease: tagged")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), taggedEnv, "verify-release-authorization.sh", "v0.0.8", sourceSHA, "tagged"); err != nil || output != "42\n" {
		t.Fatalf("verify tagged release authorization: %v\n%s", err, output)
	}
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), baseEnv, "verify-release-authorization.sh", "v0.0.8", sourceSHA, "tagged"); err == nil {
		t.Fatalf("pending release unexpectedly authorized for publication: %s", output)
	}
	advancedMainEnv := append([]string{}, taggedEnv...)
	advancedMainEnv = append(advancedMainEnv,
		"FAKE_SOURCE_MANIFEST_VERSION=0.0.8",
		"FAKE_MAIN_MANIFEST_VERSION=0.0.9",
	)
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), advancedMainEnv, "verify-release-authorization.sh", "v0.0.8", sourceSHA, "tagged"); err != nil || output != "42\n" {
		t.Fatalf("verify tagged immutable release after main advances: %v\n%s", err, output)
	}
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), advancedMainEnv, "verify-release-authorization.sh", "v0.0.8", sourceSHA, "prepublish"); err == nil {
		t.Fatalf("prepublish authorization ignored a newer current-main manifest: %s", output)
	}

	cases := []struct {
		name     string
		override string
	}{
		{name: "stale source manifest", override: "FAKE_SOURCE_MANIFEST_VERSION=0.0.9"},
		{name: "diverged commit", override: "FAKE_COMPARE_STATUS=diverged"},
		{name: "failed ci", override: "FAKE_CI_CONCLUSION=failure"},
		{name: "wrong App author", override: "FAKE_PR_AUTHOR=github-actions[bot]"},
		{name: "missing lifecycle label", override: "FAKE_PR_LABEL=triage"},
		{name: "wrong confirmation body", override: "FAKE_CONFIRMATION_BODY=confirm"},
		{name: "non-member confirmation", override: "FAKE_CONFIRMATION_ASSOCIATION=CONTRIBUTOR"},
		{name: "bot confirmation", override: "FAKE_CONFIRMATION_USER_TYPE=Bot"},
		{name: "post-merge confirmation", override: "FAKE_CONFIRMATION_CREATED_AT=2026-07-16T00:00:01Z"},
		{name: "post-merge edit", override: "FAKE_CONFIRMATION_UPDATED_AT=2026-07-16T00:00:01Z"},
		{name: "same-second confirmation", override: "FAKE_CONFIRMATION_CREATED_AT=2026-07-16T00:00:00Z"},
		{name: "same-second edit", override: "FAKE_CONFIRMATION_UPDATED_AT=2026-07-16T00:00:00Z"},
		{name: "duplicate confirmation", override: "FAKE_CONFIRMATION_DUPLICATE=true"},
		{name: "missing confirmation", override: "FAKE_CONFIRMATION_MISSING=true"},
	}
	for _, tc := range cases {
		t.Run(tc.name+" fails closed", func(t *testing.T) {
			env := append([]string{}, baseEnv...)
			env = append(env, tc.override)
			output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), env, "verify-release-authorization.sh", "v0.0.8", sourceSHA, "prepublish")
			if err == nil {
				t.Fatalf("authorization unexpectedly succeeded: %s", output)
			}
			if !strings.Contains(output, "release:") {
				t.Fatalf("authorization failure is not structured: %q", output)
			}
		})
	}
}

func TestVerifyReleaseProposal(t *testing.T) {
	const (
		headSHA   = "cccccccccccccccccccccccccccccccccccccccc"
		parentSHA = "dddddddddddddddddddddddddddddddddddddddd"
		mainSHA   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	commandDir := installFakeReleaseGH(t)
	baseEnv := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"RELEASE_APP_SLUG=env-vault-release-planning",
		"FAKE_PROPOSAL_HEAD_SHA=" + headSHA,
		"FAKE_PROPOSAL_PARENT_SHA=" + parentSHA,
		"FAKE_MAIN_SHA=" + mainSHA,
		"FAKE_COMPARE_STATUS=ahead",
		"FAKE_CI_HEAD_SHA=" + parentSHA,
		"FAKE_CI_CONCLUSION=success",
		"FAKE_MANIFEST_VERSION=0.0.8",
		"FAKE_PR_AUTHOR=env-vault-release-planning[bot]",
		"FAKE_PR_LABEL=autorelease: pending",
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}

	output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), baseEnv, "verify-release-proposal.sh")
	if err != nil {
		t.Fatalf("verify valid release proposal: %v\n%s", err, output)
	}
	for _, line := range []string{"proposal=true\n", "proposal_sha=" + headSHA + "\n", "proposal_base_sha=" + parentSHA + "\n", "version=v0.0.8\n"} {
		if !strings.Contains(output, line) {
			t.Fatalf("release proposal output %q missing %q", output, line)
		}
	}

	badCIEnv := append([]string{}, baseEnv...)
	badCIEnv = append(badCIEnv, "FAKE_CI_CONCLUSION=failure")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badCIEnv, "verify-release-proposal.sh"); err == nil {
		t.Fatalf("proposal based on failed main unexpectedly succeeded: %s", output)
	}

	badPathsEnv := append([]string{}, baseEnv...)
	badPathsEnv = append(badPathsEnv, "FAKE_PROPOSAL_UNEXPECTED_PATH=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badPathsEnv, "verify-release-proposal.sh"); err == nil {
		t.Fatalf("proposal with unexpected path unexpectedly succeeded: %s", output)
	}
}

func TestMarkReleasePullRequestTagged(t *testing.T) {
	commandDir := installFakeReleaseGH(t)
	state := filepath.Join(t.TempDir(), "labels.json")
	if err := os.WriteFile(state, []byte(`[{"id":1,"name":"autorelease: pending"},{"id":2,"name":"documentation"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"FAKE_LABEL_STATE=" + state,
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), env, "mark-release-pr-tagged.sh", "42")
	if err != nil {
		t.Fatalf("mark release PR tagged: %v\n%s", err, output)
	}
	data, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != `[{"id":2,"name":"documentation"},{"id":3,"name":"autorelease: tagged"}]` {
		t.Fatalf("final labels=%s", got)
	}

	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), env, "mark-release-pr-tagged.sh", "42"); err != nil {
		t.Fatalf("idempotent label reconciliation: %v\n%s", err, output)
	}
}

func TestVerifyRepositoryReleaseSettings(t *testing.T) {
	commandDir := installFakeReleaseGH(t)
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))
	baseEnv := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"RELEASECHECK=" + releasecheck,
		"GH_TOKEN=must-not-reach-offline-checker",
		"GITHUB_TOKEN=must-not-reach-offline-checker",
		"GH_ENTERPRISE_TOKEN=must-not-reach-offline-checker",
		"GITHUB_ENTERPRISE_TOKEN=must-not-reach-offline-checker",
		"OFFLINE_CHECKER_SECRET=must-not-reach-offline-checker",
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), baseEnv, "verify-repository-release-settings.sh"); err != nil {
		t.Fatalf("verify valid repository settings: %v\n%s", err, output)
	}

	badEnv := append([]string{}, baseEnv...)
	badEnv = append(badEnv, "FAKE_ALLOW_REBASE=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("unsafe repository settings unexpectedly succeeded: %s", output)
	}

	missingRepositoryEnv := append([]string{}, baseEnv...)
	missingRepositoryEnv = append(missingRepositoryEnv, "FAKE_GRAPHQL_REPOSITORY_NULL=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), missingRepositoryEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("missing GraphQL repository unexpectedly succeeded: %s", output)
	}

	failedGraphQLEnv := append([]string{}, baseEnv...)
	failedGraphQLEnv = append(failedGraphQLEnv, "FAKE_GRAPHQL_FAIL=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), failedGraphQLEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("failed GraphQL settings read unexpectedly succeeded: %s", output)
	}

	partialGraphQLEnv := append([]string{}, baseEnv...)
	partialGraphQLEnv = append(partialGraphQLEnv, "FAKE_GRAPHQL_ERRORS=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), partialGraphQLEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("GraphQL response with errors unexpectedly succeeded: %s", output)
	}

	malformedGraphQLEnv := append([]string{}, baseEnv...)
	malformedGraphQLEnv = append(malformedGraphQLEnv, "FAKE_GRAPHQL_MALFORMED_ERRORS=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), malformedGraphQLEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("GraphQL response with malformed errors unexpectedly succeeded: %s", output)
	}

	badRulesetEnv := append([]string{}, baseEnv...)
	badRulesetEnv = append(badRulesetEnv, "FAKE_RULESET_ALLOW_REBASE=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badRulesetEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("unsafe main ruleset unexpectedly succeeded: %s", output)
	}

	missingTitleCheckEnv := append([]string{}, baseEnv...)
	missingTitleCheckEnv = append(missingTitleCheckEnv, "FAKE_RULESET_OMIT_PR_TITLE=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), missingTitleCheckEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("main ruleset without required pr-title unexpectedly succeeded: %s", output)
	}

	badAppBypassEnv := append([]string{}, baseEnv...)
	badAppBypassEnv = append(badAppBypassEnv, "FAKE_RULESET_BYPASS=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badAppBypassEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("global ruleset bypass unexpectedly succeeded: %s", output)
	}

	nullGraphQLBypassEnv := append([]string{}, baseEnv...)
	nullGraphQLBypassEnv = append(nullGraphQLBypassEnv, "FAKE_GRAPHQL_BYPASS_NULL=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), nullGraphQLBypassEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("missing GraphQL bypass state unexpectedly succeeded: %s", output)
	}

	paginatedGraphQLRulesetsEnv := append([]string{}, baseEnv...)
	paginatedGraphQLRulesetsEnv = append(paginatedGraphQLRulesetsEnv, "FAKE_GRAPHQL_RULESETS_PAGINATED=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), paginatedGraphQLRulesetsEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("paginated GraphQL rulesets unexpectedly succeeded: %s", output)
	}

	nonemptyRESTBypassEnv := append([]string{}, baseEnv...)
	nonemptyRESTBypassEnv = append(nonemptyRESTBypassEnv, "FAKE_REST_RULESET_BYPASS_NONEMPTY=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), nonemptyRESTBypassEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("nonempty REST bypass list unexpectedly succeeded: %s", output)
	}

	badTagRulesetEnv := append([]string{}, baseEnv...)
	badTagRulesetEnv = append(badTagRulesetEnv, "FAKE_TAG_RULESET_ALLOW_UPDATE=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badTagRulesetEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("mutable release tag ruleset unexpectedly succeeded: %s", output)
	}

	badEvidenceRulesetEnv := append([]string{}, baseEnv...)
	badEvidenceRulesetEnv = append(badEvidenceRulesetEnv, "FAKE_EVIDENCE_RULESET_ALLOW_FORCE=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badEvidenceRulesetEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("mutable release evidence ruleset unexpectedly succeeded: %s", output)
	}
}

func TestVerifyRepositoryReleaseSettingsSealsExactOfflineProof(t *testing.T) {
	commandDir := installFakeReleaseGH(t)
	tempDir := t.TempDir()
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))

	const sourceSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	proofPath := filepath.Join(tempDir, "repository-release-settings-proof.json")
	env := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"RELEASECHECK=" + releasecheck,
		"GH_TOKEN=must-not-reach-offline-checker",
		"GITHUB_TOKEN=must-not-reach-offline-checker",
		"GH_ENTERPRISE_TOKEN=must-not-reach-offline-checker",
		"GITHUB_ENTERPRISE_TOKEN=must-not-reach-offline-checker",
		"OFFLINE_CHECKER_SECRET=must-not-reach-offline-checker",
		"RELEASE_SETTINGS_PROOF_OUTPUT=" + proofPath,
		"RELEASE_SETTINGS_SOURCE_SHA=" + sourceSHA,
		"RELEASE_SETTINGS_VERSION=v0.0.9",
		"RELEASE_SETTINGS_PLANNING_RUN_ID=29475939348",
		"RELEASE_SETTINGS_PLANNING_RUN_ATTEMPT=2",
	}
	if output, err := runReleaseAutomationScriptEnv(t, tempDir, env, "verify-repository-release-settings.sh"); err != nil {
		t.Fatalf("seal valid repository settings proof: %v\n%s", err, output)
	}
	encoded, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := releasesettings.ParseProof(encoded)
	if err != nil {
		t.Fatalf("strictly parse settings proof: %v", err)
	}
	want := releasesettings.Tuple{
		Repository: "example/env-vault", SourceSHA: sourceSHA,
		ReleaseVersion: "v0.0.9", PlanningRunID: 29475939348,
		PlanningRunAttempt: 2, CheckedAt: proof.Tuple.CheckedAt,
	}
	contract, err := releasecontract.LoadCanonical("..")
	if err != nil {
		t.Fatalf("load canonical release contract: %v", err)
	}
	if err := releasesettings.Verify(contract, proof, want); err != nil {
		t.Fatalf("verify script-produced settings proof offline: %v", err)
	}
}

func TestEnsureReleaseLifecycleLabels(t *testing.T) {
	commandDir := installFakeReleaseGH(t)
	logPath := filepath.Join(t.TempDir(), "labels.log")
	env := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"FAKE_LABEL_CALL_LOG=" + logPath,
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), env, "ensure-release-labels.sh"); err != nil {
		t.Fatalf("ensure release labels: %v\n%s", err, output)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, snippet := range []string{
		"label create autorelease: pending",
		"label create autorelease: tagged",
		"label create autorelease: abandoned",
		"labels/autorelease%3A%20pending",
		"labels/autorelease%3A%20tagged",
		"labels/autorelease%3A%20abandoned",
	} {
		if !strings.Contains(log, snippet) {
			t.Fatalf("release label bootstrap log missing %q: %s", snippet, log)
		}
	}
}

type releaseMutation struct {
	version          string
	subject          string
	staleREADME      bool
	missingChangelog bool
	emptyChangelog   bool
	unexpectedPath   bool
}

func newReleaseAutomationRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitFixture(t, repo, "init")
	gitFixture(t, repo, "config", "user.name", "Release Test")
	gitFixture(t, repo, "config", "user.email", "release-test@example.invalid")
	writeReleaseFixture(t, repo, ".release-please-manifest.json", manifestV007)
	writeReleaseFixture(t, repo, "README.md", readmeV007)
	writeReleaseFixture(t, repo, "CHANGELOG.md", changelogV007)
	writeReleaseFixture(t, repo, "code.txt", "initial\n")
	gitFixture(t, repo, "add", ".")
	gitFixture(t, repo, "commit", "-m", "chore: bootstrap")
	return repo
}

func commitReleaseFixture(t *testing.T, repo string, mutation releaseMutation) {
	t.Helper()
	version := mutation.version
	if version == "" {
		version = "0.0.8"
	}
	subject := mutation.subject
	if subject == "" {
		subject = "chore(main): release env-vault v" + version
	}
	writeReleaseFixture(t, repo, ".release-please-manifest.json", "{\n  \".\": \""+version+"\"\n}\n")
	if mutation.staleREADME {
		writeReleaseFixture(t, repo, "README.md", readmeV007+"\n")
	} else {
		writeReleaseFixture(t, repo, "README.md", "# fixture\n\nCurrent version: `v"+version+"`. <!-- x-release-please-version -->\n")
	}
	if mutation.missingChangelog {
		writeReleaseFixture(t, repo, "CHANGELOG.md", changelogV007+"\n")
	} else if mutation.emptyChangelog {
		writeReleaseFixture(t, repo, "CHANGELOG.md", "# Changelog\n\n## ["+version+"](https://example.invalid/release) (2026-07-16)\n\n"+strings.TrimPrefix(changelogV007, "# Changelog\n\n"))
	} else {
		writeReleaseFixture(t, repo, "CHANGELOG.md", "# Changelog\n\n## ["+version+"](https://example.invalid/release) (2026-07-16)\n\n- Release.\n\n"+strings.TrimPrefix(changelogV007, "# Changelog\n\n"))
	}
	if mutation.unexpectedPath {
		writeReleaseFixture(t, repo, "code.txt", "unexpected\n")
	}
	gitFixture(t, repo, "add", ".")
	gitFixture(t, repo, "commit", "-m", subject)
}

func writeReleaseFixture(t *testing.T, directory, relative, contents string) {
	t.Helper()
	path := filepath.Join(directory, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitFixture(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func runReleaseAutomationScript(t *testing.T, directory, script string, args ...string) (string, error) {
	return runReleaseAutomationScriptEnv(t, directory, nil, script, args...)
}

func runReleaseAutomationScriptEnv(t *testing.T, directory string, extraEnv []string, script string, args ...string) (string, error) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "scripts", "release", script))
	if err != nil {
		t.Fatal(err)
	}
	commandArgs := append([]string{filepath.ToSlash(path)}, args...)
	cmd := exec.Command("bash", commandArgs...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), extraEnv...)
	output, runErr := cmd.CombinedOutput()
	return string(output), runErr
}

func buildReleasecheck(t *testing.T) string {
	t.Helper()
	outputPath := filepath.Join(t.TempDir(), "releasecheck")
	build := exec.Command("go", "build", "-trimpath", "-o", outputPath, "./cmd/releasecheck")
	build.Dir = ".."
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build releasecheck: %v\n%s", err, output)
	}
	return outputPath
}

func credentialRejectingReleasecheck(t *testing.T, releasecheck string) string {
	t.Helper()
	wrapper := filepath.Join(t.TempDir(), "releasecheck-no-credentials")
	contents := `#!/bin/bash
set -euo pipefail
for name in GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN OFFLINE_CHECKER_SECRET; do
  if [[ -n "${!name+x}" ]]; then
    printf 'offline checker inherited %s\n' "$name" >&2
    exit 91
  fi
done
exec ` + strconv.Quote(releasecheck) + ` "$@"
`
	if err := os.WriteFile(wrapper, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return wrapper
}

func installFakeReleaseGH(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	script := `#!/usr/bin/env bash
set -euo pipefail
args="$*"

if [[ ${1:-} == --version ]]; then
  printf 'gh version 2.80.0 (2026-01-01)\n'
  exit 0
fi
if [[ ${1:-} == api && ${2:-} == --help ]]; then
  printf '%s\n' 'OPTIONS: --include --hostname --method --header --raw-field'
  exit 0
fi

if [[ "$args" == *"api --include --hostname github.com --method GET"* ]]; then
  transport_tmp=$(mktemp "${TMPDIR:-/tmp}/fake-release-gh.XXXXXX")
  exec 3>&1
  exec >"$transport_tmp"
  finish_transport() {
    status=$?
    trap - EXIT
    exec 1>&3
    if [[ $status == 0 ]]; then
      if [[ "$args" == *"Accept: application/vnd.github.raw+json"* ]]; then
        content_type='application/vnd.github.raw+json; charset=utf-8'
      else
        content_type='application/vnd.github+json; charset=utf-8'
      fi
      printf 'HTTP/2 200 OK\r\nContent-Type: %s\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n\r\n' "$content_type"
    fi
    cat -- "$transport_tmp"
    rm -f -- "$transport_tmp"
    exit "$status"
  }
  trap finish_transport EXIT
fi

if [[ "$args" == *"issues/42/labels"* ]]; then
  state="${FAKE_LABEL_STATE:?}"
  if [[ "$args" == *"--method POST"* ]]; then
    printf '%s\n' '[{"id":1,"name":"autorelease: pending"},{"id":2,"name":"documentation"},{"id":3,"name":"autorelease: tagged"}]' > "$state"
    exit 0
  fi
  if [[ "$args" == *"--method DELETE"* ]]; then
    printf '%s\n' '[{"id":2,"name":"documentation"},{"id":3,"name":"autorelease: tagged"}]' > "$state"
    exit 0
  fi
  cat "$state"
  exit 0
fi

if [[ "$args" == label\ create* ]]; then
  printf '%s\n' "$args" >> "${FAKE_LABEL_CALL_LOG:?}"
  exit 0
fi
if [[ "$args" == *"labels/autorelease%3A%20pending"* ]]; then
  printf '%s\n' "$args" >> "${FAKE_LABEL_CALL_LOG:?}"
  printf '%s\n' '{"name":"autorelease: pending","color":"fbca04","description":"Release Please proposal awaiting reviewed publication"}'
  exit 0
fi
if [[ "$args" == *"labels/autorelease%3A%20tagged"* ]]; then
  printf '%s\n' "$args" >> "${FAKE_LABEL_CALL_LOG:?}"
  printf '%s\n' '{"name":"autorelease: tagged","color":"0e8a16","description":"Reviewed Release Please proposal with an exact release tag"}'
  exit 0
fi
if [[ "$args" == *"labels/autorelease%3A%20abandoned"* ]]; then
  printf '%s\n' "$args" >> "${FAKE_LABEL_CALL_LOG:?}"
  printf '%s\n' '{"name":"autorelease: abandoned","color":"b60205","description":"Merged Release Please proposal permanently abandoned before tagging"}'
  exit 0
fi

case "$args" in
  *"api graphql"*)
    for required in \
      '-f owner=example' \
      '-f name=env-vault' \
      'defaultBranchRef' \
      'mergeCommitAllowed' \
      'rebaseMergeAllowed' \
      'squashMergeAllowed' \
      'squashMergeCommitTitle' \
      'squashMergeCommitMessage' \
      'rulesets' \
      'includeParents: false' \
      'bypassActors' \
      'nameWithOwner'; do
      if [[ "$args" != *"$required"* ]]; then
        printf 'GraphQL request missing %s\n' "$required" >&2
        exit 1
      fi
    done
    if [[ "${FAKE_GRAPHQL_FAIL:-false}" == "true" ]]; then
      printf '%s\n' 'GraphQL request failed' >&2
      exit 1
    fi
    if [[ "${FAKE_GRAPHQL_REPOSITORY_NULL:-false}" == "true" ]]; then
      printf '%s\n' '{"data":{"repository":null}}'
      exit 0
    fi
    if [[ "${FAKE_GRAPHQL_MALFORMED_ERRORS:-false}" == "true" ]]; then
      errors=',"errors":{}'
    elif [[ "${FAKE_GRAPHQL_ERRORS:-false}" == "true" ]]; then
      errors=',"errors":[{"message":"partial response"}]'
    else
      errors=''
    fi
    if [[ "${FAKE_RULESET_BYPASS:-false}" == "true" ]]; then
      main_bypass='{"totalCount":1}'
    elif [[ "${FAKE_GRAPHQL_BYPASS_NULL:-false}" == "true" ]]; then
      main_bypass='null'
    else
      main_bypass='{"totalCount":0}'
    fi
    printf '{"data":{"repository":{"defaultBranchRef":{"name":"main"},"squashMergeAllowed":true,"mergeCommitAllowed":false,"rebaseMergeAllowed":%s,"squashMergeCommitTitle":"PR_TITLE","squashMergeCommitMessage":"PR_BODY","rulesets":{"totalCount":3,"pageInfo":{"hasNextPage":%s},"nodes":[{"databaseId":7,"name":"Protect env-vault main","enforcement":"ACTIVE","target":"BRANCH","source":{"__typename":"Repository","nameWithOwner":"example/env-vault"},"bypassActors":%s},{"databaseId":8,"name":"Protect env-vault release tags","enforcement":"ACTIVE","target":"TAG","source":{"__typename":"Repository","nameWithOwner":"example/env-vault"},"bypassActors":{"totalCount":0}},{"databaseId":9,"name":"Protect env-vault release evidence","enforcement":"ACTIVE","target":"BRANCH","source":{"__typename":"Repository","nameWithOwner":"example/env-vault"},"bypassActors":{"totalCount":0}}]}}}%s}\n' "${FAKE_ALLOW_REBASE:-false}" "${FAKE_GRAPHQL_RULESETS_PAGINATED:-false}" "$main_bypass" "$errors"
    ;;
  *"repos/example/env-vault")
    printf '{"default_branch":"main","allow_squash_merge":true,"allow_merge_commit":false,"allow_rebase_merge":%s,"squash_merge_commit_title":"PR_TITLE","squash_merge_commit_message":"PR_BODY"}\n' "${FAKE_ALLOW_REBASE:-false}"
    ;;
  *"rulesets?per_page=100"*)
    printf '[{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","enforcement":"active"},{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","enforcement":"active"},{"id":9,"name":"Protect env-vault release evidence","target":"branch","source_type":"Repository","enforcement":"active"}]\n'
    ;;
  *"rulesets/7"*)
    if [[ "${FAKE_RULESET_ALLOW_REBASE:-false}" == "true" ]]; then
      merge_methods='["squash","rebase"]'
    else
      merge_methods='["squash"]'
    fi
    if [[ "${FAKE_RULESET_BYPASS:-false}" == "true" ]]; then
      can_bypass='always'
    else
      can_bypass='never'
    fi
    if [[ "${FAKE_REST_RULESET_BYPASS_NONEMPTY:-false}" == "true" ]]; then
      bypass=',"bypass_actors":[{"actor_id":1,"actor_type":"Integration","bypass_mode":"always"}]'
    elif [[ "${FAKE_REST_RULESET_BYPASS_EMPTY:-false}" == "true" ]]; then
      bypass=',"bypass_actors":[]'
    else
      bypass=''
    fi
    if [[ "${FAKE_RULESET_OMIT_PR_TITLE:-false}" == "true" ]]; then
      pr_title_integration_id=0
    else
      pr_title_integration_id=15368
    fi
    printf '{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","source":"example/env-vault","enforcement":"active"%s,"current_user_can_bypass":"%s","conditions":{"ref_name":{"exclude":[],"include":["refs/heads/main"]}},"rules":[{"type":"deletion"},{"type":"non_fast_forward"},{"type":"pull_request","parameters":{"required_approving_review_count":0,"dismiss_stale_reviews_on_push":false,"required_reviewers":[],"require_code_owner_review":false,"require_last_push_approval":false,"required_review_thread_resolution":true,"allowed_merge_methods":%s}},{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"do_not_enforce_on_create":false,"required_status_checks":[{"context":"quality-gate","integration_id":15368},{"context":"pr-title","integration_id":%s},{"context":"Dependency review","integration_id":15368},{"context":"Analyze (go)","integration_id":15368},{"context":"Analyze (actions)","integration_id":15368}]}}]}\n' "$bypass" "$can_bypass" "$merge_methods" "$pr_title_integration_id"
    ;;
  *"rulesets/8"*)
    if [[ "${FAKE_TAG_RULESET_ALLOW_UPDATE:-false}" == "true" ]]; then
      tag_rules='[{"type":"deletion"}]'
    else
      tag_rules='[{"type":"deletion"},{"type":"update"}]'
    fi
    printf '{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","source":"example/env-vault","enforcement":"active","current_user_can_bypass":"never","conditions":{"ref_name":{"exclude":[],"include":["refs/tags/v*"]}},"rules":%s}\n' "$tag_rules"
    ;;
  *"rulesets/9"*)
    if [[ "${FAKE_EVIDENCE_RULESET_ALLOW_FORCE:-false}" == "true" ]]; then
      evidence_rules='[{"type":"deletion"}]'
    else
      evidence_rules='[{"type":"deletion"},{"type":"non_fast_forward"}]'
    fi
    printf '{"id":9,"name":"Protect env-vault release evidence","target":"branch","source_type":"Repository","source":"example/env-vault","enforcement":"active","current_user_can_bypass":"never","conditions":{"ref_name":{"exclude":[],"include":["refs/heads/release-evidence"]}},"rules":%s}\n' "$evidence_rules"
    ;;
  *"repos/example/env-vault/pulls"*)
    printf '[{"id":4300,"number":43,"base":{"ref":"main","sha":"%s","repo":{"full_name":"example/env-vault"}},"head":{"ref":"release-please--branches--main--components--env-vault","sha":"%s","repo":{"full_name":"example/env-vault"}},"user":{"login":"%s"},"title":"chore(main): release env-vault v0.0.8","body":"Merging this unchanged reviewed pull request after the required exact tuple confirmation authorizes publication once its merge commit passes main CI. This PR was generated with Release Please.","labels":[{"name":"%s"}]}]\n' \
      "${FAKE_PROPOSAL_PARENT_SHA:?}" "${FAKE_PROPOSAL_HEAD_SHA:?}" "${FAKE_PR_AUTHOR:?}" "${FAKE_PR_LABEL:?}"
    ;;
  *"git/commits/"*)
    jq -cn --arg parent "${FAKE_PROPOSAL_PARENT_SHA:?}" '{message:"chore(main): release env-vault v0.0.8\n\nThis PR was generated with Release Please.",tree:{sha:"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},parents:[{sha:$parent}]}'
    ;;
  *"compare/${FAKE_PROPOSAL_PARENT_SHA:-missing}...${FAKE_PROPOSAL_HEAD_SHA:-missing}"*)
    if [[ "${FAKE_PROPOSAL_UNEXPECTED_PATH:-false}" == "true" ]]; then
      files='[{"filename":".release-please-manifest.json","status":"modified"},{"filename":"CHANGELOG.md","status":"modified"},{"filename":"README.md","status":"modified"},{"filename":"code.txt","status":"modified"}]'
    else
      files='[{"filename":".release-please-manifest.json","status":"modified"},{"filename":"CHANGELOG.md","status":"modified"},{"filename":"README.md","status":"modified"}]'
    fi
    printf '{"status":"ahead","ahead_by":1,"total_commits":1,"files":%s}\n' "$files"
    ;;
  *"git/trees/eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee?recursive=1"*)
    printf '%s\n' '{"tree":[{"path":".release-please-manifest.json","mode":"100644","type":"blob"},{"path":"CHANGELOG.md","mode":"100644","type":"blob"},{"path":"README.md","mode":"100644","type":"blob"}]}'
    ;;
  *"git/ref/heads/main"*)
    printf '{"object":{"sha":"%s"}}\n' "${FAKE_MAIN_SHA:?}"
    ;;
  *"compare/"*)
    printf '{"status":"%s"}\n' "${FAKE_COMPARE_STATUS:?}"
    ;;
  *"contents/.release-please-manifest.json?ref=${FAKE_SOURCE_SHA:-__missing_source__}"*)
    printf '{".":"%s"}\n' "${FAKE_SOURCE_MANIFEST_VERSION:-${FAKE_MANIFEST_VERSION:?}}"
    ;;
  *"contents/.release-please-manifest.json"*)
    printf '{".":"%s"}\n' "${FAKE_MAIN_MANIFEST_VERSION:-${FAKE_MANIFEST_VERSION:?}}"
    ;;
  *"contents/README.md"*)
    printf '%b\n' 'Current version: \x60v0.0.8\x60. <!-- x-release-please-version -->'
    ;;
  *"contents/CHANGELOG.md"*)
    printf '%s\n' '# Changelog' '' '## [0.0.8](https://example.invalid/release) (2026-07-16)' '' '- Release.'
    ;;
  *"actions/runs/7001/attempts/1"*)
    printf '{"id":7001,"run_attempt":1,"repository":{"full_name":"example/env-vault"},"head_repository":{"full_name":"example/env-vault"},"head_sha":"%s","head_branch":"main","event":"push","path":".github/workflows/ci.yml","status":"completed","conclusion":"%s","html_url":"https://github.com/example/env-vault/actions/runs/7001","name":"custom diagnostic title"}\n' \
      "${FAKE_CI_HEAD_SHA:-${FAKE_SOURCE_SHA:?}}" "${FAKE_CI_CONCLUSION:?}"
    ;;
  *"actions/workflows/ci.yml/runs"*)
    printf '{"total_count":1,"workflow_runs":[{"id":7001,"run_attempt":1,"repository":{"full_name":"example/env-vault"},"head_repository":{"full_name":"example/env-vault"},"head_sha":"%s","head_branch":"main","event":"push","path":".github/workflows/ci.yml","status":"completed","conclusion":"%s","html_url":"https://github.com/example/env-vault/actions/runs/7001","name":"custom diagnostic title"}]}\n' \
      "${FAKE_CI_HEAD_SHA:-${FAKE_SOURCE_SHA:?}}" "${FAKE_CI_CONCLUSION:?}"
    ;;
  *"issues/42/comments?per_page=100"*)
    if [[ "${FAKE_CONFIRMATION_MISSING:-false}" == "true" ]]; then
      printf '%s\n' '[]'
      exit 0
    fi
    canonical_body="ПОДТВЕРЖДАЮ RELEASE v0.0.8 PR #42 SHA ${FAKE_PR_HEAD_SHA:?}"
    body=${FAKE_CONFIRMATION_BODY:-$canonical_body}
    comment=$(jq -cn \
      --arg body "$body" \
      --arg actor "${FAKE_CONFIRMATION_ACTOR:?}" \
      --arg association "${FAKE_CONFIRMATION_ASSOCIATION:?}" \
      --arg user_type "${FAKE_CONFIRMATION_USER_TYPE:?}" \
      --arg created_at "${FAKE_CONFIRMATION_CREATED_AT:?}" \
      --arg updated_at "${FAKE_CONFIRMATION_UPDATED_AT:?}" \
      '{id:9001,html_url:"https://github.com/example/env-vault/pull/42#issuecomment-9001",body:$body,user:{login:$actor,type:$user_type},author_association:$association,created_at:$created_at,updated_at:$updated_at}')
    if [[ "${FAKE_CONFIRMATION_DUPLICATE:-false}" == "true" ]]; then
      duplicate=$(jq -cn \
        --arg body "$body" \
        --arg actor "${FAKE_CONFIRMATION_ACTOR:?}" \
        --arg association "${FAKE_CONFIRMATION_ASSOCIATION:?}" \
        --arg user_type "${FAKE_CONFIRMATION_USER_TYPE:?}" \
        --arg created_at "${FAKE_CONFIRMATION_CREATED_AT:?}" \
        --arg updated_at "${FAKE_CONFIRMATION_UPDATED_AT:?}" \
        '{id:9002,html_url:"https://github.com/example/env-vault/pull/42#issuecomment-9002",body:$body,user:{login:$actor,type:$user_type},author_association:$association,created_at:$created_at,updated_at:$updated_at}')
      printf '[%s,%s]\n' "$comment" "$duplicate"
    else
      printf '[%s]\n' "$comment"
    fi
    ;;
  *"commits/"*"/pulls"*)
    printf '[{"id":4200,"number":42,"state":"closed","merged_at":"%s","merge_commit_sha":"%s","base":{"ref":"main","repo":{"full_name":"example/env-vault"}},"head":{"ref":"release-please--branches--main--components--env-vault","sha":"%s","repo":{"full_name":"example/env-vault"}},"user":{"login":"%s"},"title":"chore(main): release env-vault v0.0.8","body":"Merging this unchanged reviewed pull request after the required exact tuple confirmation authorizes publication once its merge commit passes main CI. This PR was generated with Release Please.","labels":[{"name":"%s"}]}]\n' \
      "${FAKE_PR_MERGED_AT:?}" "${FAKE_SOURCE_SHA:?}" "${FAKE_PR_HEAD_SHA:?}" "${FAKE_PR_AUTHOR:?}" "${FAKE_PR_LABEL:?}"
    ;;
  *)
    printf 'unexpected fake gh invocation: %s\n' "$args" >&2
    exit 1
    ;;
esac
`
	path := filepath.Join(directory, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return directory
}
