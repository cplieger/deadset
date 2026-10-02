package scope_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v4"
	"github.com/cplieger/deadset/internal/scope"
)

// scopeSchema is the Contract's schema of the scope document.
const scopeSchema = "contract/scope.schema.json"

// written writes body as a scope document named name in a fresh directory and
// returns its path.
func written(t *testing.T, name string, body []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", path, err)
	}
	return path
}

// published is one document the Contract publishes.
func published(t *testing.T, name string) []byte {
	t.Helper()

	body, err := fs.ReadFile(spec.Examples, name)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", name, err)
	}
	return body
}

// TestReadResolvesEveryPathAgainstTheDocumentsDirectory reads each scope
// document the Contract publishes as an example: a relative path names a
// directory beside the document, an absolute one is kept, and every id the
// document states is carried.
func TestReadResolvesEveryPathAgainstTheDocumentsDirectory(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		build func(dir string) *scope.Document
		name  string
	}{
		{
			name:  "target-alone.json",
			build: func(dir string) *scope.Document { return &scope.Document{Target: scope.Module{Path: dir}} },
		},
		{
			name: "target-and-consumers.json",
			build: func(dir string) *scope.Document {
				return &scope.Document{
					Workspace: filepath.Join(dir, "go.work"),
					Target:    scope.Module{ID: "example.com/app", Path: filepath.Join(dir, "app")},
					Consumers: []scope.Module{
						{ID: "example.com/consumer", Path: filepath.Join(dir, "consumer")},
						{Path: "/src/example.com/tool"},
					},
				}
			},
		},
	} {
		t.Run(strings.TrimSuffix(c.name, ".json"), func(t *testing.T) {
			t.Parallel()

			path := written(t, c.name, published(t, "examples/scope/"+c.name))
			got, err := scope.Read(path)
			if err != nil {
				t.Fatalf("Read(%s) = %v, want the published example read", c.name, err)
			}
			if want := c.build(filepath.Dir(path)); !reflect.DeepEqual(got, want) {
				t.Errorf("Read(%s) = %+v, want %+v", c.name, got, want)
			}
		})
	}
}

// TestReadRefusesEveryPublishedNegative reads every scope document the
// Contract publishes as one its schema refuses, and each is refused at the
// value the negatives index names: at that value itself, or at the member of
// the object it names that is absent or undeclared.
func TestReadRefusesEveryPublishedNegative(t *testing.T) {
	t.Parallel()

	var index struct {
		Negatives []struct {
			File         string `json:"file"`
			Schema       string `json:"schema"`
			Constraint   string `json:"constraint"`
			InstancePath string `json:"instance_path"`
		} `json:"negatives"`
	}
	if err := json.Unmarshal(published(t, "examples/negatives/index.json"), &index); err != nil {
		t.Fatalf("Setup: decode the negatives index: %v", err)
	}
	read := 0
	for _, negative := range index.Negatives {
		if negative.Schema != scopeSchema {
			continue
		}
		read++
		t.Run(strings.TrimSuffix(negative.File, ".json"), func(t *testing.T) {
			t.Parallel()

			path := written(t, negative.File, published(t, "examples/negatives/"+negative.File))
			_, err := scope.Read(path)
			at := path + ": " + negative.InstancePath
			if negative.Constraint == "required" || negative.Constraint == "additionalProperties" {
				at += "/"
			}
			if !errors.Is(err, scope.ErrDocument) || !strings.Contains(err.Error(), at) {
				t.Errorf("Read(%s) = %v, want an error satisfying errors.Is(err, ErrDocument) naming %q", negative.File, err, at)
			}
		})
	}
	if read == 0 {
		t.Errorf("the negatives index names no document of %s, want the reader held to at least one", scopeSchema)
	}
}

