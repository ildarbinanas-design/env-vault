package main

import (
	"os"

	"github.com/ildarbinanas-design/env-vault/internal/cli"
)

func main() {
	cli.RunAndExit(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}
