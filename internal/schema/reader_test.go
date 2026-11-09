package schema

import (
	"bytes"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset-spec/v8/facts"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/merge"
)

func readTree(t *testing.T, files map[string]string, settings Settings) *Result {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if settings.MinConfidence == "" {
		settings.MinConfidence = vocab.Possible
	}
	result, err := Read(t.Context(), &Input{
		Target: dir, Version: "1.0.0", Settings: settings, Analyzed: []vocab.Language{vocab.Go, vocab.TS},
		Conformance: report.Conformance{CorpusVersion: "6.0.0", Result: report.ResultPass, Digest: "sha256:" + string(bytes.Repeat([]byte("0"), 64))},
	})
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	if err := report.Validate(result.Report); err != nil {
		t.Errorf("the report is invalid: %v", err)
	}
	return result
}

func record(t *testing.T, r *Result, ref string) facts.Binding {
	t.Helper()
	for _, b := range r.Facts.Bindings {
		if b.Symbol == ref {
			return b
		}
	}
	t.Fatalf("no record of %s", ref)
	return facts.Binding{}
}

// TestSuppressionFindings holds a directive bound to nothing to DS9003 and
// one with no reason to DS9001, each at its own site.
func TestSuppressionFindings(t *testing.T) {
	r := readTree(t, map[string]string{"v1/a.proto": "syntax = \"proto3\";\npackage v1;\n\n// deadset:ignore DS1802 -- bound to nothing\n\nmessage A {\n  int32 n = 1; // deadset:ignore DS1802\n}\n"}, Settings{})
	codes := map[vocab.Code]int{}
	for _, f := range r.Report.Findings {
		codes[f.Code] = f.Position.Line
	}
	for _, s := range r.Report.StaleSuppressions {
		codes[s.Code] = s.Position.Line
	}
	if codes[vocab.StaleSuppression] != 4 || codes[vocab.SuppressionWithoutReason] != 7 {
		t.Errorf("suppression findings by code and line = %v, want DS9003 at 4 and DS9001 at 7", codes)
	}
	if got := record(t, r, "proto://v1#A.n"); got.State != facts.StateDead {
		t.Errorf("A.n = %s, want dead: a directive with no reason binds nothing", got.State)
	}
}

// TestSuppressionUnderAnotherCode holds a directive naming a code its
// declaration does not report to suppression.md "Staleness": its mark keeps
// the declaration live, and its stale finding names the code the declaration
// reports. A directive of a code about no declaration marks nothing.
func TestSuppressionUnderAnotherCode(t *testing.T) {
	r := readTree(t, map[string]string{"v1/a.proto": "syntax = \"proto3\";\npackage v1;\n\nmessage A {\n  // deadset:ignore DS1801 -- wanted\n  int32 n = 1;\n  // deadset:ignore DS1601 -- wanted\n  int32 m = 2;\n}\n"}, Settings{})
	if len(r.Report.StaleSuppressions) != 2 {
		t.Fatalf("stale suppressions = %+v, want the two directives", r.Report.StaleSuppressions)
	}
	for i, want := range []string{
		"inline directive for DS1801 matches no current finding, and its declaration reports DS1802",
		"inline directive for DS1601 matches no current finding, and its declaration reports DS1802",
	} {
		if got := r.Report.StaleSuppressions[i].Message; got != want {
			t.Errorf("stale suppression %d = %q, want %q", i, got, want)
		}
	}
	if got := record(t, r, "proto://v1#A.n"); got.State != facts.StateLive {
		t.Errorf("A.n = %s, want live: a record of a declaration kind marks its declaration", got.State)
	}
	if got := record(t, r, "proto://v1#A.m"); got.State != facts.StateDead {
		t.Errorf("A.m = %s, want dead: a record of a file kind marks nothing", got.State)
	}
}

// TestBufModuleRootsNameTheSchemaFile holds a version 2 buf.yaml's module path
// to the import path a record's schema_file names.
func TestBufModuleRootsNameTheSchemaFile(t *testing.T) {
	r := readTree(t, map[string]string{
		"buf.yaml":         "version: v2\nmodules:\n  - path: proto\n",
		"proto/v1/a.proto": "syntax = \"proto3\";\npackage v1;\nimport \"v1/b.proto\";\nmessage A { B b = 1; }\n",
		"proto/v1/b.proto": "syntax = \"proto3\";\npackage v1;\nmessage B {}\n",
	}, Settings{})
	if got := record(t, r, "proto://v1#A.b"); got.SchemaFile != "v1/a.proto" || len(got.Uses) != 1 || got.Uses[0] != "proto://v1#B" {
		t.Errorf("A.b = %+v, want schema_file v1/a.proto using proto://v1#B", got)
	}
	if len(r.Report.Notes) != 0 {
		t.Errorf("notes = %+v, want none: the module root resolves the import", r.Report.Notes)
	}
}

// TestBufModuleFileSets holds the reader to the files each buf.yaml module
// builds: a schema file every module whose directory holds it leaves out
// publishes nothing, shadows nothing and resolves no import, and one no
// module's directory holds is read.
func TestBufModuleFileSets(t *testing.T) {
	message := func(pkg, name string) string {
		return "syntax = \"proto3\";\npackage " + pkg + ";\nmessage " + name + " {}\n"
	}
	cases := []struct {
		name      string
		files     map[string]string
		published []string
		dropped   []string
		notes     map[string]string // where set, note kind by path, every note of the report
	}{
		{
			name: "v1beta1 build excludes beside its roots",
			files: map[string]string{
				"buf.yaml":            "version: v1beta1\nbuild:\n  roots:\n    - proto\n  excludes:\n    - proto/tests\n",
				"proto/v1/a.proto":    message("v1", "A"),
				"proto/tests/t.proto": message("tests", "T"),
			},
			published: []string{"proto://v1#A"},
			dropped:   []string{"proto://tests#T"},
		},
		{
			name: "a file below no module directory",
			files: map[string]string{
				"buf.yaml":         "version: v2\nmodules:\n  - path: proto\n    excludes:\n      - proto/tests\n",
				"proto/v1/a.proto": message("v1", "A"),
				"tools/x.proto":    message("tools", "X"),
			},
			published: []string{"proto://v1#A", "proto://tools#X"},
		},
		{
			name: "two modules of one path, each including a part",
			files: map[string]string{
				"buf.yaml":        "version: v2\nmodules:\n  - path: proto\n    includes:\n      - proto/a\n  - path: proto\n    includes:\n      - proto/b\n",
				"proto/a/a.proto": message("a", "A"),
				"proto/b/b.proto": message("b", "B"),
				"proto/c/c.proto": message("c", "C"),
			},
			published: []string{"proto://a#A", "proto://b#B"},
			dropped:   []string{"proto://c#C"},
		},
		{
			name: "one module excluding what another of the same path includes",
			files: map[string]string{
				"buf.yaml":        "version: v2\nmodules:\n  - path: proto\n    excludes:\n      - proto/b\n  - path: proto\n    includes:\n      - proto/b\n",
				"proto/a/a.proto": message("a", "A"),
				"proto/b/b.proto": message("b", "B"),
			},
			published: []string{"proto://a#A", "proto://b#B"},
		},
		{
			name: "an unread buf.yaml excludes nothing",
			files: map[string]string{
				"buf.yaml":            "version: v2\nmodules:\n  - path: &p proto\n    excludes:\n      - proto/tests\n",
				"proto/v1/a.proto":    message("v1", "A"),
				"proto/tests/t.proto": message("tests", "T"),
			},
			published: []string{"proto://v1#A", "proto://tests#T"},
		},
		{
			name: "an excluded file shadows no file of another module",
			files: map[string]string{
				"buf.yaml":           "version: v2\nmodules:\n  - path: proto\n    excludes: [proto/old]\n  - path: legacy\n",
				"proto/old/a.proto":  message("gone", "G"),
				"legacy/old/a.proto": message("legacy", "L"),
				"proto/v1/b.proto":   message("v1", "B"),
			},
			published: []string{"proto://legacy#L", "proto://v1#B"},
			dropped:   []string{"proto://gone#G"},
			notes:     map[string]string{},
		},
		{
			name: "an import of an excluded file is unresolved",
			files: map[string]string{
				"buf.yaml":          "version: v2\nmodules:\n  - path: proto\n    excludes: [proto/old]\n",
				"proto/old/x.proto": message("old", "X"),
				"proto/v1/a.proto":  "syntax = \"proto3\";\npackage v1;\nimport \"old/x.proto\";\nmessage A { old.X x = 1; }\n",
			},
			published: []string{"proto://v1#A.x"},
			dropped:   []string{"proto://old#X"},
			notes:     map[string]string{"proto/v1/a.proto": string(report.NoteKindUnresolvedImport), ".": string(report.NoteKindSchemaFindingsWithheld)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := readTree(t, tc.files, Settings{})
			refs := map[string]bool{}
			for _, b := range r.Facts.Bindings {
				refs[b.Symbol] = true
			}
			for _, ref := range tc.published {
				if !refs[ref] {
					t.Errorf("no record of %s, want one: its module builds it", ref)
				}
			}
			for _, ref := range tc.dropped {
				if refs[ref] {
					t.Errorf("a record of %s, want none: no module builds it", ref)
				}
			}
			if tc.notes == nil {
				return
			}
			notes := map[string]string{}
			for _, n := range r.Report.Notes {
				notes[n.Path] = string(n.Kind)
			}
			if !maps.Equal(notes, tc.notes) {
				t.Errorf("notes by path = %v, want %v", notes, tc.notes)
			}
		})
	}
}

