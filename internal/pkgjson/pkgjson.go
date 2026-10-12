// Package pkgjson reads the name a TypeScript package publishes itself under
// from the package.json file in a directory, and reads nothing else of it.
package pkgjson

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// fileName is the name of the manifest that gives a directory's package its
// name.
const fileName = "package.json"

// Name is the name the package.json file in dir gives its package. named is
// false where dir holds no such file, or one that cannot be read, that is not
// a JSON object, or whose name member is absent or not a non-empty string,
// because a TypeScript analysis reads every such manifest as naming no
// package. No directory above dir is read.
func Name(dir string) (name string, named bool) {
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		return "", false
	}
	// A map, because a struct field matches a member name in any case and a
	// manifest's member names are case-sensitive.
	var members map[string]json.RawMessage
	if json.Unmarshal(data, &members) != nil {
		return "", false
	}
	if json.Unmarshal(members["name"], &name) != nil || name == "" {
		return "", false
	}
	return name, true
}
