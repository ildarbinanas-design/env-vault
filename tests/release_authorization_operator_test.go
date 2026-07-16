package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	releaseAuthorizationRepository = "ildarbinanas-design/env-vault"
	releaseAuthorizationVersion    = "v0.0.13"
	releaseAuthorizationPR         = "42"
	releaseAuthorizationHeadSHA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	releaseAuthorizationBaseSHA    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	releaseAuthorizationMergeSHA   = "cccccccccccccccccccccccccccccccccccccccc"
	releaseAuthorizationNextSHA    = "ffffffffffffffffffffffffffffffffffffffff"
)

func TestReleaseAuthorizationOperatorRecordsThenMergesExactTuple(t *testing.T) {
	root := t.TempDir()
	fakeBin, callLog := installReleaseAuthorizationFakeGitHub(t, root)

	stdout, stderr, status := runReleaseAuthorizationOperator(t, root, fakeBin, callLog, "success")
	if status != 0 {
		t.Fatalf("exit status=%d, want 0\nstdout:\n%s\nstderr:\n%s", status, stdout, stderr)
	}

	if stdout != releaseAuthorizationMergeSHA+"\n" {
		t.Fatalf("stdout=%q, want exact merge SHA", stdout)
	}

	calls := readOptionalFile(t, callLog)
	canonical := "body=ПОДТВЕРЖДАЮ RELEASE " + releaseAuthorizationVersion + " PR #" + releaseAuthorizationPR + " SHA " + releaseAuthorizationHeadSHA
	if got := strings.Count(calls, "--method POST repos/"+releaseAuthorizationRepository+"/issues/"+releaseAuthorizationPR+"/comments "+"--raw-field "+canonical); got != 1 {
		t.Fatalf("exact authorization comment writes=%d, want 1\n%s", got, calls)
	}
	mergeCall := "pr merge " + releaseAuthorizationPR + " --repo " + releaseAuthorizationRepository + " --squash --match-head-commit " + releaseAuthorizationHeadSHA
	if got := strings.Count(calls, mergeCall); got != 1 {
		t.Fatalf("exact guarded merge calls=%d, want 1\n%s", got, calls)
	}
	if strings.Index(calls, canonical) > strings.Index(calls, mergeCall) {
		t.Fatalf("merge preceded durable authorization comment:\n%s", calls)
	}
	if got := strings.Count(calls, "pr checks "+releaseAuthorizationPR+" --repo "+releaseAuthorizationRepository+" --required"); got != 2 {
		t.Fatalf("required-check observations=%d, want 2\n%s", got, calls)
	}
	contractCalls := readOptionalFile(t, filepath.Join(fakeBin, "releasecheck-calls.log"))
	contractValidation := "validate-contract --contract"
	if got := strings.Count(contractCalls, contractValidation); got != 1 {
		t.Fatalf("offline contract validations=%d, want 1\n%s", got, contractCalls)
	}
	for _, forbidden := range []string{"--admin", "--force", "--auto", "rerun"} {
		if strings.Contains(calls, forbidden) {
			t.Fatalf("operator used forbidden action %q:\n%s", forbidden, calls)
		}
	}
}

func TestReleaseAuthorizationOperatorReusesOneExactComment(t *testing.T) {
	root := t.TempDir()
	fakeBin, callLog := installReleaseAuthorizationFakeGitHub(t, root)

	_, stderr, status := runReleaseAuthorizationOperator(t, root, fakeBin, callLog, "existing-comment")
	if status != 0 {
		t.Fatalf("exit status=%d, want 0\nstderr:\n%s", status, stderr)
	}
	calls := readOptionalFile(t, callLog)
	if strings.Contains(calls, "--method POST") {
		t.Fatalf("idempotent retry created a duplicate comment:\n%s", calls)
	}
	if got := strings.Count(calls, "pr merge "+releaseAuthorizationPR); got != 1 {
		t.Fatalf("guarded merge calls=%d, want 1\n%s", got, calls)
	}
}

