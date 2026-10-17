// Package run is the order of one analysis by the orchestrator: check the
// shared documents, plan, open every source, lay out the run directory, admit every source, produce every
// report, merge them, shape the merged report and write it with every
// rendering that is a file beside it. Each stage's decision belongs to the
// package that makes it; this one holds only their order and what one stage
// hands the next.
package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cplieger/deadset-spec/v8/render"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/documents"
	"github.com/cplieger/deadset/v2/internal/failure"
	"github.com/cplieger/deadset/v2/internal/merge"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/policy"
	"github.com/cplieger/deadset/v2/internal/rundir"
	"github.com/cplieger/deadset/v2/internal/scope"
	"github.com/cplieger/deadset/v2/internal/source"
)

// Product is the analyzer name a merged report names this program by.
const Product = "deadset"

// renderings is where each format other than json, whose rendering is the
// merged report itself, is written: on standard output, an empty suffix, or
// as the file beside the merged report its suffix names. A document a crash
// cut short on a stream is not one a reader can tell from a complete one, and
// an upload step names a file, so every document is a file.
var renderings = map[vocab.Format]string{
	vocab.FormatText:     "",
	vocab.FormatGitHub:   "",
	vocab.FormatSARIF:    ".sarif",
	vocab.FormatTemplate: ".tmpl",
}

// Streamed reports whether a format is rendered on standard output.
func Streamed(format vocab.Format) bool {
	suffix, rendered := renderings[format]
	return rendered && suffix == ""
}

// Request is one analysis.
type Request struct {
	// Diagnostics receives what a source's handshake prints on its standard
	// error.
	Diagnostics io.Writer

	Resolved *config.Resolved

	// Scope is the declared scope, whose target is the target root.
	Scope *scope.Document

	// Render is what every rendering of the run reads beside the report.
	Render *render.Options

	// Started is called with the run directory's path once it exists.
	Started func(dir string)

	// Print shows what every source printed, once every source has exited
	// and before the merge, so a run whose Print fails merges no report; its
	// error is joined to the sources' refusals.
	Print func(outcomes []source.Outcome) error

	// Open makes the source of one planned source, run in dir; nil opens the
	// analyzer process its provider entry names.
	Open func(planned *plan.Source, dir string, diagnostics io.Writer) (source.Source, error)

	// Root is the target root, which every position of a report is relative
	// to and which a rendering reads source lines below.
	Root string

	// RunDir is the run directory to create, or empty for one under the
	// temporary directory.
	RunDir string

	// Version is this program's version, which the merged report names.
	Version string
}

// rendering is one rendering written beside the merged report.
type rendering struct {
	Format vocab.Format
	Path   string
}

// Result is what a run left once its run directory existed: the merged report
// and the renderings written beside it.
type Result struct {
	Merged  *report.Report
	Written []rendering
}

// Analyze runs one analysis. An error is a run that produced no merged report,
// or a rendering that could not be written beside it; the result is returned
// with it once the run directory exists.
func Analyze(ctx context.Context, req *Request) (*Result, error) {
	if err := documents.Check(ctx, req.Root); err != nil {
		return nil, err
	}
	planned, err := plan.Make(&plan.Input{Scope: req.Scope, Languages: req.Resolved.Config.Languages, Providers: req.Resolved.Config.Providers}, plan.Disk{})
	if err != nil {
		return nil, err
	}
	sources, err := open(req, planned)
	if err != nil {
		return nil, err
	}
	dir, err := runDirectory(req.RunDir)
	if err != nil {
		return nil, err
	}
	if req.Started != nil {
		req.Started(dir.Path())
	}
	result := &Result{}
	handed, err := prepare(dir, req.Resolved, planned, sources)
	if err != nil {
		return result, err
	}
	accepted := vocab.SchemaVersions()
	var refused []error
	for i := range sources {
		if refusal := sources[i].Admit(ctx, handed[i].entry, accepted); refusal != nil {
			refused = append(refused, refusal)
		}
	}
	if len(refused) > 0 {
		return result, errors.Join(refused...)
	}
	paths := make([]source.Files, len(handed))
	for i := range handed {
		paths[i] = handed[i].files
	}
	outcomes, err := source.Run(ctx, sources, paths)
	if req.Print != nil {
		err = errors.Join(err, req.Print(outcomes))
	}
	if err != nil {
		return result, err
	}
	if result.Merged, err = merged(outcomes, accepted, req.Version); err != nil {
		return result, err
	}
	policy.Apply(result.Merged, &policy.Options{
		Severity:    req.Resolved.Severity,
		BySize:      req.Resolved.Config.Reporters.BySize,
		MaxFindings: req.Resolved.Config.Reporters.MaxFindings,
	})
	if refused := dir.WriteMerged(result.Merged); refused != nil {
		return result, refused
	}
	result.Written, err = writeFiles(dir, req, result.Merged)
	return result, err
}