// TestClassRules holds a gateway plugin to possible over what its service
// reaches, and proto.closed to certain.
func TestClassRules(t *testing.T) {
	files := map[string]string{
		"buf.gen.yaml":   "version: v2\nplugins:\n  - local: protoc-gen-grpc-gateway\n    out: gen\n",
		"v1/a.proto":     "syntax = \"proto3\";\npackage v1;\nmessage Req { int32 n = 1; }\nmessage Other { int32 m = 1; }\nservice S { rpc Do(Req) returns (Req); }\n",
		"gen/a.pb.gw.go": "package gen\n",
	}
	r := readTree(t, files, Settings{})
	if got := record(t, r, "proto://v1#Req.n").Finding.Confidence; got != vocab.Possible {
		t.Errorf("Req.n = %s, want possible: the gateway serves its service", got)
	}
	if got := record(t, r, "proto://v1#Other").Finding.Confidence; got != vocab.Probable || len(r.Report.Notes) != 0 {
		t.Errorf("Other = %s, notes %+v; want probable and no note: no service reaches it", got, r.Report.Notes)
	}
	closed := readTree(t, files, Settings{Closed: true})
	if got := record(t, closed, "proto://v1#Other").Finding.Confidence; got != vocab.Certain {
		t.Errorf("Other under proto.closed = %s, want certain", got)
	}
}

// TestYAMLQuotedScalars holds a comma and a # inside quotes to the scalar, a
// doubled single quote and an escaped double quote to the quote they write,
// and a # after a space outside quotes to a comment.
func TestYAMLQuotedScalars(t *testing.T) {
	node, err := parseYAML("directories: [\"proto,a\", 'b''c, #d', plain] # the roots\nname: \"a\\\" #b\" # a comment\nother: a#b\n")
	if err != nil {
		t.Fatalf("parseYAML = %v", err)
	}
	if got := node.strings("directories"); len(got) != 3 || got[0] != "proto,a" || got[1] != "b'c, #d" || got[2] != "plain" {
		t.Errorf("directories = %q, want [proto,a b'c, #d plain]", got)
	}
	if got := node.scalar("name"); got != `a" #b` {
		t.Errorf("name = %q, want %q", got, `a" #b`)
	}
	if got := node.scalar("other"); got != "a#b" {
		t.Errorf("other = %q, want a#b: a # after no space starts no comment", got)
	}
}

func TestYAMLDoubleQuotedEscapes(t *testing.T) {
	node, err := parseYAML(`directories: ["proto\/v1", "a\_b\e\x41\u00e9\U0001F600"]` + "\n")
	if err != nil {
		t.Fatalf("parseYAML = %v", err)
	}
	if got := node.strings("directories"); len(got) != 2 || got[0] != "proto/v1" || got[1] != "a\u00a0b\x1bA\u00e9\U0001F600" {
		t.Errorf("directories = %q, want [proto/v1 a\u00a0b\x1bA\u00e9\U0001F600]", got)
	}
	for _, refused := range []string{`"a\q"`, `"a\x4"`, `"a"b"`, `"a\"`, `"\uD800"`} {
		var unread *unreadError
		if _, err := parseYAML("name: " + refused + "\n"); !errors.As(err, &unread) {
			t.Errorf("parseYAML(name: %s) = %v, want a construct outside the subset", refused, err)
		}
	}
}

func TestYAMLQuoteInsidePlainScalar(t *testing.T) {
	node, err := parseYAML("name: it's  # a note\ndirectories: [it's, b]\n")
	if err != nil {
		t.Fatalf("parseYAML = %v", err)
	}
	if got := node.scalar("name"); got != "it's" {
		t.Errorf("name = %q, want it's", got)
	}
	if got := node.strings("directories"); len(got) != 2 || got[0] != "it's" || got[1] != "b" {
		t.Errorf("directories = %q, want [it's b]", got)
	}
}

