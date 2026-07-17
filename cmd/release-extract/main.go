package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ildarbinanas-design/env-vault/internal/releasearchive"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func main() {
	inputDir := flag.String("input-dir", "", "directory containing the five release archives")
	outputDir := flag.String("output-dir", "", "fresh or empty extraction directory")
	contractPath := flag.String("contract", releasecontract.CanonicalPath, "validated release contract")
	flag.Parse()

	if flag.NArg() != 0 || *inputDir == "" || *outputDir == "" {
		fmt.Fprintln(os.Stderr, "usage: release-extract --input-dir DIR --output-dir DIR")
		os.Exit(2)
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "release-extract: %v\n", err)
		os.Exit(1)
	}
	if err := releasearchive.ExtractAll(*inputDir, *outputDir, contract); err != nil {
		fmt.Fprintf(os.Stderr, "release-extract: %v\n", err)
		os.Exit(1)
	}
}
