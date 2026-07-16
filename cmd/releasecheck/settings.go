package main

import (
	"fmt"
	"io"

	"github.com/ildarbinanas-design/env-vault/internal/releasesettings"
)

func runSettings(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, settingsUsage())
		return exitUsage
	}
	switch args[0] {
	case "seal":
		return runSettingsSeal(args[1:], stdout, stderr)
	case "verify":
		return runSettingsVerify(args[1:], stdout, stderr)
	default:
		fmt.Fprint(stderr, settingsUsage())
		return exitUsage
	}
}

func runSettingsSeal(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("settings seal")
	tupleFlags := addSettingsTupleFlags(set)
	mergeSettings := set.String("merge-settings", "", "saved GraphQL repository merge-settings JSON")
	rulesetPages := set.String("ruleset-pages", "", "saved gh --paginate --slurp repository rulesets JSON")
	mainRuleset := set.String("main-ruleset", "", "saved canonical main ruleset detail JSON")
	tagRuleset := set.String("tag-ruleset", "", "saved canonical release-tag ruleset detail JSON")
	evidenceRuleset := set.String("evidence-ruleset", "", "saved canonical release-evidence ruleset detail JSON")
	output := set.String("output", "", "new sealed settings proof, or - for stdout")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || !tupleFlags.complete() || *mergeSettings == "" || *rulesetPages == "" || *mainRuleset == "" || *tagRuleset == "" || *evidenceRuleset == "" || *output == "" {
		fmt.Fprint(stderr, settingsSealUsage())
		return exitUsage
	}
	raw, err := readSettingsRawInputs(*mergeSettings, *rulesetPages, *mainRuleset, *tagRuleset, *evidenceRuleset)
	if err != nil {
		return writeFailure(stdout, stderr, *output == "-", releasesettings.CodeInputInvalid, err, exitSnapshotInvalid)
	}
	proof, err := releasesettings.Seal(tupleFlags.value(), raw)
	if err != nil {
		return writeSettingsFailure(stdout, stderr, *output == "-", err)
	}
	encoded, err := releasesettings.MarshalJSON(proof)
	if err != nil {
		return writeFailure(stdout, stderr, *output == "-", "OUTPUT_FAILED", err, exitInternal)
	}
	if code := writeEvidenceOutput(*output, encoded, stdout, stderr); code != exitOK {
		return code
	}
	if *output != "-" {
		fmt.Fprintf(stdout, "sealed repository settings proof: version=%s source_sha=%s planning_run_id=%d attempt=%d proof_sha256=%s output=%s\n", proof.Tuple.ReleaseVersion, proof.Tuple.SourceSHA, proof.Tuple.PlanningRunID, proof.Tuple.PlanningRunAttempt, proof.ProofSHA256, *output)
	}
	return exitOK
}

func runSettingsVerify(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("settings verify")
	tupleFlags := addSettingsTupleFlags(set)
	input := set.String("input", "", "sealed repository settings proof JSON")
	jsonOutput := set.Bool("json", false, "emit the verified proof JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || !tupleFlags.complete() || *input == "" {
		fmt.Fprint(stderr, settingsVerifyUsage())
		return exitUsage
	}
	data, err := readRegularEvidenceInput(*input)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, releasesettings.CodeInputInvalid, err, exitSnapshotInvalid)
	}
	proof, err := releasesettings.ParseProof(data)
	if err != nil {
		return writeSettingsFailure(stdout, stderr, *jsonOutput, err)
	}
	if err := releasesettings.Verify(proof, tupleFlags.value()); err != nil {
		return writeSettingsFailure(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		encoded, err := releasesettings.MarshalJSON(proof)
		if err != nil {
			return writeFailure(stdout, stderr, true, "OUTPUT_FAILED", err, exitInternal)
		}
		if _, err := stdout.Write(encoded); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "verified repository settings proof: version=%s source_sha=%s planning_run_id=%d attempt=%d checked_at=%s proof_sha256=%s\n", proof.Tuple.ReleaseVersion, proof.Tuple.SourceSHA, proof.Tuple.PlanningRunID, proof.Tuple.PlanningRunAttempt, proof.Tuple.CheckedAt, proof.ProofSHA256)
	}
	return exitOK
}

type settingsTupleFlags struct {
	repository         *string
	sourceSHA          *string
	releaseVersion     *string
	planningRunID      *int64
	planningRunAttempt *int
	checkedAt          *string
}

type flagStringInt64Int interface {
	String(string, string, string) *string
	Int64(string, int64, string) *int64
	Int(string, int, string) *int
}

func addSettingsTupleFlags(set flagStringInt64Int) settingsTupleFlags {
	return settingsTupleFlags{
		repository:         set.String("repository", "", "exact owner/repository"),
		sourceSHA:          set.String("source-sha", "", "exact lowercase 40-hex release source SHA"),
		releaseVersion:     set.String("release-version", "", "exact vMAJOR.MINOR.PATCH"),
		planningRunID:      set.Int64("planning-run-id", 0, "exact planning workflow run ID"),
		planningRunAttempt: set.Int("planning-run-attempt", 0, "exact planning workflow run attempt"),
		checkedAt:          set.String("checked-at", "", "exact canonical UTC RFC3339 observation time"),
	}
}

func (flags settingsTupleFlags) complete() bool {
	return *flags.repository != "" && *flags.sourceSHA != "" && *flags.releaseVersion != "" && *flags.planningRunID > 0 && *flags.planningRunAttempt > 0 && *flags.checkedAt != ""
}

func (flags settingsTupleFlags) value() releasesettings.Tuple {
	return releasesettings.Tuple{
		Repository: *flags.repository, SourceSHA: *flags.sourceSHA,
		ReleaseVersion: *flags.releaseVersion, PlanningRunID: *flags.planningRunID,
		PlanningRunAttempt: *flags.planningRunAttempt, CheckedAt: *flags.checkedAt,
	}
}

func readSettingsRawInputs(merge, pages, main, tag, evidence string) (releasesettings.RawInputs, error) {
	paths := []string{merge, pages, main, tag, evidence}
	data := make([][]byte, len(paths))
	for index, path := range paths {
		value, err := readRegularEvidenceInput(path)
		if err != nil {
			return releasesettings.RawInputs{}, fmt.Errorf("read settings input %s: %w", path, err)
		}
		data[index] = value
	}
	return releasesettings.RawInputs{
		MergeSettings: data[0], RulesetPages: data[1], MainRuleset: data[2],
		TagRuleset: data[3], EvidenceRuleset: data[4],
	}, nil
}

func writeSettingsFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	code := releasesettings.ErrorCode(err)
	if code == "" {
		code = releasesettings.CodeInputInvalid
	}
	return writeFailure(stdout, stderr, jsonOutput, code, err, exitSnapshotInvalid)
}

func settingsUsage() string {
	return `usage: releasecheck settings <command> [flags]

Commands:
  seal    validate saved GitHub settings and create an exact-byte sealed proof
  verify  replay a sealed proof against an independently supplied exact tuple
`
}

func settingsSealUsage() string {
	return "usage: releasecheck settings seal --repository OWNER/REPO --source-sha SHA --release-version vX.Y.Z --planning-run-id ID --planning-run-attempt N --checked-at TIME --merge-settings FILE --ruleset-pages FILE --main-ruleset FILE --tag-ruleset FILE --evidence-ruleset FILE --output FILE|-\n"
}

func settingsVerifyUsage() string {
	return "usage: releasecheck settings verify --input FILE --repository OWNER/REPO --source-sha SHA --release-version vX.Y.Z --planning-run-id ID --planning-run-attempt N --checked-at TIME [--json]\n"
}
