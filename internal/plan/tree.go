package plan

import (
	"fmt"
	"os"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/detect"
	"github.com/cplieger/deadset/v2/internal/gomod"
	"github.com/cplieger/deadset/v2/internal/pkgjson"
)

// Census is a [Tree] that asks the tree it reads each question once, so every
// plan made over one Census reads one census of the filesystem. A caller must
// not modify a slice it returns, and it is not safe for concurrent use.
type Census struct {
	tree      Tree
	languages map[string]languagesRead
	names     map[nameQuestion]nameRead
}

type languagesRead struct {
	err       error
	languages []vocab.Language
}

type nameQuestion struct {
	dir    string
	source vocab.IdentitySource
}

type nameRead struct {
	err   error
	name  string
	found bool
}

// NewCensus is a Census of tree.
func NewCensus(tree Tree) *Census {
	return &Census{tree: tree, languages: map[string]languagesRead{}, names: map[nameQuestion]nameRead{}}
}

// Languages is the tree's census of dir, read once.
func (c *Census) Languages(dir string) ([]vocab.Language, error) {
	read, asked := c.languages[dir]
	if !asked {
		read.languages, read.err = c.tree.Languages(dir)
		c.languages[dir] = read
	}
	return read.languages, read.err
}

// Name is the name the tree reads source declaring for the module at dir,
// read once.
func (c *Census) Name(dir string, source vocab.IdentitySource) (name string, found bool, err error) {
	question := nameQuestion{dir: dir, source: source}
	read, asked := c.names[question]
	if !asked {
		read.name, read.found, read.err = c.tree.Name(dir, source)
		c.names[question] = read
	}
	return read.name, read.found, read.err
}

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
