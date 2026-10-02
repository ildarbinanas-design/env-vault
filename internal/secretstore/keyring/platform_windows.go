package keyring

import (
	"errors"
	"strings"
	"unsafe"

	"github.com/99designs/keyring"
	"golang.org/x/sys/windows"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

var (
	credEnumerate  = windows.NewLazySystemDLL("advapi32.dll").NewProc("CredEnumerateW")
	credFree       = windows.NewLazySystemDLL("advapi32.dll").NewProc("CredFree")
	compareOrdinal = windows.NewLazySystemDLL("kernel32.dll").NewProc("CompareStringOrdinal")
)

// Only the layout is shared with CREDENTIALW. Never copy CredentialBlob from
// enumeration; Keys/Exists retain metadata alone and never call CredRead.
type nativeCredential struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             windows.Filetime
	CredentialBlobSize      uint32
	CredentialBlob          *byte
	Persist, AttributeCount uint32
	Attributes              unsafe.Pointer
	TargetAlias, UserName   *uint16
}
type credentialMetadata struct {
	target string
	kind   uint32
}

type windowsRing struct {
	keyring.Keyring
	service   string
	enumerate func() ([]credentialMetadata, error)
}

func enumerateCredentials() ([]credentialMetadata, error) {
	// Flags=0 preserves the actual TargetName. CRED_ENUMERATE_ALL_CREDENTIALS
	// instead adds a namespace:attribute= prefix, unsuitable for identity.
	var count uint32
	var entries **nativeCredential
	ok, _, err := credEnumerate.Call(0, 0, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&entries)))
	if ok == 0 {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return nil, nil
		}
		return nil, err
	}
	defer credFree.Call(uintptr(unsafe.Pointer(entries)))
	result := make([]credentialMetadata, 0, count)
	for _, entry := range unsafe.Slice(entries, count) {
		if entry.Type == 1 {
			result = append(result, credentialMetadata{windows.UTF16PtrToString(entry.TargetName), entry.Type})
		}
	}
	return result, nil
}

func openPlatform(cfg keyring.Config) (keyring.Keyring, error) {
	// Windows has no preceding production backend. Explicit pass stays on the
	// dependency's path and keeps its case-sensitive identity.
	for _, backend := range cfg.AllowedBackends {
		one := cfg
		one.AllowedBackends = []keyring.BackendType{backend}
		ring, err := keyring.Open(one)
		if err != nil {
			continue
		}
		if backend == keyring.WinCredBackend {
			return &windowsRing{Keyring: ring, service: cfg.ServiceName, enumerate: enumerateCredentials}, nil
		}
		return ring, nil
	}
	return nil, keyring.ErrNoAvailImpl
}

func compareWindowsTargets(a, b string) (int, error) {
	left, err := windows.UTF16FromString(a)
	if err != nil {
		return 0, err
	}
	right, err := windows.UTF16FromString(b)
	if err != nil {
		return 0, err
	}
	result, _, err := compareOrdinal.Call(uintptr(unsafe.Pointer(&left[0])), uintptr(len(left)-1), uintptr(unsafe.Pointer(&right[0])), uintptr(len(right)-1), 1)
	if result == 0 {
		return 0, err
	}
	return int(result) - 2, nil
}
func credentialTarget(service, name string) string { return "keyring:" + service + ":" + name }
func (r *windowsRing) MatchName(a, b string) (bool, error) {
	cmp, err := compareWindowsTargets(credentialTarget(r.service, a), credentialTarget(r.service, b))
	return cmp == 0, err
}
func (r *windowsRing) Keys() ([]string, error) {
	entries, err := r.enumerate()
	if err != nil {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return nil, nil
		}
		return nil, err
	}
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.kind != 1 {
			continue
		}
		// Names cannot contain ':', while service names can. Split at the last
		// separator so a neighbouring service such as team:ci cannot enter team.
		separator := strings.LastIndexByte(entry.target, ':')
		if separator < 0 {
			continue
		}
		name := entry.target[separator+1:]
		if secretstore.ValidateSecretName(name) != nil {
			continue
		}
		cmp, err := compareWindowsTargets(entry.target[:separator+1], credentialTarget(r.service, ""))
		if err != nil {
			return nil, err
		}
		if cmp == 0 {
			keys = append(keys, name)
		}
	}
	return keys, nil
}

func (s Store) ValidateIdentities(records []secretstore.Metadata) error {
	if !s.usesWinCred() {
		return nil
	}
	targets := make([]string, len(records))
	for i, record := range records {
		targets[i] = credentialTarget(record.Service, record.Name)
	}
	// Sorting with an error-returning native comparison avoids quadratic work
	// for large containers and never substitutes Go Unicode case folding.
	var sortTargets func([]string) error
	sortTargets = func(values []string) error {
		if len(values) < 2 {
			return nil
		}
		middle := len(values) / 2
		if err := sortTargets(values[:middle]); err != nil {
			return err
		}
		if err := sortTargets(values[middle:]); err != nil {
			return err
		}
		merged := make([]string, 0, len(values))
		i, j := 0, middle
		for i < middle && j < len(values) {
			cmp, err := compareWindowsTargets(values[i], values[j])
			if err != nil {
				return err
			}
			if cmp == 0 {
				return secretstore.ErrIdentityCollision
			}
			if cmp < 0 {
				merged = append(merged, values[i])
				i++
			} else {
				merged = append(merged, values[j])
				j++
			}
		}
		merged = append(merged, values[i:middle]...)
		merged = append(merged, values[j:]...)
		copy(values, merged)
		return nil
	}
	return sortTargets(targets)
}
