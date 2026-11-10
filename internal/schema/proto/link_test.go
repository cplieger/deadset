package proto

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"testing"
	"testing/fstest"
)

// linked links name from one root holding files and returns its unit.
func linked(t *testing.T, files map[string]string, name string) *Unit {
	t.Helper()

	return linkedAll(t, files, name)[name]
}

// linkedAll links name from one root holding files and returns the unit of
// every file it loaded.
func linkedAll(t *testing.T, files map[string]string, name string) map[string]*Unit {
	t.Helper()

	root := fstest.MapFS{}
	for path, src := range files {
		root[path] = &fstest.MapFile{Data: []byte(src)}
	}
	units, _ := Link([]fs.FS{root}, Files{Names: []string{name}})
	if u := units[name]; u == nil || u.File == nil {
		t.Fatalf("Link(%s) = %+v, want the file loaded", name, u)
	}
	return units
}

func TestLinkUnresolvedImport(t *testing.T) {
	t.Parallel()

	u := linked(t, map[string]string{"v1/wrap.proto": `syntax = "proto3";

package v1;

import "other/missing.proto";

message Wrapper {
  other.Missing inner = 1;
}

message Alone {
  int32 count = 1;
}
`}, "v1/wrap.proto")
	if u.Err != nil {
		t.Fatalf("Err = %v, want none: a missing import is not an error", u.Err)
	}
	if !slices.Equal(u.Missing, []string{"other/missing.proto"}) {
		t.Errorf("Missing = %v, want [other/missing.proto]", u.Missing)
	}
	if want := [][]int32{{4, 0, 2, 0, 6}}; !slices.EqualFunc(u.Unresolved, want, slices.Equal) {
		t.Errorf("Unresolved = %v, want %v, inner's type_name", u.Unresolved, want)
	}
	inner, count := u.File.Messages[0].Fields[0], u.File.Messages[1].Fields[0]
	if inner.Type != 0 || inner.TypeName != "other.Missing" {
		t.Errorf("inner = %v %q, want its type left unresolved as written", inner.Type, inner.TypeName)
	}
	if count.FullName != "v1.Alone.count" || count.Type != fieldInt32 {
		t.Errorf("count = %q %v, want v1.Alone.count TypeInt32", count.FullName, count.Type)
	}
}

