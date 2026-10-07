//go:build !darwin && !linux

package cli

import (
	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

func readHiddenSecret(fd int) ([]byte, error) {
	value, err := readHiddenPassword(fd)
	if err != nil {
		bundle.Wipe(value)
		return nil, err
	}
	if len(value) > secretstore.MaxValueBytes {
		bundle.Wipe(value)
		return nil, secretstore.ErrValueTooLarge
	}
	return value, nil
}

func (a *App) readTerminalPassphrase(command string, confirm bool) ([]byte, error) {
	return a.collectPassphrase(command, confirm, a.terminalPassphrase(command))
}
