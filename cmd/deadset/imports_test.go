package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// module is the path of this module.
const module = "github.com/cplieger/deadset"

// listedPackage is the part of a go list record the import graph is read from.
type listedPackage struct {
	Module     *struct{ Path string }
	ImportPath string
	Standard   bool
}

// TestTheCommandLinksOnlyTheStandardLibraryAndItsOwnPackages lists every
// package the deadset command links and finds each one in the standard library
// or in this module: no analyzer is linked as a library, and no JSON Schema
// validator, nor any other module, is part of the binary.
func TestTheCommandLinksOnlyTheStandardLibraryAndItsOwnPackages(t *testing.T) {
	t.Parallel()

	command := module + "/cmd/deadset"
	list := exec.CommandContext(t.Context(), "go", "list", "-deps", "-json=ImportPath,Standard,Module", command)
	list.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	var stderr bytes.Buffer
	list.Stderr = &stderr
	listed, err := list.Output()
	if err != nil {
		t.Fatalf("Setup: go list -deps %s: %v\n%s", command, err, stderr.String())
	}

	found := false
	decoder := json.NewDecoder(bytes.NewReader(listed))
	for decoder.More() {
		var p listedPackage
		if err := decoder.Decode(&p); err != nil {
			t.Fatalf("Setup: decode what go list -deps %s printed: %v", command, err)
		}
		found = found || p.ImportPath == command
		if p.Standard || p.Module != nil && p.Module.Path == module {
			continue
		}
		from := "no module"
		if p.Module != nil {
			from = "the module " + p.Module.Path
		}
		t.Errorf("the deadset command links %s from %s, want only the standard library and %s's own packages", p.ImportPath, from, module)
	}
	if !found {
		t.Errorf("go list -deps %s does not list %s itself, so the graph read is not the command's", command, command)
	}
}
