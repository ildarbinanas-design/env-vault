package tests

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	homebrewTapRepository = "example/homebrew-tap"
	homebrewSourceSHA     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type homebrewTapFixture struct {
	root        string
	home        string
	origin      string
	seed        string
	fakeBin     string
	ghLog       string
	createdPR   string
	createdBody string
}

func TestPublishHomebrewPRCreatesDeterministicBranchAndPR(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.2")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	mainBefore := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", "refs/heads/main")

	output, status := fixture.runPublish(t, nil, releaseTestVersion, formula)
	if status != 0 {
		t.Fatalf("publish exit status=%d\n%s", status, output)
	}
	values := parseHomebrewPublishOutputs(t, output)
	branch := "release/env-vault-" + releaseTestVersion
	head := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", "refs/heads/"+branch)
	assertHomebrewPublishOutputs(t, values, map[string]string{
		"branch":                   branch,
		"base_branch":              "main",
		"base_sha":                 mainBefore,
		"source_sha":               homebrewSourceSHA,
		"pr_number":                "42",
		"pr_url":                   "https://github.com/example/homebrew-tap/pull/42",
		"head_sha":                 head,
		"merge_sha":                "",
		"tap_sha":                  "",
		"merge_is_ancestor_of_tap": "false",
		"state":                    "OPEN",
		"already_merged":           "false",
		"no_op":                    "false",
	})

	mainAfter := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", "refs/heads/main")
	if mainAfter != mainBefore {
		t.Fatalf("main moved from %s to %s", mainBefore, mainAfter)
	}
	gotFormula := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show", head+":Formula/env-vault.rb") + "\n"
	wantFormula, err := os.ReadFile(formula)
	if err != nil {
		t.Fatal(err)
	}
	if gotFormula != string(wantFormula) {
		t.Fatalf("published formula mismatch\ngot:\n%s\nwant:\n%s", gotFormula, wantFormula)
	}
	changed := fixture.gitOutput(t, "--git-dir="+fixture.origin, "diff", "--name-only", mainBefore, head)
	if changed != "Formula/env-vault.rb" {
		t.Fatalf("release branch changes=%q", changed)
	}

	calls := readOptionalFile(t, fixture.ghLog)
	for _, snippet := range []string{
		"auth setup-git",
		"pr create",
		"--head release/env-vault-v1.2.3",
		"--base main",
		"version=v1.2.3 source_sha=" + homebrewSourceSHA,
	} {
		if !strings.Contains(calls, snippet) {
			t.Fatalf("gh calls missing %q:\n%s", snippet, calls)
		}
	}
	if strings.Contains(calls, "--force") {
		t.Fatalf("publisher attempted a force operation:\n%s", calls)
	}
	assertTokenNotExposed(t, output, calls)
}

func TestPublishHomebrewPRReusesExactOpenPR(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.2")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	head := fixture.createReleaseBranch(t, formula, false)
	body := homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA)

	output, status := fixture.runPublish(t, map[string]string{
		"FAKE_PR_STATE":    "OPEN",
		"FAKE_PR_NUMBER":   "17",
		"FAKE_PR_HEAD_SHA": head,
		"FAKE_PR_BODY":     body,
	}, releaseTestVersion, formula)
	if status != 0 {
		t.Fatalf("reuse exit status=%d\n%s", status, output)
	}
	values := parseHomebrewPublishOutputs(t, output)
	assertHomebrewPublishOutputs(t, values, map[string]string{
		"pr_number":                "17",
		"head_sha":                 head,
		"state":                    "OPEN",
		"already_merged":           "false",
		"no_op":                    "true",
		"tap_sha":                  "",
		"merge_sha":                "",
		"merge_is_ancestor_of_tap": "false",
	})
	calls := readOptionalFile(t, fixture.ghLog)
	if strings.Contains(calls, "pr create") {
		t.Fatalf("exact open PR was duplicated:\n%s", calls)
	}
	assertTokenNotExposed(t, output, calls)
}

func TestPublishHomebrewPRRecognizesMergedPRWithDeletedBranch(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.2")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	head, merge := fixture.mergeReleaseBranch(t, formula)
	body := homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA)

	output, status := fixture.runPublish(t, map[string]string{
		"FAKE_PR_STATE":     "MERGED",
		"FAKE_PR_NUMBER":    "23",
		"FAKE_PR_HEAD_SHA":  head,
		"FAKE_PR_MERGE_SHA": merge,
		"FAKE_PR_BODY":      body,
	}, releaseTestVersion, formula)
	if status != 0 {
		t.Fatalf("merged no-op exit status=%d\n%s", status, output)
	}
	values := parseHomebrewPublishOutputs(t, output)
	assertHomebrewPublishOutputs(t, values, map[string]string{
		"pr_number":                "23",
		"head_sha":                 head,
		"merge_sha":                merge,
		"tap_sha":                  merge,
		"merge_is_ancestor_of_tap": "true",
		"state":                    "MERGED",
		"already_merged":           "true",
		"no_op":                    "true",
	})
	if fixture.refExists(t, "refs/heads/release/env-vault-"+releaseTestVersion) {
		t.Fatal("merged fixture unexpectedly retained its remote release branch")
	}
	calls := readOptionalFile(t, fixture.ghLog)
	if strings.Contains(calls, "pr create") {
		t.Fatalf("merged PR was duplicated:\n%s", calls)
	}
}

