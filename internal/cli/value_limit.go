package cli

import (
	"fmt"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

func effectiveValueLimit(store secretstore.Store) int {
	limit := secretstore.MaxValueBytes
	if limiter, ok := store.(secretstore.ValueLimiter); ok {
		if backendLimit := limiter.MaxValueBytes(); backendLimit > 0 && backendLimit < limit {
			limit = backendLimit
		}
	}
	return limit
}

func secretTooLarge(command, name string, limit int) *apperrors.AppError {
	subject := "Secret value"
	if name != "" {
		subject = "Secret " + name
	}
	return apperrors.New(command, apperrors.CodeSecretTooLarge,
		subject+" exceeds the size limit",
		fmt.Sprintf("Store a shorter value (at most %d bytes per secret)", limit),
		apperrors.ExitUsage)
}
