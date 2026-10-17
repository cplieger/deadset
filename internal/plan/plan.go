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

// unnamed is the name of a target several languages load that no identity
// source names, as languages.json states.
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

	// Index is the entry's place in the provider list.
	Index int
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
// directory or holds no language, is a setup [*failure.Error]; a language in scope no
// provider entry claims is a [*config.Error], before any consumer is read;
// consumers no analyzer claims are [*UnclaimedError]s, joined.
func Make(in *Input, tree Tree) (*Plan, error) {
	languages, err := inScope(in, tree)
	if err != nil {
		return nil, err
	}
	if refused := claimed(in.Providers, languages); refused != nil {
		return nil, refused
	}
	p := &Plan{Dir: in.Scope.Root(), Languages: languages, Scope: scope.Document{Workspace: in.Scope.Workspace}}
	p.Scope.Target, err = named(in.Scope.Target, languages, true, tree)
	if err != nil {
		return nil, err
	}
	held := make([][]vocab.Language, len(in.Scope.Consumers))
	for i, consumer := range in.Scope.Consumers {
		if held[i], err = consumerLanguages(consumer.Path, tree); err != nil {
			return nil, err
		}
		loaded := slices.DeleteFunc(slices.Clone(held[i]), func(l vocab.Language) bool { return !slices.Contains(languages, l) })
		module, err := named(consumer, loaded, false, tree)
		if err != nil {
			return nil, err
		}
		p.Scope.Consumers = append(p.Scope.Consumers, module)
	}
	if err := p.route(in.Providers, held); err != nil {
		return nil, err
	}
	return p, nil
}

// inScope is the configured language set, or the census of the target.
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
	if len(found) == 0 {
		return nil, noProgram(in.Scope.Target.Path)
	}
	return found, nil
}

func noProgram(root string) *failure.Error {
	return failure.Wrap(vocab.SetupNoProgram, fmt.Errorf("no file under %s puts a language in scope, so it holds no program", root))
}

// claimed refuses a provider list that leaves languages in scope with no
// analyzer.
func claimed(entries []config.Provider, languages []vocab.Language) error {
	var unclaimed []string
	for _, language := range languages {
		if !slices.ContainsFunc(entries, func(e config.Provider) bool { return slices.Contains(e.Languages, string(language)) }) {
			unclaimed = append(unclaimed, strconv.Quote(string(language)))
		}
	}
	if len(unclaimed) == 0 {
		return nil
	}
	return &config.Error{Message: fmt.Sprintf("%s holds no entry claiming %s, in scope for this run: "+
		"add an entry claiming each, or leave them out of scope", vocab.KeyProvidersAnalyzers, strings.Join(unclaimed, ", "))}
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
// names, else, for a target several languages load, [unnamed].
func named(module scope.Module, languages []vocab.Language, target bool, tree Tree) (scope.Module, error) {
	if module.ID != "" {
		return module, nil
	}
	rows := vocab.LanguageRows()
	for i := range rows {
		if !slices.Contains(languages, rows[i].Language) {
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
	if target && len(languages) > 1 {
		module.ID = unnamed
	}
	return module, nil
}

// route hands each provider entry claiming a language in scope the consumers
// holding one of its languages, held per consumer, and refuses a consumer no
// entry is handed.
func (p *Plan) route(entries []config.Provider, held [][]vocab.Language) error {
	routed := make([]bool, len(held))
	for i, entry := range entries {
		languages := slices.DeleteFunc(slices.Clone(p.Languages), func(l vocab.Language) bool { return !slices.Contains(entry.Languages, string(l)) })
		if len(languages) == 0 {
			continue
		}
		source := Source{Entry: entry, Index: i, Languages: languages}
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
	var unclaimed []error
	for j := range held {
		if !routed[j] {
			unclaimed = append(unclaimed, &UnclaimedError{Path: p.Scope.Consumers[j].Path, Languages: held[j]})
		}
	}
	return errors.Join(unclaimed...)
}