func TestPublishHomebrewPRVerifyOnlyChecksPublishedFormulaWithoutWrites(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.3")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	mainBefore := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", "refs/heads/main")

	output, status := fixture.runPublishMode(t, nil, []string{"--verify-only", releaseTestVersion, formula, homebrewTapRepository})
	if status != 0 {
		t.Fatalf("verify-only exit status=%d\n%s", status, output)
	}
	values := parseHomebrewPublishOutputs(t, output)
	assertHomebrewPublishOutputs(t, values, map[string]string{
		"base_sha":                 mainBefore,
		"head_sha":                 mainBefore,
		"tap_sha":                  mainBefore,
		"pr_number":                "",
		"pr_url":                   "",
		"merge_sha":                "",
		"merge_is_ancestor_of_tap": "false",
		"state":                    "PUBLISHED",
		"already_merged":           "true",
		"no_op":                    "true",
	})
	if calls := readOptionalFile(t, fixture.ghLog); calls != "" {
		t.Fatalf("verify-only invoked gh:\n%s", calls)
	}
	refs := fixture.gitOutput(t, "--git-dir="+fixture.origin, "for-each-ref", "--format=%(refname)", "refs/heads")
	if refs != "refs/heads/main" {
		t.Fatalf("verify-only changed remote refs:\n%s", refs)
	}
}

func TestPublishHomebrewPRRequiresExactUnpublishedStateWithoutWrites(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.2")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	refsBefore := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref")
	main := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", "refs/heads/main")

	output, status := fixture.runPublishMode(t, nil, []string{"--require-unpublished", releaseTestVersion, formula, homebrewTapRepository})
	if status != 0 {
		t.Fatalf("require-unpublished exit status=%d\n%s", status, output)
	}
	values := parseHomebrewPublishOutputs(t, output)
	assertHomebrewPublishOutputs(t, values, map[string]string{
		"base_sha":                 main,
		"head_sha":                 main,
		"tap_sha":                  main,
		"pr_number":                "",
		"pr_url":                   "",
		"merge_sha":                "",
		"merge_is_ancestor_of_tap": "false",
		"state":                    "UNPUBLISHED",
		"already_merged":           "false",
		"no_op":                    "true",
	})
	assertHomebrewVerifyPublishedPRDidNotMutate(t, fixture, refsBefore)
	if calls := readOptionalFile(t, fixture.ghLog); !strings.Contains(calls, "pr list") {
		t.Fatalf("unpublished verifier did not inspect deterministic PR state:\n%s", calls)
	}
}

func TestPublishHomebrewPRUnpublishedGuardRejectsExistingCoordinates(t *testing.T) {
	for name, prepare := range map[string]func(*testing.T, *homebrewTapFixture, string) map[string]string{
		"release branch": func(t *testing.T, fixture *homebrewTapFixture, formula string) map[string]string {
			fixture.createReleaseBranch(t, formula, false)
			return nil
		},
		"pull request": func(t *testing.T, fixture *homebrewTapFixture, formula string) map[string]string {
			head := fixture.createReleaseBranch(t, formula, false)
			return map[string]string{
				"FAKE_PR_STATE":    "OPEN",
				"FAKE_PR_NUMBER":   "17",
				"FAKE_PR_HEAD_SHA": head,
				"FAKE_PR_BODY":     homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA),
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newHomebrewTapFixture(t, "1.2.2")
			formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
			extra := prepare(t, fixture, formula)
			refsBefore := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref")
			output, status := fixture.runPublishMode(t, extra, []string{"--require-unpublished", releaseTestVersion, formula, homebrewTapRepository})
			if status == 0 || !strings.Contains(output, "already exists") {
				t.Fatalf("require-unpublished status=%d, want existing-coordinate failure\n%s", status, output)
			}
			assertHomebrewVerifyPublishedPRDidNotMutate(t, fixture, refsBefore)
		})
	}
}

func TestPublishHomebrewPRExpectedBaseGuardsFirstMutation(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.2")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	refsBefore := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref")

	output, status := fixture.runPublish(t, map[string]string{
		"EXPECTED_TAP_BASE_SHA": strings.Repeat("b", 40),
	}, releaseTestVersion, formula)
	if status == 0 || !strings.Contains(output, "changed from the expected pre-publication base") {
		t.Fatalf("expected-base guard status=%d\n%s", status, output)
	}
	refsAfter := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref")
	if refsAfter != refsBefore {
		t.Fatalf("expected-base guard changed remote refs\nbefore:\n%s\nafter:\n%s", refsBefore, refsAfter)
	}
	calls := readOptionalFile(t, fixture.ghLog)
	if strings.Contains(calls, "pr create") {
		t.Fatalf("expected-base guard created a pull request:\n%s", calls)
	}
}

