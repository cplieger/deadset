package invoke

import (
	"encoding/json"
	"slices"
	"testing"

	spec "github.com/cplieger/deadset-spec/v3"
)

// TestTheReportCodesAreTheContractsVerdicts pins the exit codes after which a
// report is read to the codes contract/exit-codes.json names clean, findings
// and pending, the three verdicts about a complete report.
func TestTheReportCodesAreTheContractsVerdicts(t *testing.T) {
	t.Parallel()

	body, err := spec.Contract.ReadFile("contract/exit-codes.json")
	if err != nil {
		t.Fatalf("Setup: read contract/exit-codes.json: %v", err)
	}
	var table struct {
		ExitCodes []struct {
			Name string `json:"name"`
			Code int    `json:"code"`
		} `json:"exit_codes"`
	}
	if err := json.Unmarshal(body, &table); err != nil {
		t.Fatalf("Setup: decode contract/exit-codes.json: %v", err)
	}

	var verdicts []int
	for _, c := range table.ExitCodes {
		if slices.Contains([]string{"clean", "findings", "pending"}, c.Name) {
			verdicts = append(verdicts, c.Code)
		}
	}
	slices.Sort(verdicts)
	if got := slices.Sorted(slices.Values(reportCodes)); !slices.Equal(got, verdicts) {
		t.Errorf("a report is read after the exit codes %v, want %v, the codes exit-codes.json names clean, findings and pending", got, verdicts)
	}
}
