package releasepromotion

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPromotionParsersRejectSingleWrongCaseFieldReplacements(t *testing.T) {
	proof := SourceQualityProof{
		SchemaID: "schema",
		Workflow: Workflow{RunID: 1},
		Results:  SourceQualityResults{Licenses: map[string]string{"Custom-Key": "pass"}},
	}
	encoded, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSourceQualityProof(encoded); err != nil {
		t.Fatalf("canonical source-quality shape: %v", err)
	}
	for name, replacement := range map[string][2]string{
		"root":   {`"schema_id"`, `"Schema_ID"`},
		"nested": {`"run_id"`, `"Run_ID"`},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := strings.Replace(string(encoded), replacement[0], replacement[1], 1)
			if _, err := ParseSourceQualityProof([]byte(candidate)); err == nil {
				t.Fatal("single wrong-case field replacement was accepted")
			}
		})
	}

	manifestBytes, err := json.Marshal(Manifest{})
	if err != nil {
		t.Fatal(err)
	}
	nestedMatrix := strings.Replace(string(manifestBytes), `"commit_sha"`, `"Commit_SHA"`, 1)
	if _, err := ParseManifest([]byte(nestedMatrix)); err == nil {
		t.Fatal("wrong-case field inside embedded E2E matrix proof was accepted")
	}
}

func TestAssembleRejectsNonCanonicalEmbeddedProofFileBytes(t *testing.T) {
	fixture := newPromotionFixture(t)
	compact, err := json.Marshal(fixture.manifest.SourceQuality.Proof)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, fixture.sourcePath, compact)
	if _, err := Assemble(fixture.assembleOptions()); err == nil || ErrorCode(err) != CodeDigestMismatch {
		t.Fatalf("non-canonical proof file error=%v code=%q", err, ErrorCode(err))
	}
}
