// Package releasesettings seals and verifies saved, credential-free GitHub
// repository settings observations. It intentionally has no network or
// GitHub client: transport belongs to gh, while this package validates exact
// bytes and a release tuple entirely offline.
package releasesettings

const (
	SchemaID      = "env-vault.repository-release-settings-proof.v1"
	SchemaVersion = 1

	ResultPass = "pass"
)

// Tuple is the exact release-planning identity for which repository settings
// were checked. A proof is not reusable for another source, version, workflow
// attempt, or observation time.
type Tuple struct {
	Repository         string `json:"repository"`
	SourceSHA          string `json:"source_sha"`
	ReleaseVersion     string `json:"release_version"`
	PlanningRunID      int64  `json:"planning_run_id"`
	PlanningRunAttempt int    `json:"planning_run_attempt"`
	CheckedAt          string `json:"checked_at"`
}

// Document preserves the exact saved JSON bytes as a UTF-8 string and binds
// those bytes independently of the outer proof digest.
type Document struct {
	SHA256       string `json:"sha256"`
	DocumentJSON string `json:"document_json"`
}

// Inputs contains the five GitHub responses needed to reproduce the settings
// decision. RulesetPages is the exact gh --paginate --slurp response and is
// used to prove that each canonical active ruleset is unique.
type Inputs struct {
	MergeSettings   Document `json:"merge_settings"`
	RulesetPages    Document `json:"ruleset_pages"`
	MainRuleset     Document `json:"main_ruleset"`
	TagRuleset      Document `json:"tag_ruleset"`
	EvidenceRuleset Document `json:"evidence_ruleset"`
}

// RawInputs are the untrusted saved JSON bytes supplied to Seal.
type RawInputs struct {
	MergeSettings   []byte
	RulesetPages    []byte
	MainRuleset     []byte
	TagRuleset      []byte
	EvidenceRuleset []byte
}

// Proof is a sealed, self-contained v1 repository release-settings decision.
type Proof struct {
	SchemaID      string `json:"schema_id"`
	SchemaVersion int    `json:"schema_version"`
	Tuple         Tuple  `json:"tuple"`
	Inputs        Inputs `json:"inputs"`
	Result        string `json:"result"`
	ProofSHA256   string `json:"proof_sha256"`
}
