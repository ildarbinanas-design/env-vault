//go:build !windows

package keyring

import "github.com/ildarbinanas-design/env-vault/internal/secretstore"

func (s Store) ValidateIdentities([]secretstore.Metadata) error { return nil }
