package plan_test

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/scope"
)

// tree is a [plan.Tree] a test states: the languages a census finds per
// directory, the names identity sources declare per directory and file, and
// the directories a read of fails.
type tree struct {
	languages map[string][]vocab.Language
	names     map[string]string
	broken    map[string]error
}

func (t *tree) Languages(dir string) ([]vocab.Language, error) {
	if languages, ok := t.languages[dir]; ok {
		return languages, nil
	}
	return nil, fs.ErrNotExist
}

func (t *tree) Name(dir string, source vocab.IdentitySource) (string, bool, error) {
	key := dir + "/" + source.File
	if err := t.broken[key]; err != nil {
		return "", false, err
	}
	name, ok := t.names[key]
	return name, ok, nil
}

var (
	goEntry = config.Provider{Name: "deadset-go", Command: "deadset-go", Languages: []string{"go"}}
	tsEntry = config.Provider{Name: "deadset-ts", Command: "deadset-ts", Languages: []string{"ts"}}
	both    = []vocab.Language{vocab.Go, vocab.TS}
)

// workspace is a target holding both languages and three consumers beside
// it: a Go module, a TypeScript project and one holding both.
func workspace() *tree {
	return &tree{
		languages: map[string][]vocab.Language{
			"/w/app": both, "/w/go": {vocab.Go}, "/w/ts": {vocab.TS}, "/w/both": both, "/w/docs": nil,
		},
		names: map[string]string{
			"/w/app/go.mod": "example.com/app", "/w/app/package.json": "@example/app",
			"/w/go/go.mod": "example.com/go", "/w/ts/package.json": "@example/ts",
			"/w/both/go.mod": "example.com/both", "/w/both/package.json": "@example/both",
		},
	}
}

// declared is a declared scope of the target /w/app and the consumers named.
func declared(consumers ...string) *scope.Document {
	d := &scope.Document{Target: scope.Module{Path: "/w/app"}}
	for _, consumer := range consumers {
		d.Consumers = append(d.Consumers, scope.Module{Path: "/w/" + consumer})
	}
	return d
}

// Each declared consumer is handed to every analyzer claiming a language it
// holds and to no other; an analyzer handed only some of them reads a scope
// document of its own, and every analyzer runs in the deepest directory
// holding the target and every consumer.
func TestMakeRoutesEachConsumerToTheAnalyzersOfItsLanguages(t *testing.T) {
	t.Parallel()

	got, err := plan.Make(&plan.Input{Scope: declared("go", "ts", "both"), Providers: []config.Provider{goEntry, tsEntry}}, workspace())
	if err != nil {
		t.Fatalf("Make() = %v", err)
	}
	if got.Dir != "/w" || !slices.Equal(got.Languages, both) || len(got.Sources) != 2 {
		t.Fatalf("Make() = %+v, want the directory /w, both languages and two sources", got)
	}
	for i, want := range [][]string{{"/w/go", "/w/both"}, {"/w/ts", "/w/both"}} {
		source := &got.Sources[i]
		var handed []string
		for _, consumer := range source.Scope.Consumers {
			handed = append(handed, consumer.Path)
		}
		if source.Index != i || !slices.Equal(handed, want) {
			t.Errorf("source %d = %+v handed %q, want index %d handed %q", i, source, handed, i, want)
		}
	}
	if len(got.Scope.Consumers) != 3 {
		t.Errorf("the run's scope names %d consumers, want every declared one", len(got.Scope.Consumers))
	}

	whole, err := plan.Make(&plan.Input{Scope: declared("go"), Providers: []config.Provider{goEntry, tsEntry}}, workspace())
	if err != nil || whole.Sources[0].Scope != nil || whole.Sources[1].Scope == nil {
		t.Errorf("Make(one Go consumer) = %+v, %v, want the Go analyzer to read the run's scope and the TypeScript one its own", whole, err)
	}
}