func TestYAMLCommaInABlockPlainScalarOpensNoQuote(t *testing.T) {
	src := "name: one, \"two # comment\n" +
		"out: gen, 'secondary  # a note\n" +
		"index: a[0], \"b # c\n" +
		"brace: x{y, \"z # c\n" +
		"items:\n  - one, \"two # c\n"
	node, err := parseYAML(src)
	if err != nil {
		t.Fatalf("parseYAML = %v", err)
	}
	for key, want := range map[string]string{"name": `one, "two`, "out": "gen, 'secondary", "index": `a[0], "b`, "brace": `x{y, "z`} {
		if got := node.scalar(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := node.strings("items"); len(got) != 1 || got[0] != `one, "two` {
		t.Errorf("items = %q, want [%q]", got, `one, "two`)
	}
}

func TestYAMLBlockScalarHoldingCommasIsUnread(t *testing.T) {
	for _, indicator := range []string{"|", ">", "|-", ">+"} {
		src := "out: " + indicator + "\n  gen, \"secondary # c\n  a, 'b\nversion: v2\n"
		var unread *unreadError
		if _, err := parseYAML(src); !errors.As(err, &unread) || unread.construct != "a block scalar" {
			t.Errorf("parseYAML(out: %s ...) = %v, want a block scalar outside the subset", indicator, err)
		}
	}
}

func TestYAMLFlowSequenceSplitsOnlyBetweenItems(t *testing.T) {
	node, err := parseYAML("directories: ['a,b', \"c,d\", e]  # it's, \"a note\n")
	if err != nil {
		t.Fatalf("parseYAML = %v", err)
	}
	if got := node.strings("directories"); len(got) != 3 || got[0] != "a,b" || got[1] != "c,d" || got[2] != "e" {
		t.Errorf("directories = %q, want [a,b c,d e]", got)
	}
	for _, nested := range []string{`[[a, "b, c"], d]`, `[a, [b], "c # d"]`, `[[a], 'b # c']`} {
		var unread *unreadError
		if _, err := parseYAML("directories: " + nested + "\n"); !errors.As(err, &unread) || unread.construct != "a flow sequence of other than scalars" {
			t.Errorf("parseYAML(directories: %s) = %v, want a nested flow sequence outside the subset", nested, err)
		}
	}
}

// TestUnreadBufYAMLLeavesGenerationUnknown holds a buf.yaml outside the
// subset beside a fully known generation configuration to schemas.17's note
// at the buf.yaml and the module's declarations to possible.
func TestUnreadBufYAMLLeavesGenerationUnknown(t *testing.T) {
	files := map[string]string{
		"buf.gen.yaml":   "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: gen\n",
		"gen/a.pb.go":    "package gen\n",
		"v1/a.proto":     "syntax = \"proto3\";\npackage v1;\nmessage A { int32 n = 1; }\n",
		"buf.yaml":       "version: v2\nmodules:\n  - path: &root .\n",
		"other/buf.yaml": "version: v2\n",
	}
	r := readTree(t, files, Settings{})
	if got := record(t, r, "proto://v1#A.n").Finding.Confidence; got != vocab.Possible {
		t.Errorf("A.n = %s, want possible: the module's buf.yaml is not read", got)
	}
	var notes []string
	for _, n := range r.Report.Notes {
		notes = append(notes, string(n.Kind)+" "+n.Path)
	}
	if len(notes) != 1 || notes[0] != string(report.NoteKindGenerationConfigUnread)+" buf.yaml" {
		t.Errorf("notes = %q, want one generation-config-unread note at buf.yaml", notes)
	}
	delete(files, "buf.yaml")
	if got := record(t, readTree(t, files, Settings{}), "proto://v1#A.n").Finding.Confidence; got != vocab.Probable {
		t.Errorf("A.n with no buf.yaml = %s, want probable: the generation is known", got)
	}
}

func TestUnknownBufWorkVersionIsUnread(t *testing.T) {
	files := map[string]string{
		"buf.gen.yaml":     "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: gen\n",
		"gen/a.pb.go":      "package gen\n",
		"buf.work.yaml":    "version: v99\ndirectories: [proto]\n",
		"proto/v1/a.proto": "syntax = \"proto3\";\npackage v1;\nmessage A { int32 n = 1; }\n",
	}
	r := readTree(t, files, Settings{})
	if got := record(t, r, "proto://v1#A.n"); got.SchemaFile != "proto/v1/a.proto" || got.Finding.Confidence != vocab.Possible {
		t.Errorf("A.n = %s at %s, want possible at proto/v1/a.proto: the work file is not read", got.Finding.Confidence, got.SchemaFile)
	}
	var notes []string
	for _, n := range r.Report.Notes {
		notes = append(notes, string(n.Kind)+" "+n.Path)
	}
	if len(notes) != 1 || notes[0] != string(report.NoteKindGenerationConfigUnread)+" buf.work.yaml" {
		t.Errorf("notes = %q, want one generation-config-unread note at buf.work.yaml", notes)
	}
	files["buf.work.yaml"] = "version: v1\ndirectories: [proto]\n"
	if got := record(t, readTree(t, files, Settings{}), "proto://v1#A.n"); got.SchemaFile != "v1/a.proto" || got.Finding.Confidence != vocab.Probable {
		t.Errorf("A.n under a v1 work file = %s at %s, want probable at v1/a.proto", got.Finding.Confidence, got.SchemaFile)
	}
}

// TestShadowedSchemaFilePublishesNothing holds each file whose import path an
// earlier import root resolves to another file, one of the same import path or
// one below another root, to no record and a schema-unparsed note, every
// other declaration to one record of its own file, published live since a
// shadowed file is not read cleanly.
func TestShadowedSchemaFilePublishesNothing(t *testing.T) {
	r := readTree(t, map[string]string{
		"a/b/x.proto":  "syntax = \"proto3\";\npackage v1;\nmessage B { int32 n = 1; }\n",
		"c/x.proto":    "syntax = \"proto3\";\npackage v2;\n\n\nmessage C {}\n",
		"a/v1/y.proto": "syntax = \"proto3\";\npackage v1;\nmessage Y {}\n",
		"v1/y.proto":   "syntax = \"proto3\";\npackage v9;\nmessage Z {}\n",
	}, Settings{ImportPaths: []string{"a", "a/b", "c", "."}})
	seen := map[string]int{}
	for _, b := range r.Facts.Bindings {
		seen[b.Symbol]++
	}
	if len(seen) != 3 || seen["proto://v1#B"] != 1 || seen["proto://v1#B.n"] != 1 || seen["proto://v1#Y"] != 1 {
		t.Errorf("records = %v, want B, B.n and Y once each", seen)
	}
	if got := record(t, r, "proto://v1#B"); got.SchemaFile != "b/x.proto" || got.State != facts.StateLive {
		t.Errorf("B at %s %s, want live at b/x.proto", got.SchemaFile, got.State)
	}
	var notes []string
	for _, n := range r.Report.Notes {
		notes = append(notes, string(n.Kind)+" "+n.Path)
	}
	want := []string{"schema-findings-withheld .", "schema-unparsed c/x.proto", "schema-unparsed v1/y.proto"}
	if !slices.Equal(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

// rootsCase is one tree and what its reading must publish: every note, and
// each named record once with its schema_file, its state and its one use.
type rootsCase struct {
	name        string
	files       map[string]string
	settings    Settings
	notes       map[string]string      // note kind by path, every note of the report
	messages    map[string]string      // the end of the note's message by path
	schemaFiles map[string]string      // schema_file by record
	states      map[string]facts.State // state by record
	uses        map[string]string      // the one declaration a record uses
	absent      []string               // records the reading publishes none of
}

func (tc rootsCase) check(t *testing.T) {
	t.Helper()
	r := readTree(t, tc.files, tc.settings)
	notes := map[string]string{}
	for _, n := range r.Report.Notes {
		notes[n.Path] = string(n.Kind)
	}
	if !maps.Equal(notes, tc.notes) {
		t.Errorf("notes by path = %v, want %v", notes, tc.notes)
	}
	for _, n := range r.Report.Notes {
		if want, ok := tc.messages[n.Path]; ok && !strings.HasSuffix(n.Message, want) {
			t.Errorf("%s note %q, want it to end %q", n.Path, n.Message, want)
		}
	}
	published := map[string]int{}
	for _, b := range r.Facts.Bindings {
		published[b.Symbol]++
	}
	for ref, file := range tc.schemaFiles {
		if got := record(t, r, ref); got.SchemaFile != file || published[ref] != 1 {
			t.Errorf("%s: %d records at %s, want one at %s", ref, published[ref], got.SchemaFile, file)
		}
	}
	for ref, state := range tc.states {
		if got := record(t, r, ref); got.State != state {
			t.Errorf("%s = %s, want %s", ref, got.State, state)
		}
	}
	for ref, use := range tc.uses {
		if got := record(t, r, ref); !slices.Equal(got.Uses, []string{use}) {
			t.Errorf("%s uses %v, want [%s]", ref, got.Uses, use)
		}
	}
	for _, ref := range tc.absent {
		if published[ref] != 0 {
			t.Errorf("%d records of %s, want none", published[ref], ref)
		}
	}
}

// TestFileOutsideTheImportRoots holds the search list to the import roots
// proto.import_paths or the buf modules name: an import of a file outside
// them is unresolved, and a file outside them publishes its own declarations
// under its target-relative path unless a root, or the compiler's own
// include directory, holds a file of that path or, for an upstream copy, of
// its upstream path, which shadows it and withholds the target's findings.
func TestFileOutsideTheImportRoots(t *testing.T) {
	const (
		importer = "syntax = \"proto3\";\npackage v1;\nimport \"stray.proto\";\nmessage A { stray.B b = 1; }\n"
		stray    = "syntax = \"proto3\";\npackage stray;\nmessage B {}\n"
		inside   = "syntax = \"proto3\";\npackage api;\nmessage A {}\n"
		outside  = "syntax = \"proto3\";\npackage root;\nmessage R {}\n"
		v2Module = "version: v2\nmodules:\n  - path: api\n"
		event    = "syntax = \"proto3\";\npackage v1;\nimport \"google/protobuf/timestamp.proto\";\nmessage A { google.protobuf.Timestamp at = 1; }\n"
		embedded = " in the compiler's own include directory, so the compiler reads that file and none of this file's declarations is published"
		httpCopy = "syntax = \"proto3\";\npackage google.api;\nmessage Http { string x = 1; }\n"
	)
	importPaths := Settings{ImportPaths: []string{"api"}}
	embeddedSrc := func(name string) string {
		src, err := os.ReadFile(filepath.Join("proto", "include", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(src)
	}
	belowADirectory := func(name string, files map[string]string, settings Settings) rootsCase {
		files["api/v1/a.proto"] = event
		files["third_party/google/protobuf/timestamp.proto"] = embeddedSrc("google/protobuf/timestamp.proto")
		return rootsCase{
			name: name, files: files, settings: settings,
			notes:       map[string]string{"third_party/google/protobuf/timestamp.proto": string(report.NoteKindSchemaUnparsed), ".": string(report.NoteKindSchemaFindingsWithheld)},
			messages:    map[string]string{"third_party/google/protobuf/timestamp.proto": "the import path google/protobuf/timestamp.proto of the schema file names google/protobuf/timestamp.proto" + embedded},
			schemaFiles: map[string]string{"proto://v1#A": "v1/a.proto"},
			states:      map[string]facts.State{"proto://v1#A.at": facts.StateLive},
			uses:        map[string]string{"proto://v1#A.at": "proto://google.protobuf#Timestamp"},
			absent:      []string{"proto://google.protobuf#Timestamp"},
		}
	}
	strayCopy := func(name string, files map[string]string, settings Settings) rootsCase {
		files["api/v1/a.proto"] = event
		notes, messages := map[string]string{".": string(report.NoteKindSchemaFindingsWithheld)}, map[string]string{}
		for _, stray := range []string{"google/protobuf/timestamp.proto", "google/protobuf/duration.proto", "google/protobuf/compiler/plugin.proto"} {
			files[stray], notes[stray], messages[stray] = embeddedSrc(stray), string(report.NoteKindSchemaUnparsed), "names "+stray+embedded
		}
		return rootsCase{
			name: name, files: files, settings: settings, notes: notes, messages: messages,
			schemaFiles: map[string]string{"proto://v1#A": "v1/a.proto"},
			states:      map[string]facts.State{"proto://v1#A.at": facts.StateLive},
			uses:        map[string]string{"proto://v1#A.at": "proto://google.protobuf#Timestamp"},
			absent:      []string{"proto://google.protobuf#Duration"},
		}
	}
	for _, tc := range []rootsCase{
		{
			name:        "an import of a file outside proto.import_paths",
			files:       map[string]string{"api/v1/a.proto": importer, "stray.proto": stray},
			settings:    importPaths,
			states:      map[string]facts.State{"proto://v1#A.b": facts.StateLive},
			notes:       map[string]string{"api/v1/a.proto": string(report.NoteKindUnresolvedImport), ".": string(report.NoteKindSchemaFindingsWithheld)},
			schemaFiles: map[string]string{"proto://v1#A": "v1/a.proto", "proto://stray#B": "stray.proto"},
		},
		{
			name:        "an import of a file outside the buf module",
			files:       map[string]string{"buf.yaml": v2Module, "api/v1/a.proto": importer, "stray.proto": stray},
			states:      map[string]facts.State{"proto://v1#A.b": facts.StateLive},
			notes:       map[string]string{"api/v1/a.proto": string(report.NoteKindUnresolvedImport), ".": string(report.NoteKindSchemaFindingsWithheld)},
			schemaFiles: map[string]string{"proto://v1#A": "v1/a.proto", "proto://stray#B": "stray.proto"},
		},
		{
			name:        "a file outside proto.import_paths of an import path a root holds",
			files:       map[string]string{"api/v1/a.proto": inside, "v1/a.proto": outside},
			settings:    importPaths,
			notes:       map[string]string{"v1/a.proto": string(report.NoteKindSchemaUnparsed), ".": string(report.NoteKindSchemaFindingsWithheld)},
			schemaFiles: map[string]string{"proto://api#A": "v1/a.proto"},
			absent:      []string{"proto://root#R"},
		},
		{
			name:        "a file outside the buf module of an import path the module holds",
			files:       map[string]string{"buf.yaml": v2Module, "api/v1/a.proto": inside, "v1/a.proto": outside},
			notes:       map[string]string{"v1/a.proto": string(report.NoteKindSchemaUnparsed), ".": string(report.NoteKindSchemaFindingsWithheld)},
			schemaFiles: map[string]string{"proto://api#A": "v1/a.proto"},
			absent:      []string{"proto://root#R"},
		},
		{
			name: "a file outside the buf module importing through the module",
			files: map[string]string{
				"buf.yaml": v2Module, "api/v1/b.proto": "syntax = \"proto3\";\npackage v1;\nmessage B {}\n",
				"tools/x.proto": "syntax = \"proto3\";\npackage tools;\nimport \"v1/b.proto\";\nmessage X { v1.B b = 1; }\n",
			},
			notes:       map[string]string{},
			schemaFiles: map[string]string{"proto://v1#B": "v1/b.proto", "proto://tools#X": "tools/x.proto"},
			uses:        map[string]string{"proto://tools#X.b": "proto://v1#B"},
		},
		{
			name:        "a file outside proto.import_paths redefining a name a root's file defines",
			files:       map[string]string{"api/v1/a.proto": inside, "old/a.proto": inside},
			settings:    importPaths,
			notes:       map[string]string{"old/a.proto": string(report.NoteKindSchemaUnparsed), ".": string(report.NoteKindSchemaFindingsWithheld)},
			schemaFiles: map[string]string{"proto://api#A": "v1/a.proto"},
		},
		strayCopy("a copy of an embedded file outside the buf module", map[string]string{"buf.yaml": v2Module}, Settings{}),
		strayCopy("a copy of an embedded file outside proto.import_paths", map[string]string{}, importPaths),
		belowADirectory("a copy of an embedded file below a directory outside the buf module", map[string]string{"buf.yaml": v2Module}, Settings{}),
		belowADirectory("a copy of an embedded file below a directory outside proto.import_paths", map[string]string{}, importPaths),
		{
			name: "a copy below a directory outside the buf module of an upstream path the module holds",
			files: map[string]string{
				"buf.yaml": v2Module, "api/google/api/http.proto": httpCopy, "third_party/google/api/http.proto": httpCopy,
				"api/v1/a.proto": "syntax = \"proto3\";\npackage v1;\nimport \"google/api/http.proto\";\nmessage A { google.api.Http h = 1; }\n",
			},
			notes:       map[string]string{"third_party/google/api/http.proto": string(report.NoteKindSchemaUnparsed), ".": string(report.NoteKindSchemaFindingsWithheld)},
			messages:    map[string]string{"third_party/google/api/http.proto": "names api/google/api/http.proto under an import root, so the compiler reads that file and none of this file's declarations is published"},
			schemaFiles: map[string]string{"proto://google.api#Http": "google/api/http.proto", "proto://v1#A": "v1/a.proto"},
			states:      map[string]facts.State{"proto://google.api#Http": facts.StateLive, "proto://v1#A.h": facts.StateLive},
			uses:        map[string]string{"proto://v1#A.h": "proto://google.api#Http"},
		},
		{
			name: "an upstream copy outside proto.import_paths whose own path a root holds",
			files: map[string]string{
				"proto/x/google/type/date.proto": "syntax = \"proto3\";\npackage google.type;\nmessage Date { int32 year = 1; }\n",
				"x/google/type/date.proto":       "syntax = \"proto3\";\npackage google.type;\nmessage Other { int32 year = 1; }\n",
			},
			settings:    Settings{ImportPaths: []string{"proto"}},
			notes:       map[string]string{"x/google/type/date.proto": string(report.NoteKindSchemaUnparsed), ".": string(report.NoteKindSchemaFindingsWithheld)},
			messages:    map[string]string{"x/google/type/date.proto": "the import path x/google/type/date.proto of the schema file names proto/x/google/type/date.proto under an import root, so the compiler reads that file and none of this file's declarations is published"},
			schemaFiles: map[string]string{"proto://google.type#Date": "x/google/type/date.proto"},
			absent:      []string{"proto://google.type#Other"},
		},
		{
			name: "a buf.work.yaml directory holding no buf.yaml",
			files: map[string]string{
				"buf.work.yaml": "version: v1\ndirectories:\n  - svc\n",
				"svc/a.proto":   "syntax = \"proto3\";\npackage p;\nimport \"b.proto\";\nmessage A { B b = 1; }\n",
				"svc/b.proto":   "syntax = \"proto3\";\npackage p;\nmessage B {}\n",
			},
			notes:       map[string]string{},
			schemaFiles: map[string]string{"proto://p#A": "a.proto", "proto://p#B": "b.proto"},
			uses:        map[string]string{"proto://p#A.b": "proto://p#B"},
		},
	} {
		t.Run(tc.name, tc.check)
	}
}

// TestEachSchemaFileLinksOnce holds a schema file an import reaches through
// a second import root, or the include root of a kept upstream copy, to the
// one unit of its own import path: its declarations are published once, an
// importer's field uses them, and no note is written but for a second
// tree's copy that the first tree's shadows.
func TestEachSchemaFileLinksOnce(t *testing.T) {
	const (
		timestamp = "syntax = \"proto3\";\npackage google.protobuf;\nmessage Timestamp { int64 seconds = 1; int32 nanos = 2; }\n"
		event     = "syntax = \"proto3\";\npackage v1;\nimport \"google/protobuf/timestamp.proto\";\nmessage Event { google.protobuf.Timestamp at = 1; }\n"
		item      = "syntax = \"proto3\";\npackage shop;\nmessage Item {}\n"
		ts        = "proto://google.protobuf#Timestamp"
	)
	cart := func(imported string) string {
		return "syntax = \"proto3\";\npackage shop;\nimport \"" + imported + "\";\nmessage Cart { Item item = 1; }\n"
	}
	copyCase := func(name, importer, copied string) rootsCase {
		return rootsCase{
			name:        name,
			files:       map[string]string{importer: event, copied: timestamp},
			notes:       map[string]string{},
			schemaFiles: map[string]string{ts: "google/protobuf/timestamp.proto", "proto://v1#Event": importer},
			states:      map[string]facts.State{ts: facts.StateLive, "proto://v1#Event.at": facts.StateDead},
			uses:        map[string]string{"proto://v1#Event.at": ts},
		}
	}
	service := func(pkg string) string {
		return "syntax = \"proto3\";\npackage " + pkg + ";\nimport \"google/api/http.proto\";\nmessage Req { google.api.HttpRule rule = 1; }\n"
	}
	const httpRule = "syntax = \"proto3\";\npackage google.api;\nmessage HttpRule { string get = 2; }\n"
	descriptor, err := os.ReadFile(filepath.Join("proto", "include", "google", "protobuf", "descriptor.proto"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []rootsCase{
		{
			name: "two trees each holding an upstream copy and a file of one path below it",
			files: map[string]string{
				"svc-a/proto/google/api/http.proto": httpRule, "svc-a/proto/api/v1/service.proto": service("svc_a.api.v1"),
				"svc-b/proto/google/api/http.proto": httpRule, "svc-b/proto/api/v1/service.proto": service("svc_b.api.v1"),
			},
			notes: map[string]string{"svc-b/proto/google/api/http.proto": string(report.NoteKindSchemaUnparsed), ".": string(report.NoteKindSchemaFindingsWithheld)},
			schemaFiles: map[string]string{
				"proto://svc_a.api.v1#Req": "svc-a/proto/api/v1/service.proto", "proto://svc_b.api.v1#Req": "svc-b/proto/api/v1/service.proto",
				"proto://google.api#HttpRule": "google/api/http.proto",
			},
			states: map[string]facts.State{
				"proto://svc_a.api.v1#Req": facts.StateLive, "proto://svc_b.api.v1#Req": facts.StateLive,
				"proto://svc_a.api.v1#Req.rule": facts.StateLive, "proto://svc_b.api.v1#Req.rule": facts.StateLive,
				"proto://google.api#HttpRule": facts.StateLive,
			},
			uses: map[string]string{"proto://svc_a.api.v1#Req.rule": "proto://google.api#HttpRule", "proto://svc_b.api.v1#Req.rule": "proto://google.api#HttpRule"},
		},
		{
			name: "a copy of descriptor.proto below a directory",
			files: map[string]string{
				"third_party/google/protobuf/descriptor.proto": string(descriptor),
				"api/opts.proto": "syntax = \"proto3\";\npackage opts;\nimport \"google/protobuf/descriptor.proto\";\n" +
					"extend google.protobuf.FieldOptions { bool secret = 50000; }\nmessage M { string key = 1 [(opts.secret) = true]; }\n",
			},
			notes:       map[string]string{},
			schemaFiles: map[string]string{"proto://google.protobuf#FieldOptions": "google/protobuf/descriptor.proto"},
			states:      map[string]facts.State{"proto://google.protobuf#FieldOptions": facts.StateLive, "proto://opts#secret": facts.StateLive},
		},
		copyCase("an upstream copy below a directory sorted after its importer", "api/v1/event.proto", "third_party/google/protobuf/timestamp.proto"),
		copyCase("an upstream copy below a directory sorted before its importer", "api/v1/event.proto", "3rdparty/google/protobuf/timestamp.proto"),
		{
			name: "a v1beta1 module's roots inside a workspace directory",
			files: map[string]string{
				"buf.work.yaml":             "version: v1\ndirectories:\n  - proto\n",
				"proto/buf.yaml":            "version: v1beta1\nbuild:\n  roots:\n    - src\n",
				"proto/src/shop/cart.proto": cart("shop/item.proto"),
				"proto/src/shop/item.proto": item,
			},
			notes:       map[string]string{},
			schemaFiles: map[string]string{"proto://shop#Cart": "shop/cart.proto", "proto://shop#Item": "shop/item.proto"},
			uses:        map[string]string{"proto://shop#Cart.item": "proto://shop#Item"},
		},
		{
			name: "a version 1 module in its workspace directory",
			files: map[string]string{
				"buf.work.yaml":         "version: v1\ndirectories:\n  - proto\n",
				"proto/buf.yaml":        "version: v1\n",
				"proto/shop/cart.proto": cart("shop/item.proto"),
				"proto/shop/item.proto": item,
			},
			notes:       map[string]string{},
			schemaFiles: map[string]string{"proto://shop#Cart": "shop/cart.proto", "proto://shop#Item": "shop/item.proto"},
			uses:        map[string]string{"proto://shop#Cart.item": "proto://shop#Item"},
		},
	} {
		t.Run(tc.name, tc.check)
	}
}

// TestDefaultRootsAreTheTargetRoot holds a target with no buf.yaml and no
// proto.import_paths to buf's module for a directory with no buf.yaml, the
// target root, besides the include roots of upstream copies: an import no
// root holds fails its file, with protoc v36.2's error and the remedy in an
// unresolved-import note, and every importer of that file; a name no scope
// resolves fails its file too. Either failure publishes every other record
// live.
func TestDefaultRootsAreTheTargetRoot(t *testing.T) {
	const (
		unresolved = string(report.NoteKindUnresolvedImport)
		unparsed   = string(report.NoteKindSchemaUnparsed)
		withheld   = string(report.NoteKindSchemaFindingsWithheld)
		remedy     = " Set proto.import_paths, or add a buf.yaml, to say where imports resolve from."
		item       = "syntax = \"proto3\";\npackage shop;\nmessage Item {}\n"
		httpRule   = "syntax = \"proto3\";\npackage google.api;\nmessage HttpRule { string get = 2; }\n"
		top        = "syntax = \"proto3\";\npackage top;\nmessage Top {}\n"
	)
	cart := func(imported string) string {
		return "syntax = \"proto3\";\npackage shop;\nimport \"" + imported + "\";\nmessage Cart { Item item = 1; }\n"
	}
	notFound := func(imported string) string { return `3:1: Import "` + imported + `" was not found or had errors.` }
	ownFile := func(name string, files map[string]string) rootsCase {
		files["proto/google/api/http.proto"], files["proto/shop/cart.proto"], files["proto/shop/item.proto"] = httpRule, cart("shop/item.proto"), item
		return rootsCase{
			name: name, files: files,
			notes:       map[string]string{"proto/shop/cart.proto": unresolved, ".": withheld},
			messages:    map[string]string{"proto/shop/cart.proto": notFound("shop/item.proto") + remedy},
			schemaFiles: map[string]string{"proto://shop#Item": "proto/shop/item.proto", "proto://google.api#HttpRule": "google/api/http.proto"},
			states:      map[string]facts.State{"proto://shop#Item": facts.StateLive, "proto://google.api#HttpRule": facts.StateLive},
			absent:      []string{"proto://shop#Cart", "proto://shop#Cart.item"},
		}
	}
	service := func(pkg, imported, body string) string {
		return "syntax = \"proto3\";\npackage " + pkg + ";\nimport \"" + imported + "\";\n" + body
	}
	fieldBehavior := func(values string) string {
		return "syntax = \"proto3\";\npackage google.api;\nimport \"google/protobuf/descriptor.proto\";\nenum FieldBehavior { FIELD_BEHAVIOR_UNSPECIFIED = 0; REQUIRED = 2;" + values +
			" }\nextend google.protobuf.FieldOptions { repeated FieldBehavior field_behavior = 1052 [packed = false]; }\n"
	}
	for _, tc := range []rootsCase{
		{
			name:     "a sibling imported by its base name after its importer",
			files:    map[string]string{"shop/cart.proto": cart("item.proto"), "shop/item.proto": item},
			notes:    map[string]string{"shop/cart.proto": unresolved, ".": withheld},
			messages: map[string]string{"shop/cart.proto": notFound("item.proto") + remedy},
			states:   map[string]facts.State{"proto://shop#Item": facts.StateLive},
			absent:   []string{"proto://shop#Cart", "proto://shop#Cart.item"},
		},
		{
			name:     "a sibling imported by its base name before its importer",
			files:    map[string]string{"shop/order.proto": cart("item.proto"), "shop/item.proto": item},
			notes:    map[string]string{"shop/order.proto": unresolved, ".": withheld},
			messages: map[string]string{"shop/order.proto": notFound("item.proto") + remedy},
			states:   map[string]facts.State{"proto://shop#Item": facts.StateLive},
			absent:   []string{"proto://shop#Cart", "proto://shop#Cart.item"},
		},
		{
			name:     "a file imported by its path from a parent directory",
			files:    map[string]string{"api/cart.proto": cart("v1/item.proto"), "api/v1/item.proto": item},
			notes:    map[string]string{"api/cart.proto": unresolved, ".": withheld},
			messages: map[string]string{"api/cart.proto": notFound("v1/item.proto") + remedy},
			states:   map[string]facts.State{"proto://shop#Item": facts.StateLive},
			absent:   []string{"proto://shop#Cart"},
		},
		{
			name: "a file importing one whose import no root holds",
			files: map[string]string{
				"shop/cart.proto": cart("item.proto"), "shop/item.proto": item,
				"x.proto": "syntax = \"proto3\";\npackage x;\nimport \"shop/cart.proto\";\nmessage X { shop.Cart c = 1; }\n",
			},
			notes:    map[string]string{"shop/cart.proto": unresolved, "x.proto": unparsed, ".": withheld},
			messages: map[string]string{"x.proto": notFound("shop/cart.proto")},
			absent:   []string{"proto://shop#Cart", "proto://x#X", "proto://x#X.c"},
		},
		ownFile("an own file below the include root of an upstream copy imported by its path from that root", map[string]string{}),
		ownFile("the same with a schema file directly in the include root's directory", map[string]string{"proto/top.proto": top}),
		{
			name: "two trees each holding their own file of one path",
			files: map[string]string{
				"svc-a/proto/top.proto":            "syntax = \"proto3\";\npackage svc_a;\nmessage Topa {}\n",
				"svc-a/proto/api/v1/common.proto":  "syntax = \"proto3\";\npackage svc_a.api.v1;\nmessage Common { string x = 1; }\n",
				"svc-a/proto/api/v1/service.proto": service("svc_a.api.v1", "api/v1/common.proto", "message Req { Common c = 1; }\n"),
				"svc-b/proto/top.proto":            "syntax = \"proto3\";\npackage svc_b;\nmessage Topb {}\n",
				"svc-b/proto/api/v1/common.proto":  "syntax = \"proto3\";\npackage svc_b.api.v1;\nmessage Common { string x = 1; }\nmessage Extra { string y = 1; }\n",
				"svc-b/proto/api/v1/service.proto": service("svc_b.api.v1", "api/v1/common.proto", "message Req { Extra e = 1; Common c = 2; }\n"),
			},
			notes: map[string]string{"svc-a/proto/api/v1/service.proto": unresolved, "svc-b/proto/api/v1/service.proto": unresolved, ".": withheld},
			messages: map[string]string{
				"svc-a/proto/api/v1/service.proto": notFound("api/v1/common.proto") + remedy,
				"svc-b/proto/api/v1/service.proto": notFound("api/v1/common.proto") + remedy,
			},
			states: map[string]facts.State{"proto://svc_a.api.v1#Common": facts.StateLive, "proto://svc_b.api.v1#Extra": facts.StateLive},
			absent: []string{"proto://svc_a.api.v1#Req", "proto://svc_b.api.v1#Req", "proto://svc_b.api.v1#Req.e", "proto://svc_b.api.v1#Req.c"},
		},
		{
			name: "two trees keeping differing copies of one upstream path",
			files: map[string]string{
				"svc-a/proto/google/api/http.proto": "syntax = \"proto3\";\npackage google.api;\nmessage Http { string x = 1; }\n",
				"svc-a/proto/api/v1/service.proto":  service("svc_a.api.v1", "google/api/http.proto", "message Req { google.api.Http h = 1; }\nmessage Unuseda { int32 x = 1; }\n"),
				"svc-b/proto/google/api/http.proto": "syntax = \"proto3\";\npackage google.api;\nmessage Http { string x = 1; }\nmessage HttpRule { string selector = 1; }\n",
				"svc-b/proto/api/v1/service.proto":  service("svc_b.api.v1", "google/api/http.proto", "message Req { google.api.HttpRule r = 1; }\nmessage Unusedb { int32 x = 1; }\n"),
			},
			notes: map[string]string{"svc-b/proto/google/api/http.proto": unparsed, "svc-b/proto/api/v1/service.proto": unparsed, ".": withheld},
			messages: map[string]string{
				"svc-b/proto/google/api/http.proto": "names svc-a/proto/google/api/http.proto under an earlier import root, so the compiler reads that file and none of this file's declarations is published",
				"svc-b/proto/api/v1/service.proto":  `4:15: "google.api.HttpRule" is not defined.`,
			},
			states: map[string]facts.State{"proto://svc_a.api.v1#Req.h": facts.StateLive, "proto://svc_a.api.v1#Unuseda": facts.StateLive},
			absent: []string{"proto://svc_b.api.v1#Req", "proto://svc_b.api.v1#Req.r", "proto://svc_b.api.v1#Unusedb", "proto://google.api#HttpRule"},
		},
		{
			name: "two trees keeping copies of one upstream path that differ in an option value",
			files: map[string]string{
				"svc-a/proto/google/api/field_behavior.proto": fieldBehavior(""),
				"svc-a/proto/api/v1/service.proto":            service("svc_a.api.v1", "google/api/field_behavior.proto", "message Req { string name = 1 [(google.api.field_behavior) = REQUIRED]; }\n"),
				"svc-b/proto/google/api/field_behavior.proto": fieldBehavior(" IDENTIFIER = 8;"),
				"svc-b/proto/api/v1/service.proto":            service("svc_b.api.v1", "google/api/field_behavior.proto", "message Req { string name = 1 [(google.api.field_behavior) = IDENTIFIER]; }\n"),
			},
			notes: map[string]string{"svc-b/proto/google/api/field_behavior.proto": unparsed, "svc-b/proto/api/v1/service.proto": unparsed, ".": withheld},
			messages: map[string]string{
				"svc-b/proto/api/v1/service.proto": `4:62: Enum type "google.api.FieldBehavior" has no value named "IDENTIFIER" for option "google.api.field_behavior".`,
			},
			states: map[string]facts.State{"proto://svc_a.api.v1#Req.name": facts.StateLive},
			absent: []string{"proto://svc_b.api.v1#Req", "proto://svc_b.api.v1#Req.name"},
		},
		{
			name:     "an option naming no extension",
			files:    map[string]string{"b.proto": "syntax = \"proto3\";\npackage p;\nmessage B { int32 y = 1 [(no.opt) = 1]; }\n"},
			notes:    map[string]string{"b.proto": unparsed, ".": withheld},
			messages: map[string]string{"b.proto": `3:26: Option "(no.opt)" unknown. Ensure that your proto definition file imports the proto which defines the option (i.e. via import option after edition 2024).`},
			absent:   []string{"proto://p#B", "proto://p#B.y"},
		},
	} {
		t.Run(tc.name, tc.check)
	}
}

// TestImportCyclePublishesNothing holds every file of an import cycle, and
// every file importing one, to no record and one schema-unparsed note with
// the error protoc v36.2 prints first for it.
func TestImportCyclePublishesNothing(t *testing.T) {
	const (
		unparsed = string(report.NoteKindSchemaUnparsed)
		withheld = string(report.NoteKindSchemaFindingsWithheld)
		mn       = "syntax = \"proto3\";\npackage p;\nimport \"n.proto\";\nmessage M { N n = 1; }\n"
		nm       = "syntax = \"proto3\";\npackage p;\nimport \"m.proto\";\nmessage N { int32 x = 1; }\n"
	)
	importer := func(imported string) string {
		return "syntax = \"proto3\";\npackage q;\nimport \"" + imported + "\";\nmessage X { p.M m = 1; }\n"
	}
	for _, tc := range []rootsCase{
		{
			name:     "two files",
			files:    map[string]string{"m.proto": mn, "n.proto": nm},
			notes:    map[string]string{"m.proto": unparsed, "n.proto": unparsed, ".": withheld},
			messages: map[string]string{"m.proto": "3:1: File recursively imports itself: m.proto -> n.proto -> m.proto", "n.proto": `3:1: Import "m.proto" was not found or had errors.`},
			absent:   []string{"proto://p#M", "proto://p#M.n", "proto://p#N", "proto://p#N.x"},
		},
		{
			name: "three files",
			files: map[string]string{
				"m.proto": mn,
				"n.proto": "syntax = \"proto3\";\npackage p;\nimport \"o.proto\";\nmessage N { O o = 1; }\n",
				"o.proto": "syntax = \"proto3\";\npackage p;\nimport \"m.proto\";\nmessage O { int32 z = 1; }\n",
			},
			notes: map[string]string{"m.proto": unparsed, "n.proto": unparsed, "o.proto": unparsed, ".": withheld},
			messages: map[string]string{
				"m.proto": "3:1: File recursively imports itself: m.proto -> n.proto -> o.proto -> m.proto",
				"n.proto": `3:1: Import "o.proto" was not found or had errors.`, "o.proto": `3:1: Import "m.proto" was not found or had errors.`,
			},
			absent: []string{"proto://p#M", "proto://p#N", "proto://p#N.o", "proto://p#O", "proto://p#O.z"},
		},
		{
			name:     "a file importing one, loaded first",
			files:    map[string]string{"a.proto": importer("m.proto"), "m.proto": mn, "n.proto": nm},
			notes:    map[string]string{"a.proto": unparsed, "m.proto": unparsed, "n.proto": unparsed, ".": withheld},
			messages: map[string]string{"a.proto": `3:1: Import "m.proto" was not found or had errors.`, "n.proto": `3:1: Import "m.proto" was not found or had errors.`},
			absent:   []string{"proto://q#X", "proto://q#X.m", "proto://p#M", "proto://p#N"},
		},
		{
			name:     "a file importing one, loaded last",
			files:    map[string]string{"x.proto": importer("n.proto"), "m.proto": mn, "n.proto": nm},
			notes:    map[string]string{"x.proto": unparsed, "m.proto": unparsed, "n.proto": unparsed, ".": withheld},
			messages: map[string]string{"x.proto": `3:1: Import "n.proto" was not found or had errors.`},
			absent:   []string{"proto://q#X", "proto://q#X.m", "proto://p#M", "proto://p#N"},
		},
	} {
		t.Run(tc.name, tc.check)
	}
}

// The schema-findings-withheld note's message, for one file not read
// cleanly, with why, and the default roots' remedy or none.
const (
	withheldOne   = "schema findings withheld: 1 schema file could not be read cleanly, and any declaration of the target may be one only it uses: "
	remedyDefault = " Set proto.import_paths, or add a buf.yaml, to say where imports resolve from; its note names why."
	remedyOwn     = " Its note names why."
)

// TestUnreadFileWithholdsTheTargetsFindings holds a target with a schema
// file not read cleanly, one that does not compile, holds an import or a
// name no root resolves, or is shadowed, to no dead record in any mode,
// since that file may use any declaration: each such file keeps its own
// note, and one note on the target root lists them with why.
func TestUnreadFileWithholdsTheTargetsFindings(t *testing.T) {
	const (
		unresolved = string(report.NoteKindUnresolvedImport)
		unparsed   = string(report.NoteKindSchemaUnparsed)
		withheld   = string(report.NoteKindSchemaFindingsWithheld)
		service    = "syntax = \"proto3\";\npackage v1;\nmessage PingRequest {}\nmessage PingResponse { int32 number = 1; }\n"
		dot        = "."
	)
	rpc := func(body string, imports ...string) string {
		src := "syntax = \"proto3\";\npackage v1;\n"
		for _, i := range imports {
			src += "import \"" + i + "\";\n"
		}
		return src + body + "service Pinger { rpc Ping(PingRequest) returns (PingResponse); }\n"
	}
	ping := map[string]facts.State{"proto://v1#PingRequest": facts.StateLive, "proto://v1#PingResponse": facts.StateLive, "proto://v1#PingResponse.number": facts.StateLive}
	importPaths := Settings{ImportPaths: []string{dot}}
	const (
		pingImport  = "proto/v1/rpc.proto (the import v1/service.proto names no file of the import roots)."
		pingCompile = "proto/v1/rpc.proto (it does not compile)."
		notDefined  = `3:27: "PingRequest" is not defined.`
	)
	shadowed := map[string]string{
		"a/v1/types.proto":   "syntax = \"proto3\";\npackage av1;\nmessage Other {}\n",
		"b/v1/types.proto":   "syntax = \"proto3\";\npackage v1;\nimport \"v1/service.proto\";\nmessage Wrap { PingResponse r = 1; PingRequest q = 2; }\n",
		"b/v1/service.proto": service,
	}
	for _, tc := range []rootsCase{
		{
			name:     "an import written from a directory the default roots do not name",
			files:    map[string]string{"proto/v1/service.proto": service, "proto/v1/rpc.proto": rpc("", "v1/service.proto")},
			notes:    map[string]string{"proto/v1/rpc.proto": unresolved, dot: withheld},
			messages: map[string]string{dot: withheldOne + pingImport + remedyDefault},
			states:   ping,
			absent:   []string{"proto://v1#Pinger", "proto://v1#Pinger.Ping"},
		},
		{
			name:     "the same under proto.import_paths",
			files:    map[string]string{"proto/v1/service.proto": service, "proto/v1/rpc.proto": rpc("", "v1/service.proto")},
			settings: importPaths,
			notes:    map[string]string{"proto/v1/rpc.proto": unresolved, dot: withheld},
			messages: map[string]string{
				"proto/v1/rpc.proto": "the import v1/service.proto names no file of the import roots searched (.), so the declarations it would supply types to are held live; name its root in proto.import_paths",
				dot:                  withheldOne + pingImport + remedyOwn,
			},
			states: map[string]facts.State{"proto://v1#PingResponse": facts.StateLive, "proto://v1#PingResponse.number": facts.StateLive, "proto://v1#Pinger": facts.StateLive},
		},
		{
			name:     "a type name no scope resolves under proto.import_paths",
			files:    map[string]string{"proto/v1/service.proto": service, "proto/v1/rpc.proto": rpc("")},
			settings: Settings{ImportPaths: []string{"proto"}},
			notes:    map[string]string{"proto/v1/rpc.proto": unparsed, dot: withheld},
			messages: map[string]string{"proto/v1/rpc.proto": notDefined, dot: withheldOne + pingCompile + remedyOwn},
			states:   ping,
			absent:   []string{"proto://v1#Pinger", "proto://v1#Pinger.Ping"},
		},
		{
			name:     "a type name no scope resolves under a buf.yaml",
			files:    map[string]string{"proto/buf.yaml": "version: v1\n", "proto/v1/service.proto": service, "proto/v1/rpc.proto": rpc("")},
			notes:    map[string]string{"proto/v1/rpc.proto": unparsed, dot: withheld},
			messages: map[string]string{"proto/v1/rpc.proto": notDefined, dot: withheldOne + pingCompile + remedyOwn},
			states:   ping,
			absent:   []string{"proto://v1#Pinger", "proto://v1#Pinger.Ping"},
		},
		{
			name:     "the import present under proto.import_paths",
			files:    map[string]string{"proto/v1/service.proto": service, "proto/v1/rpc.proto": rpc("", "v1/service.proto")},
			settings: Settings{ImportPaths: []string{"proto"}},
			notes:    map[string]string{},
			states:   map[string]facts.State{"proto://v1#PingResponse": facts.StateDead, "proto://v1#PingResponse.number": facts.StateDead, "proto://v1#Pinger": facts.StateDead},
		},
		{
			name:     "an own schema file shadowed by another root's file of its import path",
			files:    shadowed,
			settings: Settings{ImportPaths: []string{"a", "b"}},
			notes:    map[string]string{"b/v1/types.proto": unparsed, dot: withheld},
			messages: map[string]string{
				dot: withheldOne + "b/v1/types.proto (shadowed by a/v1/types.proto under an earlier import root)." + remedyOwn,
			},
			states: map[string]facts.State{"proto://v1#PingRequest": facts.StateLive, "proto://v1#PingResponse": facts.StateLive, "proto://av1#Other": facts.StateLive},
			absent: []string{"proto://v1#Wrap"},
		},
		{
			name: "a missing import beside one that resolves",
			files: map[string]string{
				"proto/v1/service.proto": service,
				"proto/v1/rpc.proto": "syntax = \"proto3\";\npackage v1;\nimport \"proto/v1/service.proto\";\nimport \"google/api/annotations.proto\";\n" +
					"service Pinger {\n  rpc Ping(PingRequest) returns (PingResponse) {\n    option (google.api.http) = { get: \"/ping\" };\n  }\n}\n",
			},
			notes:    map[string]string{"proto/v1/rpc.proto": unresolved, dot: withheld},
			messages: map[string]string{dot: withheldOne + "proto/v1/rpc.proto (the import google/api/annotations.proto names no file of the import roots)." + remedyDefault},
			states:   ping,
			absent:   []string{"proto://v1#Pinger"},
		},
		{
			name:     "a file that fails over a duplicate field number under proto.import_paths",
			files:    map[string]string{"proto/v1/service.proto": service, "proto/v1/rpc.proto": rpc("message Dup { int32 a = 1; int32 b = 1; }\n", "proto/v1/service.proto")},
			settings: importPaths,
			notes:    map[string]string{"proto/v1/rpc.proto": unparsed, dot: withheld},
			messages: map[string]string{
				"proto/v1/rpc.proto": `4:38: Field number 1 has already been used in "v1.Dup" by field "a". Next available field number is 2.`,
				dot:                  withheldOne + pingCompile + remedyOwn,
			},
			states: ping,
			absent: []string{"proto://v1#Pinger", "proto://v1#Dup"},
		},
		{
			name: "a missing import and a duplicate field number under proto.import_paths",
			files: map[string]string{
				"a.proto": "syntax = \"proto3\";\npackage p;\nimport \"missing.proto\";\nmessage A { int32 x = 1; int32 y = 1; }\n",
				"b.proto": "syntax = \"proto3\";\npackage p;\nmessage Alone {}\n",
			},
			settings: importPaths,
			notes:    map[string]string{"a.proto": unparsed, dot: withheld},
			messages: map[string]string{
				"a.proto": `the schema file does not compile, so none of its declarations is published: 4:36: Field number 1 has already been used in "p.A" by field "x". Next available field number is 2.`,
				dot:       withheldOne + "a.proto (it does not compile)." + remedyOwn,
			},
			states: map[string]facts.State{"proto://p#Alone": facts.StateLive},
			absent: []string{"proto://p#A"},
		},
		{
			name: "a missing import alone under proto.import_paths",
			files: map[string]string{
				"a.proto": "syntax = \"proto3\";\npackage p;\nimport \"missing.proto\";\nmessage A { B b = 1; }\nmessage Alone {}\n",
			},
			settings: importPaths,
			notes:    map[string]string{"a.proto": unresolved, dot: withheld},
			messages: map[string]string{
				"a.proto": "the import missing.proto names no file of the import roots searched (.), so the declarations it would supply types to are held live; name its root in proto.import_paths",
				dot:       withheldOne + "a.proto (the import missing.proto names no file of the import roots)." + remedyOwn,
			},
			states: map[string]facts.State{"proto://p#A.b": facts.StateLive, "proto://p#Alone": facts.StateLive},
		},
		{
			name: "two missing imports under proto.import_paths",
			files: map[string]string{
				"a.proto": "syntax = \"proto3\";\npackage p;\nimport \"m1.proto\";\nimport \"m2.proto\";\nmessage A { B b = 1; }\n",
			},
			settings: importPaths,
			notes:    map[string]string{"a.proto": unresolved, dot: withheld},
			messages: map[string]string{
				"a.proto": "the imports m1.proto, m2.proto name no file of the import roots searched (.), so the declarations they would supply types to are held live; name their roots in proto.import_paths",
				dot:       withheldOne + "a.proto (the imports m1.proto, m2.proto name no file of the import roots)." + remedyOwn,
			},
			states: map[string]facts.State{"proto://p#A.b": facts.StateLive},
		},
		{
			name: "two files not read cleanly, one through the other",
			files: map[string]string{
				"a.proto": "syntax = \"proto3\";\npackage p;\nimport \"missing.proto\";\nmessage A {}\n",
				"b.proto": "syntax = \"proto3\";\npackage p;\nimport \"a.proto\";\nmessage B {}\n",
				"c.proto": "syntax = \"proto3\";\npackage p;\nmessage C {}\n",
			},
			notes: map[string]string{"a.proto": unresolved, "b.proto": unparsed, dot: withheld},
			messages: map[string]string{
				dot: "schema findings withheld: 2 schema files could not be read cleanly, and any declaration of the target may be one only they use: " +
					"a.proto (the import missing.proto names no file of the import roots), b.proto (it does not compile). " +
					"Set proto.import_paths, or add a buf.yaml, to say where imports resolve from; the note on each names why.",
			},
			states: map[string]facts.State{"proto://p#C": facts.StateLive},
			absent: []string{"proto://p#A", "proto://p#B"},
		},
		{
			name: "two files not read cleanly for causes of their own",
			files: map[string]string{
				"a.proto": "syntax = \"proto3\";\npackage p;\nimport \"missing.proto\";\nmessage A {}\n",
				"c.proto": "syntax = \"proto3\";\npackage p;\nmessage C { int32 x = 1; int32 y = 1; }\n",
				"d.proto": "syntax = \"proto3\";\npackage p;\nmessage D {}\n",
			},
			notes: map[string]string{"a.proto": unresolved, "c.proto": unparsed, dot: withheld},
			messages: map[string]string{
				"c.proto": `3:36: Field number 1 has already been used in "p.C" by field "x". Next available field number is 2.`,
				dot: "schema findings withheld: 2 schema files could not be read cleanly, and any declaration of the target may be one only they use: " +
					"a.proto (the import missing.proto names no file of the import roots), c.proto (it does not compile). " +
					"Set proto.import_paths, or add a buf.yaml, to say where imports resolve from; the note on each names why.",
			},
			states: map[string]facts.State{"proto://p#D": facts.StateLive},
			absent: []string{"proto://p#A", "proto://p#C"},
		},
		{
			name: "a buf.work.yaml listing only its own directory",
			files: map[string]string{
				"buf.work.yaml": "version: v1\ndirectories:\n  - .\n",
				"a.proto":       "syntax = \"proto3\";\npackage p;\nimport \"missing.proto\";\nmessage A { B b = 1; }\nmessage Alone {}\n",
			},
			notes: map[string]string{"a.proto": unresolved, dot: withheld},
			messages: map[string]string{
				"a.proto": `3:1: Import "missing.proto" was not found or had errors. Set proto.import_paths, or add a buf.yaml, to say where imports resolve from.`,
				dot:       withheldOne + "a.proto (the import missing.proto names no file of the import roots)." + remedyDefault,
			},
			absent: []string{"proto://p#A", "proto://p#Alone"},
		},
	} {
		t.Run(tc.name, tc.check)
	}
}

// uncleanTrees is one tree of each way a schema file is not read cleanly,
// a.proto the file, which declares p.A, beside b.proto, which compiles and
// declares p.B.
var uncleanTrees = []struct {
	name     string
	files    map[string]string
	settings Settings
}{
	{
		name:  "an import no root resolves",
		files: map[string]string{"a.proto": "syntax = \"proto3\";\npackage p;\nimport \"missing.proto\";\nmessage A {}\n"},
	},
	{
		name:     "a type name no scope resolves under proto.import_paths",
		files:    map[string]string{"a.proto": "syntax = \"proto3\";\npackage p;\nmessage A { Gone g = 1; }\n"},
		settings: Settings{ImportPaths: []string{"."}},
	},
	{
		name: "a shadowed own file",
		files: map[string]string{
			"x/a.proto": "syntax = \"proto3\";\npackage q;\nmessage Q {}\n",
			"a.proto":   "syntax = \"proto3\";\npackage p;\nmessage A {}\n",
		},
		settings: Settings{ImportPaths: []string{"x", "."}},
	},
}

// readUnclean reads one of uncleanTrees with b.proto and the extra files.
func readUnclean(t *testing.T, tree int, extra map[string]string, settings Settings) *Result {
	t.Helper()
	files := maps.Clone(uncleanTrees[tree].files)
	files["b.proto"] = "syntax = \"proto3\";\npackage p;\nmessage B {}\n"
	maps.Copy(files, extra)
	settings.ImportPaths = uncleanTrees[tree].settings.ImportPaths
	return readTree(t, files, settings)
}

// TestUnreadFileLeavesItsSuppressionsUnjudged holds an ignore entry naming a
// declaration of a schema file not read cleanly to no stale suppression,
// since whether its finding would stand is unknown, and one naming no
// declaration of a file read cleanly to DS9003.
func TestUnreadFileLeavesItsSuppressionsUnjudged(t *testing.T) {
	for i, tree := range uncleanTrees {
		t.Run(tree.name, func(t *testing.T) {
			r := readUnclean(t, i, map[string]string{
				"deadset-ignore.json": `{"description": "d", "ignore": [` +
					`{"code": "DS1801", "symbol": "proto://p#A", "path": "a.proto", "reason": "Read by a plugin."},` +
					`{"code": "DS1801", "symbol": "proto://p#Gone", "path": "b.proto", "reason": "Deleted since."}]}`,
			}, Settings{})
			var stale []string
			for _, s := range r.Report.StaleSuppressions {
				stale = append(stale, s.Entry.Symbol)
			}
			if !slices.Equal(stale, []string{"proto://p#Gone"}) {
				t.Errorf("stale suppressions = %q, want proto://p#Gone alone", stale)
			}
		})
	}
}

// TestUnreadFileLeavesRootsUnjudged holds a roots.patterns entry to no
// DS9004 where a schema file of the target is not read cleanly, since that
// file may declare what the entry matches.
func TestUnreadFileLeavesRootsUnjudged(t *testing.T) {
	for i, tree := range uncleanTrees {
		t.Run(tree.name, func(t *testing.T) {
			r := readUnclean(t, i, nil, Settings{Roots: []string{"proto://p#A", "proto://p#Gone"}})
			for _, f := range r.Report.Findings {
				if f.Code == vocab.UnmatchedRoot {
					t.Errorf("DS9004 for %s, want none: a.proto may declare it", f.Symbol.Ref)
				}
			}
		})
	}
}

// TestUnreadFileLeavesEdgesUnjudged holds a declared edge's proto side that
// names no published declaration to live where a schema file of the target
// is not read cleanly, since that file may declare it, so the merge reports
// no DS9005 for it; a side naming a published declaration reads as that
// record, and with every file read cleanly a side naming none is absent.
func TestUnreadFileLeavesEdgesUnjudged(t *testing.T) {
	const edges = `{"edges": {"only-unclean": {"provides": "proto://p#A"}, "readable": {"provides": "proto://p#B"}, "none": {"provides": "proto://p#Gone"}}}`
	check := func(t *testing.T, r *Result, want map[string]report.State, stale []string) {
		t.Helper()
		got := map[string]report.State{}
		for _, e := range r.Report.EdgeEvaluations {
			got[e.Edge] = e.State
		}
		if !maps.Equal(got, want) {
			t.Errorf("evaluations = %v, want %v", got, want)
		}
		merged, err := merge.Merge([]merge.Input{{Report: r.Report, Facts: r.Facts, Digest: "sha256:" + strings.Repeat("ab", 32)}},
			[]string{r.Report.SchemaVersion}, &merge.Caller{
				SchemaVersion: r.Report.SchemaVersion, ContractVersion: r.Report.ContractVersion,
				Name: "deadset", Version: "0.0.0-test", MinConfidence: vocab.Possible, Conformance: r.Report.Analyzer.Conformance,
			})
		if err != nil {
			t.Fatalf("Merge = %v", err)
		}
		var edges []string
		for _, f := range merged.Findings {
			if f.Code == vocab.StaleCrossLanguageEdge {
				edges = append(edges, f.Symbol.Name)
			}
		}
		if !slices.Equal(edges, stale) {
			t.Errorf("DS9005 for %q, want %q", edges, stale)
		}
	}
	for i, tree := range uncleanTrees {
		t.Run(tree.name, func(t *testing.T) {
			r := readUnclean(t, i, map[string]string{"deadset-edges.json": edges}, Settings{})
			check(t, r, map[string]report.State{"only-unclean": report.StateLive, "readable": report.StateLive, "none": report.StateLive}, nil)
		})
	}
	t.Run("every file read cleanly", func(t *testing.T) {
		r := readTree(t, map[string]string{
			"a.proto":            "syntax = \"proto3\";\npackage p;\nmessage A {}\n",
			"b.proto":            "syntax = \"proto3\";\npackage p;\nmessage B {}\n",
			"deadset-edges.json": edges,
		}, Settings{Roots: []string{"proto://p#A", "proto://p#B"}})
		check(t, r, map[string]report.State{"only-unclean": report.StateLive, "readable": report.StateLive, "none": report.StateAbsent}, []string{"none"})
	})
}

// TestRootsPatterns holds a matched entry to a live record, an entry the
// reader reads that matches nothing to DS9004, and an entry of another
// language to nothing.
func TestRootsPatterns(t *testing.T) {
	r := readTree(t, map[string]string{"v1/a.proto": "syntax = \"proto3\";\npackage v1;\nmessage A { int32 n = 1; }\nmessage B {}\n"},
		Settings{Roots: []string{"proto://v1#A", "proto://v1#Missing", "*#NoSuchSymbol*", "go://example.com/app#Missing", "proto://v1#B*"}})
	if got := record(t, r, "proto://v1#A"); got.State != facts.StateLive {
		t.Errorf("A = %s, want live: an exact root names it", got.State)
	}
	if got := record(t, r, "proto://v1#A.n"); got.State != facts.StateDead {
		t.Errorf("A.n = %s, want dead: the exact root names A alone", got.State)
	}
	if got := record(t, r, "proto://v1#B"); got.State != facts.StateLive {
		t.Errorf("B = %s, want live: a pattern names it", got.State)
	}
	var unmatched []string
	for _, f := range r.Report.Findings {
		if f.Code == vocab.UnmatchedRoot {
			unmatched = append(unmatched, f.Symbol.Ref)
		}
	}
	if len(unmatched) != 2 || unmatched[0] != "*#NoSuchSymbol*" || unmatched[1] != "proto://v1#Missing" {
		t.Errorf("DS9004 = %q, want *#NoSuchSymbol* and proto://v1#Missing", unmatched)
	}
}

func TestEveryCodeTheReaderEmitsAppliesToProto(t *testing.T) {
	r := readTree(t, map[string]string{
		"v1/a.proto":          "syntax = \"proto3\";\npackage v1;\n\n// deadset:ignore DS1802 -- bound to nothing\n\nmessage A {\n  int32 n = 1; // deadset:ignore DS1802\n}\n",
		"deadset-ignore.json": `{"ignore": [{"code": "DS1801", "symbol": "proto://v1#A", "reason": "kept"}]}`,
	}, Settings{Roots: []string{"proto://v1#Missing"}})
	var codes []vocab.Code
	for i := range r.Report.Findings {
		codes = append(codes, r.Report.Findings[i].Code)
	}
	for i := range r.Report.StaleSuppressions {
		codes = append(codes, r.Report.StaleSuppressions[i].Code)
	}
	for i := range r.Facts.Bindings {
		if f := r.Facts.Bindings[i].Finding; f != nil {
			codes = append(codes, f.Code)
		}
	}
	for _, code := range codes {
		if kind, ok := vocab.KindOf(code); !ok || !kind.AppliesTo(vocab.Proto) {
			t.Errorf("the reader emits %s, whose kind does not list proto", code)
		}
	}
	for _, want := range []vocab.Code{vocab.SuppressionWithoutReason, vocab.UnscopedIgnoreEntry, vocab.StaleSuppression, vocab.UnmatchedRoot} {
		if !slices.Contains(codes, want) {
			t.Errorf("codes = %v, want %s among them", codes, want)
		}
	}
}

func TestUnmatchedRootIsInTheDeclaringDocument(t *testing.T) {
	target := t.TempDir()
	cases := []struct{ name, source, want string }{
		{name: "central inside", source: "central: " + filepath.Join(target, "sub", "other.json"), want: "sub/other.json"},
		{name: "repository", source: "repository: " + filepath.Join(target, "deadset.json"), want: "deadset.json"},
		{name: "central outside", source: "central: " + filepath.Join(filepath.Dir(target), "other.json"), want: "deadset.json"},
		{name: "default", source: "default", want: "deadset.json"},
		{name: "none", source: "", want: "deadset.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rootsDocument(target, c.source); got != c.want {
				t.Errorf("rootsDocument(%q) = %q, want %q", c.source, got, c.want)
			}
		})
	}
	settings, err := ParseSettings([]byte(`{"provenance": {"roots.patterns": "central: /c/other.json"}}`))
	if err != nil || settings.RootsSource != "central: /c/other.json" {
		t.Errorf("ParseSettings = %+v, %v; want RootsSource from the provenance of roots.patterns", settings.RootsSource, err)
	}
}
