package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ildarbinanas-design/env-vault/internal/releasearchive"
)

func main() {
	inputDir := flag.String("input-dir", "", "directory containing the five release archives")
	outputDir := flag.String("output-dir", "", "fresh or empty extraction directory")
	flag.Parse()

	if flag.NArg() != 0 || *inputDir == "" || *outputDir == "" {
		fmt.Fprintln(os.Stderr, "usage: release-extract --input-dir DIR --output-dir DIR")
		os.Exit(2)
	}
	if err := releasearchive.ExtractAll(*inputDir, *outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "release-extract: %v\n", err)
		os.Exit(1)
	}
}
