//go:build !linux && !windows

package keyring

import "github.com/99designs/keyring"

func openPlatform(cfg keyring.Config) (keyring.Keyring, error) { return keyring.Open(cfg) }
