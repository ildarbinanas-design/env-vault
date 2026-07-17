// Command release-contract exposes the validated declarative release contract
// to thin workflow orchestration.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "release-contract:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("command required: validate, matrix, app, or workflow")
	}
	command, args := args[0], args[1:]
	set := flag.NewFlagSet("release-contract "+command, flag.ContinueOnError)
	set.SetOutput(stderr)
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract path")
	jsonOutput := set.Bool("json", false, "emit machine-readable JSON")
	entryID := set.String("id", "", "exact contract entry ID")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return err
	}
	switch command {
	case "validate":
		if *jsonOutput {
			return encode(stdout, map[string]any{"schema_id": releasecontract.SchemaID, "valid": true, "contract": *contractPath})
		}
		_, err := fmt.Fprintf(stdout, "validated %s\n", *contractPath)
		return err
	case "matrix":
		if !*jsonOutput {
			return errors.New("matrix requires --json")
		}
		return encode(stdout, contract.Matrix())
	case "app":
		if !*jsonOutput || *entryID == "" {
			return errors.New("app requires --id ID --json")
		}
		for _, app := range contract.Apps {
			if app.ID == *entryID {
				return encode(stdout, app)
			}
		}
		return fmt.Errorf("release App contract ID %q not found", *entryID)
	case "workflow":
		if !*jsonOutput || *entryID == "" {
			return errors.New("workflow requires --id ID --json")
		}
		workflow, ok := contract.WorkflowByID(*entryID)
		if !ok {
			return fmt.Errorf("release workflow contract ID %q not found", *entryID)
		}
		return encode(stdout, workflow)
	default:
		return fmt.Errorf("unknown command %q (want validate, matrix, app, or workflow)", command)
	}
}

func encode(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
