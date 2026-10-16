// Package detect censuses a tree for the languages it holds, from the names of
// its files alone, by the rows of languages.json.
//
// A language is in scope when the tree holds one of its markers, or, where its
// row reads a program without one, one of its source extensions together with
// a companion the row names, if it names any. A directory a row skips hides
// its files from that row alone. No file's contents are read, so text that
// merely looks like a language, in a string literal, a document or an
// archive, never changes the answer, and one walk over the same tree always
// gives the same answer.
package detect

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// Languages returns the languages in scope for the directory at root, in the
// order of languages.json's rows, and none where no row's files are present.
// root is read whatever its own name, and through a symbolic link.
func Languages(root string) ([]vocab.Language, error) {
	start, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("detect: %w", err)
	}
	rows := vocab.LanguageRows()
	c := census{rows: rows, seen: make([]sighting, len(rows))}
	if err := c.walk(start); err != nil {
		return nil, fmt.Errorf("detect: census %s: %w", start, err)
	}
	var found []vocab.Language
	for i := range rows {
		if c.inScope(i) {
			found = append(found, rows[i].Language)
		}
	}
	return found, nil
}

// sighting is what one census has seen of one row's files.
type sighting struct {
	marker, source, companion bool
}

// census holds one sighting per row, in the rows' order.
type census struct {
	rows []vocab.LanguageRow
	seen []sighting
}

// walk records the name of every file below start for each row that does not
// skip a directory between, and stops once every language is in scope.
//
// reading holds, per directory walked, the rows that still read it; a
// directory every row skips is not entered.
func (c *census) walk(start string) error {
	every := make([]bool, len(c.rows))
	for i := range every {
		every[i] = true
	}
	reading := map[string][]bool{start: every}
	return filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == start {
			return err
		}
		parent := reading[filepath.Dir(p)]
		if d.IsDir() {
			held, read := c.entered(parent, d.Name())
			if !read {
				return fs.SkipDir
			}
			reading[p] = held
			return nil
		}
		c.record(parent, d.Name())
		if c.complete() {
			return fs.SkipAll
		}
		return nil
	})
}

// entered is, of the rows that read a directory's parent, those that read a
// directory of that name, and whether any does.
func (c *census) entered(parent []bool, name string) (held []bool, read bool) {
	held = make([]bool, len(c.rows))
	for i := range c.rows {
		held[i] = parent[i] && !c.rows[i].Skips(name)
		read = read || held[i]
	}
	return held, read
}

// record notes what one file name is to each row that reads its directory.
func (c *census) record(reading []bool, name string) {
	for i := range c.rows {
		if !reading[i] {
			continue
		}
		r, s := &c.rows[i], &c.seen[i]
		s.marker = s.marker || r.Marks(name)
		s.source = s.source || r.Sources(name)
		s.companion = s.companion || r.Accompanies(name)
	}
}

// complete reports whether every language is already in scope, after which no
// further name can change the answer.
func (c *census) complete() bool {
	for i := range c.rows {
		if !c.inScope(i) {
			return false
		}
	}
	return true
}

// inScope reports whether the sightings put one row's language in scope.
func (c *census) inScope(i int) bool {
	r, s := &c.rows[i], c.seen[i]
	return s.marker || (r.ProgramWithoutMarker && s.source && (len(r.Companions) == 0 || s.companion))
}
