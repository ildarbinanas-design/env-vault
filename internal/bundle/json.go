package bundle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// decodeStrictJSON is shared by the container and decrypted payload. Decode
// into the fixed schema first: unknown fields, wrong types, and unsupported
// nesting fail before the duplicate-field walk. No diagnostic includes input.
func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid JSON document", ErrInvalid)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON data", ErrInvalid)
	}

	// Token reads unescape member names, so escaped spellings cannot hide a
	// duplicate. Values are discarded; only names in each open object are kept.
	// Both schemas have fixed shallow nesting and a few fields per object.
	tokens := json.NewDecoder(bytes.NewReader(raw))
	tokens.UseNumber()
	return uniqueJSONFields(tokens)
}

func uniqueJSONFields(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: invalid JSON document", ErrInvalid)
	}
	opening, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	closing := json.Delim(']')
	switch opening {
	case '{':
		closing = '}'
		var names []string
		for decoder.More() {
			field, err := decoder.Token()
			name, ok := field.(string)
			if err != nil || !ok {
				return fmt.Errorf("%w: invalid JSON document", ErrInvalid)
			}
			for _, previous := range names {
				// encoding/json matches struct fields ignoring case. A second spelling
				// of the same field must not silently replace the first value either.
				if strings.EqualFold(previous, name) {
					return fmt.Errorf("%w: duplicate JSON field", ErrInvalid)
				}
			}
			names = append(names, name)
			if err := uniqueJSONFields(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := uniqueJSONFields(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%w: invalid JSON document", ErrInvalid)
	}
	if token, err := decoder.Token(); err != nil || token != closing {
		return fmt.Errorf("%w: invalid JSON document", ErrInvalid)
	}
	return nil
}