// Each want is protoc v36.2's error list for the input, less the import error
// protoc adds to a cycle's first file after its recursive-import error;
// others holds the lists of files it imports.
func TestLinkDuplicateFieldNumber(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		files  map[string]string
		others map[string]Errors
		name   string
		input  string
		want   Errors
	}{
		{
			name:  "a duplicate field number",
			files: map[string]string{"v1/bad.proto": "syntax = \"proto3\";\n\npackage v1;\n\nmessage Broken {\n  int32 a = 1;\n  int32 b = 1;\n}\n"},
			input: "v1/bad.proto",
			want:  Errors{{Line: 7, Column: 13, Message: `Field number 1 has already been used in "v1.Broken" by field "a". Next available field number is 2.`}},
		},
		{
			name:  "a field in an extension range, with the suggestion",
			files: map[string]string{"r.proto": "syntax = \"proto2\";\npackage p;\nmessage A {\n  optional int32 a = 5;\n  extensions 1 to 10;\n}\n"},
			input: "r.proto",
			want: Errors{
				{Line: 5, Column: 14, Message: `Extension range 1 to 10 includes field "a" (5).`},
				{Line: 5, Column: 14, Message: "Suggested field numbers for p.A: 11"},
			},
		},
		{
			name:  "an enum value outside its enum's scope",
			files: map[string]string{"col.proto": "syntax = \"proto3\";\npackage p;\nenum DsNegA { DS_NEG_COLLIDE = 0; }\nenum DsNegB { DS_NEG_COLLIDE = 0; }\n"},
			input: "col.proto",
			want: Errors{
				{Line: 4, Column: 15, Message: `"DS_NEG_COLLIDE" is already defined in "p".`},
				{Line: 4, Column: 15, Message: `Note that enum values use C++ scoping rules, meaning that enum values are siblings of their type, not children of it.  Therefore, "DS_NEG_COLLIDE" must be unique within "p", not just within "DsNegB".`},
			},
		},
		{
			name:  "a default JSON name conflict",
			files: map[string]string{"json.proto": "syntax = \"proto3\";\npackage p;\nmessage DsNegJson { int32 foo_bar = 1; int32 fooBar = 2; }\n"},
			input: "json.proto",
			want:  Errors{{Line: 3, Column: 46, Message: `The default JSON name of field "fooBar" ("fooBar") conflicts with the default JSON name of field "foo_bar".`}},
		},
		{
			name:  "an open enum whose first value is not zero",
			files: map[string]string{"enz.proto": "syntax = \"proto3\";\npackage p;\nenum DsNegZ { DS_NEG_Z_ONE = 1; }\n"},
			input: "enz.proto",
			want:  Errors{{Line: 3, Column: 30, Message: "The first enum value must be zero for open enums."}},
		},
		{
			name:  "edition 2024 naming style",
			files: map[string]string{"sty.proto": "edition = \"2024\";\npackage p;\nmessage ds_neg_bad_style { int32 BadField = 1; }\n"},
			input: "sty.proto",
			want: Errors{
				{Line: 3, Column: 9, Message: "Message name ds_neg_bad_style should be TitleCase (features.enforce_naming_style = STYLE_LEGACY can be used to opt out of this check)"},
				{Line: 3, Column: 34, Message: "Field name BadField should be lower_snake_case (features.enforce_naming_style = STYLE_LEGACY can be used to opt out of this check)"},
			},
		},
		{
			name:  "a required field in proto3",
			files: map[string]string{"req.proto": "syntax = \"proto3\";\npackage p;\nmessage DsNegReq { required int32 a = 1; }\n"},
			input: "req.proto",
			want:  Errors{{Line: 3, Column: 29, Message: "Required fields are not allowed in proto3."}},
		},
		{
			name:  "an unknown option",
			files: map[string]string{"unk.proto": "syntax = \"proto3\";\npackage p;\noption ds_neg_unknown_option = true;\n"},
			input: "unk.proto",
			want:  Errors{{Line: 3, Column: 8, Message: `Option "ds_neg_unknown_option" unknown. Ensure that your proto definition file imports the proto which defines the option (i.e. via import option after edition 2024).`}},
		},
		{
			name:  "an option value of the wrong type",
			files: map[string]string{"o.proto": "syntax = \"proto3\";\npackage p;\noption java_package = 5;\n"},
			input: "o.proto",
			want:  Errors{{Line: 3, Column: 23, Message: `Value must be quoted string for string option "google.protobuf.FileOptions.java_package".`}},
		},
		{
			name:  "an aggregate naming no field",
			files: map[string]string{"g.proto": "syntax = \"proto3\";\npackage p;\nimport \"google/protobuf/descriptor.proto\";\nmessage X {\n  int32 a = 1;\n}\nextend google.protobuf.FileOptions {\n  X x = 50000;\n}\noption (x) = { nope: 1 };\n"},
			input: "g.proto",
			want:  Errors{{Line: 10, Column: 14, Message: `Error while parsing option value for "x": Message type "p.X" has no field named "nope".`}},
		},
		{
			name: "a nested type another edition 2024 file cannot see",
			files: map[string]string{
				"lib.proto": "edition = \"2024\";\npackage lib;\nmessage Outer {\n  message Inner {}\n}\n",
				"use.proto": "edition = \"2024\";\npackage app;\nimport \"lib.proto\";\nmessage M {\n  lib.Outer.Inner inner = 1;\n}\n",
			},
			input: "use.proto",
			want:  Errors{{Line: 5, Column: 3, Message: `Symbol "lib.Outer.Inner", defined in "lib.proto"  is not visible from "use.proto". It defaulted to local from file-level 'option features.default_symbol_visibility = 'EXPORT_TOP_LEVEL'; and cannot be accessed outside its own file`}},
		},
		{
			name:  "a field number reserved for the library, which has no position",
			files: map[string]string{"res.proto": "syntax = \"proto3\";\npackage p;\nmessage A { int32 a = 19000; }\n"},
			input: "res.proto",
			want:  Errors{{Message: "Field numbers 19000 through 19999 are reserved for the protocol buffer library implementation."}},
		},
		{
			name: "an import cycle",
			files: map[string]string{
				"a.proto": "syntax = \"proto3\";\nimport \"b.proto\";\n",
				"b.proto": "syntax = \"proto3\";\nimport \"a.proto\";\n",
			},
			input:  "a.proto",
			want:   Errors{{Line: 2, Column: 1, Message: "File recursively imports itself: a.proto -> b.proto -> a.proto"}},
			others: map[string]Errors{"b.proto": {{Line: 2, Column: 1, Message: `Import "a.proto" was not found or had errors.`}}},
		},
		{
			name: "an import cycle of three files",
			files: map[string]string{
				"a.proto": "syntax = \"proto3\";\nimport \"b.proto\";\n",
				"b.proto": "syntax = \"proto3\";\nimport \"c.proto\";\n",
				"c.proto": "syntax = \"proto3\";\nimport \"a.proto\";\n",
			},
			input: "a.proto",
			want:  Errors{{Line: 2, Column: 1, Message: "File recursively imports itself: a.proto -> b.proto -> c.proto -> a.proto"}},
			others: map[string]Errors{
				"b.proto": {{Line: 2, Column: 1, Message: `Import "c.proto" was not found or had errors.`}},
				"c.proto": {{Line: 2, Column: 1, Message: `Import "a.proto" was not found or had errors.`}},
			},
		},
		{
			name: "an import cycle a file closes twice",
			files: map[string]string{
				"a.proto": "syntax = \"proto3\";\npackage p;\nimport \"b.proto\";\nmessage A {}\n",
				"b.proto": "syntax = \"proto3\";\npackage p;\nimport \"c.proto\";\nimport \"a.proto\";\nmessage B {}\n",
				"c.proto": "syntax = \"proto3\";\npackage p;\nimport \"a.proto\";\nmessage C {}\n",
			},
			input: "a.proto",
			want:  Errors{{Line: 3, Column: 1, Message: "File recursively imports itself: a.proto -> b.proto -> c.proto -> a.proto"}},
			others: map[string]Errors{
				"b.proto": {
					{Line: 3, Column: 1, Message: `Import "c.proto" was not found or had errors.`},
					{Line: 4, Column: 1, Message: `Import "a.proto" was not found or had errors.`},
				},
				"c.proto": {{Line: 3, Column: 1, Message: `Import "a.proto" was not found or had errors.`}},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			units := linkedAll(t, c.files, c.input)
			for name, want := range c.others {
				var got Errors
				if u := units[name]; u == nil || !errors.As(u.Err, &got) || !slices.Equal(got, want) {
					t.Errorf("%s: %+v, want Err\n%v", name, u, want)
				}
			}
			var got Errors
			if !errors.As(units[c.input].Err, &got) {
				t.Fatalf("Err = %v, want Errors", units[c.input].Err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("Err =\n%v\nwant\n%v", got, c.want)
			}
		})
	}
}

