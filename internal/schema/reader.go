// Package schema is deadset's schema reader, the analysis of the schema
// languages of languages.json, as grammar/analysis/schemas.md states it:
// [Read] reads every Protocol Buffers file of a target into one report and one
// facts document, the facts holding one schema record per declaration for the
// merge to decide against the analyzers' bindings.
package schema

import (
	"context"
	"fmt"
	"os"

	"github.com/cplieger/deadset-spec/v8/facts"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// Name is the reader's name, the analyzer the proto row of languages.json
// names, which its report, its facts and its component ids carry.
const Name = "deadset-proto"

// Input is one reading of a target.
type Input struct {
	// Target is the target root, absolute; every file is read inside it.
	Target string

	// Root and Identity are the target as the report names it: its root
	// relative to the run's directory and its identity.
	Root, Identity string

	// Version is deadset's version, which the report names its analyzer at.
	Version string

	// Analyzed is every language the run's analyzers read, which decides
	// whether the run reads a plugin's output.
	Analyzed []vocab.Language

	Conformance report.Conformance
	Settings    Settings
}

// Result is what one reading wrote.
type Result struct {
	Report *report.Report
	Facts  *facts.Document
}

// SetupError is a reading that ends with the failure code, of one
// setup-failure class.
type SetupError struct {
	Class  string
	Detail string
}

func (e *SetupError) Error() string { return e.Class + ": " + e.Detail }

// Read reads every schema file of the target. A target holding no schema file
// and a directory proto.import_paths names that does not exist are each a
// [*SetupError], a malformed suppression a [*suppress.MalformedError], and any
// other error a failure to read the tree.
func Read(ctx context.Context, in *Input) (*Result, error) {
	root, openErr := os.OpenRoot(in.Target)
	if openErr != nil {
		return nil, fmt.Errorf("schema: %w", openErr)
	}
	defer func() { _ = root.Close() }()
	r := &reading{in: in, tree: root.FS(), byFull: map[string]*decl{}, byRef: map[string]*decl{}, bufDocs: map[string]bufDoc{}, unclean: map[string]uncleanFile{}, notes: []report.Note{}}
	if err := r.load(); err != nil {
		return nil, err
	}
	r.generation()
	r.declare()
	r.exempt()
	r.classify()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := r.suppress(ctx); err != nil {
		return nil, err
	}
	r.withhold()
	r.mint()
	evaluations, err := r.edges(ctx)
	if err != nil {
		return nil, err
	}
	return r.result(evaluations)
}