// Every module is handed under the declared name, else the one the identity
// source of the first row of a language it is loaded in declares, every run;
// a target several languages load that no source names is ".".
func TestMakeNamesEveryModule(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		edit      func(*tree)
		scope     *scope.Document
		name      string
		languages []string
		target    string
		ids       []string
	}{
		{
			name: "both-languages", scope: declared("go", "ts", "both"), target: "example.com/app",
			ids: []string{"example.com/go", "@example/ts", "example.com/both"},
		},
		{
			name: "one-language", languages: []string{"go"}, scope: declared("go", "both"), target: "example.com/app",
			ids: []string{"example.com/go", "example.com/both"},
		},
		{name: "declared-names-kept", scope: &scope.Document{
			Target:    scope.Module{ID: "@example/app", Path: "/w/app"},
			Consumers: []scope.Module{{ID: "@example/both", Path: "/w/both"}},
		}, target: "@example/app", ids: []string{"@example/both"}},
		{name: "no-go-module", edit: func(t *tree) { delete(t.names, "/w/app/go.mod") }, scope: declared(), target: "@example/app"},
		{
			name: "no-name", edit: func(t *tree) { delete(t.names, "/w/app/go.mod"); delete(t.names, "/w/app/package.json") },
			scope: declared(), target: ".",
		},
		{name: "one-language-no-name", languages: []string{"go"}, edit: func(t *tree) { delete(t.names, "/w/app/go.mod") }, scope: declared()},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			tr := workspace()
			if c.edit != nil {
				c.edit(tr)
			}
			got, err := plan.Make(&plan.Input{Scope: c.scope, Languages: c.languages, Providers: []config.Provider{goEntry, tsEntry}}, tr)
			if err != nil {
				t.Fatalf("Make() = %v", err)
			}
			var ids []string
			for _, consumer := range got.Scope.Consumers {
				ids = append(ids, consumer.ID)
			}
			if got.Scope.Target.ID != c.target || !slices.Equal(ids, c.ids) {
				t.Errorf("Make() names the target %q and the consumers %q, want %q and %q", got.Scope.Target.ID, ids, c.target, c.ids)
			}
		})
	}
}

// A configured language set replaces the census, in the rows' order.
func TestMakeTakesTheConfiguredLanguages(t *testing.T) {
	t.Parallel()

	got, err := plan.Make(&plan.Input{Scope: declared(), Languages: []string{"ts", "go"}, Providers: []config.Provider{goEntry, tsEntry}},
		&tree{languages: map[string][]vocab.Language{}})
	if err != nil || !slices.Equal(got.Languages, both) {
		t.Errorf("Make(languages ts, go over an empty tree) = %+v, %v, want go and ts", got, err)
	}
}

// Every way a plan is refused, with what it names.
func TestMakeRefuses(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		check     func(error) bool
		edit      func(*tree)
		scope     *scope.Document
		name      string
		providers []config.Provider
		names     []string
	}{
		{
			name: "no-program", scope: declared(),
			edit:  func(t *tree) { t.languages["/w/app"] = nil },
			check: setupClass(vocab.SetupNoProgram),
			names: []string{"/w/app", "go reads one from go.mod or a .go file", "ts reads one from tsconfig*.json"},
		},
		{
			name: "unclaimed-language", scope: declared(), providers: []config.Provider{tsEntry},
			check: func(err error) bool { _, ok := errors.AsType[*config.Error](err); return ok },
			names: []string{"providers.analyzers", `"go"`},
		},
		{name: "consumer-absent", scope: declared("gone"), check: setupClass(vocab.SetupMissingConsumer), names: []string{"/w/gone", "cannot be read"}},
		{name: "consumer-of-no-language", scope: declared("docs"), check: setupClass(vocab.SetupMissingConsumer), names: []string{"/w/docs", "no language"}},
		{
			name: "consumer-no-analyzer-claims", scope: declared("go", "ts"), providers: []config.Provider{{Name: "both", Command: "x", Languages: []string{"go"}}},
			edit:  func(t *tree) { t.languages["/w/app"] = []vocab.Language{vocab.Go} },
			check: func(err error) bool { _, ok := errors.AsType[*plan.UnclaimedError](err); return ok },
			names: []string{"/w/ts holds ts", "no analyzer of the run claims"},
		},
		{
			name: "identity-unreadable", scope: declared(),
			edit:  func(t *tree) { t.broken = map[string]error{"/w/app/go.mod": errors.New("go.mod: no module directive")} },
			check: func(err error) bool { return err != nil },
			names: []string{"no module directive"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			tr := workspace()
			if c.edit != nil {
				c.edit(tr)
			}
			providers := c.providers
			if providers == nil {
				providers = []config.Provider{goEntry, tsEntry}
			}
			got, err := plan.Make(&plan.Input{Scope: c.scope, Providers: providers}, tr)
			if got != nil || !c.check(err) {
				t.Fatalf("Make() = %+v, %v (%T), want a refusal of its class", got, err, err)
			}
			for _, named := range c.names {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("Make() = %v, want it to name %q", err, named)
				}
			}
		})
	}
}

func setupClass(class string) func(error) bool {
	return func(err error) bool {
		failure, ok := errors.AsType[*plan.SetupError](err)
		return ok && failure.Class == class
	}
}

// Every identity source a language row names has a reader on disk.
func TestDiskReadsEveryRowsIdentitySource(t *testing.T) {
	t.Parallel()

	for _, row := range vocab.LanguageRows() {
		if _, _, err := (plan.Disk{}).Name(t.TempDir(), row.IdentitySource); err != nil {
			t.Errorf("Disk.Name(%s's %+v) = %v, want a reader", row.Language, row.IdentitySource, err)
		}
	}
}