// TestLinkStrict holds a strict link to protoc v36.2's error list for each
// input: a missing import and every name no scope resolves fail the file. A
// link that is not strict fails a file with no missing import the same way.
func TestLinkStrict(t *testing.T) {
	t.Parallel()

	const head = "syntax = \"proto3\";\npackage p;\nimport \"google/protobuf/descriptor.proto\";\n"
	for _, c := range []struct {
		name, src string
		want      Errors
		missing   bool
	}{
		{
			name: "a missing import",
			src:  "syntax = \"proto3\";\npackage p;\nimport \"other/missing.proto\";\nmessage A { other.M m = 1; }\n",
			want: Errors{
				{Line: 3, Column: 1, Message: `Import "other/missing.proto" was not found or had errors.`},
				{Line: 4, Column: 13, Message: `"other.M" is not defined.`},
			},
			missing: true,
		},
		{
			name: "names no scope resolves",
			src:  head + "message A { Nope n = 1; }\nservice S { rpc M(NoIn) returns (NoOut); }\nextend NoMsg { int32 x = 50000; }\n",
			want: Errors{
				{Line: 4, Column: 13, Message: `"Nope" is not defined.`},
				{Line: 6, Column: 8, Message: `"NoMsg" is not defined.`},
				{Line: 5, Column: 19, Message: `"NoIn" is not defined.`},
				{Line: 5, Column: 34, Message: `"NoOut" is not defined.`},
			},
		},
		{
			name: "an option naming no extension",
			src:  "syntax = \"proto3\";\npackage p;\nmessage B { int32 y = 1 [(no.opt) = 1]; }\n",
			want: Errors{{Line: 3, Column: 26, Message: `Option "(no.opt)" unknown. Ensure that your proto definition file imports the proto which defines the option (i.e. via import option after edition 2024).`}},
		},
		{
			name: "an aggregate value naming no extension",
			src:  head + "message X { int32 a = 1; }\nextend google.protobuf.FileOptions { X x = 50000; }\noption (x) = { [no.ext]: 1 };\n",
			want: Errors{{Line: 6, Column: 14, Message: `Error while parsing option value for "x": Extension "no.ext" is not defined or is not an extension of "p.X".`}},
		},
		{
			name: "an Any value of a type no file defines",
			src: "syntax = \"proto3\";\npackage p;\nimport \"google/protobuf/any.proto\";\nimport \"google/protobuf/descriptor.proto\";\n" +
				"message X { google.protobuf.Any a = 1; }\nextend google.protobuf.FileOptions { X x = 50000; }\noption (x) = { a { [type.googleapis.com/no.Such] { } } };\n",
			want: Errors{{Line: 7, Column: 14, Message: `Error while parsing option value for "x": Could not find type "type.googleapis.com/no.Such" stored in google.protobuf.Any.`}},
		},
	} {
		for _, strict := range []bool{true, false} {
			if !strict && c.missing {
				continue
			}
			t.Run(fmt.Sprintf("%s strict=%t", c.name, strict), func(t *testing.T) {
				t.Parallel()

				units, _ := Link([]fs.FS{fstest.MapFS{"a.proto": {Data: []byte(c.src)}}}, Files{Names: []string{"a.proto"}, Strict: strict})
				var got Errors
				if u := units["a.proto"]; u == nil || !errors.As(u.Err, &got) || !slices.Equal(got, c.want) || len(u.Unresolved) > 0 {
					t.Errorf("a.proto: %+v, errors\n%v\nwant\n%v", u, got, c.want)
				}
			})
		}
	}
}

