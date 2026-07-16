package strictjson

import (
	"encoding/json"
	"strings"
	"testing"
)

type testDocument struct {
	SchemaID string            `json:"schema_id"`
	Nested   testNested        `json:"nested"`
	Labels   map[string]string `json:"labels"`
	Opaque   json.RawMessage   `json:"opaque"`
}

type testNested struct {
	SourceSHA string `json:"source_sha"`
}

func TestDecodeRequiresExactStructFieldNames(t *testing.T) {
	valid := `{"schema_id":"v1","nested":{"source_sha":"abc"},"labels":{"Mixed-Key":"ok"},"opaque":{"VendorField":true}}`
	var document testDocument
	if err := Decode([]byte(valid), 4096, &document); err != nil {
		t.Fatalf("valid document: %v", err)
	}
	for name, input := range map[string]string{
		"root replacement":   strings.Replace(valid, `"schema_id"`, `"Schema_ID"`, 1),
		"nested replacement": strings.Replace(valid, `"source_sha"`, `"Source_SHA"`, 1),
		"root duplicate":     strings.Replace(valid, `"schema_id":"v1"`, `"schema_id":"v1","schema_id":"v2"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := Decode([]byte(input), 4096, &document); err == nil {
				t.Fatal("non-canonical JSON field shape was accepted")
			}
		})
	}
}

func TestDecodeKeepsMapsAndRawMessagesSchemaOpaque(t *testing.T) {
	valid := `{"schema_id":"v1","nested":{"source_sha":"abc"},"labels":{"UPPER":"one","lower":"two"},"opaque":{"VendorField":{"NestedVendor":true}}}`
	var document testDocument
	if err := Decode([]byte(valid), 4096, &document); err != nil {
		t.Fatalf("opaque keys were rejected: %v", err)
	}

	caseVariant := strings.Replace(valid, `"NestedVendor":true`, `"NestedVendor":true,"nestedvendor":false`, 1)
	if err := Decode([]byte(caseVariant), 4096, &document); err == nil {
		t.Fatal("case-variant members inside opaque JSON were accepted")
	}
}

func TestDecodeRejectsMultipleValuesAndBounds(t *testing.T) {
	var document testDocument
	if err := Decode([]byte(`{} {}`), 4096, &document); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
	if err := Decode(nil, 4096, &document); err == nil {
		t.Fatal("empty JSON was accepted")
	}
	if err := Decode([]byte(`{}`), 1, &document); err == nil {
		t.Fatal("oversized JSON was accepted")
	}
}