func TestReleaseAuthorizationOperatorReconcilesAmbiguousCommentWrite(t *testing.T) {
	root := t.TempDir()
	fakeBin, callLog := installReleaseAuthorizationFakeGitHub(t, root)

	_, stderr, status := runReleaseAuthorizationOperator(t, root, fakeBin, callLog, "ambiguous-write")
	if status != 0 {
		t.Fatalf("exit status=%d, want 0\nstderr:\n%s", status, stderr)
	}
	calls := readOptionalFile(t, callLog)
	if got := strings.Count(calls, "--method POST repos/"+releaseAuthorizationRepository+"/issues/"+releaseAuthorizationPR+"/comments"); got != 1 {
		t.Fatalf("ambiguous write attempts=%d, want exactly 1\n%s", got, calls)
	}
	if got := strings.Count(calls, "pr merge "+releaseAuthorizationPR); got != 1 {
		t.Fatalf("guarded merge calls=%d, want 1\n%s", got, calls)
	}
}

func TestReleaseAuthorizationOperatorReconcilesAmbiguousMerge(t *testing.T) {
	root := t.TempDir()
	fakeBin, callLog := installReleaseAuthorizationFakeGitHub(t, root)

	stdout, stderr, status := runReleaseAuthorizationOperator(t, root, fakeBin, callLog, "ambiguous-merge")
	if status != 0 || stdout != releaseAuthorizationMergeSHA+"\n" {
		t.Fatalf("status=%d stdout=%q, want reconciled exact merge\nstderr:\n%s", status, stdout, stderr)
	}
	calls := readOptionalFile(t, callLog)
	if got := strings.Count(calls, "pr merge "+releaseAuthorizationPR); got != 1 {
		t.Fatalf("ambiguous merge attempts=%d, want exactly 1\n%s", got, calls)
	}
}

func TestReleaseAuthorizationOperatorRetriesReadOnlyObservation(t *testing.T) {
	root := t.TempDir()
	fakeBin, callLog := installReleaseAuthorizationFakeGitHub(t, root)

	_, stderr, status := runReleaseAuthorizationOperator(t, root, fakeBin, callLog, "transient-comments-read")
	if status != 0 {
		t.Fatalf("exit status=%d, want 0\nstderr:\n%s", status, stderr)
	}
	calls := readOptionalFile(t, callLog)
	if got := strings.Count(calls, "issues/"+releaseAuthorizationPR+"/comments?per_page=100"); got < 4 {
		t.Fatalf("comment read calls=%d, want a bounded retry plus normal observations\n%s", got, calls)
	}
	if got := strings.Count(calls, "--method POST repos/"+releaseAuthorizationRepository+"/issues/"+releaseAuthorizationPR+"/comments"); got != 1 {
		t.Fatalf("comment mutation calls=%d, want exactly 1\n%s", got, calls)
	}
}

func TestReleaseAuthorizationOperatorWaitsForMergedFieldsAndMain(t *testing.T) {
	for _, mode := range []string{"delayed-merge-fields", "stale-main-after-merge", "main-advanced-after-merge"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			fakeBin, callLog := installReleaseAuthorizationFakeGitHub(t, root)

			stdout, stderr, status := runReleaseAuthorizationOperator(t, root, fakeBin, callLog, mode)
			if status != 0 || stdout != releaseAuthorizationMergeSHA+"\n" {
				t.Fatalf("status=%d stdout=%q, want exact reconciled merge\nstderr:\n%s", status, stdout, stderr)
			}
			calls := readOptionalFile(t, callLog)
			if got := strings.Count(calls, "pr merge "+releaseAuthorizationPR); got != 1 {
				t.Fatalf("merge calls=%d, want exactly 1\n%s", got, calls)
			}
		})
	}
}