// TestLinkEmbeddedPlugin holds an import of google/protobuf/compiler/plugin.proto,
// which protoc v36.2's include directory ships, to that file.
func TestLinkEmbeddedPlugin(t *testing.T) {
	t.Parallel()

	u := linked(t, map[string]string{
		"a.proto": "syntax = \"proto3\";\npackage app;\nimport \"google/protobuf/compiler/plugin.proto\";\nmessage A { google.protobuf.compiler.CodeGeneratorRequest r = 1; }\n",
	}, "a.proto")
	if u.Err != nil || len(u.Missing) > 0 || len(u.Unresolved) > 0 {
		t.Fatalf("a.proto: Err %v, Missing %v, Unresolved %v, want it linked", u.Err, u.Missing, u.Unresolved)
	}
	if r := u.File.Messages[0].Fields[0]; r.TypeName != ".google.protobuf.compiler.CodeGeneratorRequest" || r.Type != fieldMessage {
		t.Errorf("A.r = %v %q, want the message .google.protobuf.compiler.CodeGeneratorRequest", r.Type, r.TypeName)
	}
	if !Embedded("google/protobuf/compiler/plugin.proto") {
		t.Error("Embedded(google/protobuf/compiler/plugin.proto) = false, want true")
	}
}

// TestLinkImportOfAFailedFile holds an importer of a file that fails to the
// error protoc v36.2 prints first for it, at the import, where a missing
// import is no error.
func TestLinkImportOfAFailedFile(t *testing.T) {
	t.Parallel()

	u := linked(t, map[string]string{
		"a.proto": "syntax = \"proto3\";\npackage p;\n\nimport \"b.proto\";\n\nmessage A { q.B b = 1; }\n",
		"b.proto": "syntax = \"proto3\";\npackage q;\nmessage B { int32 x = 1; int32 y = 1; }\n",
	}, "a.proto")
	var got Errors
	if !errors.As(u.Err, &got) || len(got) == 0 {
		t.Fatalf("Err = %v, want Errors", u.Err)
	}
	if want := (fileError{Line: 4, Column: 1, Message: `Import "b.proto" was not found or had errors.`}); got[0] != want {
		t.Errorf("first error = %v, want %v", got[0], want)
	}
}

