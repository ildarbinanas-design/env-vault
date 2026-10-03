//go:build !darwin && !linux

package cli

import "golang.org/x/term"

func readHiddenPassword(fd int) ([]byte, error) { return term.ReadPassword(fd) }