func TestPublishHomebrewPRExpectedBaseRejectsRacedBranchFromOlderBase(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.2")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	fixture.createReleaseBranch(t, formula, false)

	fixture.gitRun(t, "-C", fixture.seed, "switch", "main")
	fixture.gitRun(t, "-C", fixture.seed, "reset", "--hard", "origin/main")
	if err := os.WriteFile(filepath.Join(fixture.seed, "README.md"), []byte("tap advanced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.gitRun(t, "-C", fixture.seed, "add", "README.md")
	fixture.gitRun(t, "-C", fixture.seed, "commit", "-m", "advance tap main")
	fixture.gitRun(t, "-C", fixture.seed, "push", "origin", "main")
	newMain := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", "refs/heads/main")
	refsBefore := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref")

	output, status := fixture.runPublish(t, map[string]string{
		"EXPECTED_TAP_BASE_SHA": newMain,
	}, releaseTestVersion, formula)
	if status == 0 || !strings.Contains(output, "not based directly on the expected tap default branch") {
		t.Fatalf("raced-branch base guard status=%d\n%s", status, output)
	}
	if refsAfter := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref"); refsAfter != refsBefore {
		t.Fatalf("raced-branch guard changed remote refs\nbefore:\n%s\nafter:\n%s", refsBefore, refsAfter)
	}
	if calls := readOptionalFile(t, fixture.ghLog); strings.Contains(calls, "pr create") {
		t.Fatalf("raced-branch guard created a pull request:\n%s", calls)
	}
}

func TestPublishHomebrewPRUnpublishedGuardFindsWrongBasePRByDeterministicHead(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.2")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	refsBefore := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref")

	output, status := fixture.runPublishMode(t, map[string]string{
		"FAKE_PR_STATE":    "CLOSED",
		"FAKE_PR_NUMBER":   "19",
		"FAKE_PR_HEAD_SHA": strings.Repeat("b", 40),
		"FAKE_PR_BASE_REF": "maintenance",
	}, []string{"--require-unpublished", releaseTestVersion, formula, homebrewTapRepository})
	if status == 0 || !strings.Contains(output, "deterministic release pull request already exists") {
		t.Fatalf("wrong-base PR collision status=%d\n%s", status, output)
	}
	if refsAfter := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref"); refsAfter != refsBefore {
		t.Fatalf("wrong-base PR collision changed remote refs\nbefore:\n%s\nafter:\n%s", refsBefore, refsAfter)
	}
	calls := readOptionalFile(t, fixture.ghLog)
	for _, line := range strings.Split(calls, "\n") {
		if strings.Contains(line, "pr list") && strings.Contains(line, "--base") {
			t.Fatalf("deterministic-head discovery was incorrectly narrowed by base:\n%s", calls)
		}
	}
}

func TestPublishHomebrewPRVerifyPublishedPRIsStrictlyReadOnly(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.2")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	head, merge := fixture.mergeReleaseBranch(t, formula)
	body := homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA)
	refsBefore := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref")
	workDir := filepath.Join(fixture.root, "verify-published-pr-worktree")

	output, status := fixture.runPublishMode(t, map[string]string{
		"FAKE_PR_STATE":     "MERGED",
		"FAKE_PR_NUMBER":    "23",
		"FAKE_PR_HEAD_SHA":  head,
		"FAKE_PR_MERGE_SHA": merge,
		"FAKE_PR_BODY":      body,
	}, []string{"--verify-published-pr", releaseTestVersion, formula, homebrewTapRepository, workDir})
	if status != 0 {
		t.Fatalf("verify-published-pr exit status=%d\n%s", status, output)
	}
	values := parseHomebrewPublishOutputs(t, output)
	assertHomebrewPublishOutputs(t, values, map[string]string{
		"branch":                   "release/env-vault-" + releaseTestVersion,
		"base_branch":              "main",
		"base_sha":                 merge,
		"source_sha":               homebrewSourceSHA,
		"pr_number":                "23",
		"pr_url":                   "https://github.com/example/homebrew-tap/pull/23",
		"head_sha":                 head,
		"merge_sha":                merge,
		"tap_sha":                  merge,
		"merge_is_ancestor_of_tap": "true",
		"state":                    "MERGED",
		"already_merged":           "true",
		"no_op":                    "true",
	})
	assertHomebrewVerifyPublishedPRDidNotMutate(t, fixture, refsBefore)
	assertTokenNotExposed(t, output, readOptionalFile(t, fixture.ghLog))
}