// TestLinkSameFileUnderTwoNames holds a file an import reaches through a
// second root under another name to the one unit Same names, an import cycle
// through that name to protoc's error at each file's importing statement.
func TestLinkSameFileUnderTwoNames(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, b string
		want    Errors // a's
		wantB   Errors
	}{
		{name: "an import by the second name", b: "syntax = \"proto3\";\npackage api;\nmessage B {}\n"},
		{
			name:  "a cycle through the second name",
			b:     "syntax = \"proto3\";\npackage api;\nimport \"a.proto\";\nmessage B {}\n",
			want:  Errors{{Line: 3, Column: 1, Message: "File recursively imports itself: api/a.proto -> api/b.proto -> api/a.proto"}},
			wantB: Errors{{Line: 3, Column: 1, Message: `Import "a.proto" was not found or had errors.`}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			a := "syntax = \"proto3\";\npackage api;\nimport \"b.proto\";\nmessage A { B b = 1; }\n"
			target := fstest.MapFS{"api/a.proto": {Data: []byte(a)}, "api/b.proto": {Data: []byte(c.b)}}
			sub, err := fs.Sub(target, "api")
			if err != nil {
				t.Fatal(err)
			}
			units, _ := Link([]fs.FS{target, sub}, Files{
				Names: []string{"api/a.proto", "api/b.proto"},
				Same:  func(root int, name string) string { return map[int]string{0: "", 1: "api/"}[root] + name },
			})
			if _, twice := units["b.proto"]; twice {
				t.Errorf("units = %v, want b.proto linked only as api/b.proto", slices.Sorted(maps.Keys(units)))
			}
			u := units["api/a.proto"]
			var got Errors
			if u.Err != nil && !errors.As(u.Err, &got) {
				t.Fatalf("Err = %v, want Errors", u.Err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("Err =\n%v\nwant\n%v", got, c.want)
			}
			var gotB Errors
			if b := units["api/b.proto"].Err; b != nil && !errors.As(b, &gotB) {
				t.Fatalf("api/b.proto Err = %v, want Errors", b)
			}
			if !slices.Equal(gotB, c.wantB) {
				t.Errorf("api/b.proto Err =\n%v\nwant\n%v", gotB, c.wantB)
			}
		})
	}
}

// field returns the value of field number n of m, or nil.
func field(m *MessageValue, n int32) *fieldValue {
	if m == nil {
		return nil
	}
	return m.Fields[n]
}

// scalars lists the scalar values of fv.
func scalars(fv *fieldValue) []string {
	if fv == nil {
		return nil
	}
	out := make([]string, 0, len(fv.Values))
	for _, v := range fv.Values {
		out = append(out, v.Scalar)
	}
	return out
}

