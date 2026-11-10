// Package plan decides what one run of the orchestrator does, from the
// configuration, the declared scope and what a census of the tree finds: the
// languages in scope, the name each module is handed under, which analyzer
// runs each language and which consumers each analyzer loads. [Make] reads the
// filesystem only through a [Tree], so a plan is a value a test states.
package plan

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/failure"
	"github.com/cplieger/deadset/v2/internal/scope"
)

// unnamed is the name of a target several languages or none load that no
// identity source names, as languages.json states.
const unnamed = "."

// Tree is what planning reads of the filesystem.
type Tree interface {
	// Languages is every language a census puts in scope for the directory
	// dir, in languages.json's order.
	Languages(dir string) ([]vocab.Language, error)

	// Name is the name source declares for the module at dir, and false where
	// no such file names one.
	Name(dir string, source vocab.IdentitySource) (string, bool, error)
}

// Input is what a run is asked to plan.
type Input struct {
	// Scope is the declared scope: the target root and the consumers the
	// invocation's scope document names, every path absolute.
	Scope *scope.Document

	// Languages is analysis.languages: the languages in scope, or none when
	// a census of the target decides them.
	Languages []string

	// Omitted is the languages the census found that the run leaves out,
	// because their analyzers found no program of them.
	Omitted []vocab.Language

	Providers []config.Provider
}

// Plan is one run's decisions.
type Plan struct {
	// Dir is the directory every analyzer runs in, the deepest one holding
	// every path the scope names, because a report names its target root and
	// its loaded consumers relative to the directory its analyzer runs in.
	Dir string

	// Scope is the run's scope document: the declared one with every module
	// named as [Make] names it.
	Scope scope.Document

	// Languages is every language in scope, in languages.json's order.
	Languages []vocab.Language

	// Sources is one entry per provider entry the run invokes, in the
	// provider list's order.
	Sources []Source

	// Dropped is every declared consumer the run leaves out with the
	// languages [Input.Omitted] names, in the scope document's order.
	Dropped []Dropped

	// Detected reports whether a census of the target put Languages in
	// scope, analysis.languages naming none.
	Detected bool
}

// Source is one provider entry the run invokes and what it is handed.
type Source struct {
	// Scope is the scope document of the entry's analyzer alone where it is
	// handed only some of the run's consumers, and nil where it reads the
	// run's.
	Scope *scope.Document

	// Languages is every language in scope the entry claims.
	Languages []vocab.Language

	Entry config.Provider

	// Index is the entry's place in the provider list, -1 for an in-process
	// reader.
	Index int

	// InProcess is a schema reader linked into this program, its entry the
	// name and the language of its languages.json row, which no provider
	// list holds.
	InProcess bool

	// Facts asks a process analyzer for a facts document beside its report:
	// a schema language is in scope, whose reader the bindings decide.
	Facts bool
}

// Dropped is a declared consumer the run leaves out, and every language it
// holds.
type Dropped struct {
	Path      string
	Languages []vocab.Language
}

// UnclaimedError is a declared consumer no analyzer of the run claims a
// language of, which the run refuses as the invocation that declared it.
type UnclaimedError struct {
	Path      string
	Languages []vocab.Language
}

func (e *UnclaimedError) Error() string {
	held := make([]string, len(e.Languages))
	for i, language := range e.Languages {
		held[i] = string(language)
	}
	return fmt.Sprintf("the consumer %s holds %s, and no analyzer of the run claims a language of it", e.Path, strings.Join(held, ", "))
}

// Make plans the run in asks for, from what tree holds. A target in which the
// census finds no language, and a declared consumer that is absent, is no
// directory or holds no language, is a setup [*failure.Error];
// a language in scope no provider entry claims is a [*config.Error], before
// any consumer is read. A consumer is [Plan.Dropped] when every language it
// holds is left out, or when in.Omitted is not empty and it holds no language
// of the plan; each kept consumer no analyzer claims is an [*UnclaimedError].
func Make(in *Input, tree Tree) (*Plan, error) {
	languages, err := inScope(in, tree)
	if err != nil {
		return nil, err
	}
	if refused := claimed(in.Providers, languages); refused != nil {
		return nil, refused
	}
	p := &Plan{
		Dir: in.Scope.Root(), Languages: languages, Detected: len(in.Languages) == 0,
		Scope: scope.Document{Workspace: in.Scope.Workspace},
	}
	p.Scope.Target, err = named(in.Scope.Target, languages, true, tree)
	if err != nil {
		return nil, err
	}
	held, err := p.consumers(in, tree)
	if err != nil {
		return nil, err
	}
	if err := p.route(in.Providers, held); err != nil {
		return nil, err
	}
	return p, nil
}

