package report_test

import (
	"encoding/json"
	"slices"
	"testing"

	spec "github.com/cplieger/deadset-spec/v3"
	"github.com/cplieger/deadset/internal/report"
)

// TestTheVersionsAreThePinnedContracts pins the Contract version this module
// names and the report schema version its types model to the contract.json of
// the Contract the module pins, so a pin that moves either one fails here
// until the types catch up.
func TestTheVersionsAreThePinnedContracts(t *testing.T) {
	t.Parallel()

	body, err := spec.Contract.ReadFile("contract/contract.json")
	if err != nil {
		t.Fatalf("Setup: read contract/contract.json: %v", err)
	}
	var contract struct {
		ContractVersion string   `json:"contract_version"`
		SchemaVersions  []string `json:"schema_versions"`
	}
	if err := json.Unmarshal(body, &contract); err != nil {
		t.Fatalf("Setup: decode contract/contract.json: %v", err)
	}

	if report.ContractVersion != contract.ContractVersion {
		t.Errorf("ContractVersion = %q, want %q, the pinned contract.json's contract_version",
			report.ContractVersion, contract.ContractVersion)
	}
	if want := []string{report.SchemaVersion}; !slices.Equal(contract.SchemaVersions, want) {
		t.Errorf("contract.json's schema_versions = %q, want %q: the types model one schema version",
			contract.SchemaVersions, want)
	}
}
