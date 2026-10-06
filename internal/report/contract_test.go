package report_test

import (
	"encoding/json"
	"slices"
	"testing"

	spec "github.com/cplieger/deadset-spec/v6"
	"github.com/cplieger/deadset/internal/report"
)

// TestTheVersionsAreThePinnedContracts pins the Contract version this module
// names and the report schema version its types model to the contract.json of
// the Contract the module pins, so a pin that moves either one fails here
// until the types catch up. The schema versions the Contract admits are the
// ones the module accepts, and the one it writes is the latest of them.
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

	if len(contract.SchemaVersions) == 0 {
		t.Fatal("Setup: contract.json names no schema version")
	}
	if report.ContractVersion != contract.ContractVersion {
		t.Errorf("ContractVersion = %q, want %q, the pinned contract.json's contract_version",
			report.ContractVersion, contract.ContractVersion)
	}
	if !slices.Equal(report.SchemaVersions, contract.SchemaVersions) {
		t.Errorf("SchemaVersions = %q, want %q, the pinned contract.json's schema_versions",
			report.SchemaVersions, contract.SchemaVersions)
	}
	if last := contract.SchemaVersions[len(contract.SchemaVersions)-1]; report.SchemaVersion != last {
		t.Errorf("SchemaVersion = %q, want %q, the latest of the pinned contract.json's schema_versions",
			report.SchemaVersion, last)
	}
}
