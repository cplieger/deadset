package plan_test

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/failure"
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
	if got.Dir != "/w" || !slices.Equal(got.Languages, both) || !got.Detected || len(got.Sources) != 2 {
		t.Fatalf("Make() = %+v, want the directory /w, both languages detected and two sources", got)
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
// a target several languages load that no source names is ".". A language the
// run left out loads no module, so it names none.
func TestMakeNamesEveryModule(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		edit      func(*tree)
		scope     *scope.Document
		name      string
		languages []string
		omitted   []vocab.Language
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
		{
			name: "no-go-module-both-loaded", edit: func(t *tree) { delete(t.names, "/w/app/go.mod"); delete(t.names, "/w/both/go.mod") },
			scope: declared("both"), target: "@example/app", ids: []string{"@example/both"},
		},
		{
			name: "no-go-module-typescript-left-out", omitted: []vocab.Language{vocab.TS},
			edit:  func(t *tree) { delete(t.names, "/w/app/go.mod"); delete(t.names, "/w/both/go.mod") },
			scope: declared("both"), ids: []string{""},
		},
		{
			name: "no-name-typescript-left-out", omitted: []vocab.Language{vocab.TS},
			edit:  func(t *tree) { delete(t.names, "/w/app/go.mod"); delete(t.names, "/w/app/package.json") },
			scope: declared(),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			tr := workspace()
			if c.edit != nil {
				c.edit(tr)
			}
			got, err := plan.Make(&plan.Input{Scope: c.scope, Languages: c.languages, Omitted: c.omitted, Providers: []config.Provider{goEntry, tsEntry}}, tr)
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

// A declared consumer every language of which the run left out is dropped
// with them, and named in no scope document; a consumer holding a language in
// scope beside one left out is kept.
func TestMakeDropsAConsumerOfOnlyALanguageLeftOut(t *testing.T) {
	t.Parallel()

	got, err := plan.Make(&plan.Input{Scope: declared("go", "ts", "both"), Omitted: []vocab.Language{vocab.TS}, Providers: []config.Provider{goEntry, tsEntry}}, workspace())
	if err != nil {
		t.Fatalf("Make() = %v", err)
	}
	var kept []string
	for _, consumer := range got.Scope.Consumers {
		kept = append(kept, consumer.Path)
	}
	if !slices.Equal(kept, []string{"/w/go", "/w/both"}) || len(got.Dropped) != 1 || got.Dropped[0].Path != "/w/ts" || !slices.Equal(got.Dropped[0].Languages, []vocab.Language{vocab.TS}) {
		t.Errorf("Make() keeps %q and drops %+v, want /w/go and /w/both kept and /w/ts dropped with ts", kept, got.Dropped)
	}
	if len(got.Sources) != 1 || got.Sources[0].Scope != nil {
		t.Errorf("Make() = %+v, want the Go analyzer alone, reading the run's scope", got.Sources)
	}
}

// A declared consumer holding a language left out beside one neither left out
// nor in scope is not dropped: no analyzer the run keeps claims a language of
// it, so the run refuses it.
func TestMakeRefusesAConsumerOfALeftOutLanguageAndOneOutOfScope(t *testing.T) {
	t.Parallel()

	census := workspace()
	census.languages["/w/schema"] = []vocab.Language{vocab.TS, vocab.Proto}
	got, err := plan.Make(&plan.Input{Scope: declared("go", "schema"), Omitted: []vocab.Language{vocab.TS}, Providers: []config.Provider{goEntry, tsEntry}}, census)
	refused, ok := errors.AsType[*plan.UnclaimedError](err)
	if !ok || refused.Path != "/w/schema" || !slices.Equal(refused.Languages, []vocab.Language{vocab.TS, vocab.Proto}) {
		t.Errorf("Make() = %+v, %v, want /w/schema refused as holding ts and proto, no analyzer of the run claiming either", got, err)
	}
}

// countingTree counts the questions asked of the tree it reads.
type countingTree struct {
	tree             plan.Tree
	languages, names int
}

func (c *countingTree) Languages(dir string) ([]vocab.Language, error) {
	c.languages++
	return c.tree.Languages(dir)
}

func (c *countingTree) Name(dir string, source vocab.IdentitySource) (string, bool, error) {
	c.names++
	return c.tree.Name(dir, source)
}

// A Census asks its tree each question once, a failed read included, and
// answers it again as the tree did.
func TestCensusAsksEachQuestionOnce(t *testing.T) {
	t.Parallel()

	counted := &countingTree{tree: workspace()}
	census := plan.NewCensus(counted)
	goMod := vocab.LanguageRows()[0].IdentitySource
	for range 2 {
		if languages, err := census.Languages("/w/app"); err != nil || !slices.Equal(languages, both) {
			t.Errorf("Languages(/w/app) = %v, %v, want go and ts", languages, err)
		}
		if _, err := census.Languages("/w/gone"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Languages(/w/gone) = %v, want fs.ErrNotExist", err)
		}
		if name, found, err := census.Name("/w/app", goMod); err != nil || !found || name != "example.com/app" {
			t.Errorf("Name(/w/app, %+v) = %q, %t, %v, want example.com/app", goMod, name, found, err)
		}
	}
	if counted.languages != 2 || counted.names != 1 {
		t.Errorf("the tree was asked %d censuses and %d names, want 2 and 1", counted.languages, counted.names)
	}
}

// A configured language set replaces the census, in the rows' order, and the
// plan records that no census decided it.
func TestMakeTakesTheConfiguredLanguages(t *testing.T) {
	t.Parallel()

	got, err := plan.Make(&plan.Input{Scope: declared(), Languages: []string{"ts", "go"}, Providers: []config.Provider{goEntry, tsEntry}},
		&tree{languages: map[string][]vocab.Language{}})
	if err != nil || !slices.Equal(got.Languages, both) || got.Detected {
		t.Errorf("Make(languages ts, go over an empty tree) = %+v, %v, want go and ts, not detected", got, err)
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
		held, ok := errors.AsType[*failure.Error](err)
		return ok && len(held.Failures) == 1 && held.Failures[0].Class == class
	}
}

// Every identity source a language row names has a reader on disk.
func TestDiskReadsEveryRowsIdentitySource(t *testing.T) {
	t.Parallel()

	for _, row := range vocab.LanguageRows() {
		if row.IdentitySource.File == "" {
			continue
		}
		if _, _, err := (plan.Disk{}).Name(t.TempDir(), row.IdentitySource); err != nil {
			t.Errorf("Disk.Name(%s's %+v) = %v, want a reader", row.Language, row.IdentitySource, err)
		}
	}
}
