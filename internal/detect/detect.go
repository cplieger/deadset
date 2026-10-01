// Package detect decides which languages a target holds, from the names of its
// files alone.
//
// A language is in scope when the tree holds one of its marker files, or one of
// its source extensions together with any companion file the language requires.
// No file's contents are read, so text that merely looks like a language, in a
// string literal, a document or an archive, never changes the answer, and one
// walk over the same tree always gives the same answer.
package detect

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// ErrNoLanguage reports a census that found no language in scope, which leaves
// nothing to analyze.
var ErrNoLanguage = errors.New("detect: no language in scope")

// ErrFilter reports a path filter that is not a relative path inside the target.
var ErrFilter = errors.New("detect: path filter is not a relative path inside the target")

// Options are the inputs to a detection besides the tree itself.
type Options struct {
	// Languages is the configured language set. A non-empty set is the answer
	// and the tree is not censused; an empty set means detect.
	Languages []string

	// Filters are the path filters, slash-separated and relative to the target
	// root, each naming a directory or a file. Only what lies under a filter is
	// censused, and an empty list censuses the whole tree. A directory a filter
	// names is censused even where its name would be skipped below it.
	Filters []string
}

// row maps one language to the file names that put it in scope. A pattern is
// matched against a file's base name with [path.Match].
type row struct {
	language   string
	markers    []string // any one puts the language in scope
	extensions []string // source files, in scope alone unless companions is set
	companions []string // one must also be present for a source file to count
}

// table is every language detection recognizes.
var table = []row{
	{
		language:   "go",
		markers:    []string{"go.mod"},
		extensions: []string{".go"},
	},
	{
		language:   "ts",
		markers:    []string{"tsconfig*.json"},
		extensions: []string{".ts", ".tsx", ".mts", ".cts"},
		companions: []string{"package.json"},
	},
}

// unscanned names the directories whose contents are never a target's own
// sources in any language: installed packages, vendored dependencies and test
// inputs. A directory whose name opens with a full stop, which holds version
// control or tool state, is skipped as well.
var unscanned = []string{"node_modules", "testdata", "vendor"}

// Languages returns the languages in scope for the target at root, in ascending
// order and each once.
//
// The target and every path filter are checked first: a filter that is not a
// relative path inside the target is an [ErrFilter], and a target or filter
// naming nothing is an error matching [fs.ErrNotExist]. The configured set, when
// there is one, is then the answer. Otherwise the census walks the target, or
// each filter, once, and a census that finds no language is an [ErrNoLanguage].
func Languages(root string, opts Options) ([]string, error) {
	starts, err := walkStarts(root, opts.Filters)
	if err != nil {
		return nil, err
	}
	if len(opts.Languages) > 0 {
		return slices.Compact(slices.Sorted(slices.Values(opts.Languages))), nil
	}

	seen := make(census, len(table))
	for _, start := range starts {
		if err := seen.walk(start); err != nil {
			return nil, fmt.Errorf("detect: census %s: %w", start, err)
		}
	}
	found := seen.inScope()
	if len(found) == 0 {
		return nil, fmt.Errorf("%w: no file under %s puts a language in scope", ErrNoLanguage, strings.Join(starts, ", "))
	}
	slices.Sort(found)
	return found, nil
}

// walkStarts resolves the directories and files the census walks: the target
// root, or every path filter under it, each with any symbolic link resolved so
// that a target reached through a link is walked rather than reported as the
// link itself.
func walkStarts(root string, filters []string) ([]string, error) {
	if len(filters) == 0 {
		start, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, fmt.Errorf("detect: target: %w", err)
		}
		return []string{start}, nil
	}
	starts := make([]string, 0, len(filters))
	for _, filter := range filters {
		local := filepath.FromSlash(filter)
		if !filepath.IsLocal(local) {
			return nil, fmt.Errorf("%w: %q", ErrFilter, filter)
		}
		start, err := filepath.EvalSymlinks(filepath.Join(root, local))
		if err != nil {
			return nil, fmt.Errorf("detect: path filter %q: %w", filter, err)
		}
		starts = append(starts, start)
	}
	return starts, nil
}

// sighting is what one detection has seen of one row's files.
type sighting struct {
	marker, source, companion bool
}

// census holds one sighting per row of the table, in table order.
type census []sighting

// walk records the name of every file under start, skipping the directories
// [unscanned] names and those whose name opens with a full stop, and stops once
// every language is in scope.
func (c census) walk(start string) error {
	if c.complete() {
		return nil
	}
	return filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != start && skipped(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		c.record(d.Name())
		if c.complete() {
			return fs.SkipAll
		}
		return nil
	})
}

// skipped reports whether the census leaves a directory of that name unread.
func skipped(name string) bool {
	return strings.HasPrefix(name, ".") || slices.Contains(unscanned, name)
}

// record notes what one file name is to every row.
func (c census) record(name string) {
	ext := path.Ext(name)
	for i := range table {
		r, s := &table[i], &c[i]
		s.marker = s.marker || matchesAny(r.markers, name)
		s.source = s.source || slices.Contains(r.extensions, ext)
		s.companion = s.companion || matchesAny(r.companions, name)
	}
}

// complete reports whether every language is already in scope, after which no
// further name can change the answer.
func (c census) complete() bool {
	for i := range table {
		if !table[i].inScope(c[i]) {
			return false
		}
	}
	return true
}

// inScope returns the languages the sightings put in scope.
func (c census) inScope() []string {
	var found []string
	for i := range table {
		if table[i].inScope(c[i]) {
			found = append(found, table[i].language)
		}
	}
	return found
}

// inScope reports whether the sightings put this row's language in scope.
func (r *row) inScope(s sighting) bool {
	return s.marker || (s.source && (len(r.companions) == 0 || s.companion))
}

// matchesAny reports whether a file name matches one of the patterns. A pattern
// path.Match cannot parse matches nothing.
func matchesAny(patterns []string, name string) bool {
	return slices.ContainsFunc(patterns, func(pattern string) bool {
		matched, err := path.Match(pattern, name)
		return err == nil && matched
	})
}
