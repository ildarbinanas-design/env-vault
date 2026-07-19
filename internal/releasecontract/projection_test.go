package releasecontract

import (
	"reflect"
	"testing"
)

func TestOperationalProjectionBindsCanonicalContract(t *testing.T) {
	contract := loadCanonicalForTest(t)
	wantDigest, err := SemanticSHA256(contract)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := contract.OperationalProjection()
	if err != nil {
		t.Fatal(err)
	}
	if projection.SchemaID != OperationalProjectionSchema || projection.SchemaVersion != OperationalProjectionVersion ||
		projection.ContractSchemaID != SchemaID || projection.ContractSchemaVersion != SchemaVersion ||
		projection.ContractSemanticSHA256 != wantDigest {
		t.Fatalf("projection identity=%+v", projection)
	}
	if !reflect.DeepEqual(projection.Repositories, contract.Repositories) ||
		projection.Version.Pattern != contract.VersionPolicy.Pattern ||
		projection.Version.TagPrefix != contract.VersionPolicy.TagPrefix ||
		!reflect.DeepEqual(projection.Version.ReleasePlease, contract.VersionPolicy.ReleasePlease) ||
		!reflect.DeepEqual(projection.Naming, contract.Naming) ||
		!reflect.DeepEqual(projection.Platforms, contract.Platforms) ||
		!reflect.DeepEqual(projection.Assets, contract.Assets) ||
		!reflect.DeepEqual(projection.Homebrew, contract.Homebrew) ||
		!reflect.DeepEqual(projection.Workflows, contract.Workflows) ||
		!reflect.DeepEqual(projection.Concurrency, contract.Concurrency) ||
		!reflect.DeepEqual(projection.Apps, contract.Apps) ||
		!reflect.DeepEqual(projection.MainRequiredChecks, contract.MainRequiredChecks) {
		t.Fatal("operational projection omitted or changed a live contract identity")
	}
}

func TestOperationalProjectionOwnsIndependentSlices(t *testing.T) {
	contract := loadCanonicalForTest(t)
	projection, err := contract.OperationalProjection()
	if err != nil {
		t.Fatal(err)
	}
	projection.Platforms[0].ID = "changed"
	projection.Assets[0] = "changed"
	projection.Homebrew.Platforms[0] = "changed"
	projection.Workflows[0].Events[0] = "changed"
	projection.Workflows[0].Jobs[0] = "changed"
	projection.Concurrency.Release.Workflows[0] = "changed"
	projection.Apps[0].ID = "changed"
	projection.MainRequiredChecks[0].Name = "changed"
	if contract.Platforms[0].ID == "changed" || contract.Assets[0] == "changed" ||
		contract.Homebrew.Platforms[0] == "changed" || contract.Workflows[0].Events[0] == "changed" ||
		contract.Workflows[0].Jobs[0] == "changed" || contract.Concurrency.Release.Workflows[0] == "changed" ||
		contract.Apps[0].ID == "changed" || contract.MainRequiredChecks[0].Name == "changed" {
		t.Fatal("projection mutation changed the source contract")
	}
}

func TestOperationalProjectionRejectsHistoricalContract(t *testing.T) {
	if _, err := (Contract{SchemaID: LegacySchemaID, SchemaVersion: LegacySchemaVersion}).OperationalProjection(); err == nil {
		t.Fatal("historical contract produced an operational projection")
	}
}