// TestReadRefusesWhatTheClosedDecodeRefuses pins the refusals of the closed
// decode no published negative carries, each at the JSON Pointer it names; at
// is the pointer and its separator, empty for the whole document.
func TestReadRefusesWhatTheClosedDecodeRefuses(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, body, at string
	}{
		{name: "member-named-twice", body: `{"target": {"path": "."}, "target": {"path": "app"}}`, at: "/target: "},
		{name: "path-named-twice", body: `{"target": {"path": ".", "path": "app"}}`, at: "/target/path: "},
		{name: "trailing-content", body: `{"target": {"path": "."}} {}`, at: ""},
		{name: "not-an-object", body: `["target"]`, at: ""},
		{name: "target-role-consumer", body: `{"target": {"role": "consumer", "path": "."}}`, at: "/target/role: "},
		{name: "target-path-absent", body: `{"target": {"id": "example.com/app"}}`, at: "/target/path: "},
		{name: "consumers-null", body: `{"target": {"path": "."}, "consumers": null}`, at: "/consumers: "},
		{name: "consumers-an-object", body: `{"target": {"path": "."}, "consumers": {"path": "consumer"}}`, at: "/consumers: "},
		{name: "consumer-path-null", body: `{"target": {"path": "."}, "consumers": [{"path": "one"}, {"path": null}]}`, at: "/consumers/1/path: "},
		{name: "consumer-undeclared-member", body: `{"target": {"path": "."}, "consumers": [{"path": "one", "url": "x"}]}`, at: "/consumers/0/url: "},
		{name: "workspace-empty", body: `{"target": {"path": "."}, "workspace": ""}`, at: "/workspace: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			path := written(t, "scope.json", []byte(c.body))
			_, err := scope.Read(path)
			if want := path + ": " + c.at; !errors.Is(err, scope.ErrDocument) || !strings.Contains(err.Error(), want) {
				t.Errorf("Read(%s) = %v, want an error satisfying errors.Is(err, ErrDocument) naming %q", c.body, err, want)
			}
		})
	}
}

// TestReadNamesAnAbsentDocument pins that a document that does not exist is an
// error a caller can tell from one the schema refuses.
func TestReadNamesAnAbsentDocument(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "scope.json")
	if _, err := scope.Read(path); !errors.Is(err, fs.ErrNotExist) || errors.Is(err, scope.ErrDocument) {
		t.Errorf("Read(%s) = %v, want an error satisfying errors.Is(err, fs.ErrNotExist) and not ErrDocument", path, err)
	}
}

// TestRootIsTheDeepestDirectoryHoldingEveryPath pins the directory a run over
// a scope is invoked from: the deepest one holding the target, every consumer
// and the workspace file, whichever of them sits highest.
func TestRootIsTheDeepestDirectoryHoldingEveryPath(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		document scope.Document
		name     string
		want     string
	}{
		{name: "target-alone", document: scope.Document{Target: scope.Module{Path: "/src/lib"}}, want: "/src/lib"},
		{
			name:     "consumer-beneath-the-target",
			document: scope.Document{Target: scope.Module{Path: "/src/lib"}, Consumers: []scope.Module{{Path: "/src/lib/examples/app"}}},
			want:     "/src/lib",
		},
		{
			name:     "sibling-consumer",
			document: scope.Document{Target: scope.Module{Path: "/src/lib"}, Consumers: []scope.Module{{Path: "/src/library"}}},
			want:     "/src",
		},
		{
			name: "consumers-apart",
			document: scope.Document{
				Target:    scope.Module{Path: "/src/a/lib"},
				Consumers: []scope.Module{{Path: "/src/a/app"}, {Path: "/work/tool"}},
			},
			want: "/",
		},
		{
			name: "workspace-above-every-module",
			document: scope.Document{
				Workspace: "/src/go.work",
				Target:    scope.Module{Path: "/src/a/lib"},
				Consumers: []scope.Module{{Path: "/src/a/app"}},
			},
			want: "/src",
		},
		{
			name:     "workspace-in-the-target",
			document: scope.Document{Workspace: "/src/lib/go.work", Target: scope.Module{Path: "/src/lib"}},
			want:     "/src/lib",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.document.Root(); got != c.want {
				t.Errorf("(%+v).Root() = %q, want %q", c.document, got, c.want)
			}
		})
	}
}
