package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// module is the path of this module.
const module = "github.com/cplieger/deadset/v2"

// listedPackage is the part of a go list record the import graph is read from.
type listedPackage struct {
	Module     *struct{ Path string }
	ImportPath string
	Standard   bool
}

// TestTheCommandLinksOnlyTheStandardLibraryAndFirstPartyModules lists every
// package the deadset command links and finds each one in the standard library,
// in golang.org/x, or in a first-party module, deadset-spec among them: no
// third-party module, a JSON Schema validator included, is part of the binary.
// No analyzer is a library dependency, so none is linked.
func TestTheCommandLinksOnlyTheStandardLibraryAndFirstPartyModules(t *testing.T) {
	t.Parallel()

	command := module + "/cmd/deadset"
	list := exec.CommandContext(t.Context(), "go", "list", "-deps", "-json=ImportPath,Standard,Module", command)
	list.Env = append(os.Environ(), "GOFLAGS=")
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
		if p.Standard || p.Module != nil && allowedModule(p.Module.Path) {
			continue
		}
		from := "no module"
		if p.Module != nil {
			from = "the module " + p.Module.Path
		}
		t.Errorf("the deadset command links %s from %s, want only the standard library, golang.org/x and first-party modules", p.ImportPath, from)
	}
	if !found {
		t.Errorf("go list -deps %s does not list %s itself, so the graph read is not the command's", command, command)
	}
}

// allowedModule reports whether the command may link a package of one module:
// golang.org/x, or a first-party github.com/cplieger module.
func allowedModule(path string) bool {
	return strings.HasPrefix(path, "golang.org/x/") || strings.HasPrefix(path, "github.com/cplieger/")
}
