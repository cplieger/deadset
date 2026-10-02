package render

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"testing"

	spec "github.com/cplieger/deadset-spec/v4"
	"github.com/cplieger/deadset/internal/report"
)

// mergedCase is one published merge case that produces a merged report: the
// report, and every input report it was merged from.
type mergedCase struct {
	merged *report.Report
	inputs []*report.Report
	name   string
}

// mergedCases is every published merge case that carries an expected merged
// report, in the order of their names.
func mergedCases(t *testing.T) []mergedCase {
	t.Helper()

	expected, err := fs.Glob(spec.Vectors, "vectors/merge/*/expected.json")
	if err != nil || len(expected) == 0 {
		t.Fatalf("Setup: glob the merge vectors = %v, %v", expected, err)
	}
	cases := make([]mergedCase, 0, len(expected))
	for _, file := range expected {
		dir := path.Dir(file)
		held := mergedCase{name: path.Base(dir), merged: decodeVector(t, file)}
		inputs, err := fs.Glob(spec.Vectors, dir+"/inputs/*.json")
		if err != nil || len(inputs) == 0 {
			t.Fatalf("Setup: glob the inputs of %s = %v, %v", dir, inputs, err)
		}
		for _, input := range inputs {
			held.inputs = append(held.inputs, decodeVector(t, input))
		}
		cases = append(cases, held)
	}
	return cases
}

// decodeVector decodes one report of the published vectors.
func decodeVector(t *testing.T, file string) *report.Report {
	t.Helper()

	body, err := spec.Vectors.ReadFile(file)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", file, err)
	}
	r, err := report.Decode(body)
	if err != nil {
		t.Fatalf("Setup: Decode(%s) = %v", file, err)
	}
	return r
}

// sourceLines is the number of lines every synthesized source file holds,
// more than any position of the published vectors names.
const sourceLines = 64

// synthesizedSource stands in for the target tree a merged vector report
// describes: every path holds sourceLines lines, each naming its path and its
// number, so two lines never hash alike.
func synthesizedSource(file string) ([]byte, error) {
	var text bytes.Buffer
	for line := range sourceLines {
		fmt.Fprintf(&text, "// %s line %d\n", file, line+1)
	}
	return text.Bytes(), nil
}

// compareGolden compares got with the golden file at testdata/<dir>/<name>,
// or writes it there when UPDATE_GOLDEN=1.
func compareGolden(t *testing.T, dir, name string, got []byte) {
	t.Helper()

	golden := filepath.Join("testdata", dir, name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(golden), err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", golden, err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s (run UPDATE_GOLDEN=1 go test ./internal/render/ to record it): %v", golden, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the rendering differs from %s:\n--- want\n%s\n+++ got\n%s\n(review the diff, then run UPDATE_GOLDEN=1 go test ./internal/render/ to record it)",
			golden, want, got)
	}
}
