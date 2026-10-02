package config_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/cplieger/deadset/internal/config"
)

// testContractVersion is the Contract version the tests resolve under.
const testContractVersion = "3.2.0"

// fromRepository returns the inputs of a run whose one source is a repository
// configuration holding document.
func fromRepository(document string) *config.Inputs {
	return &config.Inputs{
		ContractVersion: testContractVersion,
		Repository:      config.Document{Path: "deadset.json", Data: []byte(document)},
	}
}

// printedDocument is a printed configuration, decoded.
type printedDocument struct {
	Values     map[string]any
	Provenance map[string]string
}

// printed decodes what a resolution prints.
func printed(t *testing.T, r *config.Resolved) printedDocument {
	t.Helper()

	var out bytes.Buffer
	if err := r.Print(&out); err != nil {
		t.Fatalf("Print() = error %v", err)
	}
	return decodePrinted(t, out.Bytes())
}

// decodePrinted decodes a configuration in the form Print writes, its provenance
// object apart from its values.
func decodePrinted(t *testing.T, document []byte) printedDocument {
	t.Helper()

	var values map[string]any
	if err := json.Unmarshal(document, &values); err != nil {
		t.Fatalf("decode a printed configuration: %v\n%s", err, document)
	}
	var provenance struct {
		Provenance map[string]string `json:"provenance"`
	}
	if err := json.Unmarshal(document, &provenance); err != nil {
		t.Fatalf("decode the provenance of a printed configuration: %v\n%s", err, document)
	}
	delete(values, "provenance")
	return printedDocument{Values: values, Provenance: provenance.Provenance}
}