// runDirectory creates the run directory path names, or one of its own when
// it names none.
func runDirectory(path string) (*rundir.Dir, error) {
	if path == "" {
		return rundir.CreateTemp()
	}
	return rundir.Create(path)
}

// open makes every planned source, before anything is written, so a command
// that resolves to nothing leaves no run directory behind.
func open(req *Request, planned *plan.Plan) ([]source.Source, error) {
	opener := req.Open
	if opener == nil {
		opener = func(planned *plan.Source, dir string, diagnostics io.Writer) (source.Source, error) {
			return source.Open(planned, dir, diagnostics)
		}
	}
	sources := make([]source.Source, len(planned.Sources))
	var failures []error
	for i := range planned.Sources {
		var err error
		if sources[i], err = opener(&planned.Sources[i], planned.Dir, req.Diagnostics); err != nil {
			failures = append(failures, err)
		}
	}
	return sources, errors.Join(failures...)
}

// handedFiles is one source's files in the run directory.
type handedFiles struct {
	entry rundir.Entry
	files source.Files
}

// prepare writes the run's scope document and, per source, its configuration
// and any scope document of its own.
func prepare(dir *rundir.Dir, resolved *config.Resolved, planned *plan.Plan, sources []source.Source) ([]handedFiles, error) {
	whole, err := planned.Scope.Encode()
	if err != nil {
		return nil, err
	}
	if err := dir.WriteScope(whole); err != nil {
		return nil, err
	}
	handed := make([]handedFiles, len(sources))
	for i := range sources {
		entry, err := dir.Entry(sources[i].Name())
		if err != nil {
			return nil, err
		}
		if handed[i], err = hand(dir, entry, resolved, &planned.Sources[i]); err != nil {
			return nil, err
		}
	}
	return handed, nil
}

// hand writes one source's configuration, the split of resolved for its
// languages, and its own scope document where it has one.
func hand(dir *rundir.Dir, entry rundir.Entry, resolved *config.Resolved, planned *plan.Source) (handedFiles, error) {
	split, err := resolved.Split(planned.Languages...)
	if err != nil {
		return handedFiles{}, err
	}
	if err := entry.WriteConfig(split); err != nil {
		return handedFiles{}, err
	}
	scopePath := dir.Scope()
	if planned.Scope != nil {
		encoded, err := planned.Scope.Encode()
		if err != nil {
			return handedFiles{}, err
		}
		if err := entry.WriteScope(encoded); err != nil {
			return handedFiles{}, err
		}
		scopePath = entry.Scope()
	}
	return handedFiles{entry: entry, files: source.Files{Scope: scopePath, Config: entry.Config(), Report: entry.Report()}}, nil
}

// merged is the merge of every outcome's report.
func merged(outcomes []source.Outcome, accepted []string, version string) (*report.Report, error) {
	inputs := make([]merge.Input, len(outcomes))
	for i := range outcomes {
		inputs[i] = merge.Input{Report: outcomes[i].Report, Digest: outcomes[i].Digest}
	}
	conformance, err := merge.Conformance()
	if err != nil {
		return nil, err
	}
	return merge.Merge(inputs, accepted, &merge.Caller{
		SchemaVersion:   vocab.SchemaVersion,
		ContractVersion: vocab.ContractVersion,
		Name:            Product,
		Version:         version,
		Conformance:     conformance,
	})
}

// writeFiles writes every rendering the formats name that is a file beside
// the merged report, in their order. The SARIF rendering hashes the source
// lines its results name, which it reads below the target root.
func writeFiles(dir *rundir.Dir, req *Request, r *report.Report) ([]rendering, error) {
	root, err := os.OpenRoot(req.Root)
	if err != nil {
		return nil, failure.Wrap(vocab.RunUnwrittenOutput, fmt.Errorf("render beside the merged report: %w", err))
	}
	defer func() { _ = root.Close() }()
	opts := *req.Render
	opts.Read = func(path string) ([]byte, error) { return root.ReadFile(filepath.FromSlash(path)) }
	var written []rendering
	for _, format := range req.Resolved.Config.Reporters.Formats {
		suffix := renderings[format]
		if suffix == "" {
			continue
		}
		var document bytes.Buffer
		if err := render.Render(&document, format, r, &opts); err != nil {
			return written, failure.Wrap(vocab.RunUnwrittenOutput, fmt.Errorf("render %s beside the merged report: %w", format, err))
		}
		if err := dir.WriteRendering(suffix, document.Bytes()); err != nil {
			return written, err
		}
		written = append(written, rendering{Format: format, Path: dir.Rendering(suffix)})
	}
	return written, nil
}
