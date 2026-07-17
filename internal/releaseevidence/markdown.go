package releaseevidence

import (
	"bytes"
	"fmt"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

// Markdown renders a deterministic short index over the machine JSON. The JSON
// remains authoritative; this index is deliberately compact and contains no
// free-form diagnosis.
func Markdown(evidence Evidence, contract releasecontract.Contract) ([]byte, error) {
	if err := Verify(evidence, contract); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "# env-vault %s release evidence\n\n", evidence.ReleaseVersion)
	fmt.Fprintf(&output, "- Result: `pass`\n")
	fmt.Fprintf(&output, "- Source SHA: `%s`\n", evidence.SourceSHA)
	fmt.Fprintf(&output, "- Tag ref SHA: `%s`\n", evidence.Tag.RefSHA)
	fmt.Fprintf(&output, "- Tag target SHA: `%s`\n", evidence.Tag.TargetSHA)
	fmt.Fprintf(&output, "- Release: [%s](%s)\n", evidence.ReleaseVersion, evidence.Release.URL)
	fmt.Fprintf(&output, "- Authorized release PR: `#%d` at `%s`\n", evidence.Authorization.GeneratedReleasePR.Number, evidence.Authorization.GeneratedReleasePR.HeadSHA)
	fmt.Fprintf(&output, "- Exact confirmation: [comment `%d`](%s) by `%s` (`%s`) at `%s`; body SHA-256 `%s`\n", evidence.Authorization.Confirmation.CommentID, evidence.Authorization.Confirmation.URL, evidence.Authorization.Confirmation.Actor, evidence.Authorization.Confirmation.ActorAssociation, evidence.Authorization.Confirmation.CreatedAt, evidence.Authorization.Confirmation.BodySHA256)
	fmt.Fprintf(&output, "- Planning run / attempt: `%d` / `%d`\n", evidence.Authorization.PlanningWorkflow.RunID, evidence.Authorization.PlanningWorkflow.RunAttempt)
	fmt.Fprintf(&output, "- Release PR CI run / attempt: `%d` / `%d`\n", evidence.Authorization.ReleasePRCI.RunID, evidence.Authorization.ReleasePRCI.RunAttempt)
	fmt.Fprintf(&output, "- Evidence run / attempt: `%d` / `%d`\n", evidence.Authorization.EvidenceWorkflow.RunID, evidence.Authorization.EvidenceWorkflow.RunAttempt)
	fmt.Fprintf(&output, "- Publisher run / attempt / repair: `%d` / `%d` / `%s`\n", evidence.PublisherMetrics.RunID, evidence.PublisherMetrics.Attempt, evidence.PublisherRepairMode)
	fmt.Fprintf(&output, "- Promotion manifest SHA-256: `%s`\n", evidence.Promotion.ManifestSHA256)
	fmt.Fprintf(&output, "- Evidence SHA-256: `%s`\n", evidence.EvidenceSHA256)
	fmt.Fprintf(&output, "- Observed at: `%s`\n\n", evidence.ObservedAt)

	fmt.Fprintln(&output, "## Workflow metrics")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "| Workflow | Run / attempt | Jobs | Queue (s) | Wall (s) | Runner (s) | Retries |")
	fmt.Fprintln(&output, "| --- | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, metric := range []struct {
		label               string
		run                 int64
		attempt, jobs       int
		queue, wall, runner int64
		retries             int
	}{
		{label: "CI", run: evidence.CIMetrics.RunID, attempt: evidence.CIMetrics.Attempt, jobs: evidence.CIMetrics.JobCount, queue: evidence.CIMetrics.QueueSeconds, wall: evidence.CIMetrics.WallSeconds, runner: evidence.CIMetrics.AggregateRunnerSeconds, retries: evidence.CIMetrics.RetryCount},
		{label: "Publisher", run: evidence.PublisherMetrics.RunID, attempt: evidence.PublisherMetrics.Attempt, jobs: evidence.PublisherMetrics.JobCount, queue: evidence.PublisherMetrics.QueueSeconds, wall: evidence.PublisherMetrics.WallSeconds, runner: evidence.PublisherMetrics.AggregateRunnerSeconds, retries: evidence.PublisherMetrics.RetryCount},
	} {
		fmt.Fprintf(&output, "| %s | %d / %d | %d | %d | %d | %d | %d |\n", metric.label, metric.run, metric.attempt, metric.jobs, metric.queue, metric.wall, metric.runner, metric.retries)
	}

	fmt.Fprintln(&output, "\n## Published assets")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "| Asset | Bytes | SHA-256 |")
	fmt.Fprintln(&output, "| --- | ---: | --- |")
	for _, asset := range evidence.Assets {
		fmt.Fprintf(&output, "| `%s` | %d | `%s` |\n", asset.Name, asset.Size, asset.SHA256)
	}

	fmt.Fprintln(&output, "\n## Supply-chain attestations")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "| Archive | Provenance run / attempt | SPDX run / attempt |")
	fmt.Fprintln(&output, "| --- | ---: | ---: |")
	for _, attestation := range evidence.Attestations {
		fmt.Fprintf(&output, "| `%s` | %d / %d | %d / %d |\n", attestation.AssetName, attestation.Provenance.WorkflowRunID, attestation.Provenance.WorkflowRunAttempt, attestation.SBOM.WorkflowRunID, attestation.SBOM.WorkflowRunAttempt)
	}

	fmt.Fprintln(&output, "\n## Homebrew")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Pull request: [#%d](%s)\n", evidence.Homebrew.PRNumber, evidence.Homebrew.PRURL)
	fmt.Fprintf(&output, "- PR head SHA: `%s`\n", evidence.Homebrew.PRHeadSHA)
	fmt.Fprintf(&output, "- Exact release merge SHA: `%s`\n", evidence.Homebrew.PRMergeSHA)
	fmt.Fprintf(&output, "- Current tap SHA: `%s` (contains release merge: `%t`)\n", evidence.Homebrew.TapSHA, evidence.Homebrew.MergeIsAncestorOfTap)
	fmt.Fprintf(&output, "- PR-head CI: run `%d`, attempt `%d`\n", evidence.Homebrew.PRHeadCI.RunID, evidence.Homebrew.PRHeadCI.RunAttempt)
	fmt.Fprintf(&output, "- Post-merge CI: run `%d`, attempt `%d`\n", evidence.Homebrew.PostMergeCI.RunID, evidence.Homebrew.PostMergeCI.RunAttempt)
	fmt.Fprintf(&output, "- Formula SHA-256: `%s`\n", evidence.Homebrew.FormulaSHA256)

	fmt.Fprintln(&output, "\n## Preserved blocked-tag policy")
	fmt.Fprintln(&output)
	for _, blocked := range evidence.BlockedVersions {
		fmt.Fprintf(&output, "- `%s`: tag `%s` exists; GitHub Release absent\n", blocked.Version, blocked.TagSHA)
	}

	fmt.Fprintln(&output, "\n## Preserved abandoned-release policy")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- `%s`: merged PR `#%d` at `%s` is labeled `%s`; tag and GitHub Release absent\n",
		evidence.AbandonedRelease.Version,
		evidence.AbandonedRelease.GeneratedReleasePR.Number,
		evidence.AbandonedRelease.SourceSHA,
		contract.VersionPolicy.ReleasePleaseRecovery.AbandonedLabel)

	return output.Bytes(), nil
}
