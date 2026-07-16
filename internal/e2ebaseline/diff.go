package e2ebaseline

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

type BaselineDiff struct {
	SchemaID       string       `json:"schema_id"`
	SchemaVersion  int          `json:"schema_version"`
	PreviousDigest string       `json:"previous_digest"`
	UpdatedDigest  string       `json:"updated_digest"`
	Changes        []DiffChange `json:"changes"`
}

type DiffChange struct {
	Path   string `json:"path"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

func DiffJSON(previous []byte, updated Baseline) (BaselineDiff, error) {
	var before any
	if err := json.Unmarshal(previous, &before); err != nil {
		return BaselineDiff{}, fmt.Errorf("decode previous baseline for diff: %w", err)
	}
	updatedBytes, err := json.Marshal(updated)
	if err != nil {
		return BaselineDiff{}, err
	}
	var after any
	if err := json.Unmarshal(updatedBytes, &after); err != nil {
		return BaselineDiff{}, err
	}
	previousDigest, err := Digest(before)
	if err != nil {
		return BaselineDiff{}, err
	}
	updatedDigest, err := Digest(updated)
	if err != nil {
		return BaselineDiff{}, err
	}
	diff := BaselineDiff{SchemaID: DiffSchemaID, SchemaVersion: 1, PreviousDigest: previousDigest, UpdatedDigest: updatedDigest}
	diffValues("$", before, after, &diff.Changes)
	return diff, nil
}

func diffValues(path string, before, after any, changes *[]DiffChange) {
	leftObject, leftIsObject := before.(map[string]any)
	rightObject, rightIsObject := after.(map[string]any)
	if leftIsObject && rightIsObject {
		keys := make(map[string]bool, len(leftObject)+len(rightObject))
		for key := range leftObject {
			keys[key] = true
		}
		for key := range rightObject {
			keys[key] = true
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			diffValues(path+"."+key, leftObject[key], rightObject[key], changes)
		}
		return
	}
	leftArray, leftIsArray := before.([]any)
	rightArray, rightIsArray := after.([]any)
	if leftIsArray && rightIsArray && len(leftArray) == len(rightArray) {
		for index := range leftArray {
			diffValues(fmt.Sprintf("%s[%d]", path, index), leftArray[index], rightArray[index], changes)
		}
		return
	}
	if !reflect.DeepEqual(before, after) {
		*changes = append(*changes, DiffChange{Path: path, Before: before, After: after})
	}
}