func TestReleaseAuthorizationOperatorResumesExactMergedTupleWithoutMutation(t *testing.T) {
	root := t.TempDir()
	fakeBin, callLog := installReleaseAuthorizationFakeGitHub(t, root)

	stdout, stderr, status := runReleaseAuthorizationOperator(t, root, fakeBin, callLog, "already-merged")
	if status != 0 || stdout != releaseAuthorizationMergeSHA+"\n" {
		t.Fatalf("status=%d stdout=%q, want resumed exact merge\nstderr:\n%s", status, stdout, stderr)
	}
	calls := readOptionalFile(t, callLog)
	for _, mutation := range []string{"--method POST", "pr merge " + releaseAuthorizationPR} {
		if strings.Contains(calls, mutation) {
			t.Fatalf("resume path performed mutation %q:\n%s", mutation, calls)
		}
	}
}

func TestReleaseAuthorizationOperatorFailsClosed(t *testing.T) {
	tests := []struct {
		name          string
		mode          string
		wantError     string
		wantComment   bool
		wantMergeCall bool
	}{
		{name: "head drift", mode: "head-drift", wantError: "tuple or provenance changed"},
		{name: "untrusted viewer", mode: "untrusted-viewer", wantError: "not the repository owner"},
		{name: "base contract differs", mode: "contract-base-mismatch", wantError: "local release contract differs"},
		{name: "contract validator fails", mode: "contract-validator-fails", wantError: "contract validation failed"},
		{name: "resumed proposal invalid", mode: "already-merged-invalid-proposal", wantError: "generated release proposal verification failed"},
		{name: "required checks fail", mode: "checks-fail", wantError: "required checks are not all successful"},
		{name: "required checks empty", mode: "empty-checks", wantError: "required checks are incomplete or malformed"},
		{name: "required check missing", mode: "missing-check", wantError: "required checks are incomplete or malformed"},
		{name: "required check duplicated", mode: "duplicate-check", wantError: "required checks are incomplete or malformed"},
		{name: "required check malformed", mode: "malformed-check", wantError: "required checks are incomplete or malformed"},
		{name: "required check event changed", mode: "wrong-check-event", wantError: "required checks are incomplete or malformed"},
		{name: "required check identities drift", mode: "check-set-drift", wantError: "required check identities changed", wantComment: true},
		{name: "duplicate trusted comments", mode: "duplicate-comment", wantError: "multiple exact release confirmation", wantComment: false},
		{name: "malformed trusted comment", mode: "malformed-trusted-comment", wantError: "malformed release confirmation comments", wantComment: false},
		{name: "comment actor untrusted", mode: "untrusted-comment", wantError: "exactly one exact release confirmation", wantComment: true},
		{name: "server second never advances", mode: "stalled-date", wantError: "did not expose a later server second", wantComment: true},
		{name: "server time moves backward", mode: "backward-date", wantError: "did not expose a later server second", wantComment: true},
		{name: "comment timestamp ahead of server", mode: "future-comment", wantError: "did not expose a later server second", wantComment: true},
		{name: "comment edited before merge", mode: "edited-comment", wantError: "confirmation changed before merge", wantComment: true},
		{name: "comment edited after merge", mode: "edited-after-merge", wantError: "confirmation changed after merge", wantComment: true, wantMergeCall: true},
		{name: "comment deleted after merge", mode: "deleted-after-merge", wantError: "exactly one exact release confirmation", wantComment: true, wantMergeCall: true},
		{name: "merge command fails", mode: "merge-failure", wantError: "cannot reconcile the failed", wantComment: true, wantMergeCall: true},
		{name: "merge fields never settle", mode: "merge-fields-never-settle", wantError: "timed out waiting for the exact", wantComment: true, wantMergeCall: true},
		{name: "main conflicts after merge", mode: "main-conflict-after-merge", wantError: "current main conflicts", wantComment: true, wantMergeCall: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			fakeBin, callLog := installReleaseAuthorizationFakeGitHub(t, root)
			stdout, stderr, status := runReleaseAuthorizationOperator(t, root, fakeBin, callLog, test.mode)
			if status == 0 {
				t.Fatalf("unexpected success\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("failure stdout=%q, want empty", stdout)
			}
			if !strings.Contains(stderr, test.wantError) {
				t.Fatalf("stderr missing %q:\n%s", test.wantError, stderr)
			}
			calls := readOptionalFile(t, callLog)
			hasComment := strings.Contains(calls, "--method POST repos/"+releaseAuthorizationRepository+"/issues/"+releaseAuthorizationPR+"/comments")
			if hasComment != test.wantComment {
				t.Fatalf("comment written=%t, want %t\n%s", hasComment, test.wantComment, calls)
			}
			hasMerge := strings.Contains(calls, "pr merge "+releaseAuthorizationPR)
			if hasMerge != test.wantMergeCall {
				t.Fatalf("merge called=%t, want %t\n%s", hasMerge, test.wantMergeCall, calls)
			}
		})
	}
}

