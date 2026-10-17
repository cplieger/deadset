package documents_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/documents"
)

// Each shared document is refused, naming it, when it is not strict JSON or
// holds a value outside its form, an undeclared member of an object before a
// fault inside it, and a tree holding well-formed documents of every scheme,
// or none, is accepted.
func TestCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		files   map[string]string
		name    string
		refuses string
	}{
		{name: "none"},
		{name: "well-formed", files: map[string]string{
			"deadset-ignore.json":   `{"ignore": [{"code": "DS1001", "symbol": "js://x/a.js#A", "path": "a.js", "reason": "r"}]}`,
			"deadset-baseline.json": `{"description": "d", "baseline": []}`,
			"deadset-edges.json":    `{"edges": {"w/a": {"because": "b", "provides": "go://x#A", "used_by": "py://x#A"}}}`,
		}},
		{name: "ignore-not-json", files: map[string]string{"deadset-ignore.json": `{"ignore": []} {}`}, refuses: "deadset-ignore.json"},
		{name: "baseline-closed-keys", files: map[string]string{"deadset-baseline.json": `{"baseline": [], "rows": []}`}, refuses: "deadset-baseline.json"},
		{name: "edges-member-twice", files: map[string]string{"deadset-edges.json": `{"edges": {}, "edges": {}}`}, refuses: "deadset-edges.json"},
		{name: "edges-identifier-twice", files: map[string]string{"deadset-edges.json": `{"edges": {` +
			`"w/a": {"provides": "go://x#A", "used_by": "ts://x#A"}, "w/a": {"provides": "go://x#B", "used_by": "ts://x#B"}}}`}, refuses: "deadset-edges.json"},
		{name: "edges-identifier-form", files: map[string]string{"deadset-edges.json": `{"edges": {"a b": {"provides": "go://x#A", "used_by": "ts://x#A"}}}`}, refuses: `"a b"`},
		{name: "edges-in-a-list", files: map[string]string{"deadset-edges.json": `{"edges": [{"id": "w/a", "provides": "go://x#A", "used_by": "ts://x#A"}]}`}, refuses: "/edges"},
		{name: "edges-undeclared-member-first", files: map[string]string{"deadset-edges.json": `{"edges": {"a b": {"provides": "go://x#A", "used_by": "ts://x#A"}}, "extra": 1}`}, refuses: `"extra"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for name, body := range c.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
					t.Fatalf("Setup: write %s: %v", name, err)
				}
			}
			err := documents.Check(t.Context(), root)
			_, refused := errors.AsType[*documents.Error](err)
			switch {
			case c.refuses == "" && err != nil:
				t.Errorf("Check(%s) = %v, want nil", c.name, err)
			case c.refuses != "" && (!refused || !strings.Contains(err.Error(), c.refuses)):
				t.Errorf("Check(%s) = %v, want a *documents.Error naming %s", c.name, err, c.refuses)
			}
		})
	}
}

// A shared document is read whole up to the bound and refused above it, and
// one that is not a regular file inside the target root is a failure of the
// run with nothing read from it, whatever the file it leads to holds.
func TestCheckReadsADocumentInsideTheTargetUpToTheBound(t *testing.T) {
	t.Parallel()

	baseline := []byte(`{"baseline": []}`)
	padded := func(size int) []byte {
		return append(bytes.Clone(baseline[:len(baseline)-1]), append(bytes.Repeat([]byte(" "), size-len(baseline)), '}')...)
	}
	cases := []struct {
		setup   func(t *testing.T, root string)
		name    string
		refuses bool
		fails   string
	}{
		{name: "at-the-bound", setup: func(t *testing.T, root string) { write(t, root, padded(vocab.DocumentMaxBytes)) }},
		{name: "above-the-bound", refuses: true, setup: func(t *testing.T, root string) { write(t, root, padded(vocab.DocumentMaxBytes+1)) }},
		{name: "symbolic-link-out-of-the-target", fails: "deadset-baseline.json", setup: func(t *testing.T, root string) {
			outside := filepath.Join(t.TempDir(), "baseline.json")
			if err := os.WriteFile(outside, []byte(`{"baseline": [], "rows": []}`), 0o600); err != nil {
				t.Fatalf("Setup: %v", err)
			}
			link(t, outside, filepath.Join(root, "deadset-baseline.json"))
		}},
		{name: "symbolic-link-to-an-endless-device", fails: "deadset-baseline.json", setup: func(t *testing.T, root string) {
			link(t, "/dev/zero", filepath.Join(root, "deadset-baseline.json"))
		}},
		{name: "named-pipe", fails: "not a regular file", setup: func(t *testing.T, root string) {
			if err := syscall.Mkfifo(filepath.Join(root, "deadset-baseline.json"), 0o600); err != nil {
				t.Fatalf("Setup: %v", err)
			}
		}},
		{name: "symbolic-link-inside-the-target", setup: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "recorded.json"), baseline, 0o600); err != nil {
				t.Fatalf("Setup: %v", err)
			}
			link(t, "recorded.json", filepath.Join(root, "deadset-baseline.json"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			c.setup(t, root)
			err := documents.Check(t.Context(), root)
			_, refused := errors.AsType[*documents.Error](err)
			switch {
			case c.refuses:
				if !refused || !strings.Contains(err.Error(), "deadset-baseline.json") || !strings.Contains(err.Error(), "larger than") {
					t.Errorf("Check(%s) = %v, want a *documents.Error naming deadset-baseline.json as too large", c.name, err)
				}
			case c.fails != "":
				if err == nil || refused || !strings.Contains(err.Error(), c.fails) {
					t.Errorf("Check(%s) = %v, want a failure naming %q that is no refusal", c.name, err, c.fails)
				}
			case err != nil:
				t.Errorf("Check(%s) = %v, want nil", c.name, err)
			}
		})
	}
}

// write writes the baseline at root.
func write(t *testing.T, root string, data []byte) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, "deadset-baseline.json"), data, 0o600); err != nil {
		t.Fatalf("Setup: %v", err)
	}
}

// link makes name a symbolic link to target.
func link(t *testing.T, target, name string) {
	t.Helper()

	if err := os.Symlink(target, name); err != nil {
		t.Fatalf("Setup: %v", err)
	}
}
