package cli

import (
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	keyringstore "github.com/ildarbinanas-design/env-vault/internal/secretstore/keyring"
)

func TestWindowsImportCollisionsBeforeAnyWrite(t *testing.T) {
	for _, records := range [][]secretstore.Metadata{
		{{Service: "env-vault", Name: "TOKEN"}, {Service: "env-vault", Name: "token"}},
		{{Service: "Team", Name: "TOKEN"}, {Service: "team", Name: "token"}},
		{{Service: "Équipe", Name: "TOKEN"}, {Service: "éQUIPE", Name: "token"}},
	} {
		checkImportPreflight(t, records, keyringstore.New().ValidateIdentities, 0, apperrors.CodeBundleInvalid)
	}
}
