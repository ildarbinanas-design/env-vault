package releasecontract

// OperationalProjection is the validated, typed subset consumed by release
// orchestration. It intentionally omits historical incidents, repair history,
// and diagnostic code registries while retaining every live identity.
type OperationalProjection struct {
	SchemaID               string             `json:"schema_id"`
	SchemaVersion          int                `json:"schema_version"`
	ContractSchemaID       string             `json:"contract_schema_id"`
	ContractSchemaVersion  int                `json:"contract_schema_version"`
	ContractSemanticSHA256 string             `json:"contract_semantic_sha256"`
	ContractFileSHA256     string             `json:"contract_file_sha256"`
	Repositories           Repositories       `json:"repositories"`
	Version                OperationalVersion `json:"version"`
	Naming                 Naming             `json:"naming"`
	Platforms              []Platform         `json:"platforms"`
	Assets                 []string           `json:"assets"`
	Homebrew               Homebrew           `json:"homebrew"`
	Workflows              []Workflow         `json:"workflows"`
	Concurrency            Concurrency        `json:"concurrency"`
	Apps                   []App              `json:"apps"`
	MainRequiredChecks     []RequiredCheck    `json:"main_required_checks"`
}

type OperationalVersion struct {
	Pattern       string              `json:"pattern"`
	TagPrefix     string              `json:"tag_prefix"`
	ReleasePlease ReleasePleasePolicy `json:"release_please"`
}

func (c Contract) OperationalProjection() (OperationalProjection, error) {
	if c.SchemaID != SchemaID || c.SchemaVersion != SchemaVersion {
		return OperationalProjection{}, &ProjectionError{Message: "operational projection requires the canonical v2 contract"}
	}
	if !sha256Pattern.MatchString(c.FileSHA256()) {
		return OperationalProjection{}, &ProjectionError{Message: "operational projection requires an exact loaded contract file digest"}
	}
	digest, err := SemanticSHA256(c)
	if err != nil {
		return OperationalProjection{}, err
	}
	homebrew := c.Homebrew
	homebrew.Platforms = append([]string(nil), c.Homebrew.Platforms...)
	workflows := make([]Workflow, len(c.Workflows))
	for index, workflow := range c.Workflows {
		workflows[index] = workflow
		workflows[index].Events = append([]string(nil), workflow.Events...)
		workflows[index].Jobs = append([]string(nil), workflow.Jobs...)
	}
	concurrency := c.Concurrency
	concurrency.Release.Workflows = append([]string(nil), c.Concurrency.Release.Workflows...)
	return OperationalProjection{
		SchemaID: OperationalProjectionSchema, SchemaVersion: OperationalProjectionVersion,
		ContractSchemaID: c.SchemaID, ContractSchemaVersion: c.SchemaVersion, ContractSemanticSHA256: digest,
		ContractFileSHA256: c.FileSHA256(),
		Repositories:       c.Repositories,
		Version:            OperationalVersion{Pattern: c.VersionPolicy.Pattern, TagPrefix: c.VersionPolicy.TagPrefix, ReleasePlease: c.VersionPolicy.ReleasePlease},
		Naming:             c.Naming, Platforms: append([]Platform(nil), c.Platforms...), Assets: append([]string(nil), c.Assets...),
		Homebrew: homebrew, Workflows: workflows, Concurrency: concurrency,
		Apps: append([]App(nil), c.Apps...), MainRequiredChecks: append([]RequiredCheck(nil), c.MainRequiredChecks...),
	}, nil
}

type ProjectionError struct{ Message string }

func (e *ProjectionError) Error() string { return e.Message }
