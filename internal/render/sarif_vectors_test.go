package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"reflect"
	"strconv"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v7"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/verdict"
)

const sarifVectors = "vectors/sarif"

// sarifCase is one case directory of vectors/sarif, read: the report, the
// input reports of a merged one, the target files, and either the expected
// document or the exit code the rendering ends with.
type sarifCase struct {
	report   *report.Report
	files    map[string]string
	inputs   []*report.Report
	expected []byte
	exit     int
}

// Every published SARIF case renders as its expected document, compared as
// decoded values, or fails with the exit code it states.
func TestSARIFReproducesEveryPublishedVector(t *testing.T) {
	t.Parallel()

	names := vectorCaseNames(t, sarifVectors)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := readSARIFCase(t, name)
			var out bytes.Buffer
			err := SARIF(&out, c.report, &Sources{Read: c.read, Inputs: c.inputs})
			if c.expected == nil {
				if err == nil || verdict.ForError(err) != c.exit || out.Len() != 0 {
					t.Errorf("SARIF(%s) = %v (exit %d) and wrote %d bytes, want a refusal ending the run with %d and nothing written",
						name, err, verdict.ForError(err), out.Len(), c.exit)
				}
				return
			}
			if err != nil {
				t.Fatalf("SARIF(%s) = %v, want the document", name, err)
			}
			if got, want := decodedValue(t, out.Bytes()), decodedValue(t, c.expected); !reflect.DeepEqual(got, want) {
				t.Errorf("SARIF(%s) =\n%s\nwant the value of expected.json\n%s", name, out.Bytes(), c.expected)
			}
		})
	}
	var merged int
	for _, name := range names {
		if _, err := fs.Stat(spec.Vectors, path.Join(sarifVectors, name, "inputs")); err == nil {
			merged++
		}
	}
	if merged == 0 {
		t.Errorf("vectors/sarif holds no merged case, want the merged rendering run as well")
	}
}

// read returns the content sources.json gives a target-relative path.
func (c *sarifCase) read(file string) ([]byte, error) {
	content, held := c.files[file]
	if !held {
		return nil, fmt.Errorf("%s: %w", file, fs.ErrNotExist)
	}
	return []byte(content), nil
}

// readSARIFCase reads the case directory named name.
func readSARIFCase(t *testing.T, name string) *sarifCase {
	t.Helper()

	dir := path.Join(sarifVectors, name)
	c := &sarifCase{report: decodeVector(t, path.Join(dir, "report.json"))}
	var sources struct {
		Files       map[string]string `json:"files"`
		Description string            `json:"description"`
	}
	if err := json.Unmarshal(readSpecVector(t, path.Join(dir, "sources.json")), &sources); err != nil {
		t.Fatalf("Setup: decode %s/sources.json: %v", dir, err)
	}
	c.files = sources.Files
	inputs, err := fs.Glob(spec.Vectors, path.Join(dir, "inputs", "*.json"))
	if err != nil {
		t.Fatalf("Setup: glob %s/inputs: %v", dir, err)
	}
	for _, input := range inputs {
		c.inputs = append(c.inputs, decodeVector(t, input))
	}
	if expected, err := fs.ReadFile(spec.Vectors, path.Join(dir, "expected.json")); err == nil {
		c.expected = expected
		return c
	}
	exit, err := strconv.Atoi(strings.TrimSpace(string(readSpecVector(t, path.Join(dir, "expected_exit")))))
	if err != nil {
		t.Fatalf("Setup: %s/expected_exit: %v", dir, err)
	}
	c.exit = exit
	return c
}

// vectorCaseNames is the name of every case directory under dir.
func vectorCaseNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := fs.ReadDir(spec.Vectors, dir)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		t.Fatalf("Setup: %s holds no case", dir)
	}
	return names
}

// readSpecVector is the bytes of one file of the published vectors.
func readSpecVector(t *testing.T, file string) []byte {
	t.Helper()

	body, err := spec.Vectors.ReadFile(file)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", file, err)
	}
	return body
}

// decodedValue is one JSON document as a value, its numbers kept as written,
// so two documents compare equal exactly when they decode alike.
func decodedValue(t *testing.T, document []byte) any {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode the document: %v\n%s", err, document)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("the document holds more than one value: %v", err)
	}
	return value
}