func TestPublishHomebrewPRAllowsTapMainToAdvanceAfterExactReleaseMerge(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.2")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	head, merge := fixture.mergeReleaseBranch(t, formula)
	body := homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA)

	if err := os.WriteFile(filepath.Join(fixture.seed, "README.md"), []byte("unrelated later tap change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.gitRun(t, "-C", fixture.seed, "add", "README.md")
	fixture.gitRun(t, "-C", fixture.seed, "commit", "-m", "later unrelated tap change")
	fixture.gitRun(t, "-C", fixture.seed, "push", "origin", "main")
	tap := fixture.gitOutput(t, "-C", fixture.seed, "rev-parse", "HEAD")
	if tap == merge {
		t.Fatal("fixture did not advance tap main")
	}

	output, status := fixture.runPublishMode(t, map[string]string{
		"FAKE_PR_STATE":     "MERGED",
		"FAKE_PR_NUMBER":    "23",
		"FAKE_PR_HEAD_SHA":  head,
		"FAKE_PR_MERGE_SHA": merge,
		"FAKE_PR_BODY":      body,
	}, []string{"--verify-published-pr", releaseTestVersion, formula, homebrewTapRepository})
	if status != 0 {
		t.Fatalf("advanced tap verification exit status=%d\n%s", status, output)
	}
	values := parseHomebrewPublishOutputs(t, output)
	assertHomebrewPublishOutputs(t, values, map[string]string{
		"head_sha":                 head,
		"merge_sha":                merge,
		"tap_sha":                  tap,
		"merge_is_ancestor_of_tap": "true",
		"state":                    "MERGED",
		"already_merged":           "true",
		"no_op":                    "true",
	})
}

func TestPublishHomebrewPRVerifyPublishedPRFailsClosed(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*testing.T, *homebrewTapFixture, string, string, string) map[string]string
		wantError string
	}{
		{
			name: "wrong body marker",
			mutate: func(t *testing.T, _ *homebrewTapFixture, formula, head, merge string) map[string]string {
				return map[string]string{
					"FAKE_PR_BODY":      homebrewPRBody(t, formula, releaseTestVersion, strings.Repeat("b", 40)),
					"FAKE_PR_HEAD_SHA":  head,
					"FAKE_PR_MERGE_SHA": merge,
				}
			},
			wantError: "release marker does not match",
		},
		{
			name: "extra changed file",
			mutate: func(t *testing.T, _ *homebrewTapFixture, formula, head, merge string) map[string]string {
				return map[string]string{
					"FAKE_PR_BODY":      homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA),
					"FAKE_PR_FILES":     "Formula/env-vault.rb\nREADME.md",
					"FAKE_PR_HEAD_SHA":  head,
					"FAKE_PR_MERGE_SHA": merge,
				}
			},
			wantError: "pull request must change only Formula/env-vault.rb",
		},
		{
			name: "reported head differs from pull ref",
			mutate: func(t *testing.T, _ *homebrewTapFixture, formula, _ string, merge string) map[string]string {
				return map[string]string{
					"FAKE_PR_BODY":      homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA),
					"FAKE_PR_HEAD_SHA":  strings.Repeat("b", 40),
					"FAKE_PR_MERGE_SHA": merge,
				}
			},
			wantError: "pull request head SHA does not match its Git ref",
		},
		{
			name: "formula at head differs",
			mutate: func(t *testing.T, fixture *homebrewTapFixture, formula, _ string, merge string) map[string]string {
				wrongHead := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", merge+"^1")
				fixture.gitRun(t, "--git-dir="+fixture.origin, "update-ref", "refs/pull/23/head", wrongHead)
				return map[string]string{
					"FAKE_PR_BODY":      homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA),
					"FAKE_PR_HEAD_SHA":  wrongHead,
					"FAKE_PR_MERGE_SHA": merge,
				}
			},
			wantError: "tap head formula does not exactly match the generated formula",
		},
		{
			name: "reported merge formula differs even when current tap was restored",
			mutate: func(t *testing.T, fixture *homebrewTapFixture, formula, head, _ string) map[string]string {
				wrongFormula := fixture.writeFormula(t, "wrong-merge.rb", "1.2.3", "  # wrong merge bytes\n")
				copyTestFile(t, wrongFormula, filepath.Join(fixture.seed, "Formula", "env-vault.rb"))
				fixture.gitRun(t, "-C", fixture.seed, "add", "Formula/env-vault.rb")
				fixture.gitRun(t, "-C", fixture.seed, "commit", "-m", "unexpected merge formula")
				wrongMerge := fixture.gitOutput(t, "-C", fixture.seed, "rev-parse", "HEAD")
				copyTestFile(t, formula, filepath.Join(fixture.seed, "Formula", "env-vault.rb"))
				fixture.gitRun(t, "-C", fixture.seed, "add", "Formula/env-vault.rb")
				fixture.gitRun(t, "-C", fixture.seed, "commit", "-m", "restore exact formula")
				fixture.gitRun(t, "-C", fixture.seed, "push", "origin", "main")
				return map[string]string{
					"FAKE_PR_BODY":      homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA),
					"FAKE_PR_HEAD_SHA":  head,
					"FAKE_PR_MERGE_SHA": wrongMerge,
				}
			},
			wantError: "tap head formula does not exactly match the generated formula",
		},
		{
			name: "wrong pull request metadata",
			mutate: func(t *testing.T, _ *homebrewTapFixture, formula, head, merge string) map[string]string {
				return map[string]string{
					"FAKE_PR_BODY":      homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA),
					"FAKE_PR_HEAD_SHA":  head,
					"FAKE_PR_MERGE_SHA": merge,
					"FAKE_PR_TITLE":     "unexpected title",
				}
			},
			wantError: "pull request metadata does not match the release",
		},
		{
			name: "more than one deterministic pull request",
			mutate: func(t *testing.T, _ *homebrewTapFixture, formula, head, merge string) map[string]string {
				return map[string]string{
					"FAKE_PR_BODY":      homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA),
					"FAKE_PR_HEAD_SHA":  head,
					"FAKE_PR_MERGE_SHA": merge,
					"FAKE_PR_DUPLICATE": "true",
				}
			},
			wantError: "more than one pull request exists",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHomebrewTapFixture(t, "1.2.2")
			formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
			head, merge := fixture.mergeReleaseBranch(t, formula)
			extra := test.mutate(t, fixture, formula, head, merge)
			extra["FAKE_PR_STATE"] = "MERGED"
			extra["FAKE_PR_NUMBER"] = "23"
			refsBeforeVerification := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref")
			if refsBeforeVerification == "" {
				t.Fatal("fixture unexpectedly has no refs")
			}

			output, status := fixture.runPublishMode(t, extra, []string{"--verify-published-pr", releaseTestVersion, formula, homebrewTapRepository})
			if status == 0 || !strings.Contains(output, test.wantError) {
				t.Fatalf("verify-published-pr status=%d, want error %q\n%s", status, test.wantError, output)
			}
			assertHomebrewVerifyPublishedPRDidNotMutate(t, fixture, refsBeforeVerification)
			assertTokenNotExposed(t, output, readOptionalFile(t, fixture.ghLog))
		})
	}
}

