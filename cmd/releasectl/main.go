package main

import (
	"os"

	"github.com/ildarbinanas-design/env-vault/internal/releasectl"
)

func main() {
	os.Exit(releasectl.Run(os.Args[1:], os.Stdout, os.Stderr))
}