func runReleaseAuthorizationOperator(t *testing.T, root, fakeBin, callLog, mode string) (stdout, stderr string, status int) {
	t.Helper()
	checker := filepath.Join(fakeBin, "releasecheck")
	if mode == "contract-validator-fails" {
		checker = filepath.Join(fakeBin, "releasecheck-fail")
	}
	cmd := exec.Command("bash", "../scripts/release/authorize-and-merge-release-pr.sh",
		releaseAuthorizationVersion, releaseAuthorizationPR, releaseAuthorizationHeadSHA)
	cmd.Env = environmentWithOverrides(map[string]string{
		"FAKE_RELEASE_AUTH_MODE":  mode,
		"FAKE_RELEASE_AUTH_STATE": filepath.Join(root, "state"),
		"FAKE_GH_CALL_LOG":        callLog,
		"GITHUB_REPOSITORY":       releaseAuthorizationRepository,
		"PATH":                    fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"RELEASECHECK":            checker,
		"FAKE_RELEASE_CONTRACT":   filepath.Join("..", "release", "contract.v1.json"),
		"TMPDIR":                  root,
		"GH_TOKEN":                "fake-gh-token",
		"GITHUB_TOKEN":            "fake-github-token",
		"GH_DEBUG":                "api",
		"GIT_TRACE":               "1",
		"GIT_TRACE_CURL":          "1",
		"GIT_CURL_VERBOSE":        "1",
		"GIT_TRACE_PACKET":        "1",
	})
	var stdoutBuffer, stderrBuffer bytes.Buffer
	cmd.Stdout = &stdoutBuffer
	cmd.Stderr = &stderrBuffer
	err := cmd.Run()
	status = 0
	if err != nil {
		exitError, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run release authorization operator: %v", err)
		}
		status = exitError.ExitCode()
	}
	return stdoutBuffer.String(), stderrBuffer.String(), status
}