func TestPublishHomebrewPRRejectsPublishedFormulaWithoutDeterministicPR(t *testing.T) {
	fixture := newHomebrewTapFixture(t, "1.2.3")
	formula := fixture.writeFormula(t, "target.rb", "1.2.3", "")
	main := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", "refs/heads/main")

	output, status := fixture.runPublish(t, nil, releaseTestVersion, formula)
	if status == 0 || !strings.Contains(output, "deterministic release pull request is missing") {
		t.Fatalf("published formula without PR status=%d\n%s", status, output)
	}
	if got := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", "refs/heads/main"); got != main {
		t.Fatalf("failed closed path changed main: got %s want %s", got, main)
	}
	calls := readOptionalFile(t, fixture.ghLog)
	if strings.Contains(calls, "pr create") {
		t.Fatalf("already-published formula created a PR:\n%s", calls)
	}
}

func TestPublishHomebrewPRFailsClosedOnVersionAndContentConflicts(t *testing.T) {
	tests := []struct {
		name             string
		published        string
		target           string
		extraFormula     string
		prepare          func(*testing.T, *homebrewTapFixture, string) map[string]string
		wantError        string
		wantBranchIntact bool
	}{
		{
			name:      "version downgrade",
			published: "1.2.3",
			target:    "1.2.2",
			wantError: "refusing to lower Homebrew",
		},
		{
			name:         "same version different formula",
			published:    "1.2.3",
			target:       "1.2.3",
			extraFormula: "  # conflicting checksum\n",
			wantError:    "differs from the generated formula",
		},
		{
			name:      "release branch changes another file",
			published: "1.2.2",
			target:    "1.2.3",
			prepare: func(t *testing.T, fixture *homebrewTapFixture, formula string) map[string]string {
				fixture.createReleaseBranch(t, formula, true)
				return nil
			},
			wantError:        "must change only Formula/env-vault.rb",
			wantBranchIntact: true,
		},
		{
			name:      "PR reports another file",
			published: "1.2.2",
			target:    "1.2.3",
			prepare: func(t *testing.T, fixture *homebrewTapFixture, formula string) map[string]string {
				head := fixture.createReleaseBranch(t, formula, false)
				return map[string]string{
					"FAKE_PR_STATE":    "OPEN",
					"FAKE_PR_NUMBER":   "31",
					"FAKE_PR_HEAD_SHA": head,
					"FAKE_PR_BODY":     homebrewPRBody(t, formula, releaseTestVersion, homebrewSourceSHA),
					"FAKE_PR_FILES":    "Formula/env-vault.rb\nREADME.md",
				}
			},
			wantError: "pull request must change only Formula/env-vault.rb",
		},
		{
			name:      "PR marker has another source SHA",
			published: "1.2.2",
			target:    "1.2.3",
			prepare: func(t *testing.T, fixture *homebrewTapFixture, formula string) map[string]string {
				head := fixture.createReleaseBranch(t, formula, false)
				return map[string]string{
					"FAKE_PR_STATE":    "OPEN",
					"FAKE_PR_NUMBER":   "32",
					"FAKE_PR_HEAD_SHA": head,
					"FAKE_PR_BODY":     homebrewPRBody(t, formula, releaseTestVersion, strings.Repeat("b", 40)),
				}
			},
			wantError: "release marker does not match",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHomebrewTapFixture(t, test.published)
			version := "v" + test.target
			formula := fixture.writeFormula(t, "target.rb", test.target, test.extraFormula)
			mainBefore := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", "refs/heads/main")
			extra := map[string]string{}
			if test.prepare != nil {
				for key, value := range test.prepare(t, fixture, formula) {
					extra[key] = value
				}
			}
			branchBefore := ""
			branchRef := "refs/heads/release/env-vault-" + version
			if fixture.refExists(t, branchRef) {
				branchBefore = fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", branchRef)
			}

			output, status := fixture.runPublish(t, extra, version, formula)
			if status == 0 {
				t.Fatalf("conflicting publication unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(output, test.wantError) {
				t.Fatalf("output missing %q:\n%s", test.wantError, output)
			}
			mainAfter := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", "refs/heads/main")
			if mainAfter != mainBefore {
				t.Fatalf("failed publication moved main from %s to %s", mainBefore, mainAfter)
			}
			if test.wantBranchIntact {
				branchAfter := fixture.gitOutput(t, "--git-dir="+fixture.origin, "rev-parse", branchRef)
				if branchAfter != branchBefore {
					t.Fatalf("conflicting release branch moved from %s to %s", branchBefore, branchAfter)
				}
			}
			assertTokenNotExposed(t, output, readOptionalFile(t, fixture.ghLog))
		})
	}
}

