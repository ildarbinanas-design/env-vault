// Package strictjson decodes security-sensitive JSON using exact struct field
// names. The standard encoding/json decoder intentionally matches struct
// fields case-insensitively and keeps the last duplicate object member; both
// behaviours are unsuitable for durable release evidence.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

var rawMessageType = reflect.TypeOf(json.RawMessage{})

// Validate accepts exactly one bounded JSON value and rejects duplicate or
// case-variant object members at every depth. It is intended for untrusted
// transport responses whose vendor-owned field set may grow over time.
func Validate(data []byte, limit int) error {
	if len(data) == 0 || len(data) > limit {
		return fmt.Errorf("JSON size %d is outside 1..%d", len(data), limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeOpaqueValue(decoder, "$"); err != nil {
		return err
	}
	return requireEOF(decoder)
}

// Decode validates one bounded JSON value against destination's exact field
// shape before performing the typed decode. Struct fields must use their exact
// json tag spelling. Maps and json.RawMessage values remain schema-opaque, but
// duplicate or case-variant object members are still rejected within them.
func Decode(data []byte, limit int, destination any) error {
	if len(data) == 0 || len(data) > limit {
		return fmt.Errorf("JSON size %d is outside 1..%d", len(data), limit)
	}
	typeOfDestination := reflect.TypeOf(destination)
	if typeOfDestination == nil || typeOfDestination.Kind() != reflect.Pointer || typeOfDestination.Elem().Kind() == reflect.Invalid {
		return errors.New("destination must be a non-nil pointer")
	}

	shapeDecoder := json.NewDecoder(bytes.NewReader(data))
	shapeDecoder.UseNumber()
	if err := consumeValue(shapeDecoder, typeOfDestination.Elem(), "$"); err != nil {
		return err
	}
	if err := requireEOF(shapeDecoder); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return requireEOF(decoder)
}

func consumeValue(decoder *json.Decoder, expected reflect.Type, path string) error {
	for expected.Kind() == reflect.Pointer {
		expected = expected.Elem()
	}
	if expected == rawMessageType || expected.Kind() == reflect.Interface {
		return consumeOpaqueValue(decoder, path)
	}

	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		// Type compatibility, including null handling and custom scalar types,
		// remains encoding/json's responsibility. This pass exists to make
		// object member identity exact before its permissive field matching.
		return nil
	}

	switch delimiter {
	case '{':
		switch expected.Kind() {
		case reflect.Struct:
			return consumeStruct(decoder, expected, path)
		case reflect.Map:
			return consumeMap(decoder, expected.Elem(), path)
		default:
			return consumeOpaqueObject(decoder, path)
		}
	case '[':
		if expected.Kind() == reflect.Slice || expected.Kind() == reflect.Array {
			return consumeArray(decoder, expected.Elem(), path)
		}
		return consumeOpaqueArray(decoder, path)
	default:
		return fmt.Errorf("%s has unexpected JSON delimiter %q", path, delimiter)
	}
}

func consumeStruct(decoder *json.Decoder, expected reflect.Type, path string) error {
	fields := jsonFields(expected)
	seen := make(map[string]bool, len(fields))
	for decoder.More() {
		name, err := objectKey(decoder, path)
		if err != nil {
			return err
		}
		fieldType, known := fields[name]
		if !known {
			return fmt.Errorf("%s has unknown or non-canonical field %q", path, name)
		}
		if seen[name] {
			return fmt.Errorf("%s has duplicate field %q", path, name)
		}
		seen[name] = true
		if err := consumeValue(decoder, fieldType, fieldPath(path, name)); err != nil {
			return err
		}
	}
	return consumeClosing(decoder, '}', path)
}

func consumeMap(decoder *json.Decoder, valueType reflect.Type, path string) error {
	seen := make(map[string]string)
	for decoder.More() {
		name, err := objectKey(decoder, path)
		if err != nil {
			return err
		}
		if err := recordOpaqueKey(seen, name, path); err != nil {
			return err
		}
		if err := consumeValue(decoder, valueType, fieldPath(path, name)); err != nil {
			return err
		}
	}
	return consumeClosing(decoder, '}', path)
}

func consumeArray(decoder *json.Decoder, elementType reflect.Type, path string) error {
	index := 0
	for decoder.More() {
		if err := consumeValue(decoder, elementType, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
		index++
	}
	return consumeClosing(decoder, ']', path)
}

func consumeOpaqueValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return consumeOpaqueObject(decoder, path)
	case '[':
		return consumeOpaqueArray(decoder, path)
	default:
		return fmt.Errorf("%s has unexpected JSON delimiter %q", path, delimiter)
	}
}

func consumeOpaqueObject(decoder *json.Decoder, path string) error {
	seen := make(map[string]string)
	for decoder.More() {
		name, err := objectKey(decoder, path)
		if err != nil {
			return err
		}
		if err := recordOpaqueKey(seen, name, path); err != nil {
			return err
		}
		if err := consumeOpaqueValue(decoder, fieldPath(path, name)); err != nil {
			return err
		}
	}
	return consumeClosing(decoder, '}', path)
}

func consumeOpaqueArray(decoder *json.Decoder, path string) error {
	index := 0
	for decoder.More() {
		if err := consumeOpaqueValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
		index++
	}
	return consumeClosing(decoder, ']', path)
}

func objectKey(decoder *json.Decoder, path string) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	name, ok := token.(string)
	if !ok {
		return "", fmt.Errorf("%s has a non-string object key", path)
	}
	return name, nil
}

func recordOpaqueKey(seen map[string]string, name, path string) error {
	folded := strings.ToLower(name)
	if first, exists := seen[folded]; exists {
		return fmt.Errorf("%s has duplicate or case-variant fields %q and %q", path, first, name)
	}
	seen[folded] = name
	return nil
}

func consumeClosing(decoder *json.Decoder, want json.Delim, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != want {
		return fmt.Errorf("%s has malformed closing delimiter", path)
	}
	return nil
}

func jsonFields(structType reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, structType.NumField())
	for index := 0; index < structType.NumField(); index++ {
		field := structType.Field(index)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

func fieldPath(parent, name string) string {
	return fmt.Sprintf("%s[%q]", parent, name)
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing data: %w", err)
	}
	return errors.New("input contains multiple JSON values")
}