func TestInterpretOptions(t *testing.T) {
	t.Parallel()

	u := linked(t, map[string]string{
		"acme/opts.proto": `syntax = "proto3";
package acme;
import "google/protobuf/any.proto";
import "google/protobuf/descriptor.proto";
message Rule {
  string name = 1;
  repeated int32 codes = 2;
  google.protobuf.Any detail = 3;
}
message Note {
  string text = 1;
}
extend google.protobuf.MessageOptions {
  bool tagged = 50002;
}
extend google.protobuf.FieldOptions {
  Rule rule = 50001;
}
`,
		"app/use.proto": `syntax = "proto3";
package app;
import "acme/opts.proto";
message Item {
  option (acme.tagged) = true;
  string id = 1 [deprecated = true, (acme.rule) = {
    name: "x"
    codes: [1, 2]
    detail { [type.googleapis.com/acme.Note] { text: "hi" } }
  }];
}
`,
	}, "app/use.proto")
	if u.Err != nil {
		t.Fatalf("Err = %v, want none", u.Err)
	}
	item := u.File.Messages[0]
	tagged := field(item.Options.Interpreted, 50002)
	if tagged == nil || tagged.Field.FullName != "acme.tagged" || !slices.Equal(scalars(tagged), []string{"true"}) {
		t.Errorf("(acme.tagged) = %+v, want the extension acme.tagged set to true", tagged)
	}
	id := item.Fields[0].Options
	if len(id.Uninterpreted) != 0 || !slices.Equal(scalars(field(id.Interpreted, 3)), []string{"true"}) {
		t.Errorf("id options = %+v, want every option interpreted and deprecated true", id)
	}
	rule := field(id.Interpreted, 50001)
	if rule == nil || len(rule.Values) != 1 {
		t.Fatalf("(acme.rule) = %+v, want one message value", rule)
	}
	r := rule.Values[0].Message
	if !slices.Equal(scalars(field(r, 1)), []string{"x"}) || !slices.Equal(scalars(field(r, 2)), []string{"1", "2"}) {
		t.Errorf("rule name and codes = %v %v, want x and [1 2]", scalars(field(r, 1)), scalars(field(r, 2)))
	}
	detail := field(r, 3)
	if detail == nil || len(detail.Values) != 1 {
		t.Fatalf("rule detail = %+v, want one Any", detail)
	}
	anyValue := detail.Values[0].Message
	note := field(anyValue, 2)
	if !slices.Equal(scalars(field(anyValue, 1)), []string{"type.googleapis.com/acme.Note"}) || note == nil || note.Values[0].Message.Type.FullName != "acme.Note" ||
		!slices.Equal(scalars(field(note.Values[0].Message, 1)), []string{"hi"}) {
		t.Errorf("rule detail = %+v, want the Any expanded to acme.Note{text: hi}", anyValue)
	}
	for _, c := range []struct {
		path []int32
		span []int32
	}{
		{[]int32{4, 0, 7, 50002}, []int32{4, 2, 30}},
		{[]int32{4, 0, 2, 0, 8, 3}, []int32{5, 17, 34}},
		{[]int32{4, 0, 2, 0, 8, 50001}, []int32{5, 36, 9, 3}},
	} {
		if l := locationAt(u.File, c.path...); l == nil || !slices.Equal(l.Span, c.span) {
			t.Errorf("location %v = %+v, want span %v as protoc rewrites it", c.path, l, c.span)
		}
	}
	for _, l := range u.File.Locations {
		if slices.Contains(l.Path, 999) {
			t.Errorf("location %v still names an uninterpreted option", l.Path)
		}
	}
}

func TestFeaturesResolveFromEditionDefaults(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, src string
		open      bool
	}{
		{name: "a proto2 enum is closed", src: "syntax = \"proto2\";\nenum E {\n  E_A = 1;\n}\n", open: false},
		{name: "a proto3 enum is open", src: "syntax = \"proto3\";\nenum E {\n  E_A = 0;\n}\n", open: true},
		{name: "an edition 2023 enum is open", src: "edition = \"2023\";\nenum E {\n  E_A = 0;\n}\n", open: true},
		{name: "an enum's own feature closes it", src: "edition = \"2023\";\nenum E {\n  option features.enum_type = CLOSED;\n  E_A = 1;\n}\n", open: false},
		{name: "an enum inherits its file's feature", src: "edition = \"2023\";\noption features.enum_type = CLOSED;\nmessage M {\n  enum E {\n    E_A = 1;\n  }\n}\n", open: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			u := linked(t, map[string]string{"e.proto": c.src}, "e.proto")
			if u.Err != nil {
				t.Fatalf("Err = %v, want none", u.Err)
			}
			e := u.File.Enums
			if len(e) == 0 {
				e = u.File.Messages[0].Enums
			}
			if e[0].Open != c.open {
				t.Errorf("Open = %t, want %t", e[0].Open, c.open)
			}
		})
	}
}
