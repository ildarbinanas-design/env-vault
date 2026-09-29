package cli

import (
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

type limitedStore struct {
	secretstore.Store
	limit int
}

func (s limitedStore) MaxValueBytes() int { return s.limit }

func TestImportRefusesAnOversizedValueBeforeWriting(t *testing.T) {
	entries := []bundle.SecretEntry{
		{Service: secretstore.DefaultService, Name: "small", Value: make([]byte, 4)},
		{Service: secretstore.DefaultService, Name: "large", Value: make([]byte, 5)},
	}
	err := refuseOversizedValues(limitedStore{limit: 4}, entries)
	appErr, ok := apperrors.From(err)
	if !ok || appErr.Code != apperrors.CodeSecretTooLarge || appErr.ExitCode != apperrors.ExitUsage || appErr.Message != "Secret large is larger than the backend stores" {
		t.Fatalf("err=%v, want SECRET_TOO_LARGE for large", err)
	}
	for name, store := range map[string]secretstore.Store{
		"a limit every value fits": limitedStore{limit: 5},
		"no known limit":           limitedStore{limit: 0},
		"a store without limits":   struct{ secretstore.Store }{},
	} {
		if err := refuseOversizedValues(store, entries); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
