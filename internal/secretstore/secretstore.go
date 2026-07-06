package secretstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

const DefaultService = "env-vault"

var (
	ErrNotFound    = errors.New("secret not found")
	ErrUnavailable = errors.New("secret backend unavailable")
)

type Metadata struct {
	Service     string
	Name        string
	Fingerprint string
}

type Store interface {
	Set(ctx context.Context, service, name string, value []byte) error
	Get(ctx context.Context, service, name string) ([]byte, error)
	Exists(ctx context.Context, service, name string) (bool, error)
	Delete(ctx context.Context, service, name string) error
	List(ctx context.Context, service string) ([]Metadata, error)
}

func Fingerprint(service, name string) string {
	sum := sha256.Sum256([]byte(service + "\x00" + name))
	return hex.EncodeToString(sum[:])[:16]
}