// consumers names in p's scope document every declared consumer the run does
// not drop, and returns the languages each of them holds, in that order. It
// drops a consumer when every language it holds is left out, or when the run
// left a language out and the consumer holds none of p's languages.
func (p *Plan) consumers(in *Input, tree Tree) ([][]vocab.Language, error) {
	var held [][]vocab.Language
	for _, consumer := range in.Scope.Consumers {
		holds, err := consumerLanguages(consumer.Path, tree)
		if err != nil {
			return nil, err
		}
		kept := func(l vocab.Language) bool {
			return !slices.Contains(in.Omitted, l) && (len(in.Omitted) == 0 || slices.Contains(p.Languages, l))
		}
		if !slices.ContainsFunc(holds, kept) {
			p.Dropped = append(p.Dropped, Dropped{Path: consumer.Path, Languages: holds})
			continue
		}
		loaded := slices.DeleteFunc(slices.Clone(holds), func(l vocab.Language) bool { return !slices.Contains(p.Languages, l) })
		module, err := named(consumer, loaded, false, tree)
		if err != nil {
			return nil, err
		}
		p.Scope.Consumers = append(p.Scope.Consumers, module)
		held = append(held, holds)
	}
	return held, nil
}

// inScope is the configured language set, or the census of the target
// without the languages the run left out, none where it left out every one.
func inScope(in *Input, tree Tree) ([]vocab.Language, error) {
	if len(in.Languages) > 0 {
		var configured []vocab.Language
		rows := vocab.LanguageRows()
		for i := range rows {
			if slices.Contains(in.Languages, string(rows[i].Language)) {
				configured = append(configured, rows[i].Language)
			}
		}
		return configured, nil
	}
	found, err := tree.Languages(in.Scope.Target.Path)
	if err != nil {
		return nil, err
	}
	found = slices.DeleteFunc(slices.Clone(found), func(l vocab.Language) bool { return slices.Contains(in.Omitted, l) })
	if len(found) == 0 && len(in.Omitted) == 0 {
		return nil, noProgram(in.Scope.Target.Path)
	}
	return found, nil
}

// Quoted is languages quoted and joined by commas, as a line names them.
func Quoted(languages []vocab.Language) string {
	quoted := make([]string, len(languages))
	for i, language := range languages {
		quoted[i] = strconv.Quote(string(language))
	}
	return strings.Join(quoted, ", ")
}

// DroppedWith is the path of every dropped consumer holding one of
// languages, in [Plan.Dropped]'s order: the consumers a line naming those
// languages left out names with them.
func (p *Plan) DroppedWith(languages []vocab.Language) []string {
	var paths []string
	for _, d := range p.Dropped {
		if slices.ContainsFunc(languages, func(l vocab.Language) bool { return slices.Contains(d.Languages, l) }) {
			paths = append(paths, d.Path)
		}
	}
	return paths
}

func noProgram(root string) *failure.Error {
	return failure.Wrap(vocab.SetupNoProgram, fmt.Errorf("no file under %s puts a language in scope, so it holds no program", root))
}

// claimed refuses a provider list that leaves languages in scope with no
// analyzer; a language whose row is read in process needs none.
func claimed(entries []config.Provider, languages []vocab.Language) error {
	var unclaimed []vocab.Language
	for _, language := range languages {
		if row, _ := vocab.LanguageRowOf(language); row.InProcess() {
			continue
		}
		if !slices.ContainsFunc(entries, func(e config.Provider) bool { return slices.Contains(e.Languages, string(language)) }) {
			unclaimed = append(unclaimed, language)
		}
	}
	if len(unclaimed) == 0 {
		return nil
	}
	return &config.Error{Message: fmt.Sprintf("%s holds no entry claiming %s, in scope for this run: "+
		"add an entry claiming each, or leave them out of scope", vocab.KeyProvidersAnalyzers, Quoted(unclaimed))}
}

