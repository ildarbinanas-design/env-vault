// Command release-evidence validates authoritative JSON evidence and generates
// a compact Markdown index without free-text diagnosis.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releaseevidence"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "release-evidence:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	return runWithRunner(args, stdout, stderr, releaseevidence.GHRunner{})
}

func runWithRunner(args []string, stdout, stderr io.Writer, runner releaseevidence.CommandRunner) error {
	if len(args) == 0 {
		return errors.New("command required: trigger, collect, validate, validate-records, implementation, or index")
	}
	set := flag.NewFlagSet("release-evidence "+args[0], flag.ContinueOnError)
	set.SetOutput(stderr)
	switch args[0] {
	case "trigger":
		eventFile := set.String("event", "", "workflow_run event JSON")
		contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract")
		timeout := set.Duration("timeout", 2*time.Minute, "GET-only observation timeout")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if set.NArg() != 0 || *eventFile == "" || *timeout <= 0 {
			return errors.New("trigger requires --event and a positive timeout")
		}
		contract, err := releasecontract.LoadFile(*contractPath)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		identity, err := releaseevidence.ResolveTrigger(ctx, *eventFile, contract, runner)
		if err != nil {
			return err
		}
		for _, field := range [][2]string{
			{"repository", identity.Repository}, {"version", identity.Version}, {"source_sha", identity.SourceSHA},
			{"publisher_run_id", fmt.Sprint(identity.PublisherRunID)}, {"publisher_run_attempt", fmt.Sprint(identity.PublisherRunAttempt)},
			{"publisher_conclusion", identity.PublisherConclusion}, {"publisher_event", identity.PublisherEvent}, {"repair_mode", identity.RepairMode},
			{"repair_state_digest", identity.RepairStateDigest},
			{"ci_run_id", fmt.Sprint(identity.CIRunID)}, {"ci_run_attempt", fmt.Sprint(identity.CIRunAttempt)},
		} {
			if _, err := fmt.Fprintf(stdout, "%s=%s\n", field[0], field[1]); err != nil {
				return err
			}
		}
		return nil
	case "validate":
		input := set.String("input", "", "machine evidence JSON")
		contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if set.NArg() != 0 || *input == "" {
			return errors.New("validate requires --input and no positional arguments")
		}
		contract, err := releasecontract.LoadFile(*contractPath)
		if err != nil {
			return err
		}
		if _, err := releaseevidence.LoadWithContract(*input, contract); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "validated %s as %s\n", *input, releaseevidence.SchemaID)
		return err
	case "validate-records":
		records := set.String("records", filepath.Join("evidence", "records"), "checked-in implementation record directory")
		evidenceDirectory := set.String("evidence", "evidence", "checked-in generated evidence directory")
		contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if set.NArg() != 0 {
			return errors.New("validate-records accepts no positional arguments")
		}
		contract, err := releasecontract.LoadFile(*contractPath)
		if err != nil {
			return err
		}
		if err := releaseevidence.ValidateImplementationTree(*records, *evidenceDirectory, contract); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "validated checked-in implementation evidence as %s\n", releaseevidence.SchemaID)
		return err
	case "collect":
		contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract")
		repository := set.String("repo", "", "OWNER/REPO")
		version := set.String("version", "", "exact vX.Y.Z")
		sourceSHA := set.String("source-sha", "", "exact release source SHA")
		runID := set.Int64("publisher-run-id", 0, "exact publisher workflow run ID")
		runAttempt := set.Int("publisher-run-attempt", 0, "exact publisher workflow run attempt")
		assets := set.String("assets", "", "downloaded exact release asset directory")
		promotionManifest := set.String("promotion-manifest", "", "exact main-CI promotion manifest")
		output := set.String("output", "", "authoritative *.evidence.json output")
		timeout := set.Duration("timeout", 10*time.Minute, "GET-only observation timeout")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if set.NArg() != 0 || *repository == "" || *version == "" || *sourceSHA == "" || *runID <= 0 || *runAttempt <= 0 || *output == "" || *timeout <= 0 {
			return errors.New("collect requires --repo --version --source-sha --publisher-run-id --publisher-run-attempt --output and a positive timeout")
		}
		contract, err := releasecontract.LoadFile(*contractPath)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		evidence, err := releaseevidence.Collect(ctx, releaseevidence.CollectOptions{
			Contract: contract, ContractPath: *contractPath, Repository: *repository, Version: *version, SourceSHA: *sourceSHA,
			PublisherRunID: *runID, PublisherRunAttempt: *runAttempt, AssetsDirectory: *assets,
			PromotionManifest: *promotionManifest,
		}, runner)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(evidence, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*output, data, 0o644); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "generated %s as %s\n", *output, releaseevidence.SchemaID)
		return err
	case "implementation":
		record := set.String("record", "", "versioned implementation record JSON")
		candidateSHA := set.String("candidate-sha", "", "exact implementation commit SHA")
		contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract")
		timeout := set.Duration("timeout", 30*time.Second, "local repository inspection timeout")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if set.NArg() != 0 || *record == "" || *candidateSHA == "" || *timeout <= 0 {
			return errors.New("implementation requires --record --candidate-sha and a positive timeout")
		}
		contract, err := releasecontract.LoadFile(*contractPath)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		artifacts, err := releaseevidence.NormalizeImplementationRecord(ctx, *record, *candidateSHA, contract)
		if err != nil {
			return err
		}
		if err := os.WriteFile(artifacts.EvidencePath, artifacts.EvidenceJSON, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(artifacts.IndexPath, artifacts.Index, 0o644); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "generated %s and %s for exact candidate %s\n", artifacts.EvidencePath, artifacts.IndexPath, *candidateSHA)
		return err
	case "index":
		directory := set.String("directory", "evidence", "evidence directory")
		output := set.String("output", filepath.Join("evidence", "README.md"), "generated Markdown index")
		contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if set.NArg() != 0 {
			return errors.New("index accepts no positional arguments")
		}
		contract, err := releasecontract.LoadFile(*contractPath)
		if err != nil {
			return err
		}
		data, err := releaseevidence.IndexWithContract(*directory, contract)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*output, data, 0o644); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "generated %s from validated machine evidence\n", *output)
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
