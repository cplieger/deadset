// Package gomod reads the path a Go module publishes itself under from the
// go.mod file that governs a directory, the way the go command finds and reads
// that file, and reads nothing else of it.
package gomod

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// fileName is the name of the file that makes a directory a module root.
const fileName = "go.mod"

// keyword opens the module directive.
const keyword = "module"

// Path is the module path of the Go module holding dir: the module directive
// of the go.mod file in dir, or else in the nearest directory above it, which
// is the file the go command takes as the main module's for a build in dir.
// found is false where no directory up to the filesystem root holds one.
//
// A go.mod file that cannot be read, or whose module directive is absent or
// holds no module path the go.mod grammar admits, is an error naming the file.
func Path(dir string) (path string, found bool, err error) {
	file, found, err := governing(dir)
	if err != nil || !found {
		return "", found, err
	}
	path, err = read(file)
	if err != nil {
		return "", false, fmt.Errorf("gomod: %s: %w", file, err)
	}
	return path, true, nil
}

// governing is the go.mod file in dir or the nearest directory above it.
func governing(dir string) (file string, found bool, err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", false, fmt.Errorf("gomod: %w", err)
	}
	for {
		file = filepath.Join(dir, fileName)
		info, err := os.Stat(file)
		switch {
		case err == nil && !info.IsDir():
			return file, true, nil
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return "", false, fmt.Errorf("gomod: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}

// read is the module path the module directive of the go.mod file at path
// declares, in either of the forms the grammar admits: the path on the
// directive's own line, or alone on a line inside parentheses.
func read(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	lines := bufio.NewScanner(f)
	inBlock := false
	for lines.Scan() {
		line := withoutComment(lines.Text())
		if inBlock {
			if line == "" {
				continue
			}
			return modulePath(line)
		}
		rest, isDirective := directive(line)
		if !isDirective {
			continue
		}
		if rest == "(" {
			inBlock = true
			continue
		}
		return modulePath(rest)
	}
	if err := lines.Err(); err != nil {
		return "", err
	}
	return "", errors.New("the file holds no module directive")
}

// withoutComment is line with any comment removed and its surrounding white
// space trimmed. A comment runs from // to the end of the line, and a module
// path never holds //, so the first one opens the comment.
func withoutComment(line string) string {
	if before, _, isComment := strings.Cut(line, "//"); isComment {
		line = before
	}
	return strings.TrimSpace(line)
}

// directive reports whether line opens the module directive, and what follows
// the keyword on it.
func directive(line string) (string, bool) {
	rest, isDirective := strings.CutPrefix(line, keyword)
	if !isDirective || rest == "" || (rest[0] != ' ' && rest[0] != '\t' && rest[0] != '(') {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// modulePath is the module path token names: an identifier as it is written,
// or an interpreted or raw string literal unquoted.
func modulePath(token string) (string, error) {
	path := token
	if token[0] == '"' || token[0] == '`' {
		unquoted, err := strconv.Unquote(token)
		if err != nil {
			return "", fmt.Errorf("the module directive holds %s, which is not a string literal: %w", token, err)
		}
		path = unquoted
	}
	if path == "" || strings.ContainsAny(path, " \t()") {
		return "", fmt.Errorf("the module directive holds %q, which is not a module path", token)
	}
	return path, nil
}
