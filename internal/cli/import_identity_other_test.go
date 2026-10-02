//go:build !windows

package cli

import (
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	keyringstore "github.com/ildarbinanas-design/env-vault/internal/secretstore/keyring"
	"testing"
)

func TestOtherPlatformsRetainCaseSensitiveIdentity(t *testing.T) {
	if err := keyringstore.New().ValidateIdentities([]secretstore.Metadata{{Service: "Team", Name: "TOKEN"}, {Service: "team", Name: "token"}}); err != nil {
		t.Fatal("Windows identity rules reached another platform")
	}
}