func installReleaseAuthorizationFakeGitHub(t *testing.T, root string) (binDir, callLog string) {
	t.Helper()
	binDir = filepath.Join(root, "bin")
	callLog = filepath.Join(root, "gh-calls.log")
	makeDirectory(t, binDir)
	writeExecutable(t, filepath.Join(binDir, "sleep"), `#!/usr/bin/env bash
set -euo pipefail
case ${1:-} in
  1|2) ;;
  *) echo "unexpected sleep duration: ${1:-missing}" >&2; exit 1 ;;
esac
printf '%s\n' "$1" >> "${FAKE_RELEASE_AUTH_STATE:?}/sleep-calls.log"
`)
	writeExecutable(t, filepath.Join(binDir, "releasecheck"), `#!/bin/bash
set -euo pipefail
for name in GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN; do
  [[ -z ${!name+x} ]] || {
    echo "credential environment leaked into offline releasecheck" >&2
    exit 1
  }
done
[[ ${1:-} == validate-contract && ${2:-} == --contract && -n ${3:-} && ${4:-} == --json ]] || {
  echo "unexpected fake releasecheck call: $*" >&2
  exit 1
}
printf '%s\n' "$*" >> "${0%/*}/releasecheck-calls.log"
: > "${0%/*}/releasecheck-ok"
printf '%s\n' '{"schema_id":"env-vault.contract-validation.v1","schema_version":1,"ok":true,"release_contract_schema":"env-vault.release-contract.v1","semantic_contract_sha256":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","platform_count":5,"asset_count":10}'
`)
	writeExecutable(t, filepath.Join(binDir, "releasecheck-fail"), `#!/bin/bash
set -euo pipefail
for name in GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN; do
  [[ -z ${!name+x} ]] || exit 1
done
exit 1
`)
	writeExecutable(t, filepath.Join(binDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail

if [[ -n ${GH_DEBUG:-} || -n ${GIT_TRACE:-} || -n ${GIT_TRACE_CURL:-} || -n ${GIT_CURL_VERBOSE:-} || -n ${GIT_TRACE_PACKET:-} ]]; then
  echo "credential-bearing debug or trace environment leaked into gh" >&2
  exit 1
fi
[[ ${GH_TOKEN:-} == fake-gh-token && ${GITHUB_TOKEN:-} == fake-github-token ]] || {
  echo "GitHub credentials were not retained for gh transport" >&2
  exit 1
}

mode=${FAKE_RELEASE_AUTH_MODE:?}
state_dir=${FAKE_RELEASE_AUTH_STATE:?}
call_log=${FAKE_GH_CALL_LOG:?}
mkdir -p "$state_dir"
printf '%s\n' "$*" >> "$call_log"

repository=ildarbinanas-design/env-vault
version=v0.0.13
pr=42
head=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
base=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
merge=cccccccccccccccccccccccccccccccccccccccc
next=ffffffffffffffffffffffffffffffffffffffff
tree=dddddddddddddddddddddddddddddddddddddddd
actor=ildarbinanas-design
comment_id=9001
canonical_body="ПОДТВЕРЖДАЮ RELEASE $version PR #$pr SHA $head"
pr_body='Merging this unchanged reviewed pull request after the required exact tuple confirmation authorizes publication once its merge commit passes main CI. This PR was generated with Release Please.'
release_branch=release-please--branches--main--components--env-vault

has_arg() {
  local expected=$1
  shift
  local arg
  for arg in "$@"; do
    [[ "$arg" == "$expected" ]] && return 0
  done
  return 1
}

comment_json() {
  local association=OWNER
  local updated_at=2026-07-16T22:00:00Z
  [[ "$mode" == untrusted-comment ]] && association=CONTRIBUTOR
  if [[ "$mode" == edited-comment && ${1:-normal} == edited ]]; then
    updated_at=2026-07-16T22:00:01Z
  fi
  if [[ "$mode" == edited-after-merge && ${1:-normal} == edited-after-merge ]]; then
    updated_at=2026-07-16T22:00:03Z
  fi
  if [[ "$mode" == future-comment ]]; then
    updated_at=2026-07-16T22:00:05Z
  fi
  record=$(jq -cn \
    --arg body "$canonical_body" \
    --arg actor "$actor" \
    --arg association "$association" \
    --arg updated_at "$updated_at" \
    --arg repository "$repository" \
    --argjson pr "$pr" \
    --argjson id "$comment_id" \
    '{id:$id,html_url:("https://github.com/"+$repository+"/pull/"+($pr|tostring)+"#issuecomment-"+($id|tostring)),body:$body,user:{login:$actor,type:"User"},author_association:$association,created_at:"2026-07-16T22:00:00Z",updated_at:$updated_at}')
  if [[ "$mode" == malformed-trusted-comment ]]; then
    record=$(jq -c '.id="not-an-integer"' <<< "$record")
  fi
  printf '%s\n' "$record"
}

pull_json() {
  local observed_head=$head
  [[ "$mode" == head-drift ]] && observed_head=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
  if [[ -f "$state_dir/merged" || "$mode" == already-merged || "$mode" == already-merged-invalid-proposal ]]; then
    count=0
    [[ -f "$state_dir/merged-pull-count" ]] && read -r count < "$state_dir/merged-pull-count"
    count=$((count + 1))
    printf '%s\n' "$count" > "$state_dir/merged-pull-count"
    if [[ "$mode" == merge-fields-never-settle || ("$mode" == delayed-merge-fields && $count -eq 1) ]]; then
      jq -cn \
        --arg repository "$repository" --arg head "$observed_head" --arg base "$base" \
        --arg branch "$release_branch" --arg body "$pr_body" --argjson pr "$pr" \
        '{number:$pr,state:"closed",merged:false,draft:false,merged_at:null,merge_commit_sha:null,base:{ref:"main",sha:$base,repo:{full_name:$repository}},head:{ref:$branch,sha:$head,repo:{full_name:$repository}},user:{login:"env-vault-release-planning[bot]"},title:"chore(main): release env-vault v0.0.13",body:$body,labels:[{name:"autorelease: pending"}]}'
      return
    fi
    jq -cn \
      --arg repository "$repository" --arg head "$observed_head" --arg base "$base" --arg merge "$merge" \
      --arg branch "$release_branch" --arg body "$pr_body" --argjson pr "$pr" \
      '{number:$pr,state:"closed",merged:true,draft:false,merged_at:"2026-07-16T22:00:02Z",merge_commit_sha:$merge,base:{ref:"main",sha:$base,repo:{full_name:$repository}},head:{ref:$branch,sha:$head,repo:{full_name:$repository}},user:{login:"env-vault-release-planning[bot]"},title:"chore(main): release env-vault v0.0.13",body:$body,labels:[{name:"autorelease: pending"}]}'
  else
    jq -cn \
      --arg repository "$repository" --arg head "$observed_head" --arg base "$base" \
      --arg branch "$release_branch" --arg body "$pr_body" --argjson pr "$pr" \
      '{number:$pr,state:"open",merged:false,draft:false,merged_at:null,merge_commit_sha:null,base:{ref:"main",sha:$base,repo:{full_name:$repository}},head:{ref:$branch,sha:$head,repo:{full_name:$repository}},user:{login:"env-vault-release-planning[bot]"},title:"chore(main): release env-vault v0.0.13",body:$body,labels:[{name:"autorelease: pending"}]}'
  fi
}

if [[ ${1:-} == pr && ${2:-} == checks ]]; then
  if [[ "$mode" == checks-fail ]]; then
    exit 1
  fi
  if [[ "$mode" == empty-checks ]]; then
    printf '[]\n'
    exit 0
  fi
  count=0
  [[ -f "$state_dir/check-count" ]] && read -r count < "$state_dir/check-count"
  count=$((count + 1))
  printf '%s\n' "$count" > "$state_dir/check-count"
  quality_link=https://github.com/test/quality
  if [[ "$mode" == check-set-drift && $count -ge 2 ]]; then
    quality_link=https://github.com/test/quality-changed
  fi
  jq -cn --arg quality "$quality_link" --arg mode "$mode" '[
    {name:"Analyze (actions)",state:"SUCCESS",bucket:"pass",link:"https://github.com/test/actions",workflow:"CodeQL",event:"dynamic"},
    {name:"Analyze (go)",state:"SUCCESS",bucket:"pass",link:"https://github.com/test/go",workflow:"CodeQL",event:"dynamic"},
    {name:"Dependency review",state:"SUCCESS",bucket:"pass",link:"https://github.com/test/dependency",workflow:"Dependency review",event:"pull_request"},
    {name:"pr-title",state:"SUCCESS",bucket:"pass",link:"https://github.com/test/title",workflow:"pr-title",event:"pull_request"},
    {name:"quality-gate",state:"SUCCESS",bucket:"pass",link:$quality,workflow:"ci",event:"pull_request"}
  ] |
  if $mode == "missing-check" then .[0:4]
  elif $mode == "duplicate-check" then .[4] = .[3]
  elif $mode == "malformed-check" then .[0].link = null
  elif $mode == "wrong-check-event" then .[4].event = "workflow_dispatch"
  else .
  end'
  exit
fi

if [[ ${1:-} == pr && ${2:-} == merge ]]; then
  if [[ "$mode" == merge-failure ]]; then
    exit 1
  fi
  touch "$state_dir/merged"
  [[ "$mode" != ambiguous-merge ]] || exit 1
  exit 0
fi

[[ ${1:-} == api ]] || {
  echo "unsupported fake gh call: $*" >&2
  exit 1
}
args=" $* "

if [[ "$args" == " api user " ]]; then
  jq -cn --arg actor "$actor" '{login:$actor,type:"User"}'
  exit 0
fi

if [[ "$args" == *" repos/$repository/pulls/$pr "* ]]; then
  pull_json
  exit 0
fi

if [[ "$args" == *" repos/$repository/pulls "* ]]; then
  record=$(pull_json)
  printf '[[%s]]\n' "$record"
  exit 0
fi

if [[ "$args" == *" repos/$repository/git/commits/$head "* ]]; then
  jq -cn --arg head "$head" --arg base "$base" --arg tree "$tree" \
    '{sha:$head,message:"chore(main): release env-vault v0.0.13",tree:{sha:$tree},parents:[{sha:$base}]}'
  exit 0
fi

if [[ "$args" == *" repos/$repository/compare/$base...$head "* ]]; then
  jq -cn '{status:"ahead",ahead_by:1,total_commits:1,files:[{filename:".release-please-manifest.json",status:"modified"},{filename:"CHANGELOG.md",status:"modified"},{filename:"README.md",status:"modified"}]}'
  exit 0
fi

if [[ "$args" == *" repos/$repository/git/trees/$tree?recursive=1 "* ]]; then
  jq -cn '{tree:[{path:".release-please-manifest.json",mode:"100644",type:"blob"},{path:"CHANGELOG.md",mode:"100644",type:"blob"},{path:"README.md",mode:"100644",type:"blob"}]}'
  exit 0
fi

if [[ "$args" == *" repos/$repository/git/ref/heads/main "* ]]; then
  current=$base
  if [[ -f "$state_dir/merged" || "$mode" == already-merged || "$mode" == already-merged-invalid-proposal ]]; then
    current=$merge
    if [[ "$mode" == main-advanced-after-merge || "$mode" == main-conflict-after-merge ]]; then
      current=$next
    elif [[ "$mode" == stale-main-after-merge ]]; then
      count=0
      [[ -f "$state_dir/merged-main-count" ]] && read -r count < "$state_dir/merged-main-count"
      count=$((count + 1))
      printf '%s\n' "$count" > "$state_dir/merged-main-count"
      [[ $count -gt 1 ]] || current=$base
    fi
  fi
  if has_arg --jq "$@"; then
    printf '%s\n' "$current"
  else
    jq -cn --arg sha "$current" '{object:{sha:$sha}}'
  fi
  exit 0
fi

if [[ "$args" == *" repos/$repository/compare/$base...$base "* ]]; then
  jq -cn '{status:"identical"}'
  exit 0
fi

if [[ "$args" == *" repos/$repository/compare/$base...$merge "* ]]; then
  jq -cn '{status:"ahead"}'
  exit 0
fi

if [[ "$args" == *" repos/$repository/compare/$merge...$merge "* ]]; then
  jq -cn '{status:"identical"}'
  exit 0
fi

if [[ "$args" == *" repos/$repository/compare/$merge...$base "* ]]; then
  jq -cn '{status:"behind"}'
  exit 0
fi

if [[ "$args" == *" repos/$repository/compare/$merge...$next "* ]]; then
  if [[ "$mode" == main-conflict-after-merge ]]; then
    jq -cn '{status:"diverged"}'
  else
    jq -cn '{status:"ahead"}'
  fi
  exit 0
fi

if [[ "$args" == *" repos/$repository/actions/workflows/ci.yml/runs "* ]]; then
  jq -cn --arg base "$base" '{workflow_runs:[{head_sha:$base,head_branch:"main",event:"push",conclusion:"success"}]}'
  exit 0
fi

if [[ "$args" == *" repos/$repository/contents/.release-please-manifest.json?ref=$head "* ]]; then
  if [[ "$mode" == already-merged-invalid-proposal ]]; then
    printf '{".":"0.0.99"}\n'
  else
    printf '{".":"0.0.13"}\n'
  fi
  exit 0
fi

if [[ "$args" == *" repos/$repository/contents/release/contract.v1.json?ref=$base "* ]]; then
  if [[ "$mode" == contract-base-mismatch ]]; then
    printf '{}\n'
  else
    cat "${FAKE_RELEASE_CONTRACT:?}"
  fi
  exit 0
fi

if [[ "$args" == *" repos/$repository/contents/README.md?ref=$head "* ]]; then
  printf 'Current version: \x60v0.0.13\x60. <!-- x-release-please-version -->\n'
  exit 0
fi

if [[ "$args" == *" repos/$repository/contents/CHANGELOG.md?ref=$head "* ]]; then
  printf '# Changelog\n\n## [0.0.13](https://github.com/%s/compare/v0.0.12...v0.0.13) (2026-07-16)\n\n### Bug Fixes\n\n* **release:** record authorization before merge\n' "$repository"
  exit 0
fi

if [[ "$args" == *" repos/$repository/issues/$pr/comments?per_page=100 "* ]]; then
  count=0
  [[ -f "$state_dir/comment-list-count" ]] && read -r count < "$state_dir/comment-list-count"
  count=$((count + 1))
  printf '%s\n' "$count" > "$state_dir/comment-list-count"
  if [[ "$mode" == transient-comments-read && $count -eq 1 ]]; then
    exit 1
  fi
  if [[ "$mode" == deleted-after-merge && -f "$state_dir/merged" ]]; then
    printf '[[]]\n'
  elif [[ "$mode" == duplicate-comment ]]; then
    first=$(comment_json)
    second=$(comment_json | jq -c --arg repository "$repository" --argjson pr "$pr" '.id=9002 | .html_url=("https://github.com/"+$repository+"/pull/"+($pr|tostring)+"#issuecomment-9002")')
    printf '[[%s,%s]]\n' "$first" "$second"
  elif [[ "$mode" == existing-comment || "$mode" == already-merged || "$mode" == already-merged-invalid-proposal || "$mode" == malformed-trusted-comment || -f "$state_dir/comment" ]]; then
    variant=normal
    [[ "$mode" == edited-comment && $count -ge 3 ]] && variant=edited
    [[ "$mode" == edited-after-merge && -f "$state_dir/merged" ]] && variant=edited-after-merge
    record=$(comment_json "$variant")
    printf '[[%s]]\n' "$record"
  else
    printf '[[]]\n'
  fi
  exit 0
fi

if [[ "$args" == *" --method POST repos/$repository/issues/$pr/comments "* ]]; then
  [[ -f "${0%/*}/releasecheck-ok" ]] || {
    echo "authorization mutation preceded offline contract validation" >&2
    exit 1
  }
  [[ "$args" == *" --raw-field body=$canonical_body"* ]] || {
    echo "wrong canonical comment" >&2
    exit 1
  }
  touch "$state_dir/comment"
  comment_json
  [[ "$mode" != ambiguous-write ]] || exit 1
  exit 0
fi

if [[ "$args" == *" --include repos/$repository/issues/comments/$comment_id "* ]]; then
  count=0
  [[ -f "$state_dir/date-count" ]] && read -r count < "$state_dir/date-count"
  count=$((count + 1))
  printf '%s\n' "$count" > "$state_dir/date-count"
  second=00
  if [[ "$mode" == backward-date && $count -eq 1 ]]; then
    second=02
  elif [[ "$mode" != stalled-date && $count -ge 2 ]]; then
    second=01
  fi
  printf 'HTTP/2 200 OK\r\nDate: Thu, 16 Jul 2026 22:00:%s GMT\r\nContent-Type: application/json\r\n\r\n' "$second"
  comment_json
  exit 0
fi

if [[ "$args" == " api repos/$repository " ]]; then
  owner=$actor
  [[ "$mode" == untrusted-viewer ]] && owner=someone-else
  jq -cn --arg owner "$owner" '{owner:{login:$owner,type:"User"}}'
  exit 0
fi

echo "unsupported fake gh api call: $*" >&2
exit 1
`)
	return binDir, callLog
}
