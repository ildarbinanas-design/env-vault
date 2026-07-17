package e2ebaseline

import (
	"errors"
	"fmt"

	"github.com/ildarbinanas-design/env-vault/internal/e2esuite"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

type GenerateOptions struct {
	ProofPath           string
	RepositoryRoot      string
	SuiteTransitionCode string
}

// Generate derives a deterministic baseline exclusively from the sealed
// matrix proof. Raw reports are deliberately outside this API so baseline
// updates cannot accidentally introduce a second deep-validation path.
func Generate(options GenerateOptions, contract releasecontract.Contract) (Baseline, error) {
	proof, err := LoadMatrixProof(options.ProofPath, contract)
	if err != nil {
		return Baseline{}, err
	}
	first := proof.PlatformEvidence[0]
	semanticHash, err := e2esuite.Hash(options.RepositoryRoot)
	if err != nil {
		return Baseline{}, err
	}
	transition := options.SuiteTransitionCode
	if semanticHash == proof.SuiteHash {
		if transition != "" {
			return Baseline{}, errors.New("suite transition supplied but proof and checkout suite hashes match")
		}
	} else if transition != ReviewedSuiteTransition {
		return Baseline{}, fmt.Errorf("proof suite %s differs from checkout suite %s; explicit %s transition is required", proof.SuiteHash, semanticHash, ReviewedSuiteTransition)
	}
	baseline := Baseline{
		SchemaID:      SchemaID,
		SchemaVersion: SchemaVersion,
		SemanticSuite: SemanticSuite{Hash: semanticHash, SourceReportHash: proof.SuiteHash, TransitionCode: transition},
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
			ExpectedSkips:        append([]string(nil), actual.ExpectedSkips...),
			CriticalScenarios:    append([]ScenarioExpectation(nil), actual.CriticalScenarios...),
			Leak:                 actual.Leak,
		})
	}
	if err := baseline.Validate(contract); err != nil {
		return Baseline{}, err
	}
	return baseline, nil
}
