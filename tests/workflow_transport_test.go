package tests

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowGHAPIReadQueryFieldsRequireExplicitGET(t *testing.T) {
	for _, workflowPath := range workflowPaths(t) {
		wf := readWorkflow(t, workflowPath)
		for jobName, job := range wf.Jobs {
			for stepIndex, step := range job.Steps {
				for _, violation := range ghAPIReadQueryMethodViolations(step.Run) {
					t.Errorf("%s job %s step %d (%s): %s", workflowPath, jobName, stepIndex, step.Name, violation)
				}
			}
		}
	}
}

func TestGHAPIReadQueryMethodValidatorCoversMultilineAndAliases(t *testing.T) {
	valid := []string{
		`scripts/release/gh-api-read.sh output.json endpoint`,
		`scripts/release/gh-api-read.sh output.json \
  --paginate --slurp --method GET \
  "repos/${GITHUB_REPOSITORY}/actions/workflows/ci.yml/runs" \
  -f "head_sha=${SOURCE_SHA}" -F per_page=100`,
		`scripts/release/gh-api-read.sh output.json --method=GET endpoint --field branch=main --raw-field event=push`,
		`printf '%s\n' -f --field -F --raw-field
scripts/release/gh-api-read.sh output.json endpoint`,
	}
	for index, script := range valid {
		if violations := ghAPIReadQueryMethodViolations(script); len(violations) != 0 {
			t.Fatalf("valid fixture %d rejected: %v", index, violations)
		}
	}

	for _, queryFlag := range []string{"-f", "--field", "-F", "--raw-field"} {
		t.Run(queryFlag, func(t *testing.T) {
			script := fmt.Sprintf(`scripts/release/gh-api-read.sh output.json \
  --paginate --slurp endpoint \
  %s key=value`, queryFlag)
			violations := ghAPIReadQueryMethodViolations(script)
			if len(violations) != 1 || !strings.Contains(violations[0], "exactly one explicit --method GET") {
				t.Fatalf("query flag %s violations=%v", queryFlag, violations)
			}
		})
	}

	invalidMethods := map[string]string{
		"post":      `scripts/release/gh-api-read.sh output.json --method POST endpoint -f key=value`,
		"duplicate": `scripts/release/gh-api-read.sh output.json --method GET --method=GET endpoint -F key=value`,
		"missing":   `scripts/release/gh-api-read.sh output.json --method endpoint -f key=value`,
	}
	for name, script := range invalidMethods {
		t.Run(name, func(t *testing.T) {
			if violations := ghAPIReadQueryMethodViolations(script); len(violations) != 1 {
				t.Fatalf("invalid method fixture violations=%v", violations)
			}
		})
	}
}

func ghAPIReadQueryMethodViolations(script string) []string {
	var violations []string
	for _, words := range shellCommandWords(script) {
		adapter := -1
		for index, word := range words {
			if filepath.Base(word) == "gh-api-read.sh" {
				adapter = index
				break
			}
		}
		if adapter < 0 {
			continue
		}

		hasQueryField := false
		methods := make([]string, 0, 1)
		for index := adapter + 1; index < len(words); index++ {
			word := words[index]
			switch {
			case word == "-f" || word == "--field" || word == "-F" || word == "--raw-field":
				hasQueryField = true
			case strings.HasPrefix(word, "--field=") || strings.HasPrefix(word, "--raw-field="):
				hasQueryField = true
			case word == "--method":
				if index+1 < len(words) {
					methods = append(methods, words[index+1])
					index++
				} else {
					methods = append(methods, "")
				}
			case strings.HasPrefix(word, "--method="):
				methods = append(methods, strings.TrimPrefix(word, "--method="))
			}
		}
		if hasQueryField && (len(methods) != 1 || methods[0] != "GET") {
			violations = append(violations,
				fmt.Sprintf("gh-api-read.sh query fields require exactly one explicit --method GET; command words=%q", words))
		}
	}
	return violations
}

// shellCommandWords performs the small shell-lexing subset needed by workflow
// run blocks: quotes, escaped characters, backslash-newline continuation,
// comments, and command separators. It deliberately returns command words
// instead of matching YAML source literals so multiline invocations are
// checked the same way as single-line invocations.
func shellCommandWords(script string) [][]string {
	var commands [][]string
	var words []string
	var word strings.Builder
	inWord := false
	quote := byte(0)
	escaped := false
	inComment := false

	flushWord := func() {
		if !inWord {
			return
		}
		words = append(words, word.String())
		word.Reset()
		inWord = false
	}
	flushCommand := func() {
		flushWord()
		if len(words) == 0 {
			return
		}
		commands = append(commands, words)
		words = nil
	}

	for index := 0; index < len(script); index++ {
		character := script[index]
		if inComment {
			if character == '\n' {
				inComment = false
				flushCommand()
			}
			continue
		}
		if escaped {
			escaped = false
			if character == '\n' {
				continue
			}
			inWord = true
			word.WriteByte(character)
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
				continue
			}
			if character == '\\' && quote == '"' {
				escaped = true
				continue
			}
			word.WriteByte(character)
			continue
		}

		switch character {
		case '\\':
			escaped = true
		case '\'', '"':
			inWord = true
			quote = character
		case ' ', '\t', '\r':
			flushWord()
		case '\n', ';', '|', '&':
			flushCommand()
		case '#':
			if inWord {
				word.WriteByte(character)
			} else {
				inComment = true
			}
		default:
			inWord = true
			word.WriteByte(character)
		}
	}
	flushCommand()
	return commands
}
