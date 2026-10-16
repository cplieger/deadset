package plan

import (
	"fmt"
	"os"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/detect"
	"github.com/cplieger/deadset/v2/internal/gomod"
	"github.com/cplieger/deadset/v2/internal/pkgjson"
)

// Disk is the [Tree] of the local filesystem.
type Disk struct{}

// Languages is the census of the directory dir, which must be one.
func (Disk) Languages(dir string) ([]vocab.Language, error) {
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return nil, err
	case !info.IsDir():
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	return detect.Languages(dir)
}

// readers reads, per identity source, the name it declares for a module.
var readers = map[vocab.IdentitySource]func(dir string) (string, bool, error){
	{File: "go.mod", Field: "module"}: gomod.Path,
	{File: "package.json", Field: "name"}: func(dir string) (string, bool, error) {
		name, named := pkgjson.Name(dir)
		return name, named, nil
	},
}

// Name reads the name source declares for the module at dir.
func (Disk) Name(dir string, source vocab.IdentitySource) (name string, found bool, err error) {
	read, known := readers[source]
	if !known {
		return "", false, fmt.Errorf("plan: no reader of the %s of %s", source.Field, source.File)
	}
	return read(dir)
}