// readers is an entry for each in-process language in scope: its row's
// analyzer claiming it.
func (p *Plan) readers() []config.Provider {
	var held []config.Provider
	for _, language := range p.Languages {
		if row, _ := vocab.LanguageRowOf(language); row.InProcess() {
			held = append(held, config.Provider{Name: row.Analyzer, Languages: []string{string(language)}})
		}
	}
	return held
}

// consumerLanguages is every language a census finds in the consumer at
// path, which must be a directory holding one.
func consumerLanguages(path string, tree Tree) ([]vocab.Language, error) {
	languages, err := tree.Languages(path)
	switch {
	case err != nil:
		return nil, failure.New(vocab.SetupMissingConsumer, fmt.Sprintf(
			"the declared consumer %s cannot be read, and no finding is computed without it: %v; check it out at that path", path, err,
		))
	case len(languages) == 0:
		return nil, failure.New(vocab.SetupMissingConsumer, fmt.Sprintf(
			"the declared consumer %s holds no language in scope, and no finding is computed without it; check it out at that path", path,
		))
	}
	return languages, nil
}

// named is module with the name it is handed under: the declared one, else
// the one the identity source of the first row of a language it is loaded in
// names, else, for a target several languages or none load, [unnamed].
func named(module scope.Module, languages []vocab.Language, target bool, tree Tree) (scope.Module, error) {
	if module.ID != "" {
		return module, nil
	}
	rows := vocab.LanguageRows()
	for i := range rows {
		if !slices.Contains(languages, rows[i].Language) || rows[i].IdentitySource.File == "" {
			continue
		}
		name, found, err := tree.Name(module.Path, rows[i].IdentitySource)
		if err != nil {
			return scope.Module{}, err
		}
		if found {
			module.ID = name
			return module, nil
		}
	}
	if target && len(languages) != 1 {
		module.ID = unnamed
	}
	return module, nil
}

// route hands each provider entry claiming a language in scope the consumers
// holding one of its languages, held per consumer, plans the reader of each
// in-process language in scope over the target alone, and refuses a consumer
// no analyzer is handed. A process analyzer is asked for facts when an
// in-process language is in scope.
func (p *Plan) route(entries []config.Provider, held [][]vocab.Language) error {
	routed := make([]bool, len(held))
	readers := p.readers()
	for i, entry := range slices.Concat(entries, readers) {
		languages := slices.DeleteFunc(slices.Clone(p.Languages), func(l vocab.Language) bool { return !slices.Contains(entry.Languages, string(l)) })
		if len(languages) == 0 {
			continue
		}
		source := Source{Entry: entry, Index: i, Languages: languages, Facts: len(readers) > 0}
		if i >= len(entries) {
			own := p.Scope
			own.Consumers = nil
			source.Index, source.InProcess, source.Facts, source.Scope = -1, true, false, &own
			p.Sources = append(p.Sources, source)
			continue
		}
		var consumers []scope.Module
		for j := range held {
			if slices.ContainsFunc(held[j], func(l vocab.Language) bool { return slices.Contains(languages, l) }) {
				consumers = append(consumers, p.Scope.Consumers[j])
				routed[j] = true
			}
		}
		if len(consumers) < len(p.Scope.Consumers) {
			own := p.Scope
			own.Consumers = consumers
			source.Scope = &own
		}
		p.Sources = append(p.Sources, source)
	}
	return p.unclaimed(held, routed)
}

// unclaimed refuses each consumer route handed no source, once per path,
// since two scope entries may resolve to one directory.
func (p *Plan) unclaimed(held [][]vocab.Language, routed []bool) error {
	var refusals []error
	refused := map[string]bool{}
	for j := range held {
		if path := p.Scope.Consumers[j].Path; !routed[j] && !refused[path] {
			refused[path] = true
			refusals = append(refusals, &UnclaimedError{Path: path, Languages: held[j]})
		}
	}
	return errors.Join(refusals...)
}