func newHomebrewTapFixture(t *testing.T, publishedVersion string) *homebrewTapFixture {
	t.Helper()
	root := t.TempDir()
	fixture := &homebrewTapFixture{
		root:        root,
		home:        filepath.Join(root, "home"),
		origin:      filepath.Join(root, "homebrew-tap.git"),
		seed:        filepath.Join(root, "seed"),
		fakeBin:     filepath.Join(root, "bin"),
		ghLog:       filepath.Join(root, "gh.log"),
		createdPR:   filepath.Join(root, "created-pr"),
		createdBody: filepath.Join(root, "created-body"),
	}
	for _, directory := range []string{fixture.home, fixture.fakeBin} {
		makeDirectory(t, directory)
	}
	fixture.gitRun(t, "init", "--bare", fixture.origin)
	fixture.gitRun(t, "init", "-b", "main", fixture.seed)
	fixture.gitRun(t, "-C", fixture.seed, "config", "user.name", "Tap Test")
	fixture.gitRun(t, "-C", fixture.seed, "config", "user.email", "tap@example.invalid")
	makeDirectory(t, filepath.Join(fixture.seed, "Formula"))
	formula := homebrewFormula(publishedVersion, "")
	if err := os.WriteFile(filepath.Join(fixture.seed, "Formula", "env-vault.rb"), []byte(formula), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.gitRun(t, "-C", fixture.seed, "add", "Formula/env-vault.rb")
	fixture.gitRun(t, "-C", fixture.seed, "commit", "-m", "initial formula")
	fixture.gitRun(t, "-C", fixture.seed, "remote", "add", "origin", "file://"+fixture.origin)
	fixture.gitRun(t, "-C", fixture.seed, "push", "-u", "origin", "main")
	fixture.gitRun(t, "--git-dir="+fixture.origin, "symbolic-ref", "HEAD", "refs/heads/main")

	gitConfig := fmt.Sprintf("[url %q]\n\tinsteadOf = https://github.com/%s.git\n", "file://"+fixture.origin, homebrewTapRepository)
	if err := os.WriteFile(filepath.Join(fixture.home, ".gitconfig"), []byte(gitConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	installHomebrewFakeGH(t, fixture.fakeBin)
	return fixture
}

func (fixture *homebrewTapFixture) writeFormula(t *testing.T, name, version, extra string) string {
	t.Helper()
	path := filepath.Join(fixture.root, name)
	if err := os.WriteFile(path, []byte(homebrewFormula(version, extra)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func homebrewFormula(version, extra string) string {
	return fmt.Sprintf("class EnvVault < Formula\n  version %q\n%s  test do\n    assert_equal \"v#{version}\", shell_output(\"#{bin}/env-vault --version\").strip\n  end\nend\n", version, extra)
}

func (fixture *homebrewTapFixture) createReleaseBranch(t *testing.T, formula string, addExtraFile bool) string {
	t.Helper()
	branch := "release/env-vault-" + releaseTestVersion
	fixture.gitRun(t, "-C", fixture.seed, "switch", "main")
	fixture.gitRun(t, "-C", fixture.seed, "reset", "--hard", "origin/main")
	fixture.gitRun(t, "-C", fixture.seed, "switch", "-C", branch)
	copyTestFile(t, formula, filepath.Join(fixture.seed, "Formula", "env-vault.rb"))
	fixture.gitRun(t, "-C", fixture.seed, "add", "Formula/env-vault.rb")
	if addExtraFile {
		if err := os.WriteFile(filepath.Join(fixture.seed, "README.md"), []byte("unexpected\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		fixture.gitRun(t, "-C", fixture.seed, "add", "README.md")
	}
	fixture.gitRun(t, "-C", fixture.seed, "commit", "-m", "release "+releaseTestVersion)
	fixture.gitRun(t, "-C", fixture.seed, "push", "origin", "HEAD:refs/heads/"+branch)
	head := fixture.gitOutput(t, "-C", fixture.seed, "rev-parse", "HEAD")
	fixture.gitRun(t, "--git-dir="+fixture.origin, "update-ref", "refs/pull/17/head", head)
	fixture.gitRun(t, "--git-dir="+fixture.origin, "update-ref", "refs/pull/31/head", head)
	fixture.gitRun(t, "--git-dir="+fixture.origin, "update-ref", "refs/pull/32/head", head)
	return head
}

func (fixture *homebrewTapFixture) mergeReleaseBranch(t *testing.T, formula string) (string, string) {
	t.Helper()
	head := fixture.createReleaseBranch(t, formula, false)
	branch := "release/env-vault-" + releaseTestVersion
	fixture.gitRun(t, "--git-dir="+fixture.origin, "update-ref", "refs/pull/23/head", head)
	fixture.gitRun(t, "-C", fixture.seed, "switch", "main")
	fixture.gitRun(t, "-C", fixture.seed, "merge", "--no-ff", branch, "-m", "merge "+releaseTestVersion)
	merge := fixture.gitOutput(t, "-C", fixture.seed, "rev-parse", "HEAD")
	fixture.gitRun(t, "-C", fixture.seed, "push", "origin", "main")
	fixture.gitRun(t, "-C", fixture.seed, "push", "origin", "--delete", branch)
	return head, merge
}

func (fixture *homebrewTapFixture) runPublish(t *testing.T, extra map[string]string, version, formula string) (string, int) {
	t.Helper()
	return fixture.runPublishMode(t, extra, []string{version, formula, homebrewTapRepository})
}

func (fixture *homebrewTapFixture) runPublishMode(t *testing.T, extra map[string]string, args []string) (string, int) {
	t.Helper()
	overrides := map[string]string{
		"FAKE_CREATED_BODY":   fixture.createdBody,
		"FAKE_CREATED_PR":     fixture.createdPR,
		"FAKE_GH_LOG":         fixture.ghLog,
		"FAKE_ORIGIN":         fixture.origin,
		"FAKE_PR_FILES":       "Formula/env-vault.rb",
		"FAKE_PR_STATE":       "none",
		"FAKE_VERSION":        releaseTestVersion,
		"GH_TOKEN":            "homebrew-test-token-must-not-appear",
		"GIT_ALLOW_PROTOCOL":  "file:https",
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
		"HOME":                fixture.home,
		"PATH":                fixture.fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"SOURCE_SHA":          homebrewSourceSHA,
		"TMPDIR":              fixture.root,
		"XDG_CONFIG_HOME":     filepath.Join(fixture.home, ".config"),
	}
	for key, value := range extra {
		overrides[key] = value
	}
	return runReleaseScript(t, "../scripts/release/publish-homebrew-pr.sh", args, overrides)
}

func (fixture *homebrewTapFixture) gitRun(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = environmentWithOverrides(map[string]string{
		"GIT_AUTHOR_DATE":    "2026-07-11T00:00:00Z",
		"GIT_COMMITTER_DATE": "2026-07-11T00:00:00Z",
	})
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func (fixture *homebrewTapFixture) gitOutput(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func (fixture *homebrewTapFixture) refExists(t *testing.T, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "--git-dir="+fixture.origin, "show-ref", "--verify", "--quiet", ref)
	err := cmd.Run()
	if err == nil {
		return true
	}
	if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
		return false
	}
	t.Fatalf("inspect ref %s: %v", ref, err)
	return false
}

func installHomebrewFakeGH(t *testing.T, binDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail

{
  printf '%q ' "$@"
  printf '\n'
} >> "$FAKE_GH_LOG"

if [[ ${1:-} == auth && ${2:-} == setup-git ]]; then
  exit 0
fi

if [[ ${1:-} == pr && ${2:-} == list ]]; then
  if [[ -f $FAKE_CREATED_PR ]]; then
    head_sha=$(git --git-dir="$FAKE_ORIGIN" rev-parse "refs/heads/release/env-vault-$FAKE_VERSION")
    printf '42\thttps://github.com/example/homebrew-tap/pull/42\tOPEN\trelease/env-vault-%s\t%s\t-\tmain\tenv-vault %s\tfalse\tfalse\n' "$FAKE_VERSION" "$head_sha" "$FAKE_VERSION"
  elif [[ ${FAKE_PR_STATE:-none} != none ]]; then
    pr_url=${FAKE_PR_URL:-https://github.com/example/homebrew-tap/pull/$FAKE_PR_NUMBER}
    head_ref=${FAKE_PR_HEAD_REF:-release/env-vault-$FAKE_VERSION}
    base_ref=${FAKE_PR_BASE_REF:-main}
    title=${FAKE_PR_TITLE:-env-vault $FAKE_VERSION}
    is_draft=${FAKE_PR_IS_DRAFT:-false}
    is_cross_repository=${FAKE_PR_IS_CROSS_REPOSITORY:-false}
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$FAKE_PR_NUMBER" "$pr_url" "$FAKE_PR_STATE" "$head_ref" "$FAKE_PR_HEAD_SHA" "${FAKE_PR_MERGE_SHA:--}" "$base_ref" "$title" "$is_draft" "$is_cross_repository"
    if [[ ${FAKE_PR_DUPLICATE:-false} == true ]]; then
      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$FAKE_PR_NUMBER" "$pr_url" "$FAKE_PR_STATE" "$head_ref" "$FAKE_PR_HEAD_SHA" "${FAKE_PR_MERGE_SHA:--}" "$base_ref" "$title" "$is_draft" "$is_cross_repository"
    fi
  fi
  exit 0
fi

if [[ ${1:-} == pr && ${2:-} == view ]]; then
  case " $* " in
    *' --json files '*)
      printf '%s\n' "${FAKE_PR_FILES:-Formula/env-vault.rb}"
      ;;
    *' --json body '*)
      if [[ -f $FAKE_CREATED_PR ]]; then
        cat "$FAKE_CREATED_BODY"
        printf '\n'
      else
        printf '%s\n' "${FAKE_PR_BODY:-}"
      fi
      ;;
    *)
      exit 92
      ;;
  esac
  exit 0
fi

if [[ ${1:-} == pr && ${2:-} == create ]]; then
  body=''
  while (($#)); do
    if [[ $1 == --body ]]; then
      body=$2
      shift 2
      continue
    fi
    shift
  done
  printf '%s' "$body" > "$FAKE_CREATED_BODY"
  touch "$FAKE_CREATED_PR"
  head_sha=$(git --git-dir="$FAKE_ORIGIN" rev-parse "refs/heads/release/env-vault-$FAKE_VERSION")
  git --git-dir="$FAKE_ORIGIN" update-ref refs/pull/42/head "$head_sha"
  printf 'https://github.com/example/homebrew-tap/pull/42\n'
  exit 0
fi

printf 'fake gh: unsupported command: %s\n' "$*" >&2
exit 91
`
	writeExecutable(t, filepath.Join(binDir, "gh"), script)
}

func homebrewPRBody(t *testing.T, formula, version, sourceSHA string) string {
	t.Helper()
	contents, err := os.ReadFile(formula)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	marker := fmt.Sprintf("<!-- env-vault-release version=%s source_sha=%s formula_sha256=%x -->", version, sourceSHA, digest)
	return fmt.Sprintf("Automated Homebrew formula update for env-vault %s.\n\nSource release: https://github.com/ildarbinanas-design/env-vault/releases/tag/%s\n\n%s", version, version, marker)
}

func parseHomebrewPublishOutputs(t *testing.T, output string) map[string]string {
	t.Helper()
	known := map[string]bool{
		"branch": true, "base_branch": true, "base_sha": true, "source_sha": true,
		"pr_number": true, "pr_url": true, "head_sha": true, "merge_sha": true,
		"tap_sha": true, "merge_is_ancestor_of_tap": true,
		"state": true, "already_merged": true, "no_op": true,
	}
	values := make(map[string]string, len(known))
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || !known[key] {
			continue
		}
		if _, duplicate := values[key]; duplicate {
			t.Fatalf("duplicate output key %s:\n%s", key, output)
		}
		values[key] = value
	}
	if len(values) != len(known) {
		t.Fatalf("got %d output keys, want %d:\n%s", len(values), len(known), output)
	}
	return values
}

func assertHomebrewPublishOutputs(t *testing.T, got, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if got[key] != value {
			t.Errorf("output %s=%q, want %q", key, got[key], value)
		}
	}
}

func assertTokenNotExposed(t *testing.T, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(value, "homebrew-test-token-must-not-appear") {
			t.Fatalf("GH_TOKEN was exposed:\n%s", value)
		}
	}
}

func assertHomebrewVerifyPublishedPRDidNotMutate(t *testing.T, fixture *homebrewTapFixture, refsBefore string) {
	t.Helper()
	refsAfter := fixture.gitOutput(t, "--git-dir="+fixture.origin, "show-ref")
	if refsAfter != refsBefore {
		t.Fatalf("verify-published-pr changed remote refs\nbefore:\n%s\nafter:\n%s", refsBefore, refsAfter)
	}
	if _, err := os.Stat(fixture.createdPR); !os.IsNotExist(err) {
		t.Fatalf("verify-published-pr created mutation sentinel %s: %v", fixture.createdPR, err)
	}
	calls := readOptionalFile(t, fixture.ghLog)
	for _, forbidden := range []string{"auth setup-git", "pr create", "pr edit", "pr merge", "api --method POST", "api --method PATCH", "api --method PUT", "api --method DELETE"} {
		if strings.Contains(calls, forbidden) {
			t.Fatalf("verify-published-pr invoked mutation %q:\n%s", forbidden, calls)
		}
	}
}

func copyTestFile(t *testing.T, source, destination string) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}
