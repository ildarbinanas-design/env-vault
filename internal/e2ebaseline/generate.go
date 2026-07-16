package e2ebaseline

import (
	"errors"
	"fmt"

	"github.com/ildarbinanas-design/env-vault/internal/e2esuite"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

type GenerateOptions struct {
	ProofPath      string
	RepositoryRoot string
}

// Generate derives a deterministic baseline exclusively from a sealed matrix
// proof created by the current validator. Raw reports are outside this API so
// an update cannot accidentally create a second interpretation path.
func Generate(options GenerateOptions, contract releasecontract.Contract) (Baseline, error) {
	proof, err := LoadMatrixProof(options.ProofPath, contract)
	if err != nil {
		return Baseline{}, err
	}
	if proof.Phase != "candidate" {
		return Baseline{}, errors.New("baseline generation requires a candidate matrix proof")
	}
	semanticHash, err := e2esuite.Hash(options.RepositoryRoot)
	if err != nil {
		return Baseline{}, err
	}
	if semanticHash != proof.SuiteHash {
		return Baseline{}, fmt.Errorf("matrix proof suite hash=%s, checkout semantic suite hash=%s", proof.SuiteHash, semanticHash)
	}
	first := proof.PlatformEvidence[0]
	baseline := Baseline{
		SchemaID:      SchemaID,
		SchemaVersion: SchemaVersion,
		SemanticSuite: SemanticSuite{Algorithm: e2esuite.SchemaID, Hash: semanticHash, SourceReportHash: semanticHash},
		Toolchain:     Toolchain{GoVersion: first.GoVersion, GotestsumVersion: first.GotestsumVersion},
		Provenance: Provenance{
			Repository: proof.Run.Repository,
			CommitSHA:  proof.Run.CommitSHA,
			RunID:      proof.Run.RunID,
			RunURL:     proof.Run.RunURL,
			RunAttempt: proof.Run.RunAttempt,
			Phase:      proof.Phase,
		},
	}
	for index, declared := range contract.Platforms {
		actual := proof.PlatformEvidence[index]
		baseline.Platforms = append(baseline.Platforms, PlatformBaseline{
			ID:                   declared.ID,
			GOOS:                 declared.GOOS,
			GOARCH:               declared.GOARCH,
			ContractSHA256:       actual.ContractSHA256,
			CoverageFloorPercent: actual.StatementCoveragePercent,
			Counts:               actual.Counts,
			ExpectedSkips:        append([]string{}, actual.ExpectedSkips...),
			CriticalScenarios:    append([]ScenarioExpectation(nil), actual.CriticalScenarios...),
			Leak:                 actual.Leak,
		})
	}
	if err := baseline.Validate(contract); err != nil {
		return Baseline{}, err
	}
	return baseline, nil
}
